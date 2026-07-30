// Package sdk lets a CircleCI function perform privileged operations —
// without holding its own credentials,
// while running as a step in a CircleCI job.
package sdk

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

const socketName = "circleci-plugin.sock"

// ErrNoServer is returned when a function isn't running inside a CircleCI
// job (e.g. it's being run locally). Use errors.Is(err, ErrNoServer) to
// tell that case apart from a cache miss or a real error.
var ErrNoServer = errors.New("sdk: not running inside a CircleCI job")

// Client performs privileged operations on behalf of the current CircleCI
// job.
type Client struct {
	http    *http.Client
	baseURL string
}

// NewWithBaseURL creates a Client that sends requests to baseURL.
// Intended for use by sdktest; production code should call sdk.Run instead.
func NewWithBaseURL(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{},
	}
}

func newWithSocketPath(socketPath string) *Client {
	dialer := &net.Dialer{}
	return &Client{
		baseURL: "http://localhost",
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, "unix", socketPath)
				},
			},
		},
	}
}

func newDefault() *Client {
	if url := os.Getenv("CIRCLECI_PLUGIN_URL"); url != "" {
		return NewWithBaseURL(url)
	}
	return newWithSocketPath(filepath.Join(os.TempDir(), socketName))
}

// Run creates a default Client and calls fn. It cancels the context on
// SIGINT or SIGTERM and exits the process with a non-zero status if fn
// returns an error.
func Run(fn func(ctx context.Context, client *Client) error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := fn(ctx, newDefault()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
