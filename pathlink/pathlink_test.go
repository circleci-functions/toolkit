package pathlink_test

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/pathlink"
)

const tool = "mytool"

// fakeTool builds a directory holding a tool that reports where it lives, so a
// test can assert which copy resolves, not just which file exists.
func fakeTool(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script stand-in for a tool is not executable on Windows")
	}

	dir := t.TempDir()
	script := "#!/bin/sh\necho " + dir + "\n"
	assert.NilError(t, os.WriteFile(filepath.Join(dir, tool), []byte(script), 0755))
	return dir
}

// verifyFrom returns a Verify that accepts only the copy installed in fromDir.
func verifyFrom(fromDir string) func(string) error {
	return func(bin string) error {
		out, err := pathlink.Command(bin).Output()
		if err != nil {
			return fmt.Errorf("could not be run: %w", err)
		}
		if got := strings.TrimSpace(string(out)); got != fromDir {
			return fmt.Errorf("reports %q, not %q", got, fromDir)
		}
		return nil
	}
}

func options(fromDir string, o pathlink.Options) pathlink.Options {
	o.Names = []string{tool}
	o.FromDir = fromDir
	if o.Verify == nil {
		o.Verify = verifyFrom(fromDir)
	}
	return o
}

// A tool's own bin directory on PATH ahead of a preinstalled copy: that one is
// shadowed rather than disturbed.
func TestEnsure_ShadowsAPreinstalledCopyFromAPreferredDir(t *testing.T) {
	from := fakeTool(t)
	early, preinstalled := t.TempDir(), t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(preinstalled, tool), []byte("#!/bin/sh\n"), 0755))

	got, err := pathlink.Ensure(options(from, pathlink.Options{
		Preferred: []string{early},
		Path:      strings.Join([]string{early, preinstalled}, string(os.PathListSeparator)),
	}))
	assert.NilError(t, err)
	assert.Equal(t, got.Dir, early)
	assert.Assert(t, got.Verified)

	target, err := os.Readlink(filepath.Join(early, tool))
	assert.NilError(t, err)
	assert.Equal(t, target, filepath.Join(from, tool))

	fi, err := os.Lstat(filepath.Join(preinstalled, tool))
	assert.NilError(t, err)
	assert.Assert(t, fi.Mode()&os.ModeSymlink == 0, "the preinstalled copy must be left alone")
}

// With nothing preferred on PATH, the copy already there is taken over: the
// writable earlier entry belongs to some other tool.
func TestEnsure_TakesOverThePreinstalledCopyWhenNothingIsPreferred(t *testing.T) {
	from := fakeTool(t)
	unrelated, preinstalled := t.TempDir(), t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(preinstalled, tool), []byte("#!/bin/sh\n"), 0755))

	got, err := pathlink.Ensure(options(from, pathlink.Options{
		Path: strings.Join([]string{unrelated, preinstalled}, string(os.PathListSeparator)),
	}))
	assert.NilError(t, err)
	assert.Equal(t, got.Dir, preinstalled)

	_, err = os.Stat(filepath.Join(unrelated, tool))
	assert.Assert(t, os.IsNotExist(err), "an unrelated directory on PATH must be left alone")
}

// Verify is what catches a lookalike: the copy on PATH runs, but is not ours.
func TestEnsure_FailsWhenVerifyRejectsTheResolvedBinary(t *testing.T) {
	from := fakeTool(t)
	early := t.TempDir()

	_, err := pathlink.Ensure(options(from, pathlink.Options{
		Preferred: []string{early},
		Path:      early,
		Verify:    verifyFrom("/somewhere/else"),
	}))
	assert.ErrorContains(t, err, "PATH resolves mytool to ")
	assert.ErrorContains(t, err, "reports ")
}

// Without Verify, Ensure checks only that PATH resolves the name to the
// directory it linked into. ChooseDir already avoids being shadowed, so this
// mostly means a caller with no Verify gets no identity check at all.
func TestEnsure_WorksWithoutVerify(t *testing.T) {
	from := fakeTool(t)
	early := t.TempDir()

	opts := options(from, pathlink.Options{Preferred: []string{early}, Path: early})
	opts.Verify = nil

	got, err := pathlink.Ensure(opts)
	assert.NilError(t, err)
	assert.Equal(t, got.Dir, early)
	assert.Assert(t, got.Verified)
}

func TestEnsure_DirSkipsDiscoveryAndVerification(t *testing.T) {
	from := fakeTool(t)
	dir := t.TempDir()

	got, err := pathlink.Ensure(options(from, pathlink.Options{Dir: dir, Path: t.TempDir()}))
	assert.NilError(t, err)
	assert.Equal(t, got.Dir, dir)
	assert.Assert(t, !got.Verified, "a caller-chosen directory need not be on PATH")

	target, err := os.Readlink(filepath.Join(dir, tool))
	assert.NilError(t, err)
	assert.Equal(t, target, filepath.Join(from, tool))
}

// Already resolvable, so there is nothing to link.
func TestEnsure_NoopWhenPathAlreadyResolvesToTheTool(t *testing.T) {
	from := fakeTool(t)

	got, err := pathlink.Ensure(options(from, pathlink.Options{Path: from}))
	assert.NilError(t, err)
	assert.Equal(t, got.Dir, from)
	assert.Assert(t, got.Verified)
}

func TestEnsure_RequiresNamesAndFromDir(t *testing.T) {
	_, err := pathlink.Ensure(pathlink.Options{})
	assert.ErrorContains(t, err, "Names and FromDir are required")
}

// Taking over a directory keeps what it replaced beside the link, and says so.
func TestEnsure_KeepsTheBinaryItTakesOver(t *testing.T) {
	from := fakeTool(t)
	dir := t.TempDir()
	original := filepath.Join(dir, tool)
	assert.NilError(t, os.WriteFile(original, []byte("#!/bin/sh\necho original\n"), 0755))

	stderr := captureStderr(t, func() {
		_, err := pathlink.Ensure(options(from, pathlink.Options{Dir: dir, Path: dir}))
		assert.NilError(t, err)
	})

	target, err := os.Readlink(original)
	assert.NilError(t, err)
	assert.Equal(t, target, filepath.Join(from, tool))

	kept, err := os.ReadFile(original + ".pathlink-old") //nolint:gosec // a path this test chose
	assert.NilError(t, err)
	assert.Equal(t, string(kept), "#!/bin/sh\necho original\n")
	assert.Equal(t, stderr, fmt.Sprintf("pathlink: replaced %s, keeping the original as %s.pathlink-old\n", original, tool))
}

// A second takeover replaces the first one's link, and must not keep that
// over the original.
func TestEnsure_KeepsTheOriginalAcrossTakeovers(t *testing.T) {
	first, second := fakeTool(t), fakeTool(t)
	dir := t.TempDir()
	original := filepath.Join(dir, tool)
	assert.NilError(t, os.WriteFile(original, []byte("#!/bin/sh\necho original\n"), 0755))

	_, err := pathlink.Ensure(options(first, pathlink.Options{Dir: dir, Path: dir}))
	assert.NilError(t, err)
	stderr := captureStderr(t, func() {
		_, err = pathlink.Ensure(options(second, pathlink.Options{Dir: dir, Path: dir}))
		assert.NilError(t, err)
	})

	target, err := os.Readlink(original)
	assert.NilError(t, err)
	assert.Equal(t, target, filepath.Join(second, tool))

	kept, err := os.ReadFile(original + ".pathlink-old") //nolint:gosec // a path this test chose
	assert.NilError(t, err)
	assert.Equal(t, string(kept), "#!/bin/sh\necho original\n")
	assert.Equal(t, stderr, "")

	entries, err := os.ReadDir(dir)
	assert.NilError(t, err)
	assert.Equal(t, len(entries), 2, "only the link and the original may be left")
}

// A rerun finds its own link in place, which is nothing to keep.
func TestEnsure_LeavesItsOwnLinkAlone(t *testing.T) {
	from := fakeTool(t)
	dir := t.TempDir()

	for range 2 {
		_, err := pathlink.Ensure(options(from, pathlink.Options{Dir: dir, Path: dir}))
		assert.NilError(t, err)
	}

	_, err := os.Lstat(filepath.Join(dir, tool+".pathlink-old"))
	assert.Assert(t, os.IsNotExist(err), "a link to the same binary is not an original")
}

// captureStderr returns what fn writes to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	assert.NilError(t, err)
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	assert.NilError(t, w.Close())
	out, err := io.ReadAll(r)
	assert.NilError(t, err)
	return string(out)
}

// An unwritable directory fails before the binary it holds is touched.
func TestEnsure_LeavesTheOriginalIntactWhenItCannotLink(t *testing.T) {
	from := fakeTool(t)
	dir := t.TempDir()
	original := filepath.Join(dir, tool)
	assert.NilError(t, os.WriteFile(original, []byte("#!/bin/sh\necho original\n"), 0755))
	assert.NilError(t, os.Chmod(dir, 0500))       //nolint:gosec // the point is that it is not writable
	t.Cleanup(func() { _ = os.Chmod(dir, 0750) }) //nolint:gosec // restores what t.TempDir needs to clean up

	_, err := pathlink.Ensure(options(from, pathlink.Options{Dir: dir, Path: dir}))
	assert.Assert(t, err != nil, "expected linking into a read-only directory to fail")

	got, readErr := os.ReadFile(original) //nolint:gosec // a path this test wrote
	assert.NilError(t, readErr, "the binary that was already there must still be there")
	assert.Assert(t, strings.Contains(string(got), "echo original"))
}

func TestSearchDirs_DropsRelativeEntries(t *testing.T) {
	path := strings.Join([]string{"/usr/bin", "", "relative/bin", "/usr/local/bin/"}, string(os.PathListSeparator))
	assert.DeepEqual(t, pathlink.SearchDirs(path), []string{"/usr/bin", "/usr/local/bin"})
}

func TestIndexOf(t *testing.T) {
	empty, withTool := t.TempDir(), t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(withTool, tool), []byte("#!/bin/sh\n"), 0755))

	assert.Equal(t, pathlink.IndexOf([]string{empty, withTool}, tool), 1)
	assert.Equal(t, pathlink.IndexOf([]string{empty}, tool), -1)
}

func TestChooseDir(t *testing.T) {
	dirs := []string{"/first", "/second", "/third"}
	writable := func(names ...string) func(string) bool {
		return func(d string) bool { return slices.Contains(names, d) }
	}

	tests := []struct {
		name        string
		existing    int
		preferred   []string
		writable    func(string) bool
		wantDir     string
		wantElevate bool
	}{
		{
			name:      "a preferred directory wins over an earlier entry",
			existing:  -1,
			preferred: []string{"/third"},
			writable:  writable("/first", "/third"),
			wantDir:   "/third",
		},
		{
			name:      "a preferred directory that is not on PATH is skipped",
			existing:  -1,
			preferred: []string{"/nowhere", "/second"},
			writable:  writable("/nowhere", "/second"),
			wantDir:   "/second",
		},
		{
			name:      "a preferred directory behind the existing binary is skipped",
			existing:  1,
			preferred: []string{"/third", "/first"},
			writable:  writable("/first", "/third"),
			wantDir:   "/first",
		},
		{
			name:     "nothing preferred, so take over the existing binary",
			existing: 2,
			writable: writable("/second", "/third"),
			wantDir:  "/third",
		},
		{
			name:        "taking over a root-owned directory needs elevation",
			existing:    1,
			writable:    writable("/third"),
			wantDir:     "/second",
			wantElevate: true,
		},
		{
			name:     "nothing on PATH, so the earliest writable entry",
			existing: -1,
			writable: writable("/second", "/third"),
			wantDir:  "/second",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, elevate := pathlink.ChooseDir(dirs, tc.existing, tc.preferred, tc.writable)
			assert.Equal(t, dir, tc.wantDir)
			assert.Equal(t, elevate, tc.wantElevate)
		})
	}
}

// ~/.pyenv/shims is the first writable entry on the macOS executor's PATH, and
// pyenv rewrites it when it rehashes.
func TestChooseDir_SkipsDirectoriesOwnedByAVersionManager(t *testing.T) {
	dirs := []string{"/Users/distiller/.pyenv/shims", "/Users/distiller/.nvm/bin", "/usr/local/bin"}
	anything := func(string) bool { return true }

	dir, elevate := pathlink.ChooseDir(dirs, -1, nil, anything)
	assert.Equal(t, dir, "/usr/local/bin")
	assert.Assert(t, !elevate)
}

// What the Windows executor did when nothing was preferred: C:\tools\ruby34\bin
// was simply the first writable entry.
func TestChooseDir_PrefersTakingOverTheExistingBinaryToAnUnrelatedDir(t *testing.T) {
	dirs := []string{"/tools/ruby34/bin", "/opt/mytool/bin"}
	anything := func(string) bool { return true }

	dir, _ := pathlink.ChooseDir(dirs, 1, nil, anything)
	assert.Equal(t, dir, "/opt/mytool/bin")
}

func TestUserBinDirs(t *testing.T) {
	got := pathlink.UserBinDirs()
	assert.Assert(t, len(got) > 0)
	if runtime.GOOS != "windows" {
		assert.Equal(t, got[len(got)-1], pathlink.DefaultBinDir)
	}
}

func TestWritable(t *testing.T) {
	dir := t.TempDir()
	assert.Assert(t, pathlink.Writable(dir))
	assert.Assert(t, pathlink.Writable(filepath.Join(dir, "not-yet")),
		"a PATH entry that can be created counts as writable")

	assert.NilError(t, os.Chmod(dir, 0500))       //nolint:gosec // the point is that it is not writable
	t.Cleanup(func() { _ = os.Chmod(dir, 0750) }) //nolint:gosec // restores what t.TempDir needs to clean up
	assert.Assert(t, !pathlink.Writable(dir))
	assert.Assert(t, !pathlink.Writable(filepath.Join(dir, "child")))
}

// Aliases replace a shell alias, which only bash would have seen. setup-python
// needs `python` to run python3.
func TestEnsure_LinksAliasesUnderTheirOwnName(t *testing.T) {
	from := fakeTool(t)
	dir := t.TempDir()

	opts := options(from, pathlink.Options{Dir: dir, Path: dir})
	opts.Aliases = map[string]string{"othertool": tool}

	_, err := pathlink.Ensure(opts)
	assert.NilError(t, err)

	target, err := os.Readlink(filepath.Join(dir, "othertool"))
	assert.NilError(t, err)
	assert.Equal(t, target, filepath.Join(from, tool), "the alias runs the target, under its own name")
}

// A caller whose binary is always on PATH — python3, say — would otherwise
// take the early return and never get its aliases.
func TestEnsure_LinksAliasesEvenWhenTheBinaryAlreadyResolves(t *testing.T) {
	from := fakeTool(t)

	opts := options(from, pathlink.Options{Path: from})
	opts.Aliases = map[string]string{"othertool": tool}

	got, err := pathlink.Ensure(opts)
	assert.NilError(t, err)
	assert.Equal(t, got.Dir, from)

	target, err := os.Readlink(filepath.Join(from, "othertool"))
	assert.NilError(t, err)
	assert.Equal(t, target, filepath.Join(from, tool))
}

// fakeSudo puts a sudo on PATH that records its arguments, one call per line,
// and succeeds without running anything.
func fakeSudo(t *testing.T) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n"
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0755))
	t.Setenv("PATH", dir)
	return log
}

// refusingSudo puts a sudo on PATH that records its arguments and refuses, as
// sudo -n does without passwordless sudo.
func refusingSudo(t *testing.T) (log string) {
	t.Helper()
	dir := t.TempDir()
	log = filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\necho 'sudo: a password is required' >&2\nexit 1\n"
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0755))
	t.Setenv("PATH", dir)
	return log
}

// readOnlyTool returns a directory holding a tool that cannot be linked over
// without elevation.
func readOnlyTool(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(dir, tool), []byte("#!/bin/sh\n"), 0755))
	assert.NilError(t, os.Chmod(dir, 0500))       //nolint:gosec // the point is that it is not writable
	t.Cleanup(func() { _ = os.Chmod(dir, 0750) }) //nolint:gosec // restores what t.TempDir needs to clean up
	return dir
}

// Under sudo the original is kept too, and ln -n replaces a destination that
// is a symlink to a directory rather than making the link inside it.
func TestEnsure_SudoKeepsTheOriginalAndReplacesTheEntry(t *testing.T) {
	from := fakeTool(t)
	dir := readOnlyTool(t)
	log := fakeSudo(t)
	dst := filepath.Join(dir, tool)

	_, _ = pathlink.Ensure(options(from, pathlink.Options{Path: dir}))

	calls, err := os.ReadFile(log) //nolint:gosec // a path this test chose
	assert.NilError(t, err)
	assert.Equal(t, string(calls), fmt.Sprintf("-n mv %s %s.pathlink-old\n-n ln -sfn %s %s\n",
		dst, dst, filepath.Join(from, tool), dst))
}

// Slim and Alpine images often have no sudo, and sudo is then not there to
// say what went wrong.
func TestEnsure_SaysWhySudoFailedWhenItIsNotInstalled(t *testing.T) {
	from := fakeTool(t)
	dir := readOnlyTool(t)
	t.Setenv("PATH", t.TempDir())

	_, err := pathlink.Ensure(options(from, pathlink.Options{Path: dir}))
	assert.ErrorContains(t, err, "sudo mv: exec: \"sudo\": executable file not found")
}

// A link that would need root goes into a writable preferred directory
// instead, when the caller can put that on PATH.
func TestEnsure_PrefersAPathPrependToSudo(t *testing.T) {
	from := fakeTool(t)
	dir := readOnlyTool(t)
	log := fakeSudo(t)
	pref := filepath.Join(t.TempDir(), "bin")
	var added []string

	got, err := pathlink.Ensure(options(from, pathlink.Options{
		Preferred: []string{pref},
		Path:      dir,
		AddToPath: func(d string) error { added = append(added, d); return nil },
	}))
	assert.NilError(t, err)
	assert.DeepEqual(t, got, pathlink.Result{Dir: pref, Verified: true, AddedToPath: true})
	assert.DeepEqual(t, added, []string{pref})

	target, err := os.Readlink(filepath.Join(pref, tool))
	assert.NilError(t, err)
	assert.Equal(t, target, filepath.Join(from, tool))

	_, err = os.Stat(log)
	assert.Assert(t, os.IsNotExist(err), "sudo must not be tried")
}

// Without a way to change PATH, sudo is still the fallback, and its failure
// says what would avoid it.
func TestEnsure_SaysHowToAvoidSudo(t *testing.T) {
	from := fakeTool(t)
	dir := readOnlyTool(t)
	refusingSudo(t)
	pref := filepath.Join(t.TempDir(), "bin")

	_, err := pathlink.Ensure(options(from, pathlink.Options{Preferred: []string{pref}, Path: dir}))
	assert.ErrorContains(t, err, fmt.Sprintf("pathlink: linking %s into %s needs root: ", tool, dir))
	assert.ErrorContains(t, err, "sudo: a password is required")
	assert.ErrorContains(t, err, fmt.Sprintf("; put %s on PATH ahead of %s, or allow passwordless sudo", pref, dir))
}

// macOS's System Integrity Protection keeps even root out of /usr/bin, so
// sudo can't help there.
func TestEnsure_DoesNotTrySudoOnAMacOSSystemDirectory(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS only")
	}
	from := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(from, "true"), []byte("#!/bin/sh\necho "+from+"\n"), 0755))
	log := fakeSudo(t)

	_, err := pathlink.Ensure(pathlink.Options{
		Names:     []string{"true"},
		FromDir:   from,
		Preferred: []string{"/nonexistent/bin"},
		Path:      "/usr/bin",
		Verify:    verifyFrom(from),
	})
	assert.ErrorContains(t, err, "pathlink: linking true into /usr/bin needs root: macOS System Integrity Protection")
	assert.ErrorContains(t, err, "; put /nonexistent/bin on PATH ahead of /usr/bin")
	assert.Assert(t, !strings.Contains(err.Error(), "sudo"), err.Error())

	_, statErr := os.Stat(log)
	assert.Assert(t, os.IsNotExist(statErr), "sudo must not be tried")
}
