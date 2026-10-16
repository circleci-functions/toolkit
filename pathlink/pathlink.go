// Package pathlink makes a binary resolvable by name for the rest of a
// CircleCI job, by linking it into a directory PATH already holds.
//
// A step inherits its environment from the job, not from the step before it, so
// a PATH prepend only carries via $BASH_ENV, and only into bash. A link carries
// into any shell, and into processes that source nothing.
package pathlink

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultBinDir is the last preference on PATH. Windows has no equivalent.
const DefaultBinDir = "/usr/local/bin"

// Options configures Ensure.
type Options struct {
	// Names are the binaries to link, and Names[0] is the one verified.
	Names []string
	// FromDir holds them.
	FromDir string
	// Aliases are extra names to make resolvable, each mapped to the name in
	// FromDir it should run. A symlink where a shell alias would only have
	// worked in bash: setup-python needs `python` to run python3.
	Aliases map[string]string
	// Preferred are directories to try before scanning PATH, best first. A
	// tool's own bin directory belongs at the front; see UserBinDirs.
	Preferred []string
	// Dir links into exactly this directory and skips discovery, leaving the
	// result unverified. Tests set it, so they cannot write to the real PATH.
	Dir string
	// Path is the search path to use. Empty means this process's PATH.
	Path string
	// Verify asks the resolved binary to identify itself, and is what catches a
	// link that resolves to the wrong copy rather than to none. Without it,
	// Ensure only checks that PATH resolves Names[0] to the directory it linked
	// into, which catches shadowing but not a lookalike.
	Verify func(bin string) error
}

// Result describes what Ensure did.
type Result struct {
	// Dir is where the binaries now resolve from.
	Dir string
	// Verified reports that PATH was confirmed to resolve Names[0]. False when
	// Dir skipped discovery, since that directory need not be on PATH.
	Verified bool
}

// Ensure makes opts.Names resolvable by name, and reports where it linked them.
//
// Unless opts.Dir skips discovery, it fails if PATH does not then resolve
// Names[0] to what was linked, rather than letting the job run something else.
//
// A binary it replaces is kept beside the link as <name>.pathlink-old, and
// reported on stderr.
func Ensure(opts Options) (Result, error) {
	if len(opts.Names) == 0 || opts.FromDir == "" {
		return Result{}, errors.New("pathlink: Names and FromDir are required")
	}
	dirs := SearchDirs(opts.Path)

	if opts.Dir != "" {
		if err := linkInto(opts.Dir, opts.FromDir, opts.Names, opts.Aliases, false); err != nil {
			return Result{}, err
		}
		return Result{Dir: opts.Dir}, nil
	}

	// Already resolvable, with nothing left to add: the caller's own copy is on
	// PATH, or a rerun. Aliases have to be there too, or a caller whose binary
	// was always on PATH would never get them.
	if dir, err := verify(dirs, opts, ""); err == nil && resolveAll(dirs, opts.Aliases) {
		return Result{Dir: dir, Verified: true}, nil
	}

	dir, elevate := ChooseDir(dirs, IndexOf(dirs, opts.Names[0]), opts.Preferred, Writable)
	if dir == "" {
		return Result{}, fmt.Errorf("pathlink: no directory on PATH can hold %s (PATH=%s)",
			opts.Names[0], strings.Join(dirs, string(os.PathListSeparator)))
	}
	if err := linkInto(dir, opts.FromDir, opts.Names, opts.Aliases, elevate); err != nil {
		return Result{}, err
	}

	if _, err := verify(dirs, opts, dir); err != nil {
		return Result{Dir: dir}, fmt.Errorf("pathlink: linked %s into %s, but %w", opts.Names[0], dir, err)
	}
	return Result{Dir: dir, Verified: true}, nil
}

// verify reports the directory PATH resolves Names[0] from, once satisfied it
// is the right copy. wantDir, when set, is the directory just linked into.
func verify(dirs []string, opts Options, wantDir string) (string, error) {
	i := IndexOf(dirs, opts.Names[0])
	if i < 0 {
		return "", fmt.Errorf("PATH holds no %s at all", opts.Names[0])
	}
	bin := find(dirs[i], opts.Names[0])

	if opts.Verify != nil {
		if err := opts.Verify(bin); err != nil {
			return dirs[i], fmt.Errorf("PATH resolves %s to %s, which %w", opts.Names[0], bin, err)
		}
		return dirs[i], nil
	}
	if wantDir != "" && !sameDir(dirs[i], wantDir) {
		return dirs[i], fmt.Errorf("PATH resolves %s to %s instead", opts.Names[0], bin)
	}
	if wantDir == "" {
		return dirs[i], errors.New("no Verify to confirm which copy is on PATH")
	}
	return dirs[i], nil
}

// resolveAll reports whether every alias is already on PATH.
func resolveAll(dirs []string, aliases map[string]string) bool {
	for alias := range aliases {
		if IndexOf(dirs, alias) < 0 {
			return false
		}
	}
	return true
}

// SearchDirs splits path, dropping relative entries: those resolve against
// whatever directory a later step runs in.
func SearchDirs(path string) []string {
	if path == "" {
		path = os.Getenv("PATH")
	}
	var dirs []string
	for _, d := range filepath.SplitList(path) {
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		dirs = append(dirs, filepath.Clean(d))
	}
	return dirs
}

// IndexOf returns the index of the first directory holding an executable name,
// or -1. That is what a later step resolves, so a link has to get ahead of it
// or replace it.
func IndexOf(dirs []string, name string) int {
	for i, d := range dirs {
		if find(d, name) != "" {
			return i
		}
	}
	return -1
}

// UserBinDirs returns the conventional bin directories a job can write to,
// best first, for the tail of Options.Preferred.
func UserBinDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"), filepath.Join(home, "bin"))
	}
	if runtime.GOOS != "windows" {
		dirs = append(dirs, DefaultBinDir)
	}
	return dirs
}

// ChooseDir picks the directory to link into, by preference rather than PATH
// order: a version manager rewrites its shims when it rehashes, taking any link
// left there with it. The choice has to come ahead of the binary already on
// PATH or it would be shadowed; failing that it takes that one over, which
// needs elevation if its directory is root-owned.
//
// existing is the index to get ahead of, or -1 when PATH holds no such binary.
func ChooseDir(dirs []string, existing int, preferred []string, writable func(string) bool) (dir string, elevate bool) {
	// -1 is not on PATH at all, so a link there resolves for nobody.
	ahead := func(i int) bool { return i >= 0 && (existing < 0 || i < existing) }

	for _, p := range preferred {
		if i := indexOfDir(dirs, p); ahead(i) && writable(p) {
			return p, false
		}
	}
	// Before scanning: that directory belongs to the tool being replaced, where
	// an arbitrary writable entry belongs to whatever tool put it there.
	if existing >= 0 {
		return dirs[existing], !writable(dirs[existing])
	}
	for i, d := range dirs {
		if ahead(i) && !managed(d) && writable(d) {
			return d, false
		}
	}
	if runtime.GOOS == "windows" {
		return "", false
	}
	return DefaultBinDir, !writable(DefaultBinDir)
}

// managed reports whether dir belongs to a tool that rewrites its contents.
// pyenv, rbenv, asdf and goenv all call that directory "shims".
func managed(dir string) bool {
	for _, part := range strings.Split(filepath.ToSlash(dir), "/") {
		switch part {
		case "shims", ".nvm", "node_modules":
			return true
		}
	}
	return false
}

func indexOfDir(dirs []string, dir string) int {
	for i, d := range dirs {
		if sameDir(d, dir) {
			return i
		}
	}
	return -1
}

// Writable reports whether dir can be linked into without elevation. A missing
// directory counts if it can be created, along with any missing parents, as
// linkInto would: ~/.local/bin is often on PATH before it, or ~/.local, exists.
func Writable(dir string) bool {
	if err := canCreateIn(dir); err == nil {
		return true
	}
	for {
		if _, err := os.Stat(dir); err == nil {
			return false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		if info, err := os.Stat(parent); err == nil {
			return info.IsDir() && canCreateIn(parent) == nil
		}
		dir = parent
	}
}

// canCreateIn tries it, rather than reading mode bits: those answer for the
// wrong user under sudo and miss a read-only mount.
func canCreateIn(dir string) error {
	f, err := os.CreateTemp(dir, ".pathlink-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

// find returns an executable name in dir, or "". On Windows it accepts the
// shims linkInto falls back to.
func find(dir, name string) string {
	for _, n := range candidates(name) {
		p := filepath.Join(dir, n)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func candidates(name string) []string {
	if runtime.GOOS == "windows" {
		return []string{name + ".exe", name + ".bat", name + ".cmd"}
	}
	return []string{name}
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// sameDir compares paths as the filesystem would, so a link compares equal to
// what it points at.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if r, err := filepath.EvalSymlinks(a); err == nil {
		a = r
	}
	if r, err := filepath.EvalSymlinks(b); err == nil {
		b = r
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
