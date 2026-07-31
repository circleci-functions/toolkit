//go:build !go1.25

package rundef

import (
	"context"
	"fmt"
	"runtime"

	"go.uber.org/automaxprocs/maxprocs"

	o11y "github.com/circleci-functions/toolkit/observability"
)

// MaxProcs sets the maximum number of CPUs that can be executing simultaneously to match the CPU limit of the
// container. This is equivalent of setting the GOMAXPROC env var to k8s CPU Limit.  On non-linux systems this is a no-op.
// If no CPU limit is set this is a no-op
func MaxProcs(ctx context.Context) (err error) {
	ctx, end := o11y.StartSpan(ctx, "rundef: max procs")
	defer end(&err)

	_, err = maxprocs.Set(maxprocs.Min(1), maxprocs.Logger(func(s string, i ...any) {
		// This allows sampling using the rundef prefix which is useful for high volume situations
		prefixed := fmt.Sprintf("rundef: %s", s)

		o11y.Info(ctx, fmt.Sprintf(prefixed, i))
	}))
	if err != nil {
		return err
	}

	limit := runtime.GOMAXPROCS(0) // Get the limit that was set

	o11y.SetSpanAttribute(ctx, "limit", limit)
	return err
}
