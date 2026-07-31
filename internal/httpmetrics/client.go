package httpmetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"

	"github.com/circleci-functions/toolkit/closer"
	"github.com/circleci-functions/toolkit/internal/report"
	"github.com/circleci-functions/toolkit/secret"
)

// Client is a struct that implements the report.MetricEmitter interface
type Client struct {
	client          *retryablehttp.Client
	url             string
	userAgent       string
	token           secret.String
	publishInterval time.Duration

	mu   sync.Mutex
	data []metricData

	stop     chan bool
	stopOnce sync.Once // only close the channel once
}

var _ report.MetricEmitter = (*Client)(nil)
var _ retryablehttp.LeveledLogger = (*slog.Logger)(nil)

type Config struct {
	// URL configures a host for exporting metrics to http[s]://host[:port][/path]
	URL string
	// AuthToken is included as a bearer token on requests
	AuthToken secret.String
	// ClientName provides a name & user agent to the http client for observability. Defaults to "o11y-metrics-client"
	ClientName string
	// PublishInterval how often to publish metrics, defaults to 1 minute
	PublishInterval time.Duration
}

// New creates a Client that implements the ClosableMetricsProvider interface
func New(cfg Config) *Client {
	if cfg.ClientName == "" {
		cfg.ClientName = "http-metrics-client"
	}
	if cfg.PublishInterval == 0 {
		cfg.PublishInterval = time.Minute
	}

	c := retryablehttp.NewClient()
	c.Logger = slog.New(slog.DiscardHandler)

	return &Client{
		data:            []metricData{},
		publishInterval: cfg.PublishInterval,
		client:          c,
		url:             cfg.URL,
		userAgent:       fmt.Sprintf("%s, backplane", cfg.ClientName),
		token:           cfg.AuthToken,
		stop:            make(chan bool),
	}
}

func (c *Client) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.publishInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.stop:
			return nil
		case <-ticker.C:
		}

		c.Publish(ctx)
	}
}

func (c *Client) Teardown(_ context.Context) error {
	return nil
}

func (c *Client) Count(n string, v int64, t map[string]any) error {
	c.record("count", n, float64(v), stringifyTags(t))
	return nil
}

func (c *Client) Incr(n string, t map[string]any) error {
	c.record("count", n, float64(1), stringifyTags(t))
	return nil
}

func (c *Client) Decr(n string, t map[string]any) error {
	c.record("count", n, float64(-1), stringifyTags(t))
	return nil
}

func (c *Client) Gauge(n string, v float64, t map[string]any) error {
	c.record("gauge", n, v, stringifyTags(t))
	return nil
}

func (c *Client) Timing(n string, v time.Duration, t map[string]any) error {
	c.record("timeInMilliseconds", n, float64(v.Milliseconds()), stringifyTags(t))
	return nil
}

func (c *Client) Close() error {
	c.stopOnce.Do(func() {
		if c.stop != nil {
			close(c.stop)
		}

		ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		c.Publish(ctx)
	})
	return nil
}

func stringifyTags(tags map[string]any) (stringTags []string) {
	for k, v := range tags {
		stringTags = append(stringTags, fmt.Sprintf("%s:%v", k, v))
	}
	sort.Strings(stringTags)
	return stringTags
}

type metricData struct {
	Type      string   `json:"type"`
	Name      string   `json:"name"`
	Timestamp int64    `json:"timestamp"`
	Value     float64  `json:"value"`
	Tags      []string `json:"tags"`
}

type Tags map[string]string

func (c *Client) record(metricType, metricName string, metricValue float64, metricTags []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data = append(c.data, metricData{
		Type:      metricType,
		Name:      metricName,
		Timestamp: time.Now().Unix(),
		Value:     metricValue,
		Tags:      metricTags,
	})
}

// Publish sends the stored metrics to receiver
func (c *Client) Publish(ctx context.Context) {
	c.mu.Lock()
	data := c.data
	c.data = nil
	c.mu.Unlock()

	if len(data) == 0 {
		return
	}

	body, err := json.Marshal(struct {
		Data []metricData `json:"metrics"`
		Tags []string     `json:"tags"`
	}{
		Data: data,
		Tags: []string{},
	})

	defer func() {
		if err != nil {
			// reset metrics and replay anything written since backup was taken.
			c.mu.Lock()
			c.data = append(data, c.data...)
			c.mu.Unlock()
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()

	req, err := retryablehttp.NewRequestWithContext(ctx, http.MethodPut, c.url, body)
	if err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Accept", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Authorization", "Bearer "+c.token.Value())

	res, err := c.client.Do(req)
	if err != nil {
		return
	}

	defer closer.ErrorHandler(res.Body, &err)
	defer func() {
		_, _ = io.Copy(io.Discard, res.Body)
	}()

	if res.StatusCode >= 400 {
		err = fmt.Errorf("unexpected status code %d", res.StatusCode)
	}
}
