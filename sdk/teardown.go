package sdk

import "context"

// Conditions a teardown operation can be registered under.
const (
	whenAlways    = "always"
	whenOnSuccess = "on_success"
	whenOnFail    = "on_fail"
)

// Teardown registers work to run at the end of the job rather than
// immediately. Registered work runs in reverse order of registration, and
// still runs if the job is cancelled or times out.
//
// Obtain one with Client.AtTeardown.
type Teardown struct {
	client *Client
	when   string
}

// AtTeardown returns a view of the client whose operations are registered to
// run at the end of the job, instead of running when they are called.
//
// A cache saved this way is written once the job's steps are done, so a
// function can restore a cache and register the matching save in one go
// rather than relying on a second call later in the job:
//
//	client.AtTeardown().SaveCache(ctx, key, paths)
func (c *Client) AtTeardown() *Teardown {
	return &Teardown{client: c, when: whenAlways}
}

// OnFail registers the work to run only if the job failed. Useful for
// collecting diagnostics that are only worth keeping when something broke.
func (t *Teardown) OnFail() *Teardown {
	return &Teardown{client: t.client, when: whenOnFail}
}

// OnSuccess registers the work to run only if the job succeeded.
func (t *Teardown) OnSuccess() *Teardown {
	return &Teardown{client: t.client, when: whenOnSuccess}
}

// SaveCache registers paths to be saved under key at the end of the job. It
// returns once the save is registered, not once it has run, so it reports
// nothing about whether the save itself succeeded.
//
// The save is best effort, and a failed one does not fail the job: a cache save
// reports success whatever happens to it, whether it runs here or inline. It
// returns ErrNoServer if the function isn't running inside a CircleCI job.
func (t *Teardown) SaveCache(ctx context.Context, key string, paths []string) error {
	_, err := t.client.do(ctx, "/step/cache/save", saveRequest{
		Key:      key,
		Paths:    paths,
		Teardown: true,
		When:     t.when,
	})
	return err
}
