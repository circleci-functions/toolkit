package functionyaml_test

import (
	"testing"

	"github.com/alecthomas/kong"
	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/functionyaml"
)

const ref = "github.com/circleci-functions/widget@v1.0.0"

func describe(t *testing.T, cli any) functionyaml.Descriptor {
	t.Helper()
	k, err := kong.New(cli, kong.Name("widget"))
	assert.NilError(t, err)
	return functionyaml.FromKong(k.Model)
}

func TestRenderExampleBare(t *testing.T) {
	var cli struct {
		Version string `default:"stable"`
	}
	got, err := functionyaml.RenderExample(describe(t, &cli), ref)
	assert.NilError(t, err)
	assert.Equal(t, got, `functions:
  widget: github.com/circleci-functions/widget@v1.0.0
jobs:
  build:
    steps:
      - widget
`)
}

func TestRenderExampleTypedValues(t *testing.T) {
	var cli struct {
		Install struct {
			Version string `default:"stable" example:"1.20"`
			Cache   bool   `example:"false"`
			Retries int    `example:"3"`
			Mode    string `enum:"fast,slow" default:"fast"`
			Region  string `required:"" example:"us-east-1"`
		} `cmd:"" default:"withargs"`
	}
	d := describe(t, &cli)
	assert.NilError(t, functionyaml.Validate(d))

	got, err := functionyaml.RenderExample(d, ref)
	assert.NilError(t, err)
	assert.Equal(t, got, `functions:
  widget: github.com/circleci-functions/widget@v1.0.0
jobs:
  build:
    steps:
      - widget:
          with:
            cache: false
            region: us-east-1
            retries: 3
            version: "1.20"
`)
}

func TestRenderExampleNoRootCommand(t *testing.T) {
	var cli struct {
		Install struct{} `cmd:""`
	}
	got, err := functionyaml.RenderExample(describe(t, &cli), ref)
	assert.NilError(t, err)
	assert.Equal(t, got, "")
}

func TestValidateExample(t *testing.T) {
	tests := []struct {
		name    string
		cli     any
		wantErr string
	}{
		{
			name: "required flag without an example",
			cli: &struct {
				RoleARN string `name:"role-arn" required:""`
			}{},
			wantErr: "flag \"role-arn\" is required, so the published example needs a value for it: set one with an `example:\"...\"` struct tag, or example: on the flag in function.yaml",
		},
		{
			name: "required flag on a non-default command is not checked",
			cli: &struct {
				Profile struct {
					RoleARN string `name:"role-arn" required:""`
				} `cmd:""`
				Install struct{} `cmd:"" default:"1"`
			}{},
		},
		{
			name: "example outside the enum",
			cli: &struct {
				Mode string `enum:"fast,slow" default:"fast" example:"medium"`
			}{},
			wantErr: `flag "mode": example "medium" is not one of [fast slow]`,
		},
		{
			name: "example that isn't a bool",
			cli: &struct {
				Cache bool `example:"yes please"`
			}{},
			wantErr: `flag "cache": example "yes please" is not a valid bool`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := functionyaml.Validate(describe(t, tt.cli))
			if tt.wantErr == "" {
				assert.NilError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}
