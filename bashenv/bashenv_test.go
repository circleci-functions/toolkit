package bashenv_test

import (
	"os"
	"path/filepath"
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
	assert.Equal(t, string(got), "export FOO=bar\n")
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
	assert.Assert(t, strings.Contains(string(got), "export FOO=bar\n"))
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
	assert.Equal(t, string(got), "shopt -s expand_aliases\nalias ll=\"ls -la\"\n")
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
	assert.Assert(t, strings.Contains(string(got), "alias ll=\"ls -la\"\n"))
}

func TestAddAlias_NoBashEnv(t *testing.T) {
	t.Setenv("BASH_ENV", "")

	err := bashenv.AddAlias(map[string]string{"ll": "ls -la"})
	assert.ErrorContains(t, err, "BASH_ENV must be set")
}
