/*
Package rundef provides default runtime configurations for services. Most services should simply call Defaults
*/
package rundef

import (
	"context"
	"errors"

	o11y "github.com/circleci-functions/toolkit/observability"
)

// Defaults configures recommended go runtime options such as GOMEMLIMIT to appropriate values for the detected
// environment
func Defaults(ctx context.Context) (err error) {
	ctx, end := o11y.StartSpan(ctx, "rundef: defaults")
	defer end(&err)

	err = errors.Join(err,
		MemLimit(ctx),
		MaxProcs(ctx),
	)

	return err
}
