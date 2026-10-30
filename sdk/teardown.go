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
func (t *Teardown) SaveCache(ctx context.Context, key string, paths []string, opts ...CacheOption) error {
	o := newCacheOptions(opts)
	_, err := t.client.do(ctx, "/step/cache/save", saveRequest{
		Key:      key,
		Paths:    paths,
		Name:     o.name,
		Teardown: true,
		When:     t.when,
	})
	return err
}

// StoreArtifacts registers the file or directory at path to be stored as a
// job artifact at the end of the job. destination sets the path shown in the
// artifacts list; if empty, path itself is used, relative to the filesystem root.
//
// It returns once the registration is accepted, not once the store has run.
// It returns ErrNoServer if the function isn't running inside a CircleCI job.
func (t *Teardown) StoreArtifacts(ctx context.Context, path, destination string) error {
	_, err := t.client.do(ctx, "/step/artifacts/store", storeArtifactsRequest{
		Path:        path,
		Destination: destination,
		Teardown:    true,
		When:        t.when,
	})
	return err
}

// StoreTestResults registers the XML test-result files under path to be
// stored at the end of the job so CircleCI can parse them and surface timing
// and failure information in the web app.
//
// It returns once the registration is accepted, not once the store has run.
// It returns ErrNoServer if the function isn't running inside a CircleCI job.
func (t *Teardown) StoreTestResults(ctx context.Context, path string) error {
	_, err := t.client.do(ctx, "/step/test-results/store", storeTestResultsRequest{
		Path:     path,
		Teardown: true,
		When:     t.when,
	})
	return err
}
