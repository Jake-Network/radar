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
	paths := make([]string, 0, len(entries)+len(changed))
	for _, e := range entries {
		paths = append(paths, e.Path)
	}
	paths = append(paths, changed...)
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
			t.Modules = pythonModules(paths)
		}
		plan.Targets = append(plan.Targets, *t)
	}
}

// mypyDeclaresTargets reports whether the mypy configuration mypy reads in
// dir (mypy.ini, then .mypy.ini, pyproject.toml, setup.cfg) names files,
// packages or modules to check.
func mypyDeclaresTargets(dir string, read func(string) ([]byte, bool)) bool {
	for _, c := range []struct{ name, table string }{{"mypy.ini", "mypy"}, {".mypy.ini", "mypy"}, {"pyproject.toml", "tool.mypy"}, {"setup.cfg", "mypy"}} {
		b, ok := read(path.Join(dir, c.name))
		if !ok || !hasTable(b, c.table) {
			continue
		}
		section := false
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				section = strings.Trim(line, "[] ") == c.table
				continue
			}
			key, _, ok := strings.Cut(line, "=")
			if !ok || !section {
				continue
			}
			switch strings.TrimSpace(key) {
			case "files", "packages", "modules":
				return true
			}
		}
		return false
	}
	return false
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

// pythonModules lists the dotted names of two or more parts that the
// repository's Python files and packages can be imported as from some base
// directory (mypy_path, package bases), including files a branch deleted.
func pythonModules(paths []string) []string {
	seen := map[string]bool{}
	for _, p := range paths {
		if !pythonSource(p) || pythonDependency(p) {
			continue
		}
		parts := strings.Split(strings.TrimSuffix(strings.TrimSuffix(p, ".pyi"), ".py"), "/")
		if parts[len(parts)-1] == "__init__" {
			parts = parts[:len(parts)-1]
		}
		for i := range parts {
			for j := i + 2; j <= len(parts); j++ {
				if name := strings.Join(parts[i:j], "."); pythonDotted(parts[i:j]) {
					seen[name] = true
				}
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
