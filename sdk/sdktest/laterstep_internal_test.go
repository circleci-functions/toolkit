package sdktest

import (
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

// SkipUnlessInJob's decision, without the t.Skip that makes it unobservable
// from a test.
func TestSkipReason(t *testing.T) {
	assert.Equal(t, skipReason(true), "", "inside a job there is nothing to skip")

	reason := skipReason(false)
	assert.Assert(t, strings.Contains(reason, "CircleCI job"),
		"the reason has to say what is missing, got %q", reason)
	assert.Assert(t, strings.Contains(reason, "unsandboxed"),
		"and why the test needs it, got %q", reason)
}
