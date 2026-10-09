package indexer

import (
	"encoding/json"
	"path"
	"sort"
	"strings"
)

// TypeScript and JavaScript bare specifiers resolve through the nearest
// tsconfig.json/jsconfig.json (`baseUrl`, `paths`, relative `extends`) and
// through package.json names of packages inside the repository (workspaces).
// Package-based `extends`, project references, conditional export selection
// and node_modules are not consulted; results remain inferred.

type tsConfig struct {
	dir     string
	extends []string
	baseURL *string
	paths   map[string][]string
}

type workspacePackage struct {
	dir     string
	entries []string // package.json entry fields, most source-like first
	exports map[string]string
}

func (r *resolver) addTSConfig(p string, content []byte) {
	var raw struct {
		Extends         json.RawMessage `json:"extends"`
		CompilerOptions struct {
			BaseURL *string             `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if json.Unmarshal(stripJSONC(content), &raw) != nil {
		return
	}
	c := tsConfig{dir: path.Dir(p), baseURL: raw.CompilerOptions.BaseURL, paths: raw.CompilerOptions.Paths}
	var one string
	var many []string
	if json.Unmarshal(raw.Extends, &one) == nil && one != "" {
		c.extends = []string{one}
	} else if json.Unmarshal(raw.Extends, &many) == nil {
		c.extends = many
	}
	r.tsconfigs[p] = c
}

func (r *resolver) addPackage(p string, content []byte) {
	var raw struct {
		Name    string          `json:"name"`
		Source  string          `json:"source"`
		Types   string          `json:"types"`
		Typings string          `json:"typings"`
		Module  string          `json:"module"`
		Main    string          `json:"main"`
		Exports json.RawMessage `json:"exports"`
	}
	if json.Unmarshal(content, &raw) != nil || raw.Name == "" {
		return
	}
	pkg := workspacePackage{dir: path.Dir(p), exports: map[string]string{}}
	for _, e := range []string{raw.Source, raw.Types, raw.Typings, raw.Module, raw.Main} {
		if e != "" {
			pkg.entries = append(pkg.entries, e)
		}
	}
	var exports any
	if json.Unmarshal(raw.Exports, &exports) == nil {
		switch v := exports.(type) {
		case string:
			pkg.exports["."] = v
		case map[string]any:
			subpaths := false
			for key := range v {
				subpaths = subpaths || strings.HasPrefix(key, ".")
			}
			if !subpaths {
				v = map[string]any{".": v} // conditions for the root entry only
			}
			for key, target := range v {
				if t := exportTarget(target); t != "" {
					pkg.exports[key] = t
				}
			}
		}
	}
	if previous, ok := r.packages[raw.Name]; ok && previous.dir != pkg.dir {
		r.ambiguousPackages[raw.Name] = true
		return
	}
	r.packages[raw.Name] = pkg
}

// exportTarget picks a file from an exports value, preferring type and
// source-like conditions; nested condition objects are followed.
func exportTarget(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		for _, condition := range []string{"source", "types", "import", "module", "default", "require", "node"} {
			if s := exportTarget(t[condition]); s != "" {
				return s
			}
		}
	case []any:
		for _, item := range t {
			if s := exportTarget(item); s != "" {
				return s
			}
		}
	}
	return ""
}

// bareScript resolves a non-relative specifier through tsconfig aliases, then
// repository workspace packages.
func (r *resolver) bareScript(importer, spec string) []string {
	if c, ok := r.governingTSConfig(importer); ok {
		if found := r.tsAlias(c, spec); found != nil {
			return found
		}
	}
	return r.workspaceImport(spec)
}

// governingTSConfig finds the nearest tsconfig.json (or jsconfig.json).
func (r *resolver) governingTSConfig(importer string) (string, bool) {
	for dir := path.Dir(importer); ; dir = path.Dir(dir) {
		for _, name := range []string{"tsconfig.json", "jsconfig.json"} {
			p := path.Join(dir, name)
			if _, ok := r.tsconfigs[p]; ok {
				return p, true
			}
		}
		if dir == "." || dir == "/" || dir == "" {
			return "", false
		}
	}
}

// effective follows relative extends: the nearest config defining baseUrl or
// paths wins, each interpreted relative to the file that defines it.
func (r *resolver) effective(config string) (baseURL string, hasBase bool, paths map[string][]string, pathsDir string) {
	seen := map[string]bool{}
	var walk func(p string)
	walk = func(p string) {
		c, ok := r.tsconfigs[p]
		if !ok || seen[p] || len(seen) > 16 {
			return
		}
		seen[p] = true
		if c.baseURL != nil && !hasBase {
			baseURL, hasBase = path.Join(c.dir, *c.baseURL), true
		}
		if c.paths != nil && paths == nil {
			paths, pathsDir = c.paths, c.dir
		}
		for i := len(c.extends) - 1; i >= 0; i-- { // later entries override earlier ones
			e := c.extends[i]
			if !strings.HasPrefix(e, "./") && !strings.HasPrefix(e, "../") {
				continue // package-based extends are not resolved
			}
			target := path.Join(c.dir, e)
			if !strings.HasSuffix(target, ".json") {
				if _, ok := r.tsconfigs[target+".json"]; ok {
					target += ".json"
				} else {
					target = path.Join(target, "tsconfig.json")
				}
			}
			if within(target) {
				walk(target)
			}
		}
	}
	walk(config)
	return
}

func (r *resolver) tsAlias(config, spec string) []string {
	baseURL, hasBase, paths, pathsDir := r.effective(config)
	root := pathsDir
	if hasBase {
		root = baseURL
	}
	if paths != nil {
		pattern, capture, ok := matchPathPattern(paths, spec)
		if ok {
			for _, substitution := range paths[pattern] {
				target := path.Join(root, strings.Replace(substitution, "*", capture, 1))
				if within(target) {
					if found := r.scriptFile(target); found != nil {
						return found
					}
				}
			}
		}
	}
	if hasBase {
		if target := path.Join(baseURL, spec); within(target) {
			return r.scriptFile(target)
		}
	}
	return nil
}

// matchPathPattern applies TypeScript's rule: an exact pattern wins, then the
// wildcard pattern with the longest prefix.
func matchPathPattern(paths map[string][]string, spec string) (string, string, bool) {
	if _, ok := paths[spec]; ok && !strings.Contains(spec, "*") {
		return spec, "", true
	}
	best, capture, found := "", "", false
	patterns := make([]string, 0, len(paths))
	for p := range paths {
		patterns = append(patterns, p)
	}
	sort.Strings(patterns)
	for _, p := range patterns {
		prefix, suffix, ok := strings.Cut(p, "*")
		if !ok || strings.Contains(suffix, "*") {
			continue
		}
		if len(spec) >= len(prefix)+len(suffix) && strings.HasPrefix(spec, prefix) && strings.HasSuffix(spec, suffix) && (!found || len(prefix) > len(strings.SplitN(best, "*", 2)[0])) {
			best, capture, found = p, spec[len(prefix):len(spec)-len(suffix)], true
		}
	}
	return best, capture, found
}

func (r *resolver) workspaceImport(spec string) []string {
	name, sub := spec, ""
	if parts := strings.SplitN(spec, "/", 3); strings.HasPrefix(spec, "@") && len(parts) >= 2 {
		name = parts[0] + "/" + parts[1]
		if len(parts) == 3 {
			sub = parts[2]
		}
	} else if i := strings.Index(spec, "/"); i > 0 && !strings.HasPrefix(spec, "@") {
		name, sub = spec[:i], spec[i+1:]
	}
	pkg, ok := r.packages[name]
	if !ok || r.ambiguousPackages[name] {
		return nil
	}
	key := "."
	if sub != "" {
		key = "./" + sub
	}
	targets := []string{}
	if e, ok := pkg.exports[key]; ok {
		targets = append(targets, e)
	}
	if sub == "" {
		targets = append(targets, pkg.entries...)
		targets = append(targets, "src/index", "index")
	} else {
		targets = append(targets, sub, "src/"+sub)
	}
	for _, t := range targets {
		target := path.Join(pkg.dir, t)
		if !within(target) || (pkg.dir != "." && !strings.HasPrefix(target+"/", pkg.dir+"/")) {
			continue
		}
		if found := r.scriptFile(target); found != nil {
			return found
		}
		// Entries usually name build output; map it back to its source.
		if found := r.scriptFile(sourceForBuild(pkg.dir, target)); found != nil {
			return found
		}
	}
	return nil
}

// sourceForBuild maps dist/x.js, build/x.d.ts, lib/x.mjs or out/x.cjs below
// a package to src/x.
func sourceForBuild(dir, target string) string {
	rel := strings.TrimPrefix(target, dir+"/")
	if dir == "." {
		rel = target
	}
	for _, out := range []string{"dist/", "build/", "lib/", "out/"} {
		if rest, ok := strings.CutPrefix(rel, out); ok {
			rel = "src/" + rest
			break
		}
	}
	for _, ext := range []string{".d.ts", ".js", ".mjs", ".cjs", ".jsx"} {
		if stem, ok := strings.CutSuffix(rel, ext); ok {
			rel = stem
			break
		}
	}
	return path.Join(dir, rel)
}

// stripJSONC removes comments, then trailing commas, outside strings; tsconfig
// files commonly contain both.
func stripJSONC(b []byte) []byte {
	return stripTrailingCommas(stripComments(b))
}

// scanJSON calls visit for each byte outside string literals; visit returns
// how many bytes it consumed (0 copies the byte unchanged).
func scanJSON(b []byte, visit func(b []byte, i int, out *[]byte) int) []byte {
	out := make([]byte, 0, len(b))
	inString, escaped := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if n := visit(b, i, &out); n > 0 {
			i += n - 1
			continue
		}
		out = append(out, c)
	}
	return out
}

func stripComments(b []byte) []byte {
	return scanJSON(b, func(b []byte, i int, out *[]byte) int {
		if b[i] != '/' || i+1 >= len(b) {
			return 0
		}
		switch b[i+1] {
		case '/':
			j := i
			for j < len(b) && b[j] != '\n' {
				j++
			}
			return j - i // the newline itself is kept
		case '*':
			j := i + 2
			for j+1 < len(b) && (b[j] != '*' || b[j+1] != '/') {
				j++
			}
			*out = append(*out, ' ')
			return min(j+2, len(b)) - i
		}
		return 0
	})
}

func stripTrailingCommas(b []byte) []byte {
	return scanJSON(b, func(b []byte, i int, _ *[]byte) int {
		if b[i] != ',' {
			return 0
		}
		j := i + 1
		for j < len(b) && (b[j] == ' ' || b[j] == '\t' || b[j] == '\n' || b[j] == '\r') {
			j++
		}
		if j < len(b) && (b[j] == '}' || b[j] == ']') {
			return 1 // drop the comma
		}
		return 0
	})
}
