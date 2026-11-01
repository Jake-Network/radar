// Package typecheck chooses the type-check commands that cover a change: go
// vet for Go modules, and mypy and ruff for configured Python projects. It
// reads committed files as data and never runs anything: execution happens
// only after explicit opt-in, through internal/evidence.
package typecheck

import (
	"context"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
)

// CheckID is the aggregate gate check for combined type checking.
const CheckID = "combined_typecheck"

// ToolGo names the go vet diagnostic format.
const ToolGo = "go"

// maxManifestBytes bounds go.mod, go.work and source header reads.
const maxManifestBytes = 1 << 20

// Target is one type-check command run in CWD of the combined candidate.
// Changed lists the changed files the target is selected for; Via names the
// changed module a dependent target is selected through. Files are the
// CWD-relative paths a Python command is given, at the end of Command, and
// Packages the project's top-level module names.
type Target struct {
	ID       string   `json:"id"`
	Tool     string   `json:"tool"`
	Root     string   `json:"root"`
	CWD      string   `json:"cwd"`
	Command  []string `json:"command"`
	Changed  []string `json:"changed,omitempty"`
	Via      []string `json:"via,omitempty"`
	Files    []string `json:"files,omitempty"`
	Packages []string `json:"packages,omitempty"`
	// BaseOnly are CWD-relative paths only base has (deleted or moved
	// files): the base command is also given them, so an error a branch
	// merely moved is recognized. Declared marks a mypy run that checks
	// what its configuration names, with no paths given.
	BaseOnly []string `json:"base_only,omitempty"`
	Declared bool     `json:"declared,omitempty"`
	// Modules are the dotted names the repository's Python files have below
	// mypy's search bases: a missing one is the source's. They come from
	// mypy itself, after selection (ModulesUnder).
	Modules []string `json:"modules,omitempty"`
	// Paths are the repository's Python files, including deleted ones, and
	// Deleted the Python files the combined tree no longer has; a mypy
	// target maps them to module names below the bases mypy reports.
	Paths   []string `json:"-"`
	Deleted []string `json:"-"`
	// LogicalCommand is the unwrapped argv before a runner prefix is applied.
	// Custom declarations use Files only to trigger execution, never as proof
	// that the checker processed the changed files.
	LogicalCommand []string `json:"-"`
	Custom         bool     `json:"-"`
	Artifacts      []string `json:"-"`
}

// Gap is a changed source file no target type-checks. A gap keeps type
// checking from passing: the file was not checked. Files a selected target's
// toolchain does not compile (build constraints, cgo, directories "./..."
// skips) are found after execution, from the toolchain's own file lists.
type Gap struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// Plan lists targets in a deterministic order and the changed source files
// they leave unchecked. LocalModules are the committed Go modules: a missing
// package under one of them is a source problem, not a dependency missing
// from the environment. Unconfigured lists changed Python files that no
// mypy or ruff configuration encloses; they are outside type checking.
type Plan struct {
	Targets      []Target `json:"targets"`
	Gaps         []Gap    `json:"gaps,omitempty"`
	LocalModules []Module `json:"local_modules,omitempty"`
	Unconfigured []string `json:"unconfigured,omitempty"`
}

// Module is a committed Go module: its import path and repository directory.
type Module struct {
	Path string `json:"path"`
	Dir  string `json:"dir"`
}

type goModule struct {
	dir, path string
	replaces  []string // repository directories of relative replace targets
}

// Select reads the committed candidate and returns one target per owning Go
// module of the changed files, plus the modules that use a selected module
// through go.work or a relative replace directive, and a mypy and a ruff
// target per configured Python project owning a changed Python file.
func Select(ctx context.Context, p indexer.Provider, changed []string) (Plan, error) {
	entries, err := p.Entries(ctx)
	if err != nil {
		return Plan{}, err
	}
	byPath := make(map[string]indexer.Entry, len(entries))
	for _, e := range entries {
		byPath[e.Path] = e
	}
	read := func(name string) ([]byte, bool) {
		e, ok := byPath[name]
		if !ok {
			return nil, false
		}
		b, err := p.Read(ctx, e, maxManifestBytes)
		return b, err == nil
	}
	modules := map[string]*goModule{}
	workspaces := map[string][]string{} // go.work directory -> used module directories
	for _, e := range entries {
		dir := path.Dir(e.Path)
		switch path.Base(e.Path) {
		case "go.mod":
			if goExcluded(e.Path) || hasSegment(e.Path, "testdata") {
				continue
			}
			m := &goModule{dir: dir}
			if b, ok := read(e.Path); ok {
				m.path, m.replaces = parseGoMod(b, dir)
			}
			modules[dir] = m
		case "go.work":
			if b, ok := read(e.Path); ok {
				workspaces[dir] = parseGoWork(b, dir)
			}
		}
	}
	plan := Plan{Targets: []Target{}}
	for _, m := range modules {
		if m.path != "" {
			plan.LocalModules = append(plan.LocalModules, Module{Path: m.path, Dir: m.dir})
		}
	}
	sort.Slice(plan.LocalModules, func(i, j int) bool { return plan.LocalModules[i].Dir < plan.LocalModules[j].Dir })
	goDirs := map[string]bool{}
	for p := range byPath {
		if strings.HasSuffix(p, ".go") {
			goDirs[path.Dir(p)] = true
		}
	}
	selected := map[string]*Target{}
	selectModule := func(dir string) *Target {
		t := selected[dir]
		if t == nil {
			t = &Target{Tool: ToolGo, Root: dir, CWD: dir, Command: []string{"go", "vet", "-buildtag", "./..."}}
			t.ID = model.StableID("typecheck", t.Tool, t.Root)
			selected[dir] = t
		}
		return t
	}
	gap := func(f, reason string) { plan.Gaps = append(plan.Gaps, Gap{Path: f, Reason: reason}) }
	// seeds are changed module directories, including deleted ones, whose
	// dependents must also be checked.
	seeds := map[string]bool{}
	for _, f := range changed {
		base := path.Base(f)
		if base == "go.work" || base == "go.work.sum" {
			used, present := workspaces[path.Dir(f)]
			if !present {
				// A deleted go.work's members are unknown here: check every
				// module below it.
				for dir := range modules {
					if within(path.Dir(f), dir) {
						used = append(used, dir)
					}
				}
			}
			for _, dir := range used {
				if modules[dir] != nil {
					t := selectModule(dir)
					t.Changed = append(t.Changed, f)
				}
			}
			continue
		}
		if base == "go.mod" && modules[path.Dir(f)] == nil {
			seeds[path.Dir(f)] = true
		}
		if !strings.HasSuffix(f, ".go") && base != "go.mod" && base != "go.sum" && !goInput(f, goDirs) {
			continue
		}
		_, exists := byPath[f]
		if hasSegment(f, "testdata") {
			// The go command never builds testdata.
			continue
		}
		if goExcluded(f) {
			if exists && strings.HasSuffix(f, ".go") {
				gap(f, "inside a vendor or node_modules directory, which go vet ./... does not check")
			}
			continue
		}
		root, ok := owningModule(modules, f)
		if !ok {
			if exists && strings.HasSuffix(f, ".go") {
				gap(f, "no go.mod encloses this file")
			}
			continue
		}
		t := selectModule(root)
		t.Changed = append(t.Changed, f)
	}
	// Modules that use a changed module through go.work or a relative
	// replace can break when it changes; repeat until nothing is added.
	for dir := range selected {
		seeds[dir] = true
	}
	for added := true; added; {
		added = false
		for dir := range seeds {
			for _, dependent := range dependents(modules, workspaces, dir) {
				if selected[dependent] == nil {
					added = true
				}
				t := selectModule(dependent)
				if len(t.Changed) == 0 && !contains(t.Via, dir) {
					t.Via = append(t.Via, dir)
				}
			}
		}
		for dir := range selected {
			if !seeds[dir] {
				seeds[dir], added = true, true
			}
		}
	}
	for _, t := range selected {
		sort.Strings(t.Changed)
		sort.Strings(t.Via)
		plan.Targets = append(plan.Targets, *t)
	}
	selectPython(byPath, entries, read, changed, &plan)
	selectTypeScript(byPath, entries, read, changed, &plan)
	selectCompiled(byPath, entries, read, changed, &plan)
	sort.Slice(plan.Targets, func(i, j int) bool {
		a, b := plan.Targets[i], plan.Targets[j]
		return a.Root < b.Root || a.Root == b.Root && a.Tool < b.Tool
	})
	sort.SliceStable(plan.Gaps, func(i, j int) bool { return plan.Gaps[i].Path < plan.Gaps[j].Path })
	var gaps []Gap
	for _, g := range plan.Gaps {
		if len(gaps) == 0 || gaps[len(gaps)-1].Path != g.Path {
			gaps = append(gaps, g)
		}
	}
	plan.Gaps = gaps
	sort.Strings(plan.Unconfigured)
	return plan, nil
}

// dependents lists modules other than dir that replace it with a relative
// path or share a go.work that uses it.
func dependents(modules map[string]*goModule, workspaces map[string][]string, dir string) []string {
	var out []string
	for other, m := range modules {
		if other != dir && contains(m.replaces, dir) {
			out = append(out, other)
		}
	}
	for _, used := range workspaces {
		if !contains(used, dir) {
			continue
		}
		for _, other := range used {
			if other != dir && modules[other] != nil {
				out = append(out, other)
			}
		}
	}
	sort.Strings(out)
	return out
}

// goExcluded reports directories go vet ./... never checks as packages of
// the module, and that Radar treats as dependencies.
func goExcluded(f string) bool { return hasSegment(f, "vendor") || hasSegment(f, "node_modules") }

func hasSegment(f, name string) bool {
	for _, part := range strings.Split(f, "/") {
		if part == name {
			return true
		}
	}
	return false
}

// goInput reports a non-Go file the go command reads when it builds: C,
// assembly and object files of cgo and assembly packages, and files beside
// Go source that //go:embed may name.
func goInput(f string, goDirs map[string]bool) bool {
	switch path.Ext(f) {
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".hh", ".hpp", ".hxx", ".m", ".s", ".S", ".sx", ".syso", ".f", ".F", ".for", ".f90", ".swig", ".swigcxx":
		return true
	}
	return goDirs[path.Dir(f)]
}

func within(dir, p string) bool { return dir == "." || p == dir || strings.HasPrefix(p, dir+"/") }

// owningModule returns the nearest directory at or above f's directory that
// holds a committed go.mod.
func owningModule(modules map[string]*goModule, f string) (string, bool) {
	dir := path.Dir(f)
	for {
		if modules[dir] != nil {
			return dir, true
		}
		if dir == "." {
			return "", false
		}
		dir = path.Dir(dir)
	}
}

// parseGoMod reads the module path and the repository directories of
// relative replace targets. Unparseable lines are ignored: a module path
// that cannot be read only removes a local-module hint.
func parseGoMod(b []byte, dir string) (string, []string) {
	module := ""
	var replaces []string
	for _, line := range directiveLines(b, "replace") {
		if strings.HasPrefix(line, "module ") {
			module = unquote(strings.TrimSpace(strings.TrimPrefix(line, "module ")))
			continue
		}
		_, target, ok := strings.Cut(line, "=>")
		if !ok {
			continue
		}
		fields := strings.Fields(target)
		if len(fields) != 1 {
			continue // a module version, not a directory
		}
		if local, ok := localDir(dir, unquote(fields[0])); ok {
			replaces = append(replaces, local)
		}
	}
	return module, replaces
}

// parseGoWork returns the repository directories a go.work uses.
func parseGoWork(b []byte, dir string) []string {
	var used []string
	for _, line := range directiveLines(b, "use") {
		if strings.HasPrefix(line, "go ") || strings.HasPrefix(line, "toolchain ") || strings.HasPrefix(line, "replace ") || strings.HasPrefix(line, "godebug ") {
			continue
		}
		if local, ok := localDir(dir, unquote(strings.TrimSpace(line))); ok {
			used = append(used, local)
		}
	}
	return used
}

// directiveLines returns comment-free lines, expanding "NAME ( ... )" blocks
// of the named directive into lines without the directive keyword.
func directiveLines(b []byte, name string) []string {
	var out []string
	block := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "//", 2)[0])
		switch {
		case line == "":
		case line == name+" (" || line == name+"(":
			block = true
		case block && line == ")":
			block = false
		case block:
			out = append(out, line)
		case strings.HasPrefix(line, name+" "):
			out = append(out, strings.TrimSpace(strings.TrimPrefix(line, name+" ")))
		default:
			out = append(out, line)
		}
	}
	return out
}

func localDir(dir, target string) (string, bool) {
	target = strings.ReplaceAll(target, `\`, "/")
	if !strings.HasPrefix(target, "./") && !strings.HasPrefix(target, "../") && target != "." && target != ".." {
		return "", false
	}
	joined := path.Clean(path.Join(dir, target))
	if joined == ".." || strings.HasPrefix(joined, "../") {
		return "", false
	}
	return joined, true
}

func unquote(s string) string {
	if u, err := strconv.Unquote(s); err == nil {
		return u
	}
	return s
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
