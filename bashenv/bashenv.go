// Package bashenv provides a helper for appending shell export statements
// to $BASH_ENV and optionally echoing them to an io.Writer.
package bashenv

import (
	"fmt"
	"os"
	"strings"
)

// ExportEnvVars appends vars to $BASH_ENV, each value single-quoted so a later
// step gets it exactly as given, with nothing in it expanded or run.
// Returns an error if $BASH_ENV is not set.
func ExportEnvVars(vars map[string]string) error {
	var sb strings.Builder
	for key, val := range vars {
		fmt.Fprintf(&sb, "export %s=%s\n", key, quote(val))
	}
	return appendToBashEnv(sb.String())
}

// AddToPath appends an export statement to $BASH_ENV that prepends path to
// $PATH. Returns an error if $BASH_ENV is not set.
func AddToPath(path string) error {
	return appendToBashEnv(fmt.Sprintf("export PATH=%s:$PATH\n", path))
}

// AddAlias sets aliases to be expanded in shell scripts and appends
// a shell alias definition to $BASH_ENV for each name:command
// pair, so that later steps in the same job resolve name as command.
// The command is single-quoted, so it is expanded when the alias runs rather
// than when $BASH_ENV is sourced.
// Returns an error if $BASH_ENV is not set.
func AddAlias(aliases map[string]string) error {
	var sb strings.Builder
	sb.WriteString("shopt -s expand_aliases\n")
	for k, v := range aliases {
		fmt.Fprintf(&sb, "alias %s=%s\n", k, quote(v))
	}
	return appendToBashEnv(sb.String())
}

// quote returns s as a single POSIX shell word that a shell reads back as s,
// expanding nothing in it. Within single quotes nothing is special, so each '
// closes the quotes, adds an escaped quote, and reopens them.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// appendToBashEnv appends content to $BASH_ENV.
// Returns an error if $BASH_ENV is not set.
func appendToBashEnv(content string) error {
	bashEnv := os.Getenv("BASH_ENV")
	if bashEnv == "" {
		return fmt.Errorf("BASH_ENV must be set to configure")
	}

	f, err := os.OpenFile(bashEnv, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	_, err = f.WriteString(content)
	return err
}
