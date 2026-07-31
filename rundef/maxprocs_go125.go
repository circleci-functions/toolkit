//go:build go1.25

package rundef

import (
	"context"
	"runtime"

	o11y "github.com/circleci-functions/toolkit/observability"
)

// MaxProcs is a no-op on Go 1.25+
func MaxProcs(ctx context.Context) (err error) {
	ctx, end := o11y.StartSpan(ctx, "rundef: max procs")
	defer end(&err)

	limit := runtime.GOMAXPROCS(0) // Get the limit the go runtime has determined

	o11y.SetSpanAttribute(ctx, "limit", limit)
	return err
}
