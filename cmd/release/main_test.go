package main

import (
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
