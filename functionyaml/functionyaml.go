// Package functionyaml defines and builds the function.yaml schema.
package functionyaml

import (
	"fmt"
	"io"
	"reflect"

	"github.com/alecthomas/kong"
	"go.yaml.in/yaml/v4"
)

// Descriptor is a function's published metadata.
type Descriptor struct {
	Name        string             `yaml:"name"`
	Description string             `yaml:"description,omitempty"`
	Version     string             `yaml:"version,omitempty"`
	Commands    map[string]Command `yaml:"commands,omitempty"`
}

// Command describes one CLI subcommand, which may itself nest subcommands.
type Command struct {
	Description string             `yaml:"description,omitempty"`
	Example     string             `yaml:"example,omitempty"`
	Flags       []Flag             `yaml:"flags,omitempty"`
	Commands    map[string]Command `yaml:"commands,omitempty"`
}

// Flag describes one command-line flag. Hidden flags are never included.
type Flag struct {
	Name        string   `yaml:"name"`
	Short       string   `yaml:"short,omitempty"`
	Type        string   `yaml:"type"`
	Required    bool     `yaml:"required,omitempty"`
	Default     string   `yaml:"default,omitempty"`
	Enum        []string `yaml:"enum,omitempty"`
	Env         []string `yaml:"env,omitempty"`
	Negatable   bool     `yaml:"negatable,omitempty"`
	Description string   `yaml:"description,omitempty"`
}

// FromKong builds a Descriptor from a kong.Kong.Model. Version is left for
// the caller to set.
func FromKong(app *kong.Application) Descriptor {
	return Descriptor{
		Name:        app.Name,
		Description: app.Help,
		Commands:    commandsFromNode(app.Node),
	}
}

func commandsFromNode(node *kong.Node) map[string]Command {
	var out map[string]Command
	for _, child := range node.Children {
		if child.Type != kong.CommandNode {
			continue
		}
		if out == nil {
			out = make(map[string]Command, len(node.Children))
		}
		out[child.Name] = Command{
			Description: child.Help,
			Flags:       flagsFromNode(child),
			Commands:    commandsFromNode(child),
		}
	}
	return out
}

func flagsFromNode(node *kong.Node) []Flag {
	var out []Flag
	for _, f := range node.Flags {
		if f.Hidden {
			continue
		}
		flag := Flag{
			Name:        f.Name,
			Type:        flagType(f.Target),
			Required:    f.Required,
			Env:         f.Envs,
			Negatable:   f.Tag != nil && f.Tag.Negatable != "",
			Description: f.Help,
		}
		if f.Short != 0 {
			flag.Short = string(f.Short)
		}
		if f.HasDefault {
			// Tag.Default is pre-interpolation, unlike Value.Default.
			flag.Default = f.Tag.Default
		}
		if f.Enum != "" {
			flag.Enum = f.EnumSlice()
		}
		out = append(out, flag)
	}
	return out
}

// flagType returns a flag's Go kind, unwrapping kong's *T fields.
func flagType(target reflect.Value) string {
	t := target.Type()
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Kind().String()
}

// Embed adds a hidden --describe flag that prints the schema and exits
// during Parse, before any command runs — same mechanism as VersionFlag.
func Embed() kong.Option {
	return kong.Embed(&describeFlag{})
}

type describeFlag struct {
	Describe describeAction `hidden:"" help:"Print this function's schema and exit."`
}

type describeAction bool

// BeforeReset only fires when --describe is in argv; field values aren't
// applied yet, so d itself isn't checked here — same as VersionFlag.
func (d describeAction) BeforeReset(app *kong.Kong) error {
	if err := Write(app.Stdout, FromKong(app.Model)); err != nil {
		return err
	}
	app.Exit(0)
	return nil
}

// Write YAML-encodes d to w.
func Write(w io.Writer, d Descriptor) error {
	enc := yaml.NewEncoder(w)
	defer enc.Close()
	return enc.Encode(d)
}

// Validate checks d has a name, and every command/flag is named and typed,
// with no duplicate flags.
func Validate(d Descriptor) error {
	if d.Name == "" {
		return fmt.Errorf("name is required")
	}
	return validateCommands(d.Commands, d.Name)
}

func validateCommands(cmds map[string]Command, path string) error {
	for name, cmd := range cmds {
		if name == "" {
			return fmt.Errorf("%s: command name is empty", path)
		}
		full := path + " " + name

		seen := make(map[string]bool, len(cmd.Flags))
		for _, f := range cmd.Flags {
			if f.Name == "" {
				return fmt.Errorf("%s: flag name is empty", full)
			}
			if seen[f.Name] {
				return fmt.Errorf("%s: duplicate flag %q", full, f.Name)
			}
			seen[f.Name] = true
			if f.Type == "" {
				return fmt.Errorf("%s: flag %q has no type", full, f.Name)
			}
		}

		if err := validateCommands(cmd.Commands, full); err != nil {
			return err
		}
	}
	return nil
}
