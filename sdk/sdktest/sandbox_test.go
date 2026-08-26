package sdktest_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/bashenv"
	"github.com/circleci-functions/toolkit/sdk"
	"github.com/circleci-functions/toolkit/sdk/sdktest"
)

func TestSandbox_DirsAreDistinctAndWritable(t *testing.T) {
	s := sdktest.NewSandbox(t)

	seen := map[string]bool{}
	for _, dir := range []string{s.Home, s.BinDir, s.InstallDir} {
		assert.Assert(t, !seen[dir], "sandbox dirs must be distinct, got %q twice", dir)
		seen[dir] = true
		assert.NilError(t, os.WriteFile(filepath.Join(dir, "probe"), []byte("ok"), 0600))
	}
}

// NewSandbox must not touch process-wide state: a suite that only runs
// subprocesses should stay parallel-safe.
func TestSandbox_New_LeavesProcessEnvAlone(t *testing.T) {
	realHome, err := os.UserHomeDir()
	assert.NilError(t, err)

	s := sdktest.NewSandbox(t)

	home, err := os.UserHomeDir()
	assert.NilError(t, err)
	assert.Equal(t, home, realHome, "NewSandbox must not redirect $HOME on its own")
	assert.Assert(t, home != s.Home)
}

func TestSandbox_ApplyToProcess_RedirectsHomeAndBashEnv(t *testing.T) {
	realHome, err := os.UserHomeDir()
	assert.NilError(t, err)

	s := sdktest.NewSandbox(t)
	s.ApplyToProcess(t)

	home, err := os.UserHomeDir()
	assert.NilError(t, err)
	assert.Equal(t, home, s.Home)
	assert.Assert(t, home != realHome, "sandbox must not resolve to the real home directory")
	assert.Equal(t, os.Getenv("BASH_ENV"), s.BashEnv)
}

func TestSandbox_ApplyToProcess_BashEnvIsReadableByBashenv(t *testing.T) {
	s := sdktest.NewSandbox(t)
	s.ApplyToProcess(t)
	t.Setenv("PATH", "/usr/bin")

	assert.NilError(t, bashenv.AddToPath(filepath.Join(s.InstallDir, "bin")))

	got, err := bashenv.Path(s.BashEnv)
	assert.NilError(t, err)
	assert.Equal(t, got, filepath.Join(s.InstallDir, "bin")+":/usr/bin")
}

func TestSandbox_Env(t *testing.T) {
	s := sdktest.NewSandbox(t)
	env := s.Env()

	assert.Equal(t, env["HOME"], s.Home)
	assert.Equal(t, env["BASH_ENV"], s.BashEnv)
	assert.Equal(t, env[sdk.PluginURLEnvVar], s.Cache.URL())
}

// CmdEnv must replace an inherited value rather than append a second entry for
// the same name: a subprocess reading $HOME should not depend on which of two
// entries its libc happens to pick.
func TestSandbox_CmdEnv_ReplacesInheritedEntries(t *testing.T) {
	s := sdktest.NewSandbox(t)

	env := s.CmdEnv([]string{"HOME=/real/home", "KEEP=1"})

	assert.Equal(t, countPrefix(env, "HOME="), 1, "expected exactly one HOME entry in %q", env)
	assert.Assert(t, slices.Contains(env, "HOME="+s.Home))
	assert.Assert(t, slices.Contains(env, "KEEP=1"), "unrelated entries must be preserved")
}

// The sandbox must be usable with nothing but os/exec, since a function repo may
// depend on the SDK alone.
func TestSandbox_CmdEnv_AppliesToPlainExecCmd(t *testing.T) {
	s := sdktest.NewSandbox(t)

	cmd := exec.CommandContext(t.Context(), "sh", "-c", `echo "$HOME|$BASH_ENV"`)
	cmd.Env = s.CmdEnv(os.Environ())

	out, err := cmd.Output()
	assert.NilError(t, err)
	assert.Equal(t, strings.TrimSpace(string(out)), s.Home+"|"+s.BashEnv)
}

func TestSandbox_ClientRoundTripsCache(t *testing.T) {
	s := sdktest.NewSandbox(t)
	ctx := context.Background()

	dir := filepath.Join(s.Home, "cache")
	assert.NilError(t, os.MkdirAll(dir, 0750))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "marker"), []byte("hit"), 0600))

	key := "test-" + s.CacheKeyNonce
	assert.NilError(t, s.Client.SaveCache(ctx, key, []string{dir}))
	assert.NilError(t, os.RemoveAll(dir))

	_, err := s.Client.RestoreCache(ctx, []string{key})
	assert.NilError(t, err)

	got, err := os.ReadFile(filepath.Join(dir, "marker"))
	assert.NilError(t, err)
	assert.Equal(t, string(got), "hit")
}

// The nonce must be stable within a sandbox, or a save and its matching restore
// would disagree on the key.
func TestSandbox_CacheKeyNonce_IsStable(t *testing.T) {
	s := sdktest.NewSandbox(t)
	assert.Equal(t, s.CacheKeyNonce, s.CacheKeyNonce)
	assert.Assert(t, s.CacheKeyNonce != "")
}

// Two sandboxes must not share a nonce even inside one job run, where the run
// component is identical. Write-once would otherwise hand the second test
// whatever the first one saved.
func TestSandbox_CacheKeyNonce_DistinctWithinAJobRun(t *testing.T) {
	t.Setenv("CIRCLE_WORKFLOW_ID", "workflow-abc")

	first := sdktest.NewSandbox(t).CacheKeyNonce
	second := sdktest.NewSandbox(t).CacheKeyNonce

	assert.Assert(t, first != second, "expected distinct nonces, got %q twice", first)
	for _, n := range []string{first, second} {
		assert.Assert(t, strings.HasPrefix(n, "workflow-abc-"), "expected the run to scope the nonce, got %q", n)
	}
}

// Distinct across tests too, not just across sandboxes in one test.
func TestSandbox_CacheKeyNonce_DistinctAcrossTests(t *testing.T) {
	t.Setenv("CIRCLE_WORKFLOW_ID", "workflow-abc")

	nonces := map[string]bool{}
	for _, name := range []string{"A", "B"} {
		t.Run(name, func(t *testing.T) {
			n := sdktest.NewSandbox(t).CacheKeyNonce
			assert.Assert(t, !nonces[n], "expected a distinct nonce per test, got %q twice", n)
			nonces[n] = true
			assert.Assert(t, strings.Contains(n, name), "expected the test name in the nonce, got %q", n)
		})
	}
}

func TestSandbox_CacheKeyNonce_HasNoRunIDOutsideAJob(t *testing.T) {
	t.Setenv("CIRCLE_WORKFLOW_ID", "")

	n := sdktest.NewSandbox(t).CacheKeyNonce
	assert.Assert(t, n != "", "expected a generated nonce outside a job")
	assert.Assert(t, !strings.HasPrefix(n, "-"), "expected a generated run component, got %q", n)
}

// Without the opt-in, WithLiveCache uses the in-memory cache — so a suite
// written for CI still runs unchanged on a laptop.
func TestSandbox_WithLiveCache_NotRequested_UsesInMemoryCache(t *testing.T) {
	t.Setenv(sdktest.CacheModeEnvVar, "")

	s := sdktest.NewSandbox(t, sdktest.WithLiveCache())

	assert.Assert(t, s.Cache != nil, "expected the in-memory cache when live was not requested")
	assert.Assert(t, !s.LiveCache())
	assert.Equal(t, s.Env()[sdk.PluginURLEnvVar], s.Cache.URL())
}

// WithLiveCache is only consulted when asked for, so an unrelated value cannot
// switch a suite over by accident.
func TestSandbox_WithLiveCache_UnrecognisedMode_UsesInMemoryCache(t *testing.T) {
	t.Setenv(sdktest.CacheModeEnvVar, "yes-please")

	assert.Assert(t, sdktest.NewSandbox(t, sdktest.WithLiveCache()).Cache != nil)
}

// The mode is the caller's choice, not something inferred from the environment:
// a sandbox that has not asked for the live cache must keep the in-memory one
// even when a real one is reachable.
func TestSandbox_WithoutLiveCache_IgnoresAReachableLiveCache(t *testing.T) {
	t.Setenv(sdktest.CacheModeEnvVar, "live")
	t.Setenv(sdk.PluginURLEnvVar, "http://example.invalid")

	s := sdktest.NewSandbox(t)

	assert.Assert(t, s.Cache != nil, "expected the in-memory cache without WithLiveCache")
	assert.Equal(t, s.Env()[sdk.PluginURLEnvVar], s.Cache.URL())
}

func countPrefix(env []string, prefix string) int {
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			n++
		}
	}
	return n
}
