package sdktest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/circleci-functions/toolkit/sdk"
)

// CacheModeEnvVar selects which cache a sandbox built WithLiveCache uses.
// Set it to "live" to use CircleCI's own cache; anything else, or unset, uses
// the in-memory one.
//
// The choice belongs to whoever runs the tests, not to the environment they
// happen to run in: a suite that silently falls back reports success in CI
// having exercised nothing real.
const CacheModeEnvVar = "TOOLKIT_CACHE_MODE"

// liveCacheMode is the CacheModeEnvVar value that asks for CircleCI's cache.
const liveCacheMode = "live"

// sandboxSeq numbers sandboxes within a process, so two in one test still get
// distinct cache keys.
var sandboxSeq atomic.Uint64

// Sandbox is an isolated environment for testing a function end to end.
//
// A function runs as a job step, so its defaults point at the places a job owns:
// $HOME, $BASH_ENV, somewhere on PATH, a cache key. Run against those defaults
// on a developer's machine, a test overwrites the developer's home directory,
// their /usr/local/bin and a shared cache key. A Sandbox points them at temp
// locations, so the same test body is safe to run locally and honest to run in
// CI.
//
// It makes no assumptions about what a function does. Use the fields that apply
// and ignore the rest; add anything else your function needs to the map Env
// returns.
type Sandbox struct {
	// Home stands in for $HOME.
	Home string
	// BashEnv is the $BASH_ENV file a function appends to, if it exports
	// anything to later job steps. It does not exist until something writes to
	// it.
	BashEnv string
	// BinDir is an empty directory standing in for a location on PATH that a
	// function writes to, typically /usr/local/bin. Pass it to the flag your
	// function takes for that, if it has one.
	BinDir string
	// InstallDir is an empty directory standing in for a root a function
	// unpacks or writes content under. Pass it to the flag your function takes
	// for that, if it has one.
	InstallDir string
	// Client performs cache operations against whichever cache this sandbox uses.
	Client *sdk.Client
	// Cache is the in-memory cache backing this sandbox, or nil when running
	// against CircleCI's own cache (see WithLiveCache).
	Cache *Server
	// CacheKeyNonce is a cache-key component unique to this sandbox: unique per
	// job run, and per sandbox within a run.
	//
	// Cache keys are write-once, so a round-trip asserted under a fixed key
	// passes once and then restores whatever the first run left behind. Include
	// this in any key a test saves under. It is a field rather than a method
	// because a save and its matching restore must agree on it.
	CacheKeyNonce string
}

// SandboxOption configures a Sandbox.
type SandboxOption func(*sandboxConfig)

type sandboxConfig struct {
	liveCache bool
}

// WithLiveCache uses CircleCI's cache rather than the in-memory one, so the
// behaviour under test is the real thing.
//
// The in-memory cache mirrors CircleCI's, but only the real one is
// authoritative. Which is used is the caller's choice, made by setting
// CacheModeEnvVar, so one suite can run in-memory on a laptop and against the
// real cache in CI without a change to the test body. If live is asked for and
// unreachable, NewSandbox fails the test rather than quietly substituting the
// in-memory cache and reporting a pass.
func WithLiveCache() SandboxOption {
	return func(c *sandboxConfig) {
		c.liveCache = true
	}
}

// NewSandbox returns a Sandbox holding temp stand-ins for the locations a
// function writes to. Pass Env to a subprocess to put it in the sandbox; call
// ApplyToProcess for a test that exercises the function in-process.
func NewSandbox(t *testing.T, opts ...SandboxOption) *Sandbox {
	t.Helper()

	cfg := &sandboxConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	home := t.TempDir()
	s := &Sandbox{
		Home:          home,
		BashEnv:       filepath.Join(t.TempDir(), "bash_env"),
		BinDir:        t.TempDir(),
		InstallDir:    t.TempDir(),
		CacheKeyNonce: cacheKeyNonce(t),
	}

	useLive, err := useLiveCache(cfg.liveCache, os.Getenv(CacheModeEnvVar), sdk.InCircleCIJob)
	if err != nil {
		t.Fatal(err)
	}
	if useLive {
		s.Client = sdk.NewDefault()
		return s
	}

	s.Cache = NewServer(t)
	s.Client = s.Cache.Client()
	return s
}

// useLiveCache decides which cache a sandbox uses, and reports an error rather
// than falling back when the live cache was asked for and is unreachable.
func useLiveCache(offered bool, mode string, reachable func() bool) (bool, error) {
	if !offered || mode != liveCacheMode {
		return false, nil
	}
	if !reachable() {
		return false, fmt.Errorf(
			"%s=%s, but CircleCI's cache is not reachable: this test must run as a step inside a CircleCI job",
			CacheModeEnvVar, liveCacheMode)
	}
	return true, nil
}

// LiveCache reports whether this sandbox is using CircleCI's own cache.
func (s *Sandbox) LiveCache() bool {
	return s.Cache == nil
}

// ApplyToProcess points this process's $HOME and $BASH_ENV at the sandbox for
// the duration of t, for a test that calls the function's code directly rather
// than running its binary. A subprocess needs Env instead.
//
// It mutates process-wide state, so a test that calls it must not be parallel.
func (s *Sandbox) ApplyToProcess(t *testing.T) {
	t.Helper()

	for k, v := range s.Env() {
		t.Setenv(k, v)
	}
}

// Env returns the environment that puts a process in this sandbox. Merge it
// over the environment a subprocess would otherwise get — with runner's
// WithSandboxEnv, or by hand for a plain exec.Cmd:
//
//	cmd := exec.CommandContext(ctx, bin, args...)
//	cmd.Env = sandbox.CmdEnv(os.Environ())
func (s *Sandbox) Env() map[string]string {
	env := map[string]string{
		"HOME":           s.Home,
		"BASH_ENV":       s.BashEnv,
		"XDG_CACHE_HOME": filepath.Join(s.Home, ".cache"),
	}
	if s.Cache != nil {
		env[sdk.PluginURLEnvVar] = s.Cache.URL()
	}
	return env
}

// CmdEnv returns env with this sandbox's entries applied on top, in the form
// exec.Cmd.Env expects. Entries in env that the sandbox overrides are dropped
// rather than shadowed, so the result holds one value per name.
func (s *Sandbox) CmdEnv(env []string) []string {
	override := s.Env()

	out := make([]string, 0, len(env)+len(override))
	for _, kv := range env {
		name, _, ok := strings.Cut(kv, "=")
		if ok {
			if _, replaced := override[name]; replaced {
				continue
			}
		}
		out = append(out, kv)
	}
	for k, v := range override {
		out = append(out, k+"="+v)
	}
	return out
}

// cacheKeyNonce returns a cache-key component unique to this sandbox.
//
// Inside a job it is derived from the run, so keys a suite writes are visible
// to a later step of that same run and to nothing else. The test name and a
// sequence number keep two sandboxes in one run from colliding on a key, which
// write-once would otherwise resolve by handing one test the other's data.
func cacheKeyNonce(t *testing.T) string {
	seq := sandboxSeq.Add(1)

	run := os.Getenv("CIRCLE_WORKFLOW_ID")
	if run == "" {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			t.Fatalf("generate cache key nonce: %v", err)
		}
		run = hex.EncodeToString(b[:])
	}

	return fmt.Sprintf("%s-%s-%d", run, keyToken(t.Name()), seq)
}

// keyToken reduces s to characters safe to embed in a cache key.
func keyToken(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, s)
}
