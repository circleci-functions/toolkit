package functionyaml_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"go.yaml.in/yaml/v4"
	"gotest.tools/v3/assert"

	"github.com/circleci-functions/toolkit/functionyaml"
)

func TestFromKong(t *testing.T) {
	type installCmd struct {
		Version string `help:"Version spec." default:"stable"`
		Force   bool   `help:"Force reinstall." negatable:""`
		Mode    string `help:"Mode." enum:"fast,slow" default:"fast"`
		Region  string `help:"Region." required:"" env:"AWS_DEFAULT_REGION" short:"r"`
		Secret  string `help:"Not part of the public surface." hidden:""`
	}
	type cacheCmd struct {
		Save struct {
			Key string `help:"Cache key."`
		} `cmd:"" help:"Save cache."`
		Load struct {
			Key string `help:"Cache key."`
		} `cmd:"" help:"Load cache."`
	}
	type cli struct {
		Install installCmd `cmd:"" help:"Install the thing."`
		Cache   cacheCmd   `cmd:"" help:"Save/restore cache."`
	}

	var flags cli
	k, err := kong.New(&flags, kong.Name("widget"), kong.Description("Installs a widget."))
	assert.NilError(t, err)

	d := functionyaml.FromKong(k.Model)

	assert.Equal(t, d.Name, "widget")
	assert.Equal(t, d.Description, "Installs a widget.")
	assert.Equal(t, d.Version, "")

	install, ok := d.Commands["install"]
	assert.Assert(t, ok, "expected an install command")
	assert.Equal(t, install.Description, "Install the thing.")
	assert.Equal(t, len(install.Commands), 0)

	byName := map[string]functionyaml.Flag{}
	for _, f := range install.Flags {
		byName[f.Name] = f
	}
	_, hidden := byName["secret"]
	assert.Assert(t, !hidden, "hidden flag leaked into schema")
	assert.Equal(t, len(install.Flags), 4)

	version := byName["version"]
	assert.Equal(t, version.Type, "string")
	assert.Equal(t, version.Default, "stable")
	assert.Equal(t, version.Required, false)

	force := byName["force"]
	assert.Equal(t, force.Type, "bool")
	assert.Equal(t, force.Negatable, true)

	mode := byName["mode"]
	assert.DeepEqual(t, mode.Enum, []string{"fast", "slow"})

	region := byName["region"]
	assert.Equal(t, region.Required, true)
	assert.Equal(t, region.Short, "r")
	assert.DeepEqual(t, region.Env, []string{"AWS_DEFAULT_REGION"})

	cache, ok := d.Commands["cache"]
	assert.Assert(t, ok, "expected a cache command")
	assert.Equal(t, len(cache.Flags), 0)
	_, ok = cache.Commands["save"]
	assert.Assert(t, ok, "expected a nested cache save command")
	_, ok = cache.Commands["load"]
	assert.Assert(t, ok, "expected a nested cache load command")
}

// Mirrors setup-go: the root command is a default subcommand, so a function
// invoked without naming a command accepts that subcommand's flags. Consumers
// validating a bare invocation need `default` to know where to look.
func TestFromKongDefaultCommand(t *testing.T) {
	type installCmd struct {
		Version string `help:"Version spec." default:"stable"`
	}
	type cacheCmd struct {
		Save struct{} `cmd:"" help:"Save cache."`
		Load struct{} `cmd:"" default:"withargs" help:"Load cache."`
	}
	type cli struct {
		Debug   bool       `help:"Log verbosely."`
		Install installCmd `cmd:"" default:"withargs" help:"Install the thing."`
		Cache   cacheCmd   `cmd:"" help:"Save/restore cache."`
	}

	var flags cli
	k, err := kong.New(&flags, kong.Name("widget"))
	assert.NilError(t, err)

	d := functionyaml.FromKong(k.Model)

	assert.Equal(t, d.Default, "install")
	assert.Equal(t, d.Commands["cache"].Default, "load")
	assert.Equal(t, d.Commands["install"].Default, "")

	// Root flags are the ones accepted before any command is named. kong's own
	// --help is a HelpFlag, not part of the function's surface.
	byName := map[string]functionyaml.Flag{}
	for _, f := range d.Flags {
		byName[f.Name] = f
	}
	_, hasHelp := byName["help"]
	assert.Assert(t, !hasHelp, "kong's --help leaked into the schema")
	assert.Equal(t, byName["debug"].Type, "bool")
	assert.Equal(t, len(d.Flags), 1)

	// A root flag stays on the root; it must not be duplicated onto children,
	// or a consumer would accept it after a command name too.
	for _, f := range d.Commands["install"].Flags {
		assert.Assert(t, f.Name != "debug", "root flag leaked onto a subcommand")
	}
}

func TestFromKongNoDefaultCommand(t *testing.T) {
	type cli struct {
		Install struct{} `cmd:""`
	}
	var flags cli
	k, err := kong.New(&flags, kong.Name("widget"))
	assert.NilError(t, err)

	d := functionyaml.FromKong(k.Model)
	assert.Equal(t, d.Default, "")
	assert.Equal(t, len(d.Flags), 0)
}

func TestFromKongPointerFlag(t *testing.T) {
	// kong uses *T fields to distinguish "unset" from a zero value.
	type installCmd struct {
		CoverMode *string `help:"Coverage mode." enum:",set,count,atomic"`
		Count     *int    `help:"Repeat count."`
	}
	type cli struct {
		Install installCmd `cmd:""`
	}
	var flags cli
	k, err := kong.New(&flags, kong.Name("widget"))
	assert.NilError(t, err)

	d := functionyaml.FromKong(k.Model)
	byName := map[string]functionyaml.Flag{}
	for _, f := range d.Commands["install"].Flags {
		byName[f.Name] = f
	}
	assert.Equal(t, byName["cover-mode"].Type, "string")
	assert.Equal(t, byName["count"].Type, "int")
}

func TestFromKongUninterpolatedDefault(t *testing.T) {
	type installCmd struct {
		InstallDir string `help:"Install dir." default:"${install_dir}"`
	}
	type cli struct {
		Install installCmd `cmd:""`
	}
	var flags cli
	k, err := kong.New(&flags, kong.Name("widget"), kong.Vars{"install_dir": "/home/whoever-built-this/.cache/widget"})
	assert.NilError(t, err)

	d := functionyaml.FromKong(k.Model)
	byName := map[string]functionyaml.Flag{}
	for _, f := range d.Commands["install"].Flags {
		byName[f.Name] = f
	}
	assert.Equal(t, byName["install-dir"].Default, "${install_dir}")
}

func TestEmbedDescribe(t *testing.T) {
	type installCmd struct {
		Version string `help:"Version." default:"stable"`
	}
	type cli struct {
		Install installCmd `cmd:"" default:"withargs"`
	}
	var flags cli
	k, err := kong.New(&flags, kong.Name("widget"), kong.Description("desc"), functionyaml.Embed())
	assert.NilError(t, err)

	var buf bytes.Buffer
	k.Stdout = &buf
	exitCode := -1
	k.Exit = func(code int) { exitCode = code }

	_, err = k.Parse([]string{"--describe"})
	assert.NilError(t, err)
	assert.Equal(t, exitCode, 0)

	var d functionyaml.Descriptor
	assert.NilError(t, yaml.Unmarshal(buf.Bytes(), &d))
	assert.Equal(t, d.Name, "widget")
	_, ok := d.Commands["install"]
	assert.Assert(t, ok)
	// The test binary's module stands in for the function's path.
	assert.Assert(t, strings.Contains(d.Example, "widget: github.com/circleci-functions/toolkit@<version>"), d.Example)
}

// Mirrors setup-node: no default subcommand, so the hook must fire and exit
// before kong's "must select a command" validation runs.
func TestEmbedDescribeNoDefaultSubcommand(t *testing.T) {
	type installCmd struct{}
	type cli struct {
		Install installCmd `cmd:""`
	}
	var flags cli
	k, err := kong.New(&flags, kong.Name("widget"), functionyaml.Embed())
	assert.NilError(t, err)

	var buf bytes.Buffer
	k.Stdout = &buf
	exitCode := -1
	k.Exit = func(code int) { exitCode = code }

	_, _ = k.Parse([]string{"--describe"})
	assert.Equal(t, exitCode, 0)
	assert.Assert(t, buf.Len() > 0)
}

func TestEmbedDescribeAbsent(t *testing.T) {
	type installCmd struct {
		Version string `help:"Version." default:"stable"`
	}
	type cli struct {
		Install installCmd `cmd:"" default:"withargs"`
	}
	var flags cli
	k, err := kong.New(&flags, kong.Name("widget"), functionyaml.Embed())
	assert.NilError(t, err)

	var buf bytes.Buffer
	k.Stdout = &buf
	exitCode := -1
	k.Exit = func(code int) { exitCode = code }

	_, err = k.Parse([]string{"--version", "1.22"})
	assert.NilError(t, err)
	assert.Equal(t, exitCode, -1, "should not have exited")
	assert.Equal(t, buf.Len(), 0, "should not have written anything")
	assert.Equal(t, flags.Install.Version, "1.22")
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		d       functionyaml.Descriptor
		wantErr string
	}{
		{
			name: "valid",
			d: functionyaml.Descriptor{
				Name: "widget",
				Commands: map[string]functionyaml.Command{
					"install": {Flags: []functionyaml.Flag{{Name: "version", Type: "string"}}},
				},
			},
		},
		{
			name:    "missing name",
			d:       functionyaml.Descriptor{},
			wantErr: "name is required",
		},
		{
			name: "empty command name",
			d: functionyaml.Descriptor{
				Name:     "widget",
				Commands: map[string]functionyaml.Command{"": {}},
			},
			wantErr: "command name is empty",
		},
		{
			name: "flag with no name",
			d: functionyaml.Descriptor{
				Name: "widget",
				Commands: map[string]functionyaml.Command{
					"install": {Flags: []functionyaml.Flag{{Type: "string"}}},
				},
			},
			wantErr: "flag name is empty",
		},
		{
			name: "duplicate flag",
			d: functionyaml.Descriptor{
				Name: "widget",
				Commands: map[string]functionyaml.Command{
					"install": {Flags: []functionyaml.Flag{
						{Name: "version", Type: "string"},
						{Name: "version", Type: "string"},
					}},
				},
			},
			wantErr: `duplicate flag "version"`,
		},
		{
			name: "flag with no type",
			d: functionyaml.Descriptor{
				Name: "widget",
				Commands: map[string]functionyaml.Command{
					"install": {Flags: []functionyaml.Flag{{Name: "version"}}},
				},
			},
			wantErr: `flag "version" has no type`,
		},
		{
			name: "root flag with no type",
			d: functionyaml.Descriptor{
				Name:  "widget",
				Flags: []functionyaml.Flag{{Name: "debug"}},
			},
			wantErr: `widget: flag "debug" has no type`,
		},
		{
			name: "duplicate root flag",
			d: functionyaml.Descriptor{
				Name: "widget",
				Flags: []functionyaml.Flag{
					{Name: "debug", Type: "bool"},
					{Name: "debug", Type: "bool"},
				},
			},
			wantErr: `duplicate flag "debug"`,
		},
		{
			name: "default names an unknown command",
			d: functionyaml.Descriptor{
				Name:     "widget",
				Default:  "install",
				Commands: map[string]functionyaml.Command{"cache": {}},
			},
			wantErr: `default names an unknown command "install"`,
		},
		{
			name: "nested default names an unknown command",
			d: functionyaml.Descriptor{
				Name: "widget",
				Commands: map[string]functionyaml.Command{
					"cache": {Default: "load", Commands: map[string]functionyaml.Command{"save": {}}},
				},
			},
			wantErr: `widget cache: default names an unknown command "load"`,
		},
		{
			name: "valid defaults",
			d: functionyaml.Descriptor{
				Name:    "widget",
				Default: "install",
				Flags:   []functionyaml.Flag{{Name: "debug", Type: "bool"}},
				Commands: map[string]functionyaml.Command{
					"install": {},
					"cache":   {Default: "load", Commands: map[string]functionyaml.Command{"load": {}}},
				},
			},
		},
		{
			name: "invalid nested subcommand",
			d: functionyaml.Descriptor{
				Name: "widget",
				Commands: map[string]functionyaml.Command{
					"cache": {Commands: map[string]functionyaml.Command{
						"save": {Flags: []functionyaml.Flag{{Name: "key"}}},
					}},
				},
			},
			wantErr: `flag "key" has no type`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := functionyaml.Validate(tt.d)
			if tt.wantErr == "" {
				assert.NilError(t, err)
				return
			}
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestWrite(t *testing.T) {
	d := functionyaml.Descriptor{
		Name:        "widget",
		Description: "Installs a widget.",
		Version:     "v1.0.0",
		Commands: map[string]functionyaml.Command{
			"install": {
				Description: "Install the thing.",
				Flags: []functionyaml.Flag{
					{Name: "version", Type: "string", Default: "stable", Description: "Version spec."},
				},
			},
		},
	}

	var buf bytes.Buffer
	assert.NilError(t, functionyaml.Write(&buf, d))

	var round functionyaml.Descriptor
	assert.NilError(t, yaml.Unmarshal(buf.Bytes(), &round))
	assert.DeepEqual(t, round, d)
}
