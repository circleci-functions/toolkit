package functionyaml

import (
	"bytes"
	"fmt"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v4"
)

// RenderExample returns the config a user would write to run d's root command
// from ref (path@version): the functions block and one step passing every
// required flag plus any flag tagged with an example value. Returns "" when the
// root command can't be run without naming a subcommand.
func RenderExample(d Descriptor, ref string) (string, error) {
	flags, ok := rootFlags(d)
	if !ok {
		return "", nil
	}

	with := map[string]any{}
	for _, f := range flags {
		if f.Example == "" {
			continue
		}
		v, err := exampleValue(f)
		if err != nil {
			return "", err
		}
		with[f.Name] = v
	}

	var step any = d.Name
	if len(with) > 0 {
		step = map[string]any{d.Name: map[string]any{"with": with}}
	}

	config := struct {
		Functions map[string]string `yaml:"functions"`
		Jobs      map[string]any    `yaml:"jobs"`
	}{
		Functions: map[string]string{d.Name: ref},
		Jobs:      map[string]any{"build": map[string]any{"steps": []any{step}}},
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(config); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// rootFlags returns the flags a step can pass under `with`: the root's own plus
// its default command's. ok is false when the root has commands but no default.
func rootFlags(d Descriptor) (flags []Flag, ok bool) {
	if d.Default == "" {
		return d.Flags, len(d.Commands) == 0
	}
	return slices.Concat(d.Flags, d.Commands[d.Default].Flags), true
}

// exampleValue converts f's example to the YAML type its flag takes, so a
// version like "1.20" stays a string and a bool isn't quoted.
func exampleValue(f Flag) (any, error) {
	if len(f.Enum) > 0 && !slices.Contains(f.Enum, f.Example) {
		return nil, fmt.Errorf("flag %q: example %q is not one of %v", f.Name, f.Example, f.Enum)
	}
	var v any
	var err error
	switch f.Type {
	case "bool":
		v, err = strconv.ParseBool(f.Example)
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		v, err = strconv.ParseInt(f.Example, 10, 64)
	case "float32", "float64":
		v, err = strconv.ParseFloat(f.Example, 64)
	case "list":
		v = strings.Split(f.Example, ",")
	default:
		v = f.Example
	}
	if err != nil {
		return nil, fmt.Errorf("flag %q: example %q is not a valid %s", f.Name, f.Example, f.Type)
	}
	return v, nil
}

// localRef is the ref --describe renders with: the binary's module path, which
// is the function's path, and a placeholder for the version a release fills in.
func localRef(name string) string {
	path := name
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Path != "" {
		path = bi.Main.Path
	}
	return path + "@<version>"
}
