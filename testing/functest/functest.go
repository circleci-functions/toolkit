// Package functest provides an isolated environment for testing a CircleCI
// function end to end.
//
// A setup-style function typically installs a toolchain under $HOME, symlinks
// binaries into a directory on PATH, appends exports to $BASH_ENV, and saves
// the result to the job cache. Run against real defaults on a developer's
// machine, that overwrites their home directory, their /usr/local/bin and a
// shared CircleCI cache key. Sandbox redirects all four at temp locations, so
// the same test body is safe to run locally and honest to run in CI.
package functest

import (
	"maps"
	"path/filepath"
	"testing"

	"github.com/circleci-functions/toolkit/bashenv"
	"github.com/circleci-functions/toolkit/sdk"
	"github.com/circleci-functions/toolkit/sdk/sdktest"
)

// Sandbox is a set of temp locations standing in for the real ones a function
// would touch, plus a cache server to save and restore against.
type Sandbox struct {
	// Home stands in for $HOME, and is set as $HOME for the duration of the test.
	Home string
	// BashEnv is the $BASH_ENV file the function under test appends to. The
	// file does not exist until something writes to it.
	BashEnv string
	// BinDir stands in for a function's default symlink target (typically
	// /usr/local/bin). Pass it to your function's --bin-dir equivalent.
	BinDir string
	// InstallDir stands in for a function's default install root. Pass it to
	// your function's --install-dir equivalent.
	InstallDir string
	// Client talks to whichever cache server this sandbox is using.
	Client *sdk.Client
	// Server is the fake cache server, or nil when using the real task-agent.
	Server *sdktest.Server
}

// Option configures a Sandbox.
type Option func(*config)

type config struct {
	realAgent bool
}

// WithRealAgent talks to the real CircleCI task-agent instead of the in-process
// fake, so cache behaviour under test is the real thing — including write-once
// keys and restore-key prefix matching.
//
// Outside a CircleCI job there is no agent to talk to, so the sandbox silently
// falls back to the fake. That lets one acceptance suite run both ways: real
// cache semantics in CI, still runnable on a laptop.
func WithRealAgent() Option {
	return func(c *config) {
		c.realAgent = true
	}
}

// New returns a Sandbox and points $HOME and $BASH_ENV at it for the duration
// of t. It must not be called from a parallel test.
func New(t *testing.T, opts ...Option) *Sandbox {
	t.Helper()

	cfg := &config{}
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

	s.Server = sdktest.NewServer(t)
	s.Server.SetupEnv(t)
	s.Client = s.Server.Client()
	return s
}

// Env returns the environment overrides that put a subprocess in this sandbox,
// for passing to testing/runner's WithUnsafeAdditionalEnvVars.
func (s *Sandbox) Env() map[string]string {
	env := map[string]string{
		"HOME":           s.Home,
		"BASH_ENV":       s.BashEnv,
		"XDG_CACHE_HOME": filepath.Join(s.Home, ".cache"),
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

// Path returns the PATH a later job step would see after sourcing this
// sandbox's $BASH_ENV — i.e. with whatever the function under test prepended.
func (s *Sandbox) Path(t *testing.T) string {
	t.Helper()
	p, err := bashenv.Path(s.BashEnv)
	if err != nil {
		t.Fatalf("read sandbox bash env: %v", err)
	}
	return p
}

// CacheChecksum returns a checksum component unique to this run, for tests
// that round-trip a real save and restore. See sdk.RunID for why a constant
// checksum is the wrong choice there.
func (s *Sandbox) CacheChecksum() string {
	return sdk.RunID()
}
