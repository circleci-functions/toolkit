// Package archive provides helpers for writing files extracted from archives.
package archive

import (
	"io"
	"os"
)

// WriteFile writes the contents of r to path with the given mode, propagating
// any close error so callers don't silently swallow write failures.
func WriteFile(path string, r io.Reader, mode os.FileMode) (err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(f, r)
	return err
}
