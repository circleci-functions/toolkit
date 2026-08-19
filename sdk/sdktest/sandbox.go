package sdktest

import (
	"context"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/circleci-functions/toolkit/sdk"
)

// Sandbox is an isolated environment for testing a function end to end.
//
// A function typically installs tools under $HOME, symlinks binaries into a
// directory on PATH, appends exports to $BASH_ENV, and caches the result. Run
// against real defaults on a developer's machine that overwrites their home
// directory, their /usr/local/bin and a shared CircleCI cache key. A Sandbox
// points all of them at temp locations, so the same test body is safe to run
// locally and honest to run in CI.
type Sandbox struct {
	// Home stands in for $HOME, and is set as $HOME for the duration of the test.
	Home string
	// BashEnv is the $BASH_ENV file the function under test appends to. It does
	// not exist until something writes to it.
	BashEnv string
	// BinDir stands in for a function's default symlink target, typically
	// /usr/local/bin. Pass it to your function's --bin-dir equivalent.
	BinDir string
	// InstallDir stands in for a function's default install root. Pass it to
	// your function's --install-dir equivalent.
	InstallDir string
	// Client performs cache operations against whichever cache this sandbox uses.
	Client *sdk.Client
	// Cache is the in-memory cache backing this sandbox, or nil when running
	// against CircleCI's own cache (see WithLiveCache).
	Cache *Server
}

// SandboxOption configures a Sandbox.
type SandboxOption func(*sandboxConfig)

type sandboxConfig struct {
	liveCache bool
}

// WithLiveCache uses CircleCI's cache rather than the in-memory one, so the
// behaviour under test is the real thing.
//
// Only available to a test running as a step inside a CircleCI job; elsewhere
// the sandbox uses the in-memory cache and leaves Sandbox.Cache non-nil. One
// suite can therefore be exact in CI and still run on a laptop.
func WithLiveCache() SandboxOption {
	return func(c *sandboxConfig) {
		c.liveCache = true
	}
}

// NewSandbox returns a Sandbox and points $HOME and $BASH_ENV at it for the
// duration of t. It must not be called from a parallel test.
func NewSandbox(t *testing.T, opts ...SandboxOption) *Sandbox {
	t.Helper()

	cfg := &sandboxConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	home := t.TempDir()
	s := &Sandbox{
		Home:       home,
		BashEnv:    filepath.Join(t.TempDir(), "bash_env"),
		BinDir:     t.TempDir(),
		InstallDir: t.TempDir(),
	}

	t.Setenv("HOME", home)
	t.Setenv("BASH_ENV", s.BashEnv)

	if cfg.liveCache && sdk.InCircleCIJob() {
		s.Client = sdk.NewDefault()
		return s
	}

	s.Cache = NewServer(t)
	s.Cache.SetupEnv(t)
	s.Client = s.Cache.Client()
	return s
}

// Command returns an exec.Cmd that runs the function's binary inside this
// sandbox, so a subprocess test needs nothing beyond os/exec.
func (s *Sandbox) Command(ctx context.Context, bin string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...) //nolint:gosec
	cmd.Env = s.CmdEnv()
	return cmd
}

// CmdEnv returns the parent environment with this sandbox's overrides applied,
// in the form exec.Cmd.Env expects.
func (s *Sandbox) CmdEnv() []string {
	env := os.Environ()
	for k, v := range s.Env() {
		env = append(env, k+"="+v)
	}
	return env
}

// Env returns the environment overrides that put a subprocess in this sandbox.
// Prefer Command, which applies them for you.
//
// GOROOT is cleared: `go test` sets it for the test process, and a Go toolchain
// honours it over its own location — so leaving it set makes a toolchain the
// function under test installs run with the test runner's tools instead.
func (s *Sandbox) Env() map[string]string {
	env := map[string]string{
		"HOME":           s.Home,
		"BASH_ENV":       s.BashEnv,
		"XDG_CACHE_HOME": filepath.Join(s.Home, ".cache"),
		"GOROOT":         "",
	}
	if s.Cache != nil {
		env[sdk.PluginURLEnvVar] = s.Cache.URL()
	}
	return env
}

// EnvWith returns Env merged with extra, where extra wins.
func (s *Sandbox) EnvWith(extra map[string]string) map[string]string {
	env := s.Env()
	maps.Copy(env, extra)
	return env
}

// CacheChecksum returns a checksum component unique to this run, for tests that
// round-trip a real save and restore. See sdk.RunID for why a constant checksum
// is the wrong choice there.
func (s *Sandbox) CacheChecksum() string {
	return sdk.RunID()
}
