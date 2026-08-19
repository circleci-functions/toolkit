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
