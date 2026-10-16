package bashenv

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Vars returns the environment variables the $BASH_ENV file at path exports,
// folded in file order so a later export of the same name wins.
//
// A single-quoted value, as ExportEnvVars writes, is taken literally, as a
// shell would take it. $NAME references in any other value are resolved
// against the exports seen so far, falling back to base — so the
// "export PATH=dir:$PATH" that AddToPath writes comes back as the full PATH a
// later job step would see. Pass nil for base to resolve against the current
// process environment.
//
// Only export statements are interpreted; alias and shopt lines (see AddAlias)
// are ignored.
func Vars(path string, base map[string]string) (map[string]string, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("read bash env %q: %w", path, err)
	}
	defer f.Close()

	lookup := func(name string) string {
		if base != nil {
			return base[name]
		}
		return os.Getenv(name)
	}

	vars := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		name, value, ok := parseExport(scanner.Text())
		if !ok {
			continue
		}
		if literal, ok := singleQuoted(value); ok {
			vars[name] = literal
			continue
		}
		vars[name] = os.Expand(unquote(value), func(ref string) string {
			if v, seen := vars[ref]; seen {
				return v
			}
			return lookup(ref)
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read bash env %q: %w", path, err)
	}
	return vars, nil
}

// Path returns the PATH a later job step sees after sourcing the $BASH_ENV
// file at path, resolving $PATH against the current process environment.
func Path(path string) (string, error) {
	vars, err := Vars(path, nil)
	if err != nil {
		return "", err
	}
	if p, ok := vars["PATH"]; ok {
		return p, nil
	}
	return os.Getenv("PATH"), nil
}

func parseExport(line string) (name, value string, ok bool) {
	rest, found := strings.CutPrefix(strings.TrimSpace(line), "export ")
	if !found {
		return "", "", false
	}
	name, value, found = strings.Cut(rest, "=")
	if !found || name == "" {
		return "", "", false
	}
	return name, value, true
}

// singleQuoted reads v as a shell would when it is made only of single-quoted
// runs and backslash-escaped characters, the form quote writes. ok is false for
// anything else, which may need expanding.
func singleQuoted(v string) (literal string, ok bool) {
	if !strings.HasPrefix(v, "'") {
		return "", false
	}
	var sb strings.Builder
	for v != "" {
		switch v[0] {
		case '\'':
			end := strings.IndexByte(v[1:], '\'')
			if end < 0 {
				return "", false
			}
			sb.WriteString(v[1 : 1+end])
			v = v[2+end:]
		case '\\':
			if len(v) < 2 {
				return "", false
			}
			sb.WriteByte(v[1])
			v = v[2:]
		default:
			return "", false
		}
	}
	return sb.String(), true
}

func unquote(v string) string {
	for _, q := range []string{`"`, `'`} {
		if len(v) >= 2 && strings.HasPrefix(v, q) && strings.HasSuffix(v, q) {
			return v[1 : len(v)-1]
		}
	}
	return v
}
