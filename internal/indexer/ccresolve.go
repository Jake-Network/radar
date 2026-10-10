package indexer

import (
	"encoding/json"
	"path"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/languages"
	"github.com/Jake-Network/radar/internal/model"
)

// C and C++ include resolution follows the compiler's search order where it
// is known and repository conventions where it is not. A committed
// compile_commands.json supplies per-file include directories as data;
// CMake, Meson and Make scripts are never evaluated. Every result is
// inferred: macros, #if branches and the real build configuration can
// select other headers.

var (
	ccHeaders  = []string{".h", ".hh", ".hpp", ".hxx", ".h++"}
	ccSources  = []string{".c", ".cc", ".cpp", ".cxx", ".c++"}
	ccProjects = []string{"CMakeLists.txt", "meson.build", "Makefile", "configure.ac"}
)

// compileCommands is one committed compile_commands.json, read lazily
// because mapping its absolute paths needs the full repository file list.
type compileCommands struct {
	path    string
	content []byte
}

func hasExt(p string, exts []string) bool {
	ext := strings.ToLower(path.Ext(p))
	for _, e := range exts {
		if ext == e {
			return true
		}
	}
	return false
}

// includeDirs maps each compiled repository file to the repository
// directories its -I, -iquote and -isystem flags name, in flag order.
// Absolute paths are mapped through the entry's own file: the directory
// part that precedes a repository path is the checkout root it was built
// in. Directories outside that root are system directories and dropped.
func (r *resolver) includeDirs() map[string][]string {
	if r.ccIncludes != nil {
		return r.ccIncludes
	}
	r.ccIncludes = map[string][]string{}
	for _, cc := range r.compileCommands {
		var entries []struct {
			Directory string   `json:"directory"`
			File      string   `json:"file"`
			Command   string   `json:"command"`
			Arguments []string `json:"arguments"`
		}
		if json.Unmarshal(cc.content, &entries) != nil {
			continue
		}
		for _, e := range entries {
			file := e.File
			if !path.IsAbs(file) {
				file = path.Join(e.Directory, file)
			}
			file = path.Clean(file)
			rel, root := r.repositoryPath(file)
			if rel == "" {
				continue
			}
			args := e.Arguments
			if len(args) == 0 {
				args = strings.Fields(e.Command)
			}
			var dirs []string
			for i := 0; i < len(args); i++ {
				dir := ""
				for _, flag := range []string{"-I", "-iquote", "-isystem"} {
					if args[i] == flag && i+1 < len(args) {
						dir = args[i+1]
						i++
					} else if v, ok := strings.CutPrefix(args[i], flag); ok && v != "" && dir == "" {
						dir = v
					}
				}
				if dir == "" {
					continue
				}
				if !path.IsAbs(dir) {
					dir = path.Join(e.Directory, dir)
				}
				dir = path.Clean(dir)
				switch {
				case dir == root:
					dirs = append(dirs, ".")
				case strings.HasPrefix(dir, root+"/") || root == "/":
					if inner := strings.TrimPrefix(strings.TrimPrefix(dir, root), "/"); within(inner) {
						dirs = append(dirs, inner)
					}
				}
			}
			r.ccIncludes[rel] = dirs
		}
	}
	return r.ccIncludes
}

// repositoryPath finds the longest repository path that ends an absolute
// path, and the root directory that precedes it.
func (r *resolver) repositoryPath(abs string) (string, string) {
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	for i := range parts {
		rel := strings.Join(parts[i:], "/")
		if r.files[rel] {
			return rel, "/" + strings.Join(parts[:i], "/")
		}
	}
	return "", ""
}

// projectRoots lists the directories enclosing p that hold a C/C++ build
// file, nearest first.
func (r *resolver) projectRoots(p string) []string {
	var roots []string
	for dir := path.Dir(p); ; dir = path.Dir(dir) {
		for _, name := range ccProjects {
			if r.files[path.Join(dir, name)] {
				roots = append(roots, dir)
				break
			}
		}
		if dir == "." || dir == "/" {
			return roots
		}
	}
}

// include resolves one #include. Quoted includes search the including
// file's directory first (as compilers do), then its compile_commands
// directories, then its ancestors up to the nearest project root; system
// includes search only compile_commands directories. Both then try each
// enclosing project's include/ directory, and quoted includes its src/;
// a header found in both is ambiguous and left unresolved.
func (r *resolver) include(importer string, imp languages.Import) []string {
	spec := path.Clean(imp.Module)
	if path.IsAbs(imp.Module) || !within(spec) {
		return nil
	}
	if !imp.System {
		if found := r.first(path.Join(path.Dir(importer), spec)); found != nil {
			return found
		}
	}
	for _, dir := range r.includeDirs()[importer] {
		if found := r.first(path.Join(dir, spec)); found != nil {
			return found
		}
	}
	roots := r.projectRoots(importer)
	if !imp.System {
		limit := "."
		if len(roots) > 0 {
			limit = roots[0]
		}
		for dir := path.Dir(importer); dir != limit && dir != "."; {
			dir = path.Dir(dir)
			if found := r.first(path.Join(dir, spec)); found != nil {
				return found
			}
		}
	}
	for _, root := range roots {
		candidates := []string{path.Join(root, "include", spec)}
		if !imp.System {
			candidates = append(candidates, path.Join(root, "src", spec))
		}
		var found []string
		for _, c := range candidates {
			if r.sources[c] {
				found = append(found, c)
			}
		}
		switch len(found) {
		case 1:
			return found
		case 2:
			return nil
		}
	}
	return nil
}

// pairingEdges links a header to the one implementation file that shares
// its name, beside it or in the mirrored src/ tree of an include/ layout:
// changing foo.cpp then reaches the tests that include foo.h. The link is
// a naming convention (inferred), not a linker or build-graph fact.
func (r *resolver) pairingEdges(repository, revision string, seen map[string]bool) []model.Edge {
	headers := make([]string, 0)
	for p := range r.sources {
		if hasExt(p, ccHeaders) {
			headers = append(headers, p)
		}
	}
	sort.Strings(headers)
	var out []model.Edge
	for _, h := range headers {
		stem := strings.TrimSuffix(path.Base(h), path.Ext(h))
		dirs := []string{path.Dir(h)}
		if i := strings.LastIndex("/"+h, "/include/"); i >= 0 {
			root := strings.TrimSuffix(h[:max(i-1, 0)], "/")
			if i == 0 {
				root = "."
			}
			rest := path.Dir(h[i+len("include/"):])
			dirs = append(dirs, path.Join(root, "src", rest), path.Join(root, "src"))
		}
		var impls []string
		for _, dir := range unique(dirs) {
			for _, ext := range ccSources {
				if c := path.Join(dir, stem+ext); r.sources[c] {
					impls = append(impls, c)
				}
			}
		}
		if len(impls) != 1 {
			continue
		}
		from, to := model.FileID(h), model.FileID(impls[0])
		id := model.StableID(from, "DEPENDS_ON", to)
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, model.Edge{ID: id, From: from, To: to, Kind: "DEPENDS_ON", Provenance: model.Provenance{Repository: repository, Revision: revision, Path: h, Line: 1, Method: "header_source_pairing:" + r.language[h], Evidence: model.Inferred}})
	}
	return out
}

func unique(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range values {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}
