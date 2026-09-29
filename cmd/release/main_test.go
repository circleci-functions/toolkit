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

func TestMarshalDescriptor_KeepsOutputs(t *testing.T) {
	in := []byte(`name: widget
outputs:
  - name: digest
    type: string
  - name: tags
    type: list
`)
	out, err := marshalDescriptor(in, "v1.0.0")
	assert.NilError(t, err)
	assert.Equal(t, string(out), `name: widget
version: v1.0.0
outputs:
    - name: digest
      type: string
    - name: tags
      type: list
`)
}

func TestMarshalDescriptor_RejectsInvalidOutput(t *testing.T) {
	in := []byte(`name: widget
outputs:
  - name: digest
    type: object
`)
	_, err := marshalDescriptor(in, "v1.0.0")
	assert.ErrorContains(t, err, `invalid function.yaml: widget: output "digest" has type "object"`)
}
