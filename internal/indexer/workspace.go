package indexer

import (
	"encoding/json"
	"encoding/xml"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Jake-Network/radar/internal/model"
	"gopkg.in/yaml.v3"
)

// Workspace manifests are data, never executable configuration. Only literal
// member paths are accepted; dynamic scripts remain unresolved diagnostics.
type workspaceState struct {
	manifests       map[string][]byte
	members         map[string]bool
	pythonRoots     []string
	pythonIncludes  map[string][]string
	crates          map[string]string
	ambiguousCrates map[string]bool
	diagnostics     []model.Diagnostic
}

func newWorkspaceState() workspaceState {
	return workspaceState{manifests: map[string][]byte{}, pythonIncludes: map[string][]string{}, members: map[string]bool{}, crates: map[string]string{}, ambiguousCrates: map[string]bool{}}
}

func workspaceManifest(p string) bool {
	switch path.Base(p) {
	case "pyproject.toml", "Cargo.toml", "go.work", "pnpm-workspace.yaml", "pom.xml", "settings.gradle", "settings.gradle.kts":
		return true
	}
	return false
}

var literalString = regexp.MustCompile(`"([^"\n]*)"|'([^'\n]*)'`)

func literals(s string) []string {
	var out []string
	for _, m := range literalString.FindAllStringSubmatch(s, -1) {
		if m[1] != "" {
			out = append(out, m[1])
		} else {
			out = append(out, m[2])
		}
	}
	return out
}

func stripManifestComment(line string) string {
	var quote rune
	escaped := false
	for i, c := range line {
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && quote == '"' {
			escaped = true
			continue
		}
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '#' {
			return line[:i]
		}
	}
	return line
}

// literalMembers accepts only complete arrays of literal strings. In particular,
// a comment containing a quoted path cannot turn into a workspace member.
func literalMembers(value string) ([]string, bool) {
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		lines = append(lines, stripManifestComment(line))
	}
	value = strings.TrimSpace(strings.Join(lines, "\n"))
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil, false
	}
	rest := strings.TrimSpace(value[1 : len(value)-1])
	var out []string
	for rest != "" {
		if rest[0] != '"' && rest[0] != '\'' {
			return nil, false
		}
		quote := rest[0]
		end := strings.IndexByte(rest[1:], quote)
		if end < 0 {
			return nil, false
		}
		end++
		item := rest[1:end]
		if strings.ContainsAny(item, "\\\n\r") {
			return nil, false
		}
		out = append(out, item)
		rest = strings.TrimSpace(rest[end+1:])
		if rest == "" {
			break
		}
		if rest[0] != ',' {
			return nil, false
		}
		rest = strings.TrimSpace(rest[1:])
	}
	return out, true
}

func literalScalar(value string) []string {
	items, ok := literalMembers("[" + value + "]")
	if !ok || len(items) != 1 {
		return nil
	}
	return items
}

// tomlValue handles literal assignments including multiline arrays. It declines
// computed values instead of pretending an incomplete member list is complete.
func tomlValue(content, table, key string) string {
	section := ""
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(stripManifestComment(line))
		if strings.HasPrefix(line, "[") && !strings.HasPrefix(line, "[[") {
			section = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
			continue
		}
		if section != table {
			continue
		}
		left, right, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(left) != key {
			continue
		}
		value := strings.TrimSpace(right)
		if strings.HasPrefix(value, "[") {
			for j := i + 1; !strings.Contains(value, "]") && j < len(lines); j++ {
				value += "\n" + stripManifestComment(lines[j])
			}
		}
		return value
	}
	return ""
}
func (r *resolver) workspaceUnknown(p, reason string) {
	r.workspace.diagnostics = append(r.workspace.diagnostics, model.Diagnostic{Path: p, Severity: model.SeverityWarning, Message: "workspace import resolution unknown: " + reason})
}
func (r *resolver) expandMembers(manifest string, patterns []string, marker string) []string {
	found := map[string]bool{}
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") {
			continue
		}
		if strings.Contains(pattern, "**") {
			r.workspaceUnknown(manifest, "recursive member glob is unsupported")
			continue
		}
		full := path.Join(path.Dir(manifest), pattern)
		if !within(full) || strings.ContainsAny(pattern, "$\\") {
			r.workspaceUnknown(manifest, "member path is outside repository or dynamic")
			continue
		}
		for p := range r.files {
			if path.Base(p) != marker {
				continue
			}
			dir := path.Dir(p)
			match, err := path.Match(full, dir)
			if err != nil {
				r.workspaceUnknown(manifest, "unsupported member glob")
				break
			}
			if match {
				found[dir] = true
			}
		}
	}
	// pnpm exclusions apply after all positive patterns, independent of order.
	for _, pattern := range patterns {
		if strings.HasPrefix(pattern, "!") {
			for dir := range found {
				if match, _ := path.Match(path.Join(path.Dir(manifest), pattern[1:]), dir); match {
					delete(found, dir)
				}
			}
		}
	}
	out := []string{}
	for dir := range found {
		out = append(out, dir)
		r.workspace.members[dir] = true
	}
	sort.Strings(out)
	return out
}

func (r *resolver) prepareWorkspace() {
	keys := []string{}
	for p := range r.workspace.manifests {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for _, p := range keys {
		content := r.workspace.manifests[p]
		s := string(content)
		switch path.Base(p) {
		case "pyproject.toml":
			value := tomlValue(s, "tool.uv.workspace", "members")
			if value != "" {
				members, ok := literalMembers(value)
				if !ok {
					r.workspaceUnknown(p, "uv members must be a literal array")
					continue
				}
				excludes := tomlValue(s, "tool.uv.workspace", "exclude")
				if excludes != "" {
					items, valid := literalMembers(excludes)
					if !valid {
						r.workspaceUnknown(p, "uv exclude must be a literal array")
						continue
					}
					for _, item := range items {
						members = append(members, "!"+item)
					}
				}
				for _, dir := range r.expandMembers(p, members, "pyproject.toml") {
					r.workspace.pythonRoots = append(r.workspace.pythonRoots, dir, path.Join(dir, "src"))
				}
			}
			// Poetry may explicitly place import packages under an arbitrary source root.
			packages := tomlValue(s, "tool.poetry", "packages")
			if packages != "" {
				items, valid := poetryPackages(packages)
				if !valid {
					r.workspaceUnknown(p, "Poetry packages must be literal include/from tables")
					continue
				}
				for _, item := range items {
					root := path.Join(path.Dir(p), item.from)
					if !within(root) || !within(item.include) {
						r.workspaceUnknown(p, "Poetry package path outside repository")
						continue
					}
					r.workspace.members[path.Dir(p)] = true
					r.workspace.pythonRoots = append(r.workspace.pythonRoots, root)
					r.workspace.pythonIncludes[root] = append(r.workspace.pythonIncludes[root], item.include)
				}
			}

		case "Cargo.toml":
			value := tomlValue(s, "workspace", "members")
			if value != "" {
				members, ok := literalMembers(value)
				if !ok {
					r.workspaceUnknown(p, "Cargo members must be a literal array")
					continue
				}
				excludes := tomlValue(s, "workspace", "exclude")
				if excludes != "" {
					items, valid := literalMembers(excludes)
					if !valid {
						r.workspaceUnknown(p, "Cargo exclude must be a literal array")
						continue
					}
					for _, item := range items {
						members = append(members, "!"+item)
					}
				}
				for _, dir := range r.expandMembers(p, members, "Cargo.toml") {
					member := string(r.workspace.manifests[path.Join(dir, "Cargo.toml")])
					name := literalScalar(tomlValue(member, "lib", "name"))
					if len(name) == 0 {
						name = literalScalar(tomlValue(member, "package", "name"))
					}
					if len(name) == 0 {
						r.workspaceUnknown(p, "Cargo member has no literal package name")
						continue
					}
					crate := strings.ReplaceAll(name[0], "-", "_")
					entry := path.Join(dir, "src/lib.rs")
					if v := literalScalar(tomlValue(member, "lib", "path")); len(v) > 0 {
						entry = path.Join(dir, v[0])
					}
					if !within(entry) {
						r.workspaceUnknown(p, "Cargo lib path outside repository")
						continue
					}
					if previous, ok := r.workspace.crates[crate]; ok && previous != entry {
						r.workspace.ambiguousCrates[crate] = true
						r.workspaceUnknown(p, "ambiguous Cargo crate name")
					}
					r.workspace.crates[crate] = entry
				}
			}
		case "package.json":
			var raw struct{ Workspaces json.RawMessage }
			if json.Unmarshal(content, &raw) != nil {
				continue
			}
			var members []string
			if json.Unmarshal(raw.Workspaces, &members) != nil {
				var v struct{ Packages []string }
				if json.Unmarshal(raw.Workspaces, &v) == nil {
					members = v.Packages
				}
			}
			r.expandMembers(p, members, "package.json")
		case "pnpm-workspace.yaml":
			var raw struct {
				Packages []string `yaml:"packages"`
			}
			if yaml.Unmarshal(content, &raw) != nil {
				r.workspaceUnknown(p, "invalid pnpm member list")
				continue
			}
			r.expandMembers(p, raw.Packages, "package.json")
		case "go.work":
			block := false
			for _, line := range strings.Split(s, "\n") {
				line = strings.TrimSpace(strings.SplitN(line, "//", 2)[0])
				if line == "use (" {
					block = true
					continue
				}
				if block && line == ")" {
					block = false
					continue
				}
				if !block {
					if !strings.HasPrefix(line, "use ") {
						continue
					}
					line = strings.TrimSpace(strings.TrimPrefix(line, "use "))
				}
				if unquoted, err := strconv.Unquote(line); err == nil {
					line = unquoted
				}
				if line != "" {
					r.expandMembers(p, []string{line}, "go.mod")
				}
			}
		case "pom.xml":
			var raw struct {
				Modules []string `xml:"modules>module"`
			}
			if xml.Unmarshal(content, &raw) != nil {
				r.workspaceUnknown(p, "invalid Maven modules")
				continue
			}
			r.expandMembers(p, raw.Modules, "pom.xml")
		case "settings.gradle", "settings.gradle.kts":
			for _, line := range strings.Split(s, "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "include") || strings.HasPrefix(line, "includeBuild") {
					continue
				}
				rest := strings.TrimSpace(strings.TrimPrefix(line, "include"))
				names := literals(rest)
				leftover := literalString.ReplaceAllString(rest, "")
				leftover = strings.Trim(leftover, " (),\t\r")
				if leftover != "" || len(names) == 0 {
					r.workspaceUnknown(p, "dynamic Gradle include is unsupported")
					continue
				}
				for _, name := range names {
					if strings.ContainsAny(name, "$\\") {
						r.workspaceUnknown(p, "interpolated Gradle include is unsupported")
						continue
					}
					dir := path.Join(path.Dir(p), strings.ReplaceAll(strings.TrimPrefix(name, ":"), ":", "/"))
					if within(dir) {
						r.workspace.members[dir] = true
					}
				}
			}
		default:
			if strings.HasPrefix(path.Base(p), "tsconfig") {
				var raw struct{ References []struct{ Path string } }
				if json.Unmarshal(stripJSONC(content), &raw) == nil {
					for _, ref := range raw.References {
						dir := path.Join(path.Dir(p), ref.Path)
						if strings.HasSuffix(dir, ".json") {
							dir = path.Dir(dir)
						}
						if within(dir) {
							r.workspace.members[dir] = true
						} else {
							r.workspaceUnknown(p, "TypeScript reference outside repository")
						}
					}
				}
			}
		}
	}
	sort.Strings(r.workspace.pythonRoots)
}

func (r *resolver) workspaceEdge(importer, target string) bool {
	owner := func(p string) string {
		best := ""
		for dir := range r.workspace.members {
			if (dir == "." || strings.HasPrefix(p, dir+"/")) && len(dir) > len(best) {
				best = dir
			}
		}
		return best
	}
	a, b := owner(importer), owner(target)
	return b != "" && a != b
}

type poetryPackage struct{ include, from string }

func poetryPackages(value string) ([]poetryPackage, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "[") || !strings.HasSuffix(value, "]") {
		return nil, false
	}
	rest := strings.TrimSpace(value[1 : len(value)-1])
	var out []poetryPackage
	for rest != "" {
		if rest[0] != '{' {
			return nil, false
		}
		end := strings.IndexByte(rest, '}')
		if end < 0 {
			return nil, false
		}
		fields := strings.Split(rest[1:end], ",")
		item := poetryPackage{}
		for _, field := range fields {
			key, raw, ok := strings.Cut(field, "=")
			if !ok {
				return nil, false
			}
			key = strings.TrimSpace(key)
			values := literalScalar(strings.TrimSpace(raw))
			if len(values) != 1 {
				return nil, false
			}
			switch key {
			case "include":
				item.include = values[0]
			case "from":
				item.from = values[0]
			default:
				return nil, false
			}
		}
		if item.include == "" || strings.ContainsAny(item.include, "*?[") {
			return nil, false
		}
		out = append(out, item)
		rest = strings.TrimSpace(rest[end+1:])
		if rest == "" {
			break
		}
		if rest[0] != ',' {
			return nil, false
		}
		rest = strings.TrimSpace(rest[1:])
	}
	return out, true
}
