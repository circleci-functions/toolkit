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
	if elevate {
		return sudoSymlink(src, dst)
	}

	// Moved aside rather than deleted: taking over a preinstalled tool means
	// dst is its own binary, and a failure below must leave it working.
	restore, err := moveAside(dst)
	if err != nil {
		return err
	}

	// Windows gets shims, not a symlink: taking over a preinstalled tool links
	// into its own directory, where a symlink would make the binary resolve its
	// installation to the old one beside it. A shim invokes the absolute path
	// instead, needs no symlink privilege, and — since moveAside removed any
	// competing .exe that PATHEXT would prefer — is found by name in any shell.
	link := os.Symlink
	if runtime.GOOS == "windows" {
		link = writeShims
	}
	if err := link(src, dst); err != nil {
		return errors.Join(err, restore.undo())
	}
	return restore.commit()
}

// sudoSymlink replaces a root-owned entry on PATH. -n so a job without
// passwordless sudo fails rather than blocking on a prompt.
func sudoSymlink(src, dst string) error {
	if out, err := exec.Command("sudo", "-n", "ln", "-sf", src, dst).CombinedOutput(); err != nil {
		return fmt.Errorf("sudo ln: %s", firstLine(out))
	}
	return nil
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
type backup struct{ from, to string }

func moveAside(path string) (backup, error) {
	if _, err := os.Lstat(path); err != nil {
		return backup{}, nil
	}
	aside := path + ".pathlink-old"
	_ = os.Remove(aside)
	if err := os.Rename(path, aside); err != nil {
		return backup{}, fmt.Errorf("move %s aside: %w", path, err)
	}
	return backup{from: aside, to: path}, nil
}

// commit discards the backup, leaving the link in place.
func (b backup) commit() error {
	if b.from != "" {
		_ = os.Remove(b.from)
	}
	return nil
}

// undo puts the original back, rather than leaving the job with nothing.
func (b backup) undo() error {
	if b.from == "" {
		return nil
	}
	if err := os.Rename(b.from, b.to); err != nil {
		return fmt.Errorf("restore %s: %w", b.to, err)
	}
	return nil
}

func firstLine(b []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return line
}
