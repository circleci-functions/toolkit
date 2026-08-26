package sdktest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk/sdktest"
)

func dirWith(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "marker"), []byte(content), 0600))
	return dir
}

func TestServer_RoundTrips(t *testing.T) {
	srv := sdktest.NewServer(t)
	ctx := context.Background()
	dir := dirWith(t, "hit")

	assert.NilError(t, srv.Client().SaveCache(ctx, "key-a", []string{dir}))
	assert.NilError(t, os.RemoveAll(dir))

	_, err := srv.Client().RestoreCache(ctx, []string{"key-a"})
	assert.NilError(t, err)

	got, err := os.ReadFile(filepath.Join(dir, "marker"))
	assert.NilError(t, err)
	assert.Equal(t, string(got), "hit")
}

// A second save under an existing key must not replace the first entry —
// CircleCI cache keys are write-once, and a fake that overwrites hides the
// commonest acceptance-test bug: a constant cache key that only round-trips
// on the first run.
func TestServer_SaveUnderExistingKey_KeepsFirstEntry(t *testing.T) {
	srv := sdktest.NewServer(t)
	ctx := context.Background()

	first := dirWith(t, "first")
	assert.NilError(t, srv.Client().SaveCache(ctx, "key-a", []string{first}))

	second := dirWith(t, "second")
	assert.NilError(t, srv.Client().SaveCache(ctx, "key-a", []string{second}))
	assert.NilError(t, os.RemoveAll(second))

	_, err := srv.Client().RestoreCache(ctx, []string{"key-a"})
	assert.NilError(t, err)

	_, err = os.Stat(filepath.Join(second, "marker"))
	assert.Assert(t, os.IsNotExist(err), "restore must not repopulate a path the winning entry never held")

	got, err := os.ReadFile(filepath.Join(first, "marker"))
	assert.NilError(t, err)
	assert.Equal(t, string(got), "first")
}

func TestServer_WithMutableKeys_Overwrites(t *testing.T) {
	srv := sdktest.NewServer(t, sdktest.WithMutableKeys())
	ctx := context.Background()

	dir := dirWith(t, "first")
	assert.NilError(t, srv.Client().SaveCache(ctx, "key-a", []string{dir}))

	assert.NilError(t, os.WriteFile(filepath.Join(dir, "marker"), []byte("second"), 0600))
	assert.NilError(t, srv.Client().SaveCache(ctx, "key-a", []string{dir}))
	assert.NilError(t, os.RemoveAll(dir))

	_, err := srv.Client().RestoreCache(ctx, []string{"key-a"})
	assert.NilError(t, err)

	got, err := os.ReadFile(filepath.Join(dir, "marker"))
	assert.NilError(t, err)
	assert.Equal(t, string(got), "second")
}

// Two saved paths sharing a basename must stay distinct.
func TestServer_SamePathBasenames(t *testing.T) {
	srv := sdktest.NewServer(t)
	ctx := context.Background()

	a := filepath.Join(t.TempDir(), "cache")
	b := filepath.Join(t.TempDir(), "cache")
	for dir, content := range map[string]string{a: "a", b: "b"} {
		assert.NilError(t, os.MkdirAll(dir, 0750))
		assert.NilError(t, os.WriteFile(filepath.Join(dir, "marker"), []byte(content), 0600))
	}

	assert.NilError(t, srv.Client().SaveCache(ctx, "key-a", []string{a, b}))
	assert.NilError(t, os.RemoveAll(a))
	assert.NilError(t, os.RemoveAll(b))

	_, err := srv.Client().RestoreCache(ctx, []string{"key-a"})
	assert.NilError(t, err)

	for dir, want := range map[string]string{a: "a", b: "b"} {
		got, err := os.ReadFile(filepath.Join(dir, "marker"))
		assert.NilError(t, err)
		assert.Equal(t, string(got), want)
	}
}

func TestServer_RestoreTriesKeysInOrder(t *testing.T) {
	srv := sdktest.NewServer(t)
	ctx := context.Background()
	dir := dirWith(t, "hit")

	assert.NilError(t, srv.Client().SaveCache(ctx, "key-b", []string{dir}))
	assert.NilError(t, os.RemoveAll(dir))

	_, err := srv.Client().RestoreCache(ctx, []string{"key-a", "key-b"})
	assert.NilError(t, err)

	got, err := os.ReadFile(filepath.Join(dir, "marker"))
	assert.NilError(t, err)
	assert.Equal(t, string(got), "hit")
}

func TestServer_SavedKeys(t *testing.T) {
	srv := sdktest.NewServer(t)
	ctx := context.Background()
	dir := dirWith(t, "hit")

	assert.NilError(t, srv.Client().SaveCache(ctx, "key-b", []string{dir}))
	assert.NilError(t, srv.Client().SaveCache(ctx, "key-a", []string{dir}))

	assert.DeepEqual(t, srv.SavedKeys(), []string{"key-a", "key-b"})
}

func TestServer_RestoreMiss_IsNotAnError(t *testing.T) {
	srv := sdktest.NewServer(t)

	_, err := srv.Client().RestoreCache(context.Background(), []string{"no-such-key"})
	assert.NilError(t, err)
}

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
	assert.Equal(t, len(srv.SavedKeys()), 0, "a registered save has not been written yet")

	// The job carries on and changes what is in the cached directory.
	assert.NilError(t, os.WriteFile(marker, []byte("at-job-end"), 0o600))

	srv.EndJob(t)
	assert.Equal(t, len(srv.PendingCacheSaves()), 0)
	assert.DeepEqual(t, srv.SavedKeys(), []string{"my-key"})

	assert.NilError(t, os.RemoveAll(cached))
	_, err := client.RestoreCache(ctx, []string{"my-key"})
	assert.NilError(t, err)

	content, err := os.ReadFile(marker)
	assert.NilError(t, err)
	assert.Equal(t, string(content), "at-job-end")
}

// Saves registered at teardown run in reverse order of registration, as they do
// in a job. Two saves under one key make the order observable, and write-once
// decides the winner: the one that runs first — the last registered — takes the
// key, and the other is skipped.
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

	// Reverse order plus write-once means the latest registration ran first and
	// owns the key.
	_, err = os.Stat(filepath.Join(second, "marker"))
	assert.NilError(t, err, "the latest registration should have run first and won the key")
	_, err = os.Stat(filepath.Join(first, "marker"))
	assert.Assert(t, os.IsNotExist(err), "the earliest registration should have been skipped as a no-op")
}

// A key an inline save already wrote is write-once for a teardown save too, so
// registering one is not a way around it.
func TestServer_EndJob_SaveUnderExistingKey_IsANoOp(t *testing.T) {
	srv := sdktest.NewServer(t)
	client := srv.Client()
	ctx := context.Background()

	inline := dirWith(t, "inline")
	assert.NilError(t, client.SaveCache(ctx, "key-a", []string{inline}))

	deferred := dirWith(t, "deferred")
	assert.NilError(t, client.AtTeardown().SaveCache(ctx, "key-a", []string{deferred}))

	// The registration is still reported: whether it is a no-op is only settled
	// when the job ends.
	assert.DeepEqual(t, srv.PendingCacheSaves(), []string{"key-a"})

	srv.EndJob(t)
	assert.NilError(t, os.RemoveAll(deferred))

	_, err := client.RestoreCache(ctx, []string{"key-a"})
	assert.NilError(t, err)

	_, err = os.Stat(filepath.Join(deferred, "marker"))
	assert.Assert(t, os.IsNotExist(err), "the inline save owns the key, so the deferred one must not have run")
}

// A registration narrowed to a job outcome must only run under that outcome.
func TestServer_EndJob_HonoursRegisteredCondition(t *testing.T) {
	tests := []struct {
		name         string
		endJob       func(*sdktest.Server, *testing.T)
		wantOnFail   bool
		wantOnSucces bool
	}{
		{
			name:         "JobSucceeded",
			endJob:       func(s *sdktest.Server, t *testing.T) { s.EndJob(t) },
			wantOnSucces: true,
		},
		{
			name:       "JobFailed",
			endJob:     func(s *sdktest.Server, t *testing.T) { s.EndJobAfterFailure(t) },
			wantOnFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := sdktest.NewServer(t)
			client := srv.Client()
			ctx := context.Background()

			assert.NilError(t, client.AtTeardown().OnFail().SaveCache(ctx, "on-fail", []string{dirWith(t, "x")}))
			assert.NilError(t, client.AtTeardown().OnSuccess().SaveCache(ctx, "on-success", []string{dirWith(t, "x")}))
			assert.NilError(t, client.AtTeardown().SaveCache(ctx, "always", []string{dirWith(t, "x")}))

			tt.endJob(srv, t)

			want := []string{"always"}
			if tt.wantOnFail {
				want = append(want, "on-fail")
			}
			if tt.wantOnSucces {
				want = append(want, "on-success")
			}
			assert.DeepEqual(t, srv.SavedKeys(), want)
		})
	}
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
