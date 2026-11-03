package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"

	"github.com/circleci-functions/toolkit/testing/s3test"
)

func TestValidateVersion(t *testing.T) {
	for _, tt := range []struct {
		name    string
		version string
		valid   bool
	}{
		{name: "release", version: "v0.1.0", valid: true},
		{name: "release with commit", version: "v0.1.0-abc1234", valid: true},
		{name: "release candidate", version: "v1.2.3-rc.1", valid: true},
		{name: "pseudo version", version: "v0.0.0-20260731203344-aa726050c766", valid: true},
		{name: "empty", version: "", valid: false},
		{name: "missing leading v", version: "0.1.0-abc1234", valid: false},
		{name: "major only", version: "v1", valid: false},
		{name: "major and minor only", version: "v1.2", valid: false},
		{name: "not a version", version: "latest", valid: false},
		{name: "build metadata is discarded, so not canonical", version: "v0.1.0+abc1234", valid: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := validateVersion(tt.version)
			if tt.valid {
				assert.NilError(t, err)
				return
			}
			assert.ErrorContains(t, err, "canonical semver")
		})
	}
}

// A checked-in function.yaml gets the same example a kong function would, and
// a hand-written one is replaced since the example is derived.
func TestMarshalDescriptorRendersExample(t *testing.T) {
	data := []byte(`name: widget
example: stale
flags:
  - name: version
    type: string
    example: "1.20"
`)
	out, err := marshalDescriptor(data, "widget", "v1.2.3")
	assert.NilError(t, err)
	assert.Equal(t, publishedExample(out), `functions:
  widget: github.com/circleci-functions/widget@v1.2.3
jobs:
  build:
    steps:
      - widget:
          with:
            version: "1.20"
`)
}

// A step selects a command with its key, as `- widget/install:`, so the
// message must not say a step can't name one.
func TestNoExampleNamesTheStepForm(t *testing.T) {
	assert.Equal(t, noExample("widget"),
		"no published example: widget only groups its commands, so a step names one as widget/<command>\n\n")
}

func TestCheckUnpublished(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		err    string
	}{
		{name: "missing is a 403 from the CDN", status: http.StatusForbidden},
		{name: "missing", status: http.StatusNotFound},
		{name: "published", status: http.StatusOK, err: "github.com/circleci-functions/widget v0.2.0 is already published"},
		{name: "unexpected status", status: http.StatusBadGateway, err: "502 Bad Gateway"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var path string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path = r.Method + " " + r.URL.Path
				w.WriteHeader(tt.status)
			}))
			t.Cleanup(srv.Close)

			r := Releaser{name: "widget", version: "v0.2.0", baseURL: srv.URL, httpClient: srv.Client()}
			err := r.checkUnpublished(context.Background())

			assert.Equal(t, path, "HEAD /github.com/circleci-functions/widget/v0.2.0/checksums.txt")
			if tt.err == "" {
				assert.NilError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.err)
			assert.Check(t, cmp.Equal(errors.Is(err, errPublished), tt.status == http.StatusOK))
		})
	}
}

func TestNewReleaserPrereleaseNeedsPseudoVersion(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct {
		name    string
		version string
		err     string
	}{
		{name: "pseudo version", version: "v0.7.1-0.20261103142233-abcdef123456"},
		{name: "release", version: "v0.7.0", err: "must be a pseudo-version"},
		{name: "commit suffix", version: "v0.7.0-abc1234", err: "must be a pseudo-version"},
		{name: "release candidate", version: "v0.7.0-rc.1", err: "must be a pseudo-version"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newReleaser(ctx, Config{Name: "widget", Version: tt.version, Prerelease: true, Client: &s3.Client{}})
			if tt.err == "" {
				assert.NilError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.err)
		})
	}
}

func TestPseudoVersion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	run := func(t *testing.T, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		// Isolated from the user's git config, which may sign tags.
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
			"GIT_AUTHOR_DATE=2026-11-03T14:22:33Z", "GIT_COMMITTER_DATE=2026-11-03T14:22:33Z",
		)
		out, err := cmd.Output()
		assert.NilError(t, err)
		return strings.TrimSpace(string(out))
	}
	commit := func(t *testing.T) string {
		t.Helper()
		run(t, "commit", "--allow-empty", "--quiet", "-m", "commit")
		return run(t, "rev-parse", "HEAD")[:12]
	}
	run(t, "init", "--quiet")

	t.Run("before the first release", func(t *testing.T) {
		hash := commit(t)
		v, err := pseudoVersion(ctx, dir)
		assert.NilError(t, err)
		assert.Equal(t, v, "v0.0.1-0.20261103142233-"+hash)
	})

	t.Run("after the newest release, ignoring other tags", func(t *testing.T) {
		run(t, "tag", "v0.9.0")
		run(t, "tag", "v0.10.0")
		run(t, "tag", "v0.11.0-rc.1")
		run(t, "tag", "v1")
		run(t, "tag", "other")
		hash := commit(t)
		v, err := pseudoVersion(ctx, dir)
		assert.NilError(t, err)
		assert.Equal(t, v, "v0.10.1-0.20261103142233-"+hash)
	})

	t.Run("not a repository", func(t *testing.T) {
		_, err := pseudoVersion(ctx, t.TempDir())
		assert.ErrorContains(t, err, "git log -1")
	})
}

func TestPublish(t *testing.T) {
	ctx := context.Background()
	fix := s3test.Default(ctx, t)

	// Publish writes checksums.txt into the build, so each publish gets its own.
	build := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		bin := filepath.Join(dir, "widget", "linux", "amd64", "widget")
		assert.NilError(t, os.MkdirAll(filepath.Dir(bin), 0o750))
		assert.NilError(t, os.WriteFile(bin, []byte("binary"), 0o600))
		return dir
	}
	get := func(t *testing.T, key string) (string, error) {
		t.Helper()
		resp, err := fix.Client.GetObject(ctx, &s3.GetObjectInput{Bucket: &fix.Bucket, Key: aws.String(key)})
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()
		b, err := io.ReadAll(resp.Body)
		return string(b), err
	}
	tags := func(t *testing.T, key string) map[string]string {
		t.Helper()
		resp, err := fix.Client.GetObjectTagging(ctx, &s3.GetObjectTaggingInput{Bucket: &fix.Bucket, Key: aws.String(key)})
		assert.NilError(t, err)
		got := map[string]string{}
		for _, tag := range resp.TagSet {
			got[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
		}
		return got
	}
	published := func(version string) []string {
		prefix := "github.com/circleci-functions/widget/" + version + "/"
		return []string{prefix + "function.yaml", prefix + "checksums.txt", prefix + "linux/amd64/widget"}
	}
	const releaseTxt = "github.com/circleci-functions/widget/release.txt"

	t.Run("a release moves release.txt", func(t *testing.T) {
		r := Releaser{name: "widget", version: "v0.7.0", bucket: fix.Bucket, client: fix.Client}
		assert.NilError(t, r.publish(ctx, build(t), []byte("name: widget\n")))

		for _, key := range published("v0.7.0") {
			assert.Check(t, cmp.DeepEqual(tags(t, key), map[string]string{}), key)
		}
		got, err := get(t, releaseTxt)
		assert.NilError(t, err)
		assert.Check(t, cmp.Equal(got, "v0.7.0"))
	})

	t.Run("a pre-release leaves release.txt and is tagged", func(t *testing.T) {
		const version = "v0.7.1-0.20261103142233-abcdef123456"
		r := Releaser{name: "widget", version: version, bucket: fix.Bucket, client: fix.Client, prerelease: true}
		assert.NilError(t, r.publish(ctx, build(t), []byte("name: widget\n")))

		for _, key := range published(version) {
			assert.Check(t, cmp.DeepEqual(tags(t, key), map[string]string{"prerelease": "true"}), key)
		}
		got, err := get(t, releaseTxt)
		assert.NilError(t, err)
		assert.Check(t, cmp.Equal(got, "v0.7.0"))
	})
}

// A pre-release names the step the way a config pins it.
func TestPin(t *testing.T) {
	r := Releaser{name: "widget", version: "v0.7.1-0.20261103142233-abcdef123456"}
	assert.Equal(t, r.pin(), `pin it with:

functions:
  widget: github.com/circleci-functions/widget@v0.7.1-0.20261103142233-abcdef123456
`)
}
