/*
Package health contains utilities for defining what "healthy" means to your service.

A Check type is provided for generically defining health checks, in addition to a helper for
creating a http.Handler for running the checks

See docs/health.md for more details
*/
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hellofresh/health-go/v5"

	"github.com/circleci-functions/toolkit/observability"
)

const (
	defaultTimeout = time.Second * 5
)

// Check carries the parameters to run a health check
type Check struct {
	// The name of the resource to be checked
	Name string
	// The timeout defined for every check, default is 5 seconds
	Timeout time.Duration
	// Execute the check
	Run func(context.Context) error
}

// BuildHandler creates an http.Handler for a set of health checks
func BuildHandler(configs []Check) (http.Handler, error) {
	healthConfigs := make([]health.Config, 0, len(configs))

	for _, config := range configs {
		converted := health.Config{
			Name:    config.Name,
			Timeout: config.Timeout,
			Check:   config.Run,
		}
		if converted.Timeout == 0 {
			converted.Timeout = defaultTimeout
		}

		healthConfigs = append(healthConfigs, converted)
	}

	h, err := health.New(health.WithChecks(healthConfigs...))
	if err != nil {
		return nil, fmt.Errorf("error building health check container: %w", err)
	}

	return handleHealth(h), nil
}

func handleHealth(h *health.Health) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		c := h.Measure(ctx)

		w.Header().Set("Content-Type", "application/json")
		data, err := json.Marshal(c)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		code := http.StatusOK
		if c.Status == health.StatusUnavailable {
			code = http.StatusServiceUnavailable

			// Customise the default health handler to add observability
			for k, v := range c.Failures {
				observability.SetSpanAttribute(ctx, "failure_"+k, v)
			}
		}
		w.WriteHeader(code)
		_, _ = w.Write(data)
	})
}
