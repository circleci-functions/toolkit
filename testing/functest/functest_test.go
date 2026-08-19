package functest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/bashenv"
	"github.com/circleci-functions/toolkit/testing/functest"
)

func TestNew_RedirectsHomeAndBashEnv(t *testing.T) {
	realHome, err := os.UserHomeDir()
	assert.NilError(t, err)

	s := functest.New(t)

	home, err := os.UserHomeDir()
	assert.NilError(t, err)
	assert.Equal(t, home, s.Home)
	assert.Assert(t, home != realHome, "sandbox must not resolve to the real home directory")
	assert.Equal(t, os.Getenv("BASH_ENV"), s.BashEnv)
}

func TestNew_DirsAreDistinctAndWritable(t *testing.T) {
	s := functest.New(t)

	seen := map[string]bool{}
	for _, dir := range []string{s.Home, s.BinDir, s.InstallDir} {
		assert.Assert(t, !seen[dir], "sandbox dirs must be distinct, got %q twice", dir)
		seen[dir] = true
		assert.NilError(t, os.WriteFile(filepath.Join(dir, "probe"), []byte("ok"), 0600))
	}
}

func TestSandbox_Path_ReflectsBashEnv(t *testing.T) {
	s := functest.New(t)
	t.Setenv("PATH", "/usr/bin")

	assert.NilError(t, bashenv.AddToPath(filepath.Join(s.InstallDir, "bin")))

	assert.Equal(t, s.Path(t), filepath.Join(s.InstallDir, "bin")+":/usr/bin")
}

func TestSandbox_Env(t *testing.T) {
	s := functest.New(t)
	env := s.Env()

	assert.Equal(t, env["HOME"], s.Home)
	assert.Equal(t, env["BASH_ENV"], s.BashEnv)
	assert.Equal(t, env["CIRCLECI_PLUGIN_URL"], s.Server.URL())

	merged := s.EnvWith(map[string]string{"HOME": "/override", "EXTRA": "1"})
	assert.Equal(t, merged["HOME"], "/override")
	assert.Equal(t, merged["EXTRA"], "1")
	assert.Equal(t, merged["BASH_ENV"], s.BashEnv)
	assert.Equal(t, env["HOME"], s.Home, "EnvWith must not mutate Env")
}

func TestSandbox_ClientRoundTripsCache(t *testing.T) {
	s := functest.New(t)
	ctx := context.Background()

	dir := filepath.Join(s.Home, "cache")
	assert.NilError(t, os.MkdirAll(dir, 0750))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "marker"), []byte("hit"), 0600))

	key := "test-" + s.CacheChecksum()
	assert.NilError(t, s.Client.SaveCache(ctx, key, []string{dir}))
	assert.NilError(t, os.RemoveAll(dir))

	_, err := s.Client.RestoreCache(ctx, []string{key})
	assert.NilError(t, err)

	got, err := os.ReadFile(filepath.Join(dir, "marker"))
	assert.NilError(t, err)
	assert.Equal(t, string(got), "hit")
}

// Without a task-agent to talk to, WithRealAgent must degrade to the fake
// rather than leave the sandbox pointing at a socket that isn't there.
func TestNew_WithRealAgent_FallsBackToFakeLocally(t *testing.T) {
	if os.Getenv("CIRCLECI") != "" {
		t.Skip("a real task-agent is available in CI")
	}

	s := functest.New(t, functest.WithRealAgent())
	assert.Assert(t, s.Server != nil, "expected the fake server outside a CircleCI job")
}

func TestSandbox_CacheChecksum_UsesWorkflowID(t *testing.T) {
	t.Setenv("CIRCLE_WORKFLOW_ID", "workflow-abc")
	assert.Equal(t, functest.New(t).CacheChecksum(), "workflow-abc")
}

func TestSandbox_CacheChecksum_VariesPerRunLocally(t *testing.T) {
	t.Setenv("CIRCLE_WORKFLOW_ID", "")
	s := functest.New(t)
	first, second := s.CacheChecksum(), s.CacheChecksum()
	assert.Assert(t, first != second, "expected a fresh value per call outside a job")
}

// go test exports GOROOT for the test process; a subprocess inheriting it would
// run an installed toolchain against the test runner's tools.
func TestSandbox_Env_ClearsGOROOT(t *testing.T) {
	t.Setenv("GOROOT", "/some/host/goroot")

	goroot, ok := functest.New(t).Env()["GOROOT"]
	assert.Assert(t, ok, "GOROOT must be present so it overrides the inherited value")
	assert.Equal(t, goroot, "")
}
