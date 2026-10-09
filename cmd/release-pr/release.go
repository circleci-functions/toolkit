package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

const releaseBranch = "release/next"

type config struct {
	Repo string
	Base string
	SHA  string
	now  time.Time
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

func readChangelog() (version, string, error) {
	b, err := os.ReadFile(changelogFile)
	if err != nil {
		return version{}, "", err
	}
	v, err := latestVersion(string(b))
	return v, string(b), err
}

// runUpdate updates the release PR, unless the newest version in the changelog isn't tagged yet:
// then its release PR has just been merged, and the publish job is releasing it.
func runUpdate(ctx context.Context, cfg config, gh *gitHub) error {
	v, changelog, err := readChangelog()
	if err != nil {
		return err
	}
	commit, err := gh.tagCommit(ctx, v.tag())
	if err != nil {
		return err
	}
	if commit == "" {
		logf("%s isn't released yet; the publish job releases it", v.tag())
		return nil
	}
	return updateReleasePR(ctx, cfg, gh, v, changelog)
}

// runPending returns the version to publish: the newest in the changelog if it isn't tagged, or
// is tagged on this commit, as when a publish that failed part way is rerun. Otherwise it's "".
func runPending(ctx context.Context, cfg config, gh *gitHub) (string, error) {
	v, _, err := readChangelog()
	if err != nil {
		return "", err
	}
	commit, err := gh.tagCommit(ctx, v.tag())
	if err != nil {
		return "", err
	}
	if commit != "" && commit != cfg.SHA {
		logf("%s is already released", v.tag())
		return "", nil
	}
	return v.tag(), nil
}

// runTag makes the GitHub release, and its tag, for the newest version in the changelog, with
// the version's section as its notes. It's left alone if this commit is already tagged.
func runTag(ctx context.Context, cfg config, gh *gitHub) error {
	v, changelog, err := readChangelog()
	if err != nil {
		return err
	}
	notes, err := releaseNotes(changelog, v)
	if err != nil {
		return err
	}
	commit, err := gh.tagCommit(ctx, v.tag())
	if err != nil {
		return err
	}
	switch commit {
	case cfg.SHA:
		logf("%s is already tagged on %s", v.tag(), cfg.SHA)
		return nil
	case "":
		logf("releasing %s on %s", v.tag(), cfg.SHA)
		return gh.createRelease(ctx, v.tag(), cfg.SHA, notes)
	default:
		return fmt.Errorf("%s is already tagged on %s, not %s", v.tag(), commit, cfg.SHA)
	}
}

func updateReleasePR(ctx context.Context, cfg config, gh *gitHub, current version, changelog string) error {
	prs, err := gh.mergedPRs(ctx, current.tag(), cfg.SHA, cfg.Base)
	if err != nil {
		return err
	}
	open, err := gh.openPR(ctx, releaseBranch)
	if err != nil {
		return err
	}
	if len(prs) == 0 {
		logf("nothing merged since %s", current.tag())
		if open == 0 {
			return nil
		}
		if err := gh.closePR(ctx, open); err != nil {
			return err
		}
		return gh.deleteBranch(ctx, releaseBranch)
	}

	next := current.bump(bumpFor(prs))
	logf("updating the release PR for %s with %d PRs", next.tag(), len(prs))
	notes, err := gh.generateNotes(ctx, next.tag(), current.tag(), cfg.SHA)
	if err != nil {
		return err
	}

	if err := pushReleaseBranch(ctx, cfg, gh, next, map[string]string{
		changelogFile: addRelease(changelog, next, cfg.now.UTC().Format("2006-01-02"), notes),
	}); err != nil {
		return err
	}

	pr := map[string]string{
		"title": "Release " + next.tag(),
		"body": fmt.Sprintf("Merging this publishes %s of %s.\n\n"+
			"A merged PR's `release:major` or `release:patch` label changes the version; "+
			"it's otherwise a minor release.\n\n"+
			"---\n\n%s", next.tag(), cfg.Repo, notes),
	}
	if open != 0 {
		return gh.updatePR(ctx, open, pr)
	}
	pr["head"], pr["base"] = releaseBranch, cfg.Base
	return gh.createPR(ctx, pr)
}

// pushReleaseBranch points the release branch at a commit on sha with the files changed. It's
// left alone if it already has them, so CI doesn't run again for the same change.
func pushReleaseBranch(ctx context.Context, cfg config, gh *gitHub, next version, files map[string]string) error {
	base, err := gh.commitTree(ctx, cfg.SHA)
	if err != nil {
		return err
	}
	tree, err := gh.createTree(ctx, base, files)
	if err != nil {
		return err
	}
	existing, err := gh.branch(ctx, releaseBranch)
	if err != nil {
		return err
	}
	if existing != "" {
		existingTree, err := gh.commitTree(ctx, existing)
		if err != nil {
			return err
		}
		if existingTree == tree {
			logf("%s is up to date", releaseBranch)
			return nil
		}
	}
	commit, err := gh.createCommit(ctx, "Release "+next.tag(), tree, cfg.SHA)
	if err != nil {
		return err
	}
	return gh.setBranch(ctx, releaseBranch, commit, existing != "")
}
