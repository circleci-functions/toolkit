package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

// fakeGitHub answers the requests release makes from canned responses, keyed by method and
// path, and records the bodies it was sent.
type fakeGitHub struct {
	t         *testing.T
	responses map[string]string
	mu        sync.Mutex
	sent      map[string]string
}

func newFakeGitHub(t *testing.T, responses map[string]string) (*fakeGitHub, *gitHub) {
	f := &fakeGitHub{t: t, responses: responses, sent: map[string]string{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, newGitHub(context.Background(), srv.URL, "o/r", "gh")
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/repos/o/r")
	if r.URL.RawQuery != "" {
		key += "?" + r.URL.RawQuery
	}
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.sent[key] = string(body)
	f.mu.Unlock()
	resp, ok := f.responses[key]
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = io.WriteString(w, resp)
}

func (f *fakeGitHub) wasSent(key string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.sent[key]
	return ok
}

func (f *fakeGitHub) body(key string) map[string]any {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sent[key]
	if !ok {
		f.t.Fatalf("no request %s; got %v", key, f.sent)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		f.t.Fatalf("%s: %v", key, err)
	}
	return m
}

func testConfig() config {
	return config{
		Repo: "o/r", Base: "main", SHA: "head",
		now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
	}
}

func TestRunUpdate(t *testing.T) {
	t.Run("leaves the PR alone while the version is unreleased", func(t *testing.T) {
		t.Chdir(t.TempDir())
		err := os.WriteFile(changelogFile, []byte(changelog), 0o644)
		assert.NilError(t, err)
		f, gh := newFakeGitHub(t, map[string]string{})

		err = runUpdate(context.Background(), testConfig(), gh)
		assert.NilError(t, err)

		assert.Check(t, f.wasSent("GET /git/ref/tags/v1.3.1"))
		assert.Check(t, !f.wasSent("GET /compare/v1.3.1...head?page=1&per_page=100"),
			"looked for merged PRs before the release was tagged")
	})
}

func TestUpdateReleasePR(t *testing.T) {
	t.Run("opens it", func(t *testing.T) {
		f, gh := newFakeGitHub(t, map[string]string{
			"GET /compare/v1.3.1...head?page=1&per_page=100": `{"commits": [{"sha": "a"}, {"sha": "b"}]}`,
			"GET /commits/a/pulls": `[
				{"number": 9, "merged_at": "2026-10-01T00:00:00Z", "base": {"ref": "main"}, "head": {"ref": "fix"}}
			]`,
			"GET /commits/b/pulls": `[
				{"number": 9, "merged_at": "2026-10-01T00:00:00Z", "base": {"ref": "main"}, "head": {"ref": "fix"}},
				{"number": 10, "merged_at": null, "base": {"ref": "main"}, "head": {"ref": "closed"}}
			]`,
			"GET /pulls?head=o%3Arelease%2Fnext&state=open": `[]`,
			"POST /releases/generate-notes": `{
				"body": "## What's Changed\n* Fix it by @me in https://github.com/o/r/pull/9"
			}`,
			"GET /git/commits/head": `{"tree": {"sha": "head-tree"}}`,
			"POST /git/trees":       `{"sha": "new-tree"}`,
			"POST /git/commits":     `{"sha": "release-commit"}`,
			"POST /git/refs":        `{}`,
			"POST /pulls":           `{}`,
		})

		err := updateReleasePR(context.Background(), testConfig(), gh, version{1, 3, 1}, changelog)
		assert.NilError(t, err)

		t.Run("asks for notes on the next version", func(t *testing.T) {
			notes := f.body("POST /releases/generate-notes")
			assert.Check(t, cmp.DeepEqual(notes, map[string]any{
				"tag_name": "v1.4.0", "previous_tag_name": "v1.3.1", "target_commitish": "head",
			}))
		})

		t.Run("adds the next version to the changelog", func(t *testing.T) {
			files := map[string]string{}
			tree := f.body("POST /git/trees")
			for _, e := range tree["tree"].([]any) {
				e := e.(map[string]any)
				files[e["path"].(string)] = e["content"].(string)
			}
			assert.Check(t, cmp.Equal(tree["base_tree"], "head-tree"))
			assert.Check(t, cmp.Contains(files["CHANGELOG.md"], "## [1.4.0] - 2026-10-02\n\n### What's Changed\n* Fix it"))
		})

		t.Run("pushes the branch and opens the PR", func(t *testing.T) {
			ref := f.body("POST /git/refs")
			assert.Check(t, cmp.DeepEqual(ref, map[string]any{"ref": "refs/heads/release/next", "sha": "release-commit"}))
			pr := f.body("POST /pulls")
			assert.Check(t, cmp.Equal(pr["title"], "Release v1.4.0"))
			assert.Check(t, cmp.Equal(pr["head"], "release/next"))
			assert.Check(t, cmp.Equal(pr["base"], "main"))
		})
	})

	t.Run("leaves an up-to-date branch", func(t *testing.T) {
		f, gh := newFakeGitHub(t, map[string]string{
			"GET /compare/v1.3.1...head?page=1&per_page=100": `{"commits": [{"sha": "a"}]}`,
			"GET /commits/a/pulls": `[
				{"number": 9, "merged_at": "2026-10-01T00:00:00Z", "base": {"ref": "main"}, "head": {"ref": "fix"},
					"labels": [{"name": "release:patch"}]}
			]`,
			"GET /pulls?head=o%3Arelease%2Fnext&state=open": `[{"number": 12}]`,
			"POST /releases/generate-notes":                 `{"body": "notes"}`,
			"GET /git/commits/head":                         `{"tree": {"sha": "head-tree"}}`,
			"POST /git/trees":                               `{"sha": "new-tree"}`,
			"GET /git/ref/heads/release/next":               `{"object": {"sha": "old-release-commit"}}`,
			"GET /git/commits/old-release-commit":           `{"tree": {"sha": "new-tree"}}`,
			"PATCH /pulls/12":                               `{}`,
		})

		err := updateReleasePR(context.Background(), testConfig(), gh, version{1, 3, 1}, changelog)
		assert.NilError(t, err)

		assert.Check(t, !f.wasSent("POST /git/commits"), "made a commit for an up-to-date branch")
		pr := f.body("PATCH /pulls/12")
		assert.Check(t, cmp.Equal(pr["title"], "Release v1.3.2"))
	})

	t.Run("closes it with nothing merged", func(t *testing.T) {
		f, gh := newFakeGitHub(t, map[string]string{
			"GET /compare/v1.4.0...head?page=1&per_page=100": `{"commits": []}`,
			"GET /pulls?head=o%3Arelease%2Fnext&state=open":  `[{"number": 12}]`,
			"PATCH /pulls/12":                     `{}`,
			"DELETE /git/refs/heads/release/next": ``,
		})

		err := updateReleasePR(context.Background(), testConfig(), gh, version{1, 4, 0}, changelog)
		assert.NilError(t, err)

		pr := f.body("PATCH /pulls/12")
		assert.Check(t, cmp.DeepEqual(pr, map[string]any{"state": "closed"}))
		assert.Check(t, f.wasSent("DELETE /git/refs/heads/release/next"), "didn't delete the release branch")
	})
}

func TestRunPending(t *testing.T) {
	for _, tt := range []struct {
		name      string
		responses map[string]string
		want      string
	}{
		{
			name:      "the merged release PR's version",
			responses: map[string]string{},
			want:      "v1.3.1",
		},
		{
			name: "a rerun on the tagged commit",
			responses: map[string]string{
				"GET /git/ref/tags/v1.3.1": `{"object": {"sha": "head", "type": "commit"}}`,
			},
			want: "v1.3.1",
		},
		{
			name: "nothing once released",
			responses: map[string]string{
				"GET /git/ref/tags/v1.3.1": `{"object": {"sha": "earlier", "type": "commit"}}`,
			},
		},
		{
			name: "nothing once released with an annotated tag",
			responses: map[string]string{
				"GET /git/ref/tags/v1.3.1": `{"object": {"sha": "tag-object", "type": "tag"}}`,
				"GET /git/tags/tag-object": `{"object": {"sha": "earlier"}}`,
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			assert.NilError(t, os.WriteFile(changelogFile, []byte(changelog), 0o644))
			_, gh := newFakeGitHub(t, tt.responses)

			got, err := runPending(context.Background(), testConfig(), gh)
			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(got, tt.want))
		})
	}
}

func TestRunTag(t *testing.T) {
	t.Run("releases the newest version with its notes", func(t *testing.T) {
		t.Chdir(t.TempDir())
		assert.NilError(t, os.WriteFile(changelogFile, []byte(changelog), 0o644))
		f, gh := newFakeGitHub(t, map[string]string{"POST /releases": `{}`})

		assert.NilError(t, runTag(context.Background(), testConfig(), gh))

		assert.Check(t, cmp.DeepEqual(f.body("POST /releases"), map[string]any{
			"tag_name": "v1.3.1", "target_commitish": "head", "name": "v1.3.1",
			"body": "### Fixed\n- A fix", "make_latest": "true",
		}))
	})

	t.Run("leaves a rerun on the tagged commit alone", func(t *testing.T) {
		t.Chdir(t.TempDir())
		assert.NilError(t, os.WriteFile(changelogFile, []byte(changelog), 0o644))
		f, gh := newFakeGitHub(t, map[string]string{
			"GET /git/ref/tags/v1.3.1": `{"object": {"sha": "head", "type": "commit"}}`,
		})

		assert.NilError(t, runTag(context.Background(), testConfig(), gh))
		assert.Check(t, !f.wasSent("POST /releases"), "released an already tagged version")
	})

	t.Run("refuses a version tagged on another commit", func(t *testing.T) {
		t.Chdir(t.TempDir())
		assert.NilError(t, os.WriteFile(changelogFile, []byte(changelog), 0o644))
		_, gh := newFakeGitHub(t, map[string]string{
			"GET /git/ref/tags/v1.3.1": `{"object": {"sha": "earlier", "type": "commit"}}`,
		})

		err := runTag(context.Background(), testConfig(), gh)
		assert.Check(t, cmp.ErrorContains(err, "v1.3.1 is already tagged on earlier, not head"))
	})
}

func TestGitHubErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = io.WriteString(w, `{"message":"Repository rule violations found"}`)
	}))
	t.Cleanup(srv.Close)
	gh := newGitHub(context.Background(), srv.URL, "o/r", "gh")

	err := gh.createRelease(context.Background(), "v1.0.0", "head", "notes")
	assert.Check(t, cmp.ErrorContains(err, `422 (Unprocessable Entity)`))
	assert.Check(t, cmp.ErrorContains(err, `"message":"Repository rule violations found"`))
}
