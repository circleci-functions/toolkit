package bashenv_test

import (
	"os"
	"path/filepath"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/bashenv"
)

func writeBashEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "env.sh")
	assert.NilError(t, os.WriteFile(path, []byte(content), 0600))
	return path
}

func TestVars(t *testing.T) {
	tests := []struct {
		name    string
		content string
		base    map[string]string
		want    map[string]string
	}{
		{
			name:    "PlainExports",
			content: "export FOO=bar\nexport BAZ=qux\n",
			want:    map[string]string{"FOO": "bar", "BAZ": "qux"},
		},
		{
			name:    "LastExportWins",
			content: "export FOO=first\nexport FOO=second\n",
			want:    map[string]string{"FOO": "second"},
		},
		{
			name:    "ExpandsAgainstBase",
			content: "export PATH=/goroot/bin:$PATH\n",
			base:    map[string]string{"PATH": "/usr/bin"},
			want:    map[string]string{"PATH": "/goroot/bin:/usr/bin"},
		},
		{
			// Two AddToPath calls compose, which the single-line regex each
			// function repo hand-rolls gets wrong.
			name:    "ExpandsAgainstEarlierExports",
			content: "export PATH=/first/bin:$PATH\nexport PATH=/second/bin:$PATH\n",
			base:    map[string]string{"PATH": "/usr/bin"},
			want:    map[string]string{"PATH": "/second/bin:/first/bin:/usr/bin"},
		},
		{
			name:    "IgnoresAliasAndShopt",
			content: "shopt -s expand_aliases\nalias foo=\"bar baz\"\nexport FOO=bar\n",
			want:    map[string]string{"FOO": "bar"},
		},
		{
			name:    "StripsQuotes",
			content: "export FOO=\"bar baz\"\nexport BAZ='qux'\n",
			want:    map[string]string{"FOO": "bar baz", "BAZ": "qux"},
		},
		{
			name:    "SingleQuotedIsLiteral",
			content: "export FOO='$HOME stays'\n",
			base:    map[string]string{"HOME": "/home/me"},
			want:    map[string]string{"FOO": "$HOME stays"},
		},
		{
			name:    "EscapedSingleQuote",
			content: "export FOO='it'\\''s'\n",
			want:    map[string]string{"FOO": "it's"},
		},
		{
			name:    "EmptySingleQuoted",
			content: "export FOO=''\n",
			want:    map[string]string{"FOO": ""},
		},
		{
			name:    "EmptyValue",
			content: "export FOO=\n",
			want:    map[string]string{"FOO": ""},
		},
		{
			name:    "ValueContainingEquals",
			content: "export GOFLAGS=-mod=mod\n",
			want:    map[string]string{"GOFLAGS": "-mod=mod"},
		},
		{
			name:    "Empty",
			content: "",
			want:    map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := bashenv.Vars(writeBashEnv(t, tt.content), tt.base)
			assert.NilError(t, err)
			assert.DeepEqual(t, got, tt.want)
		})
	}
}

func TestVars_MissingFile(t *testing.T) {
	_, err := bashenv.Vars(filepath.Join(t.TempDir(), "absent"), nil)
	assert.ErrorContains(t, err, "read bash env")
}

// Round-trips against the writer, so the two stay in step.
func TestVars_ReadsWhatAddToPathWrote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.sh")
	t.Setenv("BASH_ENV", path)
	t.Setenv("PATH", "/usr/bin")

	assert.NilError(t, bashenv.AddToPath("/goroot/bin"))
	assert.NilError(t, bashenv.ExportEnvVars(map[string]string{"GOPATH": "/go"}))

	got, err := bashenv.Path(path)
	assert.NilError(t, err)
	assert.Equal(t, got, "/goroot/bin:/usr/bin")

	vars, err := bashenv.Vars(path, nil)
	assert.NilError(t, err)
	assert.Equal(t, vars["GOPATH"], "/go")
}

// ExportEnvVars quotes values for bash, and Vars has to undo exactly that.
func TestVars_ReadsBackWhatExportEnvVarsWrote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "env.sh")
	t.Setenv("BASH_ENV", path)
	want := map[string]string{
		"SPECIAL": `a b$(echo pwn)'c`,
		"QUOTES":  `'"'`,
		"EMPTY":   "",
		"REF":     "$PATH",
	}

	assert.NilError(t, bashenv.ExportEnvVars(want))

	got, err := bashenv.Vars(path, map[string]string{"PATH": "/usr/bin"})
	assert.NilError(t, err)
	assert.DeepEqual(t, got, want)
}

func TestPath_NoPathExport_FallsBackToProcessPath(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")

	got, err := bashenv.Path(writeBashEnv(t, "export FOO=bar\n"))
	assert.NilError(t, err)
	assert.Equal(t, got, "/usr/bin")
}
