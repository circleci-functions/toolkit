// Package functionyaml defines and builds the function.yaml schema.
package functionyaml

import (
	"fmt"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/alecthomas/kong"
	"go.yaml.in/yaml/v4"
)

// Descriptor is a function's published metadata.
//
// Flags and Default describe invoking the function without naming a command:
// Flags are accepted there directly, and Default names the command that runs
// when none is given, whose own flags are accepted there too.
//
// Outputs are the values the function sets for later steps to read. They belong
// to the function as a whole, so every command shares them.
//
// Example is the config a user writes to run the function, rendered by
// RenderExample from the flags' example values; it is derived, so a release
// overwrites any value already set.
type Descriptor struct {
	Name        string             `yaml:"name"`
	Description string             `yaml:"description,omitempty"`
	Version     string             `yaml:"version,omitempty"`
	Example     string             `yaml:"example,omitempty"`
	Default     string             `yaml:"default,omitempty"`
	Flags       []Flag             `yaml:"flags,omitempty"`
	Outputs     []Output           `yaml:"outputs,omitempty"`
	Commands    map[string]Command `yaml:"commands,omitempty"`
}

// Output describes one value the function sets for later steps to read. Type
// is string, bool, int or list, where a list is a list of strings. A Secret
// output is masked in the job's logs once it is set.
type Output struct {
	Name        string `yaml:"name"`
	Type        string `yaml:"type"`
	Description string `yaml:"description,omitempty"`
	Secret      bool   `yaml:"secret,omitempty"`
}

// Command describes one CLI subcommand, which may itself nest subcommands.
// Default names the nested command that runs when none is given.
type Command struct {
	Description string             `yaml:"description,omitempty"`
	Example     string             `yaml:"example,omitempty"`
	Default     string             `yaml:"default,omitempty"`
	Flags       []Flag             `yaml:"flags,omitempty"`
	Commands    map[string]Command `yaml:"commands,omitempty"`
}

// Flag describes one command-line flag. Hidden flags are never included.
//
// Example is a value to show for the flag in the descriptor's Example, read
// from an `example:"..."` struct tag. A required flag the root command accepts
// must have one, since the rendered step has to pass it.
type Flag struct {
	Name        string   `yaml:"name"`
	Short       string   `yaml:"short,omitempty"`
	Type        string   `yaml:"type"`
	Required    bool     `yaml:"required,omitempty"`
	Default     string   `yaml:"default,omitempty"`
	Enum        []string `yaml:"enum,omitempty"`
	Env         []string `yaml:"env,omitempty"`
	Negatable   bool     `yaml:"negatable,omitempty"`
	Example     string   `yaml:"example,omitempty"`
	Description string   `yaml:"description,omitempty"`
}

// FromKong builds a Descriptor from a kong.Kong.Model. Version is left for
// the caller to set.
func FromKong(app *kong.Application) Descriptor {
	return Descriptor{
		Name:        app.Name,
		Description: app.Help,
		Default:     defaultCommand(app.Node),
		Flags:       flagsFromNode(app.Node),
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
			Default:     defaultCommand(child),
			Flags:       flagsFromNode(child),
			Commands:    commandsFromNode(child),
		}
	}
	return out
}

// defaultCommand names the child that runs when no command is given, or "".
func defaultCommand(node *kong.Node) string {
	if node.DefaultCmd == nil {
		return ""
	}
	return node.DefaultCmd.Name
}

func flagsFromNode(node *kong.Node) []Flag {
	var out []Flag
	for _, f := range node.Flags {
		if f.Hidden || builtinFlag(f) {
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
		if f.Tag != nil {
			flag.Example = f.Tag.Get("example")
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

// kongPkgPath is where kong's own flag types live. Taken from an exported type
// so it tracks the module path instead of restating it.
var kongPkgPath = reflect.TypeOf(kong.VersionFlag(false)).PkgPath()

// builtinFlag reports whether f is one of kong's own flags (--help, --version,
// -C) rather than part of the function's surface. They sit on the root node and
// are not hidden, so only the declaring package tells them apart.
func builtinFlag(f *kong.Flag) bool {
	t := f.Target.Type()
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.PkgPath() == kongPkgPath
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
// during Parse, before any command runs — same mechanism as VersionFlag. Pass
// the outputs the function sets, if any, to include them in the schema.
func Embed(outputs ...Output) kong.Option {
	return kong.OptionFunc(func(k *kong.Kong) error {
		if err := kong.Embed(&describeFlag{}).Apply(k); err != nil {
			return err
		}
		return kong.Bind(describedOutputs(outputs)).Apply(k)
	})
}

// describedOutputs carries Embed's outputs to the --describe hook. It has a
// type of its own so it can't collide with anything the function binds.
type describedOutputs []Output

type describeFlag struct {
	Describe describeAction `hidden:"" help:"Print this function's schema and exit."`
}

type describeAction bool

// BeforeReset only fires when --describe is in argv; field values aren't
// applied yet, so d itself isn't checked here — same as VersionFlag.
func (d describeAction) BeforeReset(app *kong.Kong, outputs describedOutputs) error {
	desc := FromKong(app.Model)
	desc.Outputs = outputs
	example, err := RenderExample(desc, localRef(desc.Name))
	if err != nil {
		return err
	}
	desc.Example = example
	if err := Write(app.Stdout, desc); err != nil {
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

// Validate checks d has a name, every command/flag is named and typed with no
// duplicate flags, and every output has a valid, unique name and a known type.
func Validate(d Descriptor) error {
	if d.Name == "" {
		return fmt.Errorf("name is required")
	}
	if err := validateFlags(d.Flags, d.Name); err != nil {
		return err
	}
	if err := validateOutputs(d.Outputs, d.Name); err != nil {
		return err
	}
	if d.Default != "" {
		if _, ok := d.Commands[d.Default]; !ok {
			return fmt.Errorf("%s: default names an unknown command %q", d.Name, d.Default)
		}
	}
	if err := validateExample(d); err != nil {
		return err
	}
	return validateCommands(d.Commands, d.Name)
}

func validateCommands(cmds map[string]Command, path string) error {
	for name, cmd := range cmds {
		if name == "" {
			return fmt.Errorf("%s: command name is empty", path)
		}
		full := path + " " + name

		if err := validateFlags(cmd.Flags, full); err != nil {
			return err
		}
		if cmd.Default != "" {
			if _, ok := cmd.Commands[cmd.Default]; !ok {
				return fmt.Errorf("%s: default names an unknown command %q", full, cmd.Default)
			}
		}

		if err := validateCommands(cmd.Commands, full); err != nil {
			return err
		}
	}
	return nil
}

func validateFlags(flags []Flag, path string) error {
	seen := make(map[string]bool, len(flags))
	for _, f := range flags {
		if f.Name == "" {
			return fmt.Errorf("%s: flag name is empty", path)
		}
		if seen[f.Name] {
			return fmt.Errorf("%s: duplicate flag %q", path, f.Name)
		}
		seen[f.Name] = true
		if f.Type == "" {
			return fmt.Errorf("%s: flag %q has no type", path, f.Name)
		}
	}
	return nil
}

var (
	outputName  = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	outputTypes = []string{"string", "bool", "int", "list"}
)

func validateOutputs(outputs []Output, path string) error {
	seen := make(map[string]bool, len(outputs))
	for _, o := range outputs {
		if !outputName.MatchString(o.Name) {
			return fmt.Errorf("%s: output name %q must be letters, digits, - and _, starting with a letter", path, o.Name)
		}
		if seen[o.Name] {
			return fmt.Errorf("%s: duplicate output %q", path, o.Name)
		}
		seen[o.Name] = true
		if !slices.Contains(outputTypes, o.Type) {
			return fmt.Errorf("%s: output %q has type %q, must be one of %s", path, o.Name, o.Type, strings.Join(outputTypes, ", "))
		}
	}
	return nil
}

// validateExample checks a step can be rendered from d's root flags: each
// required one needs an example value, and every example must parse as its flag.
func validateExample(d Descriptor) error {
	flags, ok := rootFlags(d)
	if !ok {
		return nil
	}
	for _, f := range flags {
		if f.Required && f.Example == "" {
			return fmt.Errorf("%s: flag %q is required, so the published example needs a value for it: set one with an `example:\"...\"` struct tag, or example: on the flag in function.yaml", d.Name, f.Name)
		}
		if f.Example == "" {
			continue
		}
		if _, err := exampleValue(f); err != nil {
			return fmt.Errorf("%s: %w", d.Name, err)
		}
	}
	return nil
}
