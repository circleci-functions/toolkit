// Command release-pr releases a function by merging a release PR. It's run in CI from the root
// of a function's repo.
//
// The newest section of CHANGELOG.md is the latest release. Once it's tagged, every push to main
// runs `update`, which opens or updates the "Release vX.Y.Z" PR from the release/next branch: it
// adds a section for the next version with the PRs merged since. Merging that PR leaves the
// newest section untagged, which `pending` reports, and `tag` then releases it.
//
// Usage:
//
//	GITHUB_TOKEN=... go tool release-pr update
//	VERSION="$(GITHUB_TOKEN=... go tool release-pr pending)"
//	GITHUB_TOKEN=... go tool release-pr tag
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/alecthomas/kong"
)

type cli struct {
	API   string `default:"https://api.github.com" help:"GitHub API URL"`
	Repo  string `help:"repository to release, as owner/name (default: the CircleCI project)"`
	Base  string `default:"main" help:"branch releases are made from"`
	SHA   string `env:"CIRCLE_SHA1" help:"commit being built"`
	Token string `env:"GITHUB_TOKEN" hidden:"" help:"GitHub token"`

	Update  updateCmd  `cmd:"" help:"Open, update or close the release PR for the PRs merged since the last release"`
	Pending pendingCmd `cmd:"" help:"Print the version to publish, or nothing if it's already released"`
	Tag     tagCmd     `cmd:"" help:"Make the GitHub release and tag for the version to publish"`
}

type (
	updateCmd  struct{}
	pendingCmd struct{}
	tagCmd     struct{}
)

func (updateCmd) Run(ctx context.Context, cfg config, gh *gitHub) error {
	return runUpdate(ctx, cfg, gh)
}

func (pendingCmd) Run(ctx context.Context, cfg config, gh *gitHub) error {
	v, err := runPending(ctx, cfg, gh)
	if err != nil || v == "" {
		return err
	}
	_, err = fmt.Println(v)
	return err
}

func (tagCmd) Run(ctx context.Context, cfg config, gh *gitHub) error {
	return runTag(ctx, cfg, gh)
}

func (c cli) config() (config, error) {
	cfg := config{Repo: c.Repo, Base: c.Base, SHA: c.SHA, now: time.Now()}
	if cfg.Repo == "" {
		owner, name := os.Getenv("CIRCLE_PROJECT_USERNAME"), os.Getenv("CIRCLE_PROJECT_REPONAME")
		if owner == "" || name == "" {
			return cfg, errors.New("--repo or CIRCLE_PROJECT_USERNAME and CIRCLE_PROJECT_REPONAME must be set")
		}
		cfg.Repo = owner + "/" + name
	}
	if cfg.SHA == "" {
		return cfg, errors.New("--sha or CIRCLE_SHA1 must be set")
	}
	if c.Token == "" {
		return cfg, errors.New("GITHUB_TOKEN must be set")
	}
	return cfg, nil
}

func main() {
	var c cli
	k := kong.Parse(&c, kong.Description("Release a function by merging a release PR."))

	ctx := context.Background()
	cfg, err := c.config()
	k.FatalIfErrorf(err)

	k.BindTo(ctx, (*context.Context)(nil))
	err = k.Run(cfg, newGitHub(ctx, c.API, cfg.Repo, c.Token))
	k.FatalIfErrorf(err)
}
