package indexer

import (
	"bufio"
	"bytes"
	"path"
	"sort"
	"strings"

	"github.com/radar-engine/radar/internal/languages"
	"github.com/radar-engine/radar/internal/model"
)

// resolver maps import specifiers to repository files using each language's
// path conventions. Results are inferred: no compiler, tsconfig path alias,
// PYTHONPATH or Cargo workspace configuration is consulted.
type resolver struct {
	files   map[string]bool // every non-excluded repository path
	sources map[string]bool // indexed source files
	byDir   map[string][]string
	imports map[string][]languages.Import
	goMods  map[string]string // go.mod directory -> module path
}

func newResolver() *resolver {
	return &resolver{files: map[string]bool{}, sources: map[string]bool{}, byDir: map[string][]string{}, imports: map[string][]languages.Import{}, goMods: map[string]string{}}
}

func (r *resolver) addSource(p string, imports []languages.Import) {
	r.sources[p] = true
	dir := path.Dir(p)
	r.byDir[dir] = append(r.byDir[dir], p)
	r.imports[p] = imports
}

func (r *resolver) addGoModule(p string, content []byte) {
	s := bufio.NewScanner(bytes.NewReader(content))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if rest, ok := strings.CutPrefix(line, "module"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			module := strings.Trim(strings.TrimSpace(rest), `"`)
			if module != "" {
				r.goMods[path.Dir(p)] = module
			}
			return
		}
	}
}

// edges returns inferred file-to-file DEPENDS_ON relationships.
func (r *resolver) edges(repository, revision string) []model.Edge {
	importers := make([]string, 0, len(r.imports))
	for p := range r.imports {
		importers = append(importers, p)
	}
	sort.Strings(importers)
	seen := map[string]bool{}
	out := []model.Edge{}
	for _, importer := range importers {
		for _, imp := range r.imports[importer] {
			for _, target := range r.resolve(importer, imp) {
				if target == importer || !r.sources[target] {
					continue
				}
				from, to := model.FileID(importer), model.FileID(target)
				id := model.StableID(from, "DEPENDS_ON", to)
				if seen[id] {
					continue
				}
				seen[id] = true
				out = append(out, model.Edge{ID: id, From: from, To: to, Kind: "DEPENDS_ON", Provenance: model.Provenance{Repository: repository, Revision: revision, Path: importer, Line: imp.Line, Method: "import_path_resolution:" + imp.Language, Evidence: model.Inferred}})
			}
		}
	}
	return out
}

func (r *resolver) resolve(importer string, imp languages.Import) []string {
	switch imp.Language {
	case "typescript", "javascript":
		return r.script(importer, imp.Module)
	case "python":
		return r.python(importer, imp.Module, imp.Names)
	case "go":
		return r.goPackage(imp.Module)
	case "rust":
		return r.rust(importer, imp.Module)
	}
	return nil
}

func (r *resolver) first(candidates ...string) []string {
	for _, c := range candidates {
		if r.sources[c] {
			return []string{c}
		}
	}
	return nil
}

// within keeps resolution inside the repository.
func within(p string) bool {
	return p != ".." && !strings.HasPrefix(p, "../") && !strings.HasPrefix(p, "/")
}

var scriptExtensions = []string{".ts", ".tsx", ".d.ts", ".js", ".jsx", ".mjs", ".cjs", ".mts", ".cts"}

func (r *resolver) script(importer, spec string) []string {
	if spec != "." && spec != ".." && !strings.HasPrefix(spec, "./") && !strings.HasPrefix(spec, "../") {
		return nil // package or alias specifier
	}
	base := path.Join(path.Dir(importer), spec)
	if !within(base) {
		return nil
	}
	candidates := []string{base}
	// ESM sources import "./x.js" while the file on disk is x.ts.
	for _, ext := range []string{".js", ".jsx", ".mjs", ".cjs"} {
		if stem, ok := strings.CutSuffix(base, ext); ok {
			candidates = append(candidates, stem+".ts", stem+".tsx", stem+".mts", stem+".cts")
		}
	}
	for _, ext := range scriptExtensions {
		candidates = append(candidates, base+ext)
	}
	for _, ext := range scriptExtensions {
		candidates = append(candidates, path.Join(base, "index"+ext))
	}
	return r.first(candidates...)
}

func (r *resolver) python(importer, module string, names []string) []string {
	dots := len(module) - len(strings.TrimLeft(module, "."))
	rest := strings.ReplaceAll(module[dots:], ".", "/")
	var roots []string
	if dots > 0 {
		dir := path.Dir(importer)
		for i := 1; i < dots; i++ {
			dir = path.Dir(dir)
		}
		roots = []string{dir}
	} else {
		// Script-relative, repository-root and src-layout imports.
		roots = []string{path.Dir(importer), ".", "src"}
	}
	for _, root := range roots {
		if !within(root) {
			continue
		}
		var found []string
		pkg := path.Join(root, rest)
		if rest != "" {
			found = r.first(pkg+".py", path.Join(pkg, "__init__.py"))
		} else {
			found = r.first(path.Join(root, "__init__.py"))
		}
		// `from package import submodule` loads submodule files as well.
		if rest == "" || (len(found) == 1 && path.Base(found[0]) == "__init__.py") {
			for _, name := range names {
				found = append(found, r.first(path.Join(pkg, name+".py"), path.Join(pkg, name, "__init__.py"))...)
			}
		}
		if len(found) > 0 {
			return found
		}
	}
	return nil
}

func (r *resolver) goPackage(spec string) []string {
	bestDir, bestModule := "", ""
	for dir, module := range r.goMods {
		if (spec == module || strings.HasPrefix(spec, module+"/")) && len(module) > len(bestModule) {
			bestDir, bestModule = dir, module
		}
	}
	if bestModule == "" {
		return nil
	}
	target := path.Join(bestDir, strings.TrimPrefix(strings.TrimPrefix(spec, bestModule), "/"))
	out := []string{}
	for _, p := range r.byDir[target] {
		if strings.HasSuffix(p, ".go") && !strings.HasSuffix(p, "_test.go") {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// rustModuleDir is the directory holding a Rust file's child modules.
func rustModuleDir(file string) string {
	switch path.Base(file) {
	case "mod.rs", "lib.rs", "main.rs":
		return path.Dir(file)
	}
	return strings.TrimSuffix(file, ".rs")
}

func (r *resolver) rust(importer, spec string) []string {
	spec = strings.TrimPrefix(strings.TrimSpace(spec), "::")
	if i := strings.Index(spec, "{"); i >= 0 {
		spec = strings.TrimSuffix(spec[:i], "::")
	}
	if i := strings.Index(spec, " as "); i >= 0 {
		spec = spec[:i]
	}
	segments := strings.Split(spec, "::")
	if len(segments) == 0 {
		return nil
	}
	var base string
	switch segments[0] {
	case "crate":
		dir := path.Dir(importer)
		for !r.files[path.Join(dir, "Cargo.toml")] {
			if dir == "." || dir == "/" {
				return nil
			}
			dir = path.Dir(dir)
		}
		base = path.Join(dir, "src")
		segments = segments[1:]
	case "self":
		base = rustModuleDir(importer)
		segments = segments[1:]
	case "super":
		base = rustModuleDir(importer)
		for len(segments) > 0 && segments[0] == "super" {
			base = path.Dir(base)
			segments = segments[1:]
		}
	default:
		return nil // external crate or standard library
	}
	if !within(base) {
		return nil
	}
	for i := len(segments); i >= 1; i-- {
		p := path.Join(base, strings.Join(segments[:i], "/"))
		if found := r.first(p+".rs", path.Join(p, "mod.rs")); found != nil {
			return found
		}
	}
	// The item lives in the module file itself (a.rs, a/mod.rs or the crate root).
	return r.first(base+".rs", path.Join(base, "mod.rs"), path.Join(base, "lib.rs"), path.Join(base, "main.rs"))
}
