// Package statsd is a low-level wrapper for statsd
package statsd

import (
	"fmt"
	"sort"
	"time"

	"github.com/DataDog/datadog-go/v5/statsd"

	"github.com/circleci-functions/toolkit/internal/report"
)

type Client struct {
	cfg    ClientConfig
	client *statsd.Client
}

var _ report.MetricEmitter = (*Client)(nil)

type ClientConfig struct {
	Host string
	Port int
}

func NewClient(cfg ClientConfig) (*Client, error) {
	client, err := statsd.New(fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
	return &Client{
		cfg:    cfg,
		client: client,
	}, err
}

func (s *Client) Count(name string, value int64, tags map[string]any) error {
	return s.client.Count(name, value, stringifyTags(tags), 1)
}

func (s *Client) Incr(name string, tags map[string]any) error {
	return s.client.Incr(name, stringifyTags(tags), 1)
}

func (s *Client) Decr(name string, tags map[string]any) error {
	return s.client.Decr(name, stringifyTags(tags), 1)
}

func (s *Client) Gauge(name string, value float64, tags map[string]any) error {
	return s.client.Gauge(name, value, stringifyTags(tags), 1)
}

func (s *Client) Timing(name string, value time.Duration, tags map[string]any) error {
	return s.client.Timing(name, value, stringifyTags(tags), 1)
}

func (s *Client) Close() error {
	return s.client.Close()
}

func stringifyTags(tags map[string]any) (stringTags []string) {
	for k, v := range tags {
		stringTags = append(stringTags, fmt.Sprintf("%s:%v", k, v))
	}
	sort.Strings(stringTags)
	return stringTags
}
