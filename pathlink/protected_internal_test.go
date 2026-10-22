package pathlink

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestRootCanChange(t *testing.T) {
	for _, tc := range []struct {
		goos, dir string
		blocked   bool
	}{
		{goos: "darwin", dir: "/usr/bin", blocked: true},
		{goos: "darwin", dir: "/bin", blocked: true},
		{goos: "darwin", dir: "/sbin", blocked: true},
		{goos: "darwin", dir: "/usr/sbin/", blocked: true},
		{goos: "darwin", dir: "/usr/local/bin"},
		{goos: "darwin", dir: "/opt/homebrew/bin"},
		{goos: "linux", dir: "/usr/bin"},
	} {
		t.Run(tc.goos+tc.dir, func(t *testing.T) {
			err := rootCanChange(tc.goos, tc.dir)
			if tc.blocked {
				assert.ErrorIs(t, err, errProtected)
				return
			}
			assert.NilError(t, err)
		})
	}
}
