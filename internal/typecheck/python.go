package typecheck

import (
	"path"
	"runtime"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
)

// ToolMypy and ToolRuff name the mypy and ruff diagnostic formats.
const (
	ToolMypy = "mypy"
	ToolRuff = "ruff"
)

// maxPythonFiles bounds the files one Python command is given. Changed files
// beyond it are gaps: they were not checked.
const maxPythonFiles = 500

// pythonCommand is the argv before the checked files. mypy runs as a module
// of the python3 on PATH, the interpreter tests use, so it sees the same
// installed packages. Neither tool writes a cache into the candidate.
func pythonCommand(tool string) []string {
	if tool == ToolRuff {
		// Only names that do not resolve: a function one branch removed and
		// another still calls, or a definition another silently replaces.
		return []string{"ruff", "check", "--no-cache", "--no-force-exclude", "--output-format", "concise", "--select", "F821,F811,F822,F823", "--"}
	}
	cache := "/dev/null"
	if runtime.GOOS == "windows" {
		cache = "nul"
	}
	return []string{"python3", "-m", "mypy", "--no-error-summary", "--show-error-codes", "--show-column-numbers", "--no-pretty", "--no-color-output", "--cache-dir=" + cache, "--"}
}

func pythonSource(f string) bool { return strings.HasSuffix(f, ".py") || strings.HasSuffix(f, ".pyi") }

// pythonDependency reports installed-dependency directories Radar does not
// type-check as repository source.
func pythonDependency(f string) bool {
	return hasSegment(f, "node_modules") || hasSegment(f, "site-packages") || hasSegment(f, ".venv")
}

// pythonConfigs returns the directories that hold mypy and ruff
// configuration, read as data.
func pythonConfigs(entries []indexer.Entry, read func(string) ([]byte, bool)) (mypy, ruff map[string]bool) {
	mypy, ruff = map[string]bool{}, map[string]bool{}
	for _, e := range entries {
		if pythonDependency(e.Path) {
			continue
		}
		dir := path.Dir(e.Path)
		switch path.Base(e.Path) {
		case "mypy.ini", ".mypy.ini":
			mypy[dir] = true
		case "ruff.toml", ".ruff.toml":
			ruff[dir] = true
		case "setup.cfg":
			if b, ok := read(e.Path); ok && hasTable(b, "mypy") {
				mypy[dir] = true
			}
		case "pyproject.toml":
			if b, ok := read(e.Path); ok {
				mypy[dir] = mypy[dir] || hasTable(b, "tool.mypy")
				ruff[dir] = ruff[dir] || hasTable(b, "tool.ruff")
			}
		}
	}
	return mypy, ruff
}

// hasTable reports an INI section or TOML table named name, or a TOML
// subtable of it.
func hasTable(b []byte, name string) bool {
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "[") {
			continue
		}
		line = strings.TrimLeft(line, "[")
		if strings.HasPrefix(line, name+"]") || strings.HasPrefix(line, name+".") {
			return true
		}
	}
	return false
}

// nearest returns the closest directory at or above f's directory in roots.
func nearest(roots map[string]bool, f string) (string, bool) {
	dir := path.Dir(f)
	for {
		if roots[dir] {
			return dir, true
		}
		if dir == "." {
			return "", false
		}
		dir = path.Dir(dir)
	}
}

// selectPython adds a mypy and a ruff target for each configured project
// that owns a changed Python file, including a deleted one. ruff is given the
// changed files. mypy checks what its configuration names (files, packages
// or modules), or else the whole project ("."), so unchanged modules that
// import changed or deleted code are checked too. Which files mypy actually
// checks is confirmed after selection, from mypy itself. Changed Python
// files no configuration encloses are listed as unconfigured.
func selectPython(byPath map[string]indexer.Entry, entries []indexer.Entry, read func(string) ([]byte, bool), changed []string, plan *Plan) {
	mypyRoots, ruffRoots := pythonConfigs(entries, read)
	selected := map[[2]string]*Target{}
	target := func(tool, root string) *Target {
		key := [2]string{tool, root}
		t := selected[key]
		if t == nil {
			t = &Target{Tool: tool, Root: root, CWD: root}
			t.ID = model.StableID("typecheck", t.Tool, t.Root)
			t.Declared = tool == ToolMypy && mypyDeclaresTargets(root, read)
			selected[key] = t
		}
		return t
	}
	for _, f := range changed {
		if !pythonSource(f) {
			continue
		}
		_, exists := byPath[f]
		if pythonDependency(f) {
			if exists {
				plan.Gaps = append(plan.Gaps, Gap{Path: f, Reason: "inside an installed-dependency directory (.venv, site-packages or node_modules), which Radar does not type-check"})
			}
			continue
		}
		configured := false
		if root, ok := nearest(mypyRoots, f); ok {
			configured = true
			t := target(ToolMypy, root)
			if exists {
				t.Changed = append(t.Changed, f)
			}
			if !t.Declared {
				t.Files = []string{"."}
			}
		}
		if root, ok := nearest(ruffRoots, f); ok {
			configured = true
			t := target(ToolRuff, root)
			if exists {
				t.Changed = append(t.Changed, f)
				t.Files = append(t.Files, relativeTo(root, f))
			} else {
				t.BaseOnly = append(t.BaseOnly, relativeTo(root, f))
			}
		}
		if exists && !configured {
			plan.Unconfigured = append(plan.Unconfigured, f)
		}
	}
	selectMypyDependents(byPath, entries, read, changed, mypyRoots, selected, target)
	var paths, deleted []string
	for _, e := range entries {
		if pythonSource(e.Path) && !pythonDependency(e.Path) {
			paths = append(paths, e.Path)
		}
	}
	for _, f := range changed {
		if _, exists := byPath[f]; !exists && pythonSource(f) && !pythonDependency(f) {
			paths = append(paths, f)
			deleted = append(deleted, f)
		}
	}
	sort.Strings(paths)
	sort.Strings(deleted)
	for _, t := range selected {
		if t.Tool == ToolRuff && len(t.Files) == 0 || t.Tool == ToolMypy && !t.Declared && len(t.Files) == 0 {
			// Only deletions: nothing in the combined tree to give the tool.
			continue
		}
		sort.Strings(t.Changed)
		sort.Strings(t.Files)
		sort.Strings(t.BaseOnly)
		if len(t.Files) > maxPythonFiles {
			for _, f := range t.Files[maxPythonFiles:] {
				plan.Gaps = append(plan.Gaps, Gap{Path: path.Join(t.Root, f), Reason: "more changed Python files than one type-check command is given (500)"})
			}
			t.Files = t.Files[:maxPythonFiles]
		}
		t.Command = pythonCommand(t.Tool)
		if t.Declared {
			t.Command = t.Command[:len(t.Command)-1] // no "--": mypy checks what its configuration names
		}
		t.Command = append(t.Command, t.Files...)
		t.Packages = pythonPackages(t.Root, paths)
		if t.Tool == ToolMypy {
			t.Paths, t.Deleted = paths, deleted
		}
		plan.Targets = append(plan.Targets, *t)
	}
}

// mypyDeclaresTargets reports whether the mypy configuration mypy reads in
// dir names files, packages or modules to check.
func mypyDeclaresTargets(dir string, read func(string) ([]byte, bool)) bool {
	values := mypyConfig(dir, read)
	_, files := values["files"]
	_, packages := values["packages"]
	_, modules := values["modules"]
	return files || packages || modules
}

// mypyConfig returns the raw values of the global section of the mypy
// configuration mypy reads in dir (mypy.ini, then .mypy.ini, pyproject.toml,
// setup.cfg). A TOML array continued over several lines is joined.
func mypyConfig(dir string, read func(string) ([]byte, bool)) map[string]string {
	values := map[string]string{}
	for _, c := range []struct{ name, table string }{{"mypy.ini", "mypy"}, {".mypy.ini", "mypy"}, {"pyproject.toml", "tool.mypy"}, {"setup.cfg", "mypy"}} {
		b, ok := read(path.Join(dir, c.name))
		if !ok || !hasTable(b, c.table) {
			continue
		}
		section, key, open := false, "", false
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if open {
				values[key] += " " + line
				open = !strings.Contains(line, "]")
				continue
			}
			if strings.HasPrefix(line, "[") {
				section = strings.Trim(line, "[] ") == c.table
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok || !section {
				continue
			}
			key = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			values[key] = v
			open = strings.HasPrefix(v, "[") && !strings.Contains(v, "]")
		}
		return values
	}
	return values
}

// mypyPaths returns the repository directories that the mypy configuration
// in dir adds to its search path through mypy_path. Relative entries are
// read from dir, where mypy runs. Entries outside the repository, or that
// name an environment variable other than MYPY_CONFIG_FILE_DIR, are left
// out: what mypy reads there is found by asking mypy before it runs.
func mypyPaths(dir string, read func(string) ([]byte, bool)) []string {
	raw := mypyConfig(dir, read)["mypy_path"]
	var out []string
	for _, entry := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ':' || r == ';' }) {
		entry = strings.Trim(strings.TrimSpace(entry), "[]\"' ")
		entry = strings.ReplaceAll(strings.ReplaceAll(entry, "${MYPY_CONFIG_FILE_DIR}", "."), "$MYPY_CONFIG_FILE_DIR", ".")
		if entry == "" || strings.Contains(entry, "$") || strings.HasPrefix(entry, "/") || strings.HasPrefix(entry, "~") {
			continue
		}
		joined := path.Join(dir, entry)
		if joined == ".." || strings.HasPrefix(joined, "../") {
			continue
		}
		out = append(out, joined)
	}
	return out
}

// maxDependentScan bounds the files read to decide whether another mypy
// project imports a changed one. A larger project is selected unread.
const maxDependentScan = 5000

// selectMypyDependents adds a mypy target for each other configured mypy
// project whose own files import a changed or deleted Python file through
// its search path: the project directory itself (the file lies below it,
// under a nested configuration) or a mypy_path entry. mypy type-checks
// such imports against the combined source, so a change in one project can
// break another. A project that imports none of them is left out; its
// files cannot reach the change.
func selectMypyDependents(byPath map[string]indexer.Entry, entries []indexer.Entry, read func(string) ([]byte, bool), changed []string, mypyRoots map[string]bool, selected map[[2]string]*Target, target func(tool, root string) *Target) {
	var files []string
	for _, f := range changed {
		if pythonSource(f) && !pythonDependency(f) {
			files = append(files, f)
		}
	}
	if len(files) == 0 {
		return
	}
	roots := make([]string, 0, len(mypyRoots))
	for r := range mypyRoots {
		roots = append(roots, r)
	}
	sort.Strings(roots)
	for progress := true; progress; {
		progress = false
		for _, q := range roots {
			if selected[[2]string{ToolMypy, q}] != nil {
				continue
			}
			bases := append([]string{q, path.Join(q, "src")}, mypyPaths(q, read)...)
			byTop := map[string]map[string]bool{}
			for _, f := range files {
				owner, owned := nearest(mypyRoots, f)
				if owned && owner == q {
					continue
				}
				for _, b := range bases {
					if b == f || !within(b, f) {
						continue
					}
					parts := strings.Split(relativeTo(b, f), "/")
					top := parts[0]
					if len(parts) == 1 {
						top = strings.TrimSuffix(strings.TrimSuffix(top, ".pyi"), ".py")
					}
					if !pythonIdentifier(top) {
						continue
					}
					if byTop[top] == nil {
						byTop[top] = map[string]bool{}
					}
					if owned {
						byTop[top][owner] = true
					} else {
						byTop[top][f] = true
					}
				}
			}
			// A path depending on the execution environment cannot be resolved
			// statically. Select conservatively; the actual probe verifies it.
			raw := strings.ReplaceAll(strings.ReplaceAll(mypyConfig(q, read)["mypy_path"], "${MYPY_CONFIG_FILE_DIR}", "."), "$MYPY_CONFIG_FILE_DIR", ".")
			if strings.ContainsAny(raw, "$~") || strings.Contains(raw, "\"/") || strings.HasPrefix(strings.TrimSpace(raw), "/") {
				byTop["*"] = map[string]bool{"unresolved mypy_path": true}
			}
			if len(byTop) == 0 {
				continue
			}
			tops := map[string]bool{}
			for top := range byTop {
				tops[top] = true
			}
			used := projectImports(entries, read, mypyRoots, q, tops)
			if len(used) == 0 {
				continue
			}
			t := target(ToolMypy, q)
			if !t.Declared {
				t.Files = []string{"."}
			}
			via := map[string]bool{}
			for top := range used {
				for v := range byTop[top] {
					via[v] = true
				}
			}
			for v := range via {
				t.Via = append(t.Via, v)
			}
			sort.Strings(t.Via)
			// Its public types and reexports may propagate the change to
			// another project; reach a fixed point over project-owned files.
			for _, e := range entries {
				if owner, ok := nearest(mypyRoots, e.Path); ok && owner == q && pythonSource(e.Path) && !pythonDependency(e.Path) {
					files = append(files, e.Path)
				}
			}
			progress = true
		}
	}
}

// projectImports returns the reachable top-level names a project's files
// import. Unreadable syntax and relative imports conservatively widen selection.
func projectImports(entries []indexer.Entry, read func(string) ([]byte, bool), mypyRoots map[string]bool, q string, tops map[string]bool) map[string]bool {
	used := map[string]bool{}
	scanned := 0
	for _, e := range entries {
		if !pythonSource(e.Path) || pythonDependency(e.Path) || !within(q, e.Path) {
			continue
		}
		if owner, ok := nearest(mypyRoots, e.Path); !ok || owner != q {
			continue
		}
		if scanned++; scanned > maxDependentScan {
			return tops
		}
		b, ok := read(e.Path)
		if !ok {
			return tops
		}
		imports, err := readPythonImports(b)
		if err != nil {
			return tops
		}
		for _, imp := range imports {
			if imp.Level > 0 || tops["*"] {
				return tops
			}
			for _, m := range imp.Modules("") {
				top, _, _ := strings.Cut(m, ".")
				if tops[top] {
					used[top] = true
				}
			}
		}
	}
	return used
}

func relativeTo(root, f string) string {
	if root == "." {
		return f
	}
	return strings.TrimPrefix(f, root+"/")
}

// pythonPackages lists the top-level module names of root's Python files,
// including those a branch deleted: an import of one that cannot be found
// is a source problem, not a dependency missing from the environment.
func pythonPackages(root string, paths []string) []string {
	seen := map[string]bool{}
	for _, p := range paths {
		if !pythonSource(p) || !within(root, p) || pythonDependency(p) {
			continue
		}
		parts := strings.Split(relativeTo(root, p), "/")
		if parts[0] == "src" && len(parts) > 1 {
			seen["src"] = true
			parts = parts[1:]
		}
		name := parts[0]
		if len(parts) == 1 {
			name = strings.TrimSuffix(strings.TrimSuffix(name, ".pyi"), ".py")
		}
		if pythonIdentifier(name) {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ModulesUnder lists the dotted module names that paths (repository Python
// files, including deleted ones) have below the search bases mypy reported,
// and their package prefixes. An import of one of them that mypy cannot find
// is the source's: a branch removed or moved it. A name that is only a tail
// of some repository path, not a module below a base, is not.
func ModulesUnder(bases, paths []string) []string {
	seen := map[string]bool{}
	for _, b := range bases {
		for _, p := range paths {
			if !pythonSource(p) || pythonDependency(p) || b == p || !within(b, p) {
				continue
			}
			parts := strings.Split(strings.TrimSuffix(strings.TrimSuffix(relativeTo(b, p), ".pyi"), ".py"), "/")
			if parts[len(parts)-1] == "__init__" {
				parts = parts[:len(parts)-1]
			}
			for j := 1; j <= len(parts); j++ {
				if !pythonDotted(parts[:j]) {
					break
				}
				seen[strings.Join(parts[:j], ".")] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func pythonDotted(parts []string) bool {
	for _, p := range parts {
		if !pythonIdentifier(p) {
			return false
		}
	}
	return true
}

func pythonIdentifier(s string) bool {
	if s == "" || s[0] >= '0' && s[0] <= '9' {
		return false
	}
	for _, r := range s {
		if r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}
