package archive_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/archive"
)

func TestWriteFile(t *testing.T) {
	tests := []struct {
		name        string
		existing    string // pre-existing file content, empty means no pre-existing file
		content     string
		mode        os.FileMode
		wantContent string
		wantMode    os.FileMode
	}{
		{
			name:        "creates file with content",
			content:     "hello",
			mode:        0600,
			wantContent: "hello",
			wantMode:    0600,
		},
		{
			name:        "sets executable mode",
			content:     "",
			mode:        0750,
			wantContent: "",
			wantMode:    0750,
		},
		{
			name:        "truncates existing file",
			existing:    "old content",
			content:     "new",
			mode:        0600,
			wantContent: "new",
			wantMode:    0600,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "out")
			if tt.existing != "" {
				assert.NilError(t, os.WriteFile(path, []byte(tt.existing), 0600))
			}

			assert.NilError(t, archive.WriteFile(path, strings.NewReader(tt.content), tt.mode))

			got, err := os.ReadFile(path)
			assert.NilError(t, err)
			assert.Equal(t, string(got), tt.wantContent)

			info, err := os.Stat(path)
			assert.NilError(t, err)
			assert.Equal(t, info.Mode()&tt.wantMode, tt.wantMode)
		})
	}
}
