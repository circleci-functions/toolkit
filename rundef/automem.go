package rundef

import (
	"context"

	"github.com/KimMachineGun/automemlimit/memlimit"

	o11y "github.com/circleci-functions/toolkit/observability"
)

// MemLimit sets the GOMEMLIMIT to the recommended default of 90% of the memory available. It attempts to calculate this
// from the process cgroup first and will fall back to the total system memory if cgroups is not available.
func MemLimit(ctx context.Context) (err error) {
	ctx, end := o11y.StartSpan(ctx, "rundef: mem limit")
	defer end(&err)

	limit, err := memlimit.SetGoMemLimitWithOpts(
		memlimit.WithRatio(0.9),
		memlimit.WithProvider(
			memlimit.ApplyFallback(
				memlimit.FromCgroup,
				memlimit.FromSystem,
			)))
	if err != nil {
		return err
	}
	o11y.SetSpanAttribute(ctx, "limit", limit)
	return nil
}
