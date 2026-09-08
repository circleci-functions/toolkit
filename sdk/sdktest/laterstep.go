package sdktest

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/circleci-functions/toolkit/sdk"
)

// SkipUnlessInJob skips t unless it is running as a step inside a CircleCI job.
//
// For a test that has to run without a Sandbox — the directory a function picks
// off PATH belongs to the executor, not the sandbox — and so is only safe where
// the machine is disposable.
func SkipUnlessInJob(t *testing.T) {
	t.Helper()
	if reason := skipReason(sdk.InCircleCIJob()); reason != "" {
		t.Skip(reason)
	}
}

// skipReason returns why an unsandboxed test cannot run, or "" if it can.
func skipReason(inJob bool) string {
	if inJob {
		return ""
	}
	return "sdktest: this test runs unsandboxed, so it only runs as a step inside a CircleCI job"
}

// LaterStep runs name with args in a fresh process, resolving name through
// PATH, and returns its combined output. That is what a later step in the same
// job amounts to for a function that exports nothing to a shell.
//
// $GOROOT is dropped: `go test` exports it, and a Go toolchain honours it over
// its own location, so passing it on can resolve the toolchain that ran the
// tests instead of the one under test.
//
// Run a shell as the command to check one a function's exports never reach:
//
//	out, err := sdktest.LaterStep(t, "powershell.exe", "-Command", "mytool --version")
func LaterStep(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), name, args...) //nolint:gosec // the caller names the command
	cmd.Env = withoutVars(os.Environ(), "GOROOT")

	out, err := cmd.CombinedOutput()
	return string(out), err
}

// withoutVars drops the named variables rather than emptying them, so a child
// cannot tell them from unset.
func withoutVars(env []string, names ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(names, name) {
			out = append(out, kv)
		}
	}
	return out
}
