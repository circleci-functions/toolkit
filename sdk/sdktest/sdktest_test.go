package sdktest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk/sdktest"
)

// A save registered at teardown reads the paths when the job ends, not when it
// was registered, so a later write to those paths is what gets stored.
func TestServer_EndJobRunsRegisteredSaves(t *testing.T) {
	srv := sdktest.NewServer(t)
	client := srv.Client()
	ctx := context.Background()

	dir := t.TempDir()
	cached := filepath.Join(dir, "cached")
	marker := filepath.Join(cached, "marker")
	assert.NilError(t, os.MkdirAll(cached, 0o750))
	assert.NilError(t, os.WriteFile(marker, []byte("at-registration"), 0o600))

	assert.NilError(t, client.AtTeardown().SaveCache(ctx, "my-key", []string{cached}))

	// Registered, and not run yet.
	assert.DeepEqual(t, srv.PendingCacheSaves(), []string{"my-key"})

	// The job carries on and changes what is in the cached directory.
	assert.NilError(t, os.WriteFile(marker, []byte("at-job-end"), 0o600))

	srv.EndJob(t)
	assert.Equal(t, len(srv.PendingCacheSaves()), 0)

	assert.NilError(t, os.RemoveAll(cached))
	_, err := client.RestoreCache(ctx, []string{"my-key"})
	assert.NilError(t, err)

	content, err := os.ReadFile(marker)
	assert.NilError(t, err)
	assert.Equal(t, string(content), "at-job-end")
}

// Saves registered at teardown run in reverse order of registration, as they do
// in a job. Two saves under one key make the order observable: whichever runs
// last is the one a restore brings back.
func TestServer_EndJobRunsSavesInReverse(t *testing.T) {
	srv := sdktest.NewServer(t)
	client := srv.Client()
	ctx := context.Background()

	dir := t.TempDir()
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	for _, d := range []string{first, second} {
		assert.NilError(t, os.MkdirAll(d, 0o750))
		assert.NilError(t, os.WriteFile(filepath.Join(d, "marker"), []byte("x"), 0o600))
	}

	assert.NilError(t, client.AtTeardown().SaveCache(ctx, "shared-key", []string{first}))
	assert.NilError(t, client.AtTeardown().SaveCache(ctx, "shared-key", []string{second}))
	assert.DeepEqual(t, srv.PendingCacheSaves(), []string{"shared-key", "shared-key"})

	srv.EndJob(t)
	assert.Equal(t, len(srv.PendingCacheSaves()), 0)

	assert.NilError(t, os.RemoveAll(first))
	assert.NilError(t, os.RemoveAll(second))
	_, err := client.RestoreCache(ctx, []string{"shared-key"})
	assert.NilError(t, err)

	// Reverse order means the first registration ran last, so it owns the key.
	_, err = os.Stat(filepath.Join(first, "marker"))
	assert.NilError(t, err, "the earliest registration should have run last")
	_, err = os.Stat(filepath.Join(second, "marker"))
	assert.Assert(t, os.IsNotExist(err), "the latest registration should have run first")
}

// An ordinary save still happens immediately, so existing functions are
// unaffected.
func TestServer_SaveCacheStillRunsImmediately(t *testing.T) {
	srv := sdktest.NewServer(t)
	client := srv.Client()
	ctx := context.Background()

	dir := t.TempDir()
	cached := filepath.Join(dir, "cached")
	assert.NilError(t, os.MkdirAll(cached, 0o750))
	assert.NilError(t, os.WriteFile(filepath.Join(cached, "marker"), []byte("kept"), 0o600))

	assert.NilError(t, client.SaveCache(ctx, "my-key", []string{cached}))
	assert.Equal(t, len(srv.PendingCacheSaves()), 0)

	assert.NilError(t, os.RemoveAll(cached))
	_, err := client.RestoreCache(ctx, []string{"my-key"})
	assert.NilError(t, err)
	content, err := os.ReadFile(filepath.Join(cached, "marker"))
	assert.NilError(t, err)
	assert.Equal(t, string(content), "kept")
}
