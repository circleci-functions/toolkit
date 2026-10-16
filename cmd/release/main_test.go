package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"gotest.tools/v3/assert"
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
		})
	}
}
