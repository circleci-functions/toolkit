package sdktest_test

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/sdk/sdktest"
)

// skipUnlessSh skips tests that need a POSIX shell to stand in for a command
// on PATH. sdktest itself is exercised on Linux and macOS; what it does for a
// function author on Windows is covered by that author's own suite.
func skipUnlessSh(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
}

func TestLaterStep_RunsInAFreshProcess(t *testing.T) {
	skipUnlessSh(t)

	out, err := sdktest.LaterStep(t, "sh", "-c", "echo resolved")
	assert.NilError(t, err)
	assert.Equal(t, strings.TrimSpace(out), "resolved")
}

// A Go toolchain honours $GOROOT over its own location, and `go test` exports
// it to the test process. Passing it on would let a suite resolve the toolchain
// that ran the tests while reporting that it resolved the one under test.
func TestLaterStep_DropsGoRoot(t *testing.T) {
	skipUnlessSh(t)
	t.Setenv("GOROOT", "/somewhere/that/would/win")

	out, err := sdktest.LaterStep(t, "sh", "-c", `echo "[${GOROOT-unset}]"`)
	assert.NilError(t, err)
	assert.Equal(t, strings.TrimSpace(out), "[unset]",
		"GOROOT must be dropped rather than emptied, so a child cannot tell it from unset")
}

func TestLaterStep_PassesTheRestOfTheEnvironment(t *testing.T) {
	skipUnlessSh(t)
	t.Setenv("SDKTEST_KEPT", "kept")

	out, err := sdktest.LaterStep(t, "sh", "-c", `echo "$SDKTEST_KEPT"`)
	assert.NilError(t, err)
	assert.Equal(t, strings.TrimSpace(out), "kept")
}

// The failure a caller is usually asserting against: the function under test
// put nothing on PATH, so a later step cannot find it by name.
func TestLaterStep_ReportsANameThatIsNotOnPath(t *testing.T) {
	_, err := sdktest.LaterStep(t, "sdktest-no-such-command")
	assert.Assert(t, err != nil, "expected an error for a command that is not on PATH")
}

func TestLaterStep_ReportsTheCommandsOwnFailure(t *testing.T) {
	skipUnlessSh(t)

	out, err := sdktest.LaterStep(t, "sh", "-c", "echo to stderr >&2; exit 3")
	assert.Assert(t, err != nil, "expected the command's non-zero exit to surface")
	assert.Assert(t, strings.Contains(out, "to stderr"), "combined output must include stderr, got %q", out)
}

// Guards the doc comment's claim that the helper leaves the test process alone.
func TestLaterStep_DoesNotChangeTheTestProcessEnvironment(t *testing.T) {
	skipUnlessSh(t)
	t.Setenv("GOROOT", "/still/here")

	_, err := sdktest.LaterStep(t, "sh", "-c", "true")
	assert.NilError(t, err)
	assert.Equal(t, os.Getenv("GOROOT"), "/still/here")
}
