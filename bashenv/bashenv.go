// Package bashenv provides a helper for appending shell export statements
// to $BASH_ENV and optionally echoing them to an io.Writer.
package bashenv

import (
	"fmt"
	"os"
	"strings"
)

// ExportEnvVars appends vars to $BASH_ENV.
// Returns an error if $BASH_ENV is not set.
func ExportEnvVars(vars map[string]string) error {
	var sb strings.Builder
	for key, val := range vars {
		fmt.Fprintf(&sb, "export %s=%s\n", key, val)
	}
	return appendToBashEnv(sb.String())
}

// AddToPath appends an export statement to $BASH_ENV that prepends path to
// $PATH. Returns an error if $BASH_ENV is not set.
func AddToPath(path string) error {
	return appendToBashEnv(fmt.Sprintf("export PATH=%s:$PATH\n", path))
}

// appendToBashEnv appends content to $BASH_ENV.
// Returns an error if $BASH_ENV is not set.
func appendToBashEnv(content string) error {
	bashEnv := os.Getenv("BASH_ENV")
	if bashEnv == "" {
		return fmt.Errorf("BASH_ENV must be set to configure")
	}

	f, err := os.OpenFile(bashEnv, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	_, err = f.WriteString(content)
	return err
}
