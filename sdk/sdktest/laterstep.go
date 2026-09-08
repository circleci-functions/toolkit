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
// A Sandbox keeps a function under test from writing outside temp directories,
// which is what makes a suite safe to run on a laptop. Some of what a function
// does only happens without one: a function that puts a binary on PATH has to
// pick a directory PATH already holds, and which directory that is belongs to
// the executor rather than the sandbox. A test for that has to run unsandboxed,
// so it can only run where the machine is disposable.
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
// PATH, and returns its combined output.
//
// That is what a later step in the same job amounts to for a function that puts
// something on PATH without exporting anything to a shell: a new process that
// sourced nothing and looks the name up. Asserting on it catches a function
// whose effect reached the step that ran it and nothing after.
//
// $GOROOT is dropped from the child's environment. `go test` exports it to the
// test process, and a Go toolchain honours it over its own location, so a suite
// that left it set could pass while resolving the toolchain that ran the tests
// rather than the one under test.
//
// To check a shell that a function's exports never reach — PowerShell on
// Windows, where $BASH_ENV does nothing — run that shell as the command:
//
//	out, err := sdktest.LaterStep(t, "powershell.exe", "-Command", "mytool --version")
func LaterStep(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), name, args...) //nolint:gosec // the caller names the command
	cmd.Env = withoutVars(os.Environ(), "GOROOT")

	out, err := cmd.CombinedOutput()
	return string(out), err
}

// withoutVars returns env without the named variables, dropping them rather
// than emptying them so a child cannot tell them apart from unset.
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
