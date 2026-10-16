package bashenv_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/circleci-functions/toolkit/bashenv"

	"gotest.tools/v3/assert"
)

func TestExportEnvVars_ToBashEnv(t *testing.T) {
	bashEnvPath := filepath.Join(t.TempDir(), "env.sh")
	t.Setenv("BASH_ENV", bashEnvPath)

	assert.NilError(t, bashenv.ExportEnvVars(map[string]string{"FOO": "bar"}))

	got, err := os.ReadFile(bashEnvPath)
	assert.NilError(t, err)
	assert.Equal(t, string(got), "export FOO='bar'\n")
}

func TestExportEnvVars_QuotesValues(t *testing.T) {
	bashEnvPath := filepath.Join(t.TempDir(), "env.sh")
	t.Setenv("BASH_ENV", bashEnvPath)

	assert.NilError(t, bashenv.ExportEnvVars(map[string]string{"VALUE": `a b$(echo pwn)'c`}))

	got, err := os.ReadFile(bashEnvPath)
	assert.NilError(t, err)
	assert.Equal(t, string(got), `export VALUE='a b$(echo pwn)'\''c'`+"\n")
}

// What a later step sees is what bash makes of the file, so ask bash.
func TestExportEnvVars_BashReadsBackTheValue(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("needs bash")
	}
	dir := t.TempDir()
	bashEnvPath := filepath.Join(dir, "env.sh")
	t.Setenv("BASH_ENV", bashEnvPath)

	ran := filepath.Join(dir, "ran")
	values := []string{
		"",
		"plain",
		"has space",
		`$HOME and ${HOME}`,
		"`touch " + ran + "` $(touch " + ran + ")",
		`it's "quoted" \ back\slash`,
		"''",
		"tab\tand;semi&|pipe*glob",
	}
	for i, v := range values {
		assert.NilError(t, bashenv.ExportEnvVars(map[string]string{fmt.Sprintf("V%d", i): v}))
	}

	for i, want := range values {
		out, err := exec.Command(bash, "-c", fmt.Sprintf(`printf %%s "$V%d"`, i)).Output() //nolint:gosec // a fixed script
		assert.NilError(t, err)
		assert.Equal(t, string(out), want)
	}
	_, err = os.Stat(ran)
	assert.Assert(t, os.IsNotExist(err), "sourcing $BASH_ENV must not run anything in a value")
}

// The file can hold a token, so a file this creates is for the job's user
// only.
func TestExportEnvVars_CreatesTheFilePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes")
	}
	bashEnvPath := filepath.Join(t.TempDir(), "env.sh")
	t.Setenv("BASH_ENV", bashEnvPath)

	assert.NilError(t, bashenv.ExportEnvVars(map[string]string{"FOO": "bar"}))

	info, err := os.Stat(bashEnvPath)
	assert.NilError(t, err)
	assert.Equal(t, info.Mode().Perm(), os.FileMode(0o600))
}

func TestExportEnvVars_Append(t *testing.T) {
	bashEnvPath := filepath.Join(t.TempDir(), "env.sh")
	if err := os.WriteFile(bashEnvPath, []byte("# existing\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BASH_ENV", bashEnvPath)

	assert.NilError(t, bashenv.ExportEnvVars(map[string]string{"FOO": "bar"}))

	got, err := os.ReadFile(bashEnvPath)
	assert.NilError(t, err)
	assert.Assert(t, strings.HasPrefix(string(got), "# existing\n"), "ExportEnvVars must append, not overwrite; got:\n%s", string(got))
	assert.Assert(t, strings.Contains(string(got), "export FOO='bar'\n"))
}

func TestExportEnvVars_NoBashEnv(t *testing.T) {
	t.Setenv("BASH_ENV", "")

	err := bashenv.ExportEnvVars(map[string]string{"FOO": "bar"})
	assert.ErrorContains(t, err, "BASH_ENV must be set")
}

func TestExportEnvVars_BadPath(t *testing.T) {
	t.Setenv("BASH_ENV", "/nonexistent/dir/env.sh")

	err := bashenv.ExportEnvVars(map[string]string{"FOO": "bar"})
	assert.Assert(t, err != nil, "expected error for bad path")
}

func TestAddToPath_ToBashEnv(t *testing.T) {
	bashEnvPath := filepath.Join(t.TempDir(), "env.sh")
	t.Setenv("BASH_ENV", bashEnvPath)

	assert.NilError(t, bashenv.AddToPath("/usr/local/go/bin"))

	got, err := os.ReadFile(bashEnvPath)
	assert.NilError(t, err)
	assert.Equal(t, string(got), "export PATH=/usr/local/go/bin:$PATH\n")
}

func TestAddToPath_Append(t *testing.T) {
	bashEnvPath := filepath.Join(t.TempDir(), "env.sh")
	if err := os.WriteFile(bashEnvPath, []byte("# existing\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BASH_ENV", bashEnvPath)

	assert.NilError(t, bashenv.AddToPath("/usr/local/go/bin"))

	got, err := os.ReadFile(bashEnvPath)
	assert.NilError(t, err)
	assert.Assert(t, strings.HasPrefix(string(got), "# existing\n"), "AddToPath must append, not overwrite; got:\n%s", string(got))
	assert.Assert(t, strings.Contains(string(got), "export PATH=/usr/local/go/bin:$PATH\n"))
}

func TestAddToPath_NoBashEnv(t *testing.T) {
	t.Setenv("BASH_ENV", "")

	err := bashenv.AddToPath("/usr/local/go/bin")
	assert.ErrorContains(t, err, "BASH_ENV must be set")
}

func TestAddAlias_ToBashEnv(t *testing.T) {
	bashEnvPath := filepath.Join(t.TempDir(), "env.sh")
	t.Setenv("BASH_ENV", bashEnvPath)

	assert.NilError(t, bashenv.AddAlias(map[string]string{"ll": "ls -la"}))

	got, err := os.ReadFile(bashEnvPath)
	assert.NilError(t, err)
	assert.Equal(t, string(got), "shopt -s expand_aliases\nalias ll='ls -la'\n")
}

// Double quotes, which %q gave, still let bash expand $ and backticks when it
// sources the file.
func TestAddAlias_QuotesTheCommand(t *testing.T) {
	bashEnvPath := filepath.Join(t.TempDir(), "env.sh")
	t.Setenv("BASH_ENV", bashEnvPath)

	assert.NilError(t, bashenv.AddAlias(map[string]string{"greet": `echo "it's $USER"`}))

	got, err := os.ReadFile(bashEnvPath)
	assert.NilError(t, err)
	assert.Equal(t, string(got), "shopt -s expand_aliases\nalias greet='echo \"it'\\''s $USER\"'\n")
}

func TestAddAlias_Append(t *testing.T) {
	bashEnvPath := filepath.Join(t.TempDir(), "env.sh")
	if err := os.WriteFile(bashEnvPath, []byte("# existing\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BASH_ENV", bashEnvPath)

	assert.NilError(t, bashenv.AddAlias(map[string]string{"ll": "ls -la"}))

	got, err := os.ReadFile(bashEnvPath)
	assert.NilError(t, err)
	assert.Assert(t, strings.HasPrefix(string(got), "# existing\n"), "AddAlias must append, not overwrite; got:\n%s", string(got))
	assert.Assert(t, strings.Contains(string(got), "alias ll='ls -la'\n"))
}

func TestAddAlias_NoBashEnv(t *testing.T) {
	t.Setenv("BASH_ENV", "")

	err := bashenv.AddAlias(map[string]string{"ll": "ls -la"})
	assert.ErrorContains(t, err, "BASH_ENV must be set")
}
