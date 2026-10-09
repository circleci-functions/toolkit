package main

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/circleci-functions/toolkit/httpcl"
)

// gitHub is a thin client for the GitHub REST API endpoints the release PR needs.
type gitHub struct {
	http *httpcl.Client
	repo string
}

func newGitHub(ctx context.Context, api, repo, token string) *gitHub {
	return &gitHub{
		http: httpcl.New(ctx, httpcl.Config{
			Name:              "github",
			BaseURL:           strings.TrimRight(api, "/"),
			AuthToken:         token,
			UserAgent:         "circleci-functions-release-pr",
			Timeout:           time.Minute,
			PerRequestTimeout: 30 * time.Second,
		}),
		repo: repo,
	}
}

// call sends a request to a path under the repo, such as "/pulls". Paths are concatenated
// rather than passed as route params, which would escape the slash in release/next.
func (g *gitHub) call(ctx context.Context, method, path string, opts ...func(*httpcl.Request)) error {
	return g.http.Call(ctx, httpcl.NewRequest(method, "/repos/"+g.repo+path, opts...))
}

func (g *gitHub) owner() string { return strings.Split(g.repo, "/")[0] }

// tagCommit returns the commit the tag points to, or "" if it doesn't exist.
func (g *gitHub) tagCommit(ctx context.Context, tag string) (string, error) {
	var ref struct {
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	err := g.call(ctx, http.MethodGet, "/git/ref/tags/"+tag, httpcl.JSONDecoder(&ref))
	if httpcl.HasStatusCode(err, http.StatusNotFound) {
		return "", nil
	}
	if err != nil || ref.Object.Type != "tag" {
		return ref.Object.SHA, err
	}

	// An annotated tag's ref points at the tag object, which points at the commit.
	var annotated struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err = g.call(ctx, http.MethodGet, "/git/tags/"+ref.Object.SHA, httpcl.JSONDecoder(&annotated))
	return annotated.Object.SHA, err
}

type pullRequest struct {
	Number   int    `json:"number"`
	MergedAt string `json:"merged_at"`
	Base     struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Head struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Labels []struct {
		Name string `json:"name"`
	} `json:"labels"`
}

// mergedPRs are the PRs to base merged between the tag and sha, other than release PRs.
func (g *gitHub) mergedPRs(ctx context.Context, tag, sha, base string) ([]pullRequest, error) {
	var commits []string
	for page := 1; ; page++ {
		var cmp struct {
			Commits []struct {
				SHA string `json:"sha"`
			} `json:"commits"`
		}
		if err := g.call(ctx, http.MethodGet, "/compare/"+tag+"..."+sha,
			httpcl.QueryParam("per_page", "100"),
			httpcl.QueryParam("page", strconv.Itoa(page)),
			httpcl.JSONDecoder(&cmp),
		); err != nil {
			return nil, err
		}
		for _, c := range cmp.Commits {
			commits = append(commits, c.SHA)
		}
		if len(cmp.Commits) < 100 {
			break
		}
	}

	seen := map[int]bool{}
	var prs []pullRequest
	for _, c := range commits {
		var cprs []pullRequest
		if err := g.call(ctx, http.MethodGet, "/commits/"+c+"/pulls", httpcl.JSONDecoder(&cprs)); err != nil {
			return nil, err
		}
		for _, pr := range cprs {
			if seen[pr.Number] || pr.MergedAt == "" || pr.Base.Ref != base || pr.Head.Ref == releaseBranch {
				continue
			}
			seen[pr.Number] = true
			prs = append(prs, pr)
		}
	}
	return prs, nil
}

// generateNotes are GitHub's release notes for the PRs merged since previous.
func (g *gitHub) generateNotes(ctx context.Context, tag, previous, sha string) (string, error) {
	var notes struct {
		Body string `json:"body"`
	}
	err := g.call(ctx, http.MethodPost, "/releases/generate-notes",
		httpcl.Body(map[string]string{
			"tag_name":          tag,
			"previous_tag_name": previous,
			"target_commitish":  sha,
		}),
		httpcl.JSONDecoder(&notes),
	)
	return notes.Body, err
}

// createRelease makes the GitHub release, which creates its tag on sha.
func (g *gitHub) createRelease(ctx context.Context, tag, sha, notes string) error {
	return g.call(ctx, http.MethodPost, "/releases",
		httpcl.Body(map[string]any{
			"tag_name":         tag,
			"target_commitish": sha,
			"name":             tag,
			"body":             notes,
			"make_latest":      "true",
		}),
	)
}

func (g *gitHub) commitTree(ctx context.Context, sha string) (string, error) {
	var commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	err := g.call(ctx, http.MethodGet, "/git/commits/"+sha, httpcl.JSONDecoder(&commit))
	return commit.Tree.SHA, err
}

func (g *gitHub) createTree(ctx context.Context, baseTree string, files map[string]string) (string, error) {
	type entry struct {
		Path    string `json:"path"`
		Mode    string `json:"mode"`
		Type    string `json:"type"`
		Content string `json:"content"`
	}
	entries := make([]entry, 0, len(files))
	for path, content := range files {
		entries = append(entries, entry{Path: path, Mode: "100644", Type: "blob", Content: content})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	var tree struct {
		SHA string `json:"sha"`
	}
	err := g.call(ctx, http.MethodPost, "/git/trees",
		httpcl.Body(map[string]any{"base_tree": baseTree, "tree": entries}),
		httpcl.JSONDecoder(&tree),
	)
	return tree.SHA, err
}

func (g *gitHub) createCommit(ctx context.Context, message, tree, parent string) (string, error) {
	var commit struct {
		SHA string `json:"sha"`
	}
	err := g.call(ctx, http.MethodPost, "/git/commits",
		httpcl.Body(map[string]any{"message": message, "tree": tree, "parents": []string{parent}}),
		httpcl.JSONDecoder(&commit),
	)
	return commit.SHA, err
}

// branch returns the commit the branch points to, or "" if it doesn't exist.
func (g *gitHub) branch(ctx context.Context, name string) (string, error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	err := g.call(ctx, http.MethodGet, "/git/ref/heads/"+name, httpcl.JSONDecoder(&ref))
	if httpcl.HasStatusCode(err, http.StatusNotFound) {
		return "", nil
	}
	return ref.Object.SHA, err
}

func (g *gitHub) setBranch(ctx context.Context, name, sha string, exists bool) error {
	if exists {
		return g.call(ctx, http.MethodPatch, "/git/refs/heads/"+name,
			httpcl.Body(map[string]any{"sha": sha, "force": true}))
	}
	return g.call(ctx, http.MethodPost, "/git/refs",
		httpcl.Body(map[string]string{"ref": "refs/heads/" + name, "sha": sha}))
}

func (g *gitHub) deleteBranch(ctx context.Context, name string) error {
	return g.call(ctx, http.MethodDelete, "/git/refs/heads/"+name)
}

// openPR returns the number of the open PR from head, or 0 if there isn't one.
func (g *gitHub) openPR(ctx context.Context, head string) (int, error) {
	var prs []pullRequest
	if err := g.call(ctx, http.MethodGet, "/pulls",
		httpcl.QueryParam("state", "open"),
		httpcl.QueryParam("head", g.owner()+":"+head),
		httpcl.JSONDecoder(&prs),
	); err != nil {
		return 0, err
	}
	if len(prs) == 0 {
		return 0, nil
	}
	return prs[0].Number, nil
}

func (g *gitHub) createPR(ctx context.Context, pr map[string]string) error {
	return g.call(ctx, http.MethodPost, "/pulls", httpcl.Body(pr))
}

func (g *gitHub) updatePR(ctx context.Context, number int, pr map[string]string) error {
	return g.call(ctx, http.MethodPatch, fmt.Sprintf("/pulls/%d", number), httpcl.Body(pr))
}

func (g *gitHub) closePR(ctx context.Context, number int) error {
	return g.updatePR(ctx, number, map[string]string{"state": "closed"})
}
