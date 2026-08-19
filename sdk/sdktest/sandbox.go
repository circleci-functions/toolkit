package sdktest

import (
	"maps"
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
	// Client talks to whichever cache server this sandbox is using.
	Client *sdk.Client
	// Server is the fake cache server, or nil when using the real task-agent.
	Server *Server
}

// SandboxOption configures a Sandbox.
type SandboxOption func(*sandboxConfig)

type sandboxConfig struct {
	realAgent bool
}

// WithRealAgent talks to the real CircleCI task-agent instead of the in-process
// fake, so cache behaviour under test is the real thing — including write-once
// keys and restore-key prefix matching.
//
// Outside a CircleCI job there is no agent to talk to, so the sandbox falls back
// to the fake and Sandbox.Server is non-nil. That lets one acceptance suite run
// both ways: real cache semantics in CI, still runnable on a laptop.
func WithRealAgent() SandboxOption {
	return func(c *sandboxConfig) {
		c.realAgent = true
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

	if cfg.realAgent && sdk.ServerAvailable() {
		s.Client = sdk.NewDefault()
		return s
	}

	s.Server = NewServer(t)
	s.Server.SetupEnv(t)
	s.Client = s.Server.Client()
	return s
}

// Env returns the environment overrides that put a subprocess in this sandbox.
// Prefer Runner, which applies them for you.
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
	if s.Server != nil {
		env["CIRCLECI_PLUGIN_URL"] = s.Server.URL()
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
