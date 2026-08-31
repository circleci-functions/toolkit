package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"runtime"
)

// CacheResult is the result of a RestoreCache call.
type CacheResult struct {
	// Raw is the underlying step's run outcome as unstructured text
	// ("success", "failed", or "infra-fail") — not a cache-specific
	// hit/miss signal. A future version of the SDK may expose that
	// separately.
	Raw string
}

// ServerError is returned when a request fails with an error from
// CircleCI.
type ServerError struct {
	Msg string // error message
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("circleci: %s", e.Msg)
}

type saveRequest struct {
	Key   string   `json:"key"`
	Paths []string `json:"paths"`
	Name  string   `json:"name,omitempty"`

	// Set when the save is registered to run at the end of the job.
	Teardown bool   `json:"teardown,omitempty"`
	When     string `json:"when,omitempty"`
}

type restoreRequest struct {
	Keys []string `json:"keys"`
	Name string   `json:"name,omitempty"`
}

// CacheOption configures a cache operation.
type CacheOption func(*cacheOptions)

type cacheOptions struct {
	name string
}

// WithStepName names the step the operation creates, in place of the generic
// "Saving cache" or "Restoring cache" the job would otherwise show. Worth
// setting when a function handles more than one cache, since the steps are
// indistinguishable without it.
func WithStepName(name string) CacheOption {
	return func(o *cacheOptions) { o.name = name }
}

func newCacheOptions(opts []CacheOption) cacheOptions {
	var o cacheOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

type serverResponse struct {
	Result string `json:"result"`
	Error  string `json:"error"`

	// Set when the step was queued for the end of the job instead of run, in
	// which case Result is empty: the step has no result until it runs.
	Deferred bool `json:"deferred"`
}

// SaveCache saves paths under key. It returns ErrNoServer if the function
// isn't running inside a CircleCI job.
func (c *Client) SaveCache(ctx context.Context, key string, paths []string, opts ...CacheOption) error {
	o := newCacheOptions(opts)
	_, err := c.do(ctx, "/step/cache/save", saveRequest{Key: key, Paths: paths, Name: o.name})
	return err
}

// RestoreCache restores the cache matching any of keys. It returns
// ErrNoServer if the function isn't running inside a CircleCI job.
func (c *Client) RestoreCache(ctx context.Context, keys []string, opts ...CacheOption) (CacheResult, error) {
	o := newCacheOptions(opts)
	resp, err := c.do(ctx, "/step/cache/restore", restoreRequest{Keys: keys, Name: o.name})
	if err != nil {
		return CacheResult{}, err
	}
	return CacheResult{Raw: resp.Result}, nil
}

func (c *Client) do(ctx context.Context, path string, body any) (*serverResponse, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("sdk: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("sdk: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, ErrNoServer
		}
		return nil, fmt.Errorf("sdk: request failed: %w", err)
	}
	defer resp.Body.Close()

	var result serverResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("sdk: decode response: %w", err)
	}

	// Any 2xx is a success: a deferred step is accepted with 202, not 200.
	if resp.StatusCode >= http.StatusMultipleChoices {
		return nil, &ServerError{Msg: result.Error}
	}

	return &result, nil
}

// OSArch returns the running platform as "<GOOS>-<GOARCH>", for example
// "linux-amd64" or "darwin-arm64". Use it to scope a cache key so entries can't
// be restored onto a platform they were not built for.
//
// This is not the same string as a CircleCI config's {{ arch }}, so a function
// replacing an orb cannot reuse the orb's cache entries by building the same
// key shape.
func OSArch() string {
	return fmt.Sprintf("%s-%s", runtime.GOOS, runtime.GOARCH)
}

// LogCacheErr reports cache errors to stderr, ignoring ErrNoServer (which
// only means the function isn't running inside a CircleCI job).
func LogCacheErr(op string, err error) {
	if err == nil || errors.Is(err, ErrNoServer) {
		return
	}
	fmt.Fprintf(os.Stderr, "cache %s: %v\n", op, err)
}
