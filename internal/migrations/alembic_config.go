package migrations

import (
	"fmt"
	"os"
	"path"
	"strings"
)

func readConfig(t Tree, read Reader) ([]configuredLocation, bool, error) {
	declared, present, err := readManifestConfig(t, read)
	if err != nil {
		return nil, present, err
	}
	discovered, err := alembicINI(t, read)
	if err != nil {
		return nil, true, err
	}
	for _, c := range discovered {
		overridden := false
		for _, explicit := range declared {
			if explicit.Path == c.Path {
				overridden = true
				break
			}
		}
		if !overridden {
			declared = append(declared, c)
		}
	}
	return declared, present || len(discovered) > 0, nil
}

func configuredAlembicFile(file string, l location, config []configuredLocation) bool {
	for _, c := range config {
		if c.Tool != toolAlembic || c.Group != l.dir {
			continue
		}
		if path.Dir(file) == c.Path || c.Recursive && strings.HasPrefix(file, c.Path+"/") {
			return true
		}
	}
	return false
}

// alembicINI reads only literal repository paths. env.py is never imported.
func alembicINI(t Tree, read Reader) ([]configuredLocation, error) {
	out := []configuredLocation{}
	for _, f := range t.Files {
		if path.Base(f.Path) != "alembic.ini" {
			continue
		}
		b, e := read(f.OID)
		if e != nil {
			return nil, fmt.Errorf("Alembic configuration cannot be read")
		}
		values := map[string]string{}
		active := false
		lastKey := ""
		for _, line := range strings.Split(string(b), "\n") {
			indented := len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
				continue
			}
			if strings.HasPrefix(line, "[") {
				active = line == "[alembic]"
				continue
			}
			if !active {
				continue
			}
			if key, value, ok := strings.Cut(line, "="); ok {
				lastKey = strings.TrimSpace(key)
				values[lastKey] = strings.TrimSpace(value)
			} else if indented && lastKey != "" {
				values[lastKey] += "\n" + line
			} else {
				return nil, fmt.Errorf("Alembic computed configuration is unsupported")
			}
		}
		script := values["script_location"]
		if script == "" {
			continue
		}
		clean := func(value string) (string, error) {
			value = strings.ReplaceAll(value, "%(here)s", ".")
			if value == "" || strings.ContainsAny(value, "%$\\:\x00\r\n") || path.IsAbs(value) {
				return "", fmt.Errorf("Alembic location is outside the repository or nonliteral")
			}
			p := path.Clean(path.Join(path.Dir(f.Path), value))
			if p == ".." || strings.HasPrefix(p, "../") {
				return "", fmt.Errorf("Alembic location escapes repository")
			}
			return p, nil
		}
		group, e := clean(script)
		if e != nil {
			return nil, e
		}
		versions := values["version_locations"]
		roots := []string{}
		if versions == "" {
			roots = []string{path.Join(group, "versions")}
		} else {
			sep := values["path_separator"]
			if sep == "" {
				sep = values["version_path_separator"]
			}
			var parts []string
			switch sep {
			case "", "legacy":
				parts = strings.FieldsFunc(versions, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' })
			case "space":
				parts = strings.Fields(versions)
			case "newline":
				parts = strings.Split(versions, "\n")
			case "os":
				parts = strings.Split(versions, string(os.PathListSeparator))
			case ":", ";":
				parts = strings.Split(versions, sep)
			default:
				return nil, fmt.Errorf("Unsupported Alembic path separator")
			}
			for _, part := range parts {
				p, e := clean(strings.TrimSpace(part))
				if e != nil {
					return nil, e
				}
				roots = append(roots, p)
			}
		}
		recursive := values["recursive_version_locations"]
		if recursive != "" && recursive != "true" && recursive != "false" {
			return nil, fmt.Errorf("Unsupported Alembic recursive locations value")
		}
		for _, root := range roots {
			if !(group == "." || root == group || strings.HasPrefix(root, group+"/")) {
				return nil, fmt.Errorf("Alembic version location outside script directory needs explicit grouped locations")
			}
			out = append(out, configuredLocation{Path: root, Tool: toolAlembic, Group: group, Recursive: recursive == "true"})
		}
	}
	return out, nil
}
