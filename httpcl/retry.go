package httpcl

import (
	"context"
	"errors"
	"time"

	"github.com/cenkalti/backoff/v7"
)

// retry is customized for the httpclient and is coupled to it, in that the httpclient operation must honour
// the expectation of calling the callback in a defer block. This is so the test for whether this is the final
// attempt is taken after the main operation has completed - so that the prediction that we will exceed the
// MaxElapsed time is more accurate.
type retry struct {
	BackOff        backoff.BackOff // Strategy for calculating backoff periods.
	UseDetail      bool            // If we should use the parsed sleep from 429, rather than our own exp backoff.
	MaxElapsedTime time.Duration   // Maximum total time for all retries.
	// Operation takes a func that MUST be called in a defer block. The result of the passed in func indicates
	// if this is the final attempt.
	Operation func(context.Context, func(responseDetail) bool) error
}

func (o *retry) setDefaults() {
	if o.MaxElapsedTime == 0 {
		o.MaxElapsedTime = 15 * time.Minute
	}
	if o.BackOff == nil {
		o.BackOff = backoff.NewExponentialBackOff()
	}
	o.BackOff.Reset()
}

// Next returns the next retry delay. It gets the next delay from the configured backoff strategy, adds that to the
// passed in duration and checks if that duration is greater than the MaxElapsedTime. If so, it returns backoff.Stop.
func (o *retry) next(detail responseDetail, sinceStart time.Duration) (next time.Duration) {
	if o.UseDetail && detail.sleep != nil {
		next = *detail.sleep
	} else {
		next = o.BackOff.NextBackOff()
	}

	if sinceStart+next > o.MaxElapsedTime {
		return backoff.Stop
	}

	return next
}

// Retry attempts the operation until success, a permanent error, or backoff completion.
// It ensures the operation is executed at least once. Returns an error if retries are exhausted
// or context is cancelled, or a permanent error is returned from the operation.
func Retry(ctx context.Context, opts *retry) error {
	opts.setDefaults()

	timer := time.NewTimer(0)
	defer timer.Stop()

	var (
		startedAt = time.Now()
		next      time.Duration
		permanent *backoff.PermanentError
	)

	for {
		// Execute the operation, the passed in func should be called in a defer block in the operation.
		// This allows the operation to take particular actions when this was the last attempt.
		err := opts.Operation(ctx, func(detail responseDetail) bool {
			// Now we have done the operation, and no other conditions have exited, get the next retry delay.
			next = opts.next(detail, time.Since(startedAt))
			return next == backoff.Stop
		})
		cErr := ctx.Err()
		switch {
		case err == nil:
			return nil
		case errors.As(err, &permanent): // Handle permanent errors without retrying.
			return err
		case cErr != nil: // Stop retrying if context is cancelled.
			return cErr
		case next == backoff.Stop:
			return err
		}

		// Wait for the next backoff period or context cancellation.
		timer.Reset(next)
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
