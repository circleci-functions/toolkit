// Package bashenv provides a helper for appending shell export statements
// to $BASH_ENV and optionally echoing them to an io.Writer.
package bashenv

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// ErrNoBashEnv reports that $BASH_ENV is not set, so nothing can be passed to
// later steps. CircleCI always sets it. A caller exporting something the user
// asked for should fail on it; one exporting a convenience can warn and carry
// on.
var ErrNoBashEnv = errors.New("BASH_ENV must be set to export to later steps")

// ExportEnvVars appends vars to $BASH_ENV, each value single-quoted so a later
// step gets it exactly as given, with nothing in it expanded or run.
// Returns ErrNoBashEnv if $BASH_ENV is not set.
func ExportEnvVars(vars map[string]string) error {
	var sb strings.Builder
	for key, val := range vars {
		fmt.Fprintf(&sb, "export %s=%s\n", key, quote(val))
	}
	return appendToBashEnv(sb.String())
}

// AddToPath appends an export statement to $BASH_ENV that prepends path to
// $PATH. Returns ErrNoBashEnv if $BASH_ENV is not set.
func AddToPath(path string) error {
	return appendToBashEnv(fmt.Sprintf("export PATH=%s:$PATH\n", path))
}

// AddAlias sets aliases to be expanded in shell scripts and appends
// a shell alias definition to $BASH_ENV for each name:command
// pair, so that later steps in the same job resolve name as command.
// The command is single-quoted, so it is expanded when the alias runs rather
// than when $BASH_ENV is sourced.
// Returns ErrNoBashEnv if $BASH_ENV is not set.
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

// appendToBashEnv appends the lines of content to $BASH_ENV, skipping any
// that would change nothing, so a function run twice in a job does not stack
// up copies of its exports.
// Returns ErrNoBashEnv if $BASH_ENV is not set.
func appendToBashEnv(content string) error {
	bashEnv := os.Getenv("BASH_ENV")
	if bashEnv == "" {
		return ErrNoBashEnv
	}

	existing, err := os.ReadFile(bashEnv) //nolint:gosec // $BASH_ENV is the job's own file
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	content = withoutRepeats(string(existing), content)
	if content == "" {
		return nil
	}
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		content = "\n" + content
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

// withoutRepeats returns the lines of content that existing doesn't already
// leave in effect. A line is a repeat when the last line setting the same
// export or alias, or the identical line for anything else, is the same: an
// export of PATH that has since been prepended to again is not a repeat, since
// writing it again puts that directory back in front.
func withoutRepeats(existing, content string) string {
	last := map[string]string{}
	for line := range strings.Lines(existing) {
		line = strings.TrimSpace(line)
		last[settingKey(line)] = line
	}

	var sb strings.Builder
	for line := range strings.Lines(content) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key := settingKey(line)
		if prev, ok := last[key]; ok && prev == line {
			continue
		}
		last[key] = line
		sb.WriteString(line + "\n")
	}
	return sb.String()
}

// settingKey names what line sets: "export NAME" or "alias NAME", or the
// line itself when it is neither.
func settingKey(line string) string {
	for _, kw := range []string{"export ", "alias "} {
		if rest, ok := strings.CutPrefix(line, kw); ok {
			if name, _, ok := strings.Cut(rest, "="); ok {
				return kw + name
			}
		}
	}
	return line
}
