package pathlink

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Command runs a linked binary. CreateProcess cannot execute a .bat, so a shim
// written on Windows has to go through cmd. Use this in Options.Verify.
func Command(bin string, args ...string) *exec.Cmd {
	switch strings.ToLower(filepath.Ext(bin)) {
	case ".bat", ".cmd":
		return exec.Command("cmd", append([]string{"/c", bin}, args...)...) //nolint:gosec // the caller names the binary
	default:
		return exec.Command(bin, args...) //nolint:gosec // the caller names the binary
	}
}

// linkInto points dir/name at fromDir/name for each name, plus dir/alias at
// fromDir/target for each alias, with sudo when elevate is set.
//
// A symlink rather than a copy: a tool that locates its own installation from
// its executable path resolves symlinks on the way, where a copy would look
// beside itself.
func linkInto(dir, fromDir string, names []string, aliases map[string]string, elevate bool) error {
	if !elevate {
		if err := os.MkdirAll(dir, 0755); err != nil { //nolint:gosec // a directory on PATH is readable by design
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	link := func(from, as string) error {
		src := filepath.Join(fromDir, exeName(from))
		dst := filepath.Join(dir, exeName(as))
		if err := linkFile(src, dst, elevate); err != nil {
			return fmt.Errorf("link %s: %w", dst, err)
		}
		return nil
	}
	for _, name := range names {
		if err := link(name, name); err != nil {
			return err
		}
	}
	for alias, target := range aliases {
		if err := link(target, alias); err != nil {
			return err
		}
	}
	return nil
}

func linkFile(src, dst string, elevate bool) error {
	// PATH already points at the binary itself, so a symlink onto its own
	// target would leave the job with nothing that works.
	if src == dst {
		return nil
	}
	if target, err := os.Readlink(dst); err == nil && target == src {
		return nil
	}
	fs := files{elevate: elevate}

	// Moved aside rather than deleted: taking over a preinstalled tool means
	// dst is its own binary, and a failure below must leave it working.
	restore, err := moveAside(fs, dst)
	if err != nil {
		return err
	}
	if err := fs.symlink(src, dst); err != nil {
		return errors.Join(err, restore.undo())
	}
	return restore.commit()
}

// files changes the entries of one directory, through sudo when that
// directory is root-owned.
type files struct{ elevate bool }

func (f files) rename(from, to string) error {
	if f.elevate {
		return sudo("mv", from, to)
	}
	return os.Rename(from, to)
}

func (f files) remove(path string) error {
	if f.elevate {
		return sudo("rm", "-f", path)
	}
	return os.Remove(path)
}

// symlink links dst to src. Under sudo, ln -n so a dst that is a symlink to a
// directory is replaced, rather than followed and the link made inside it.
//
// Windows gets shims, not a symlink: taking over a preinstalled tool links
// into its own directory, where a symlink would make the binary resolve its
// installation to the old one beside it. A shim invokes the absolute path
// instead, needs no symlink privilege, and — since moveAside removed any
// competing .exe that PATHEXT would prefer — is found by name in any shell.
func (f files) symlink(src, dst string) error {
	switch {
	case f.elevate:
		return sudo("ln", "-sfn", src, dst)
	case runtime.GOOS == "windows":
		return writeShims(src, dst)
	default:
		return os.Symlink(src, dst)
	}
}

// sudo runs args as root. -n so a job without passwordless sudo fails rather
// than blocking on a prompt.
//
// sudo explains itself on stderr, but prints nothing when it can't be run at
// all, as on an image without it.
func sudo(args ...string) error {
	out, err := exec.Command("sudo", append([]string{"-n"}, args...)...).CombinedOutput() //nolint:gosec // pathlink's own commands
	if err == nil {
		return nil
	}
	if msg := firstLine(out); msg != "" {
		return fmt.Errorf("sudo %s: %s", args[0], msg)
	}
	return fmt.Errorf("sudo %s: %w", args[0], err)
}

// writeShims writes two, because a job's steps do not all run in the same
// shell: a .bat for cmd and PowerShell, and an extensionless script for Git
// Bash.
func writeShims(src, dst string) error {
	base := dst[:len(dst)-len(filepath.Ext(dst))]

	bat := fmt.Sprintf("@echo off\r\n\"%s\" %%*\r\n", src)
	if err := os.WriteFile(base+".bat", []byte(bat), 0755); err != nil { //nolint:gosec // must be executable
		return err
	}

	sh := fmt.Sprintf("#!/bin/sh\nexec \"%s\" \"$@\"\n", filepath.ToSlash(src))
	return os.WriteFile(base, []byte(sh), 0755) //nolint:gosec // must be executable
}

// backup is a file moved aside, restorable until committed.
type backup struct {
	fs       files
	from, to string
	// keep is set for the original a takeover replaced, which stays as
	// <name>.pathlink-old so it can still be found and restored by hand.
	keep bool
}

// moveAside moves path out of the way of a link. The first thing replaced is
// kept as <name>.pathlink-old. Once that exists, it is the original, and what
// is at path is a link an earlier run made, so it is only held until commit.
func moveAside(fs files, path string) (backup, error) {
	if _, err := os.Lstat(path); err != nil {
		return backup{}, nil
	}
	b := backup{fs: fs, from: path + ".pathlink-old", to: path, keep: true}
	if _, err := os.Lstat(b.from); err == nil {
		b.from, b.keep = path+".pathlink-undo", false
	}
	if err := fs.rename(b.to, b.from); err != nil {
		return backup{}, fmt.Errorf("move %s aside: %w", path, err)
	}
	return b, nil
}

// commit leaves the link in place, and reports an original it kept.
func (b backup) commit() error {
	switch {
	case b.from == "":
	case b.keep:
		fmt.Fprintf(os.Stderr, "pathlink: replaced %s, keeping the original as %s\n", b.to, filepath.Base(b.from))
	default:
		_ = b.fs.remove(b.from)
	}
	return nil
}

// undo puts the original back, rather than leaving the job with nothing.
func (b backup) undo() error {
	if b.from == "" {
		return nil
	}
	if err := b.fs.rename(b.from, b.to); err != nil {
		return fmt.Errorf("restore %s: %w", b.to, err)
	}
	return nil
}

func firstLine(b []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return line
}
