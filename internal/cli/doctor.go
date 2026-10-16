package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/languages"
	"github.com/Jake-Network/radar/internal/project"
	"github.com/Jake-Network/radar/internal/testselection"
)

// doctorProject is what doctor found in this repository: what `radar gate`
// would combine, what it would leave out, and how its tests would run. It
// reads Git metadata, committed files and bounded local resource configuration;
// nothing is executed or written.
type doctorProject struct {
	Base      string           `json:"base,omitempty"`
	Branches  []string         `json:"branches"`
	Skipped   []string         `json:"skipped_branches"`
	Worktrees int              `json:"worktrees"`
	Dirty     []doctorDirty    `json:"dirty_worktrees"`
	Languages []string         `json:"languages"`
	Tests     []doctorRunner   `json:"test_runners"`
	Contracts bool             `json:"contracts_declared"`
	Agents    map[string]bool  `json:"agent_skills_installed"`
	Problems  []string         `json:"problems"`
	Workspace string           `json:"workspace,omitempty"`
	Next      string           `json:"next"`
	Resources []doctorResource `json:"resource_advisories"`

	testsErr            error
	worktreesIncomplete bool
}

type doctorDirty struct {
	Path      string `json:"path"`
	Branch    string `json:"branch,omitempty"`
	Staged    int    `json:"staged"`
	Unstaged  int    `json:"unstaged"`
	Untracked int    `json:"untracked"`
}

// doctorRunner is one discovered test framework. Status is "available" (the
// program is the runner and is on PATH), "unverified" (the program is on PATH
// or is a repository wrapper, but the framework itself is only checked when
// tests run), "missing" (not on PATH), "not_copied" (a dependency directory
// the private candidate does not contain) or "unsupported" (no safe command).
// None of these runs anything; a test run can still fail to start.
type doctorRunner struct {
	testselection.Runner
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

func (a *app) doctor(_ options) int {
	info, e := gitrepo.Inspect(a.ctx, a.root)
	tools := map[string]any{}
	names := []string{"git", "node", "python3", "go", "cargo", "java", "mvn", "gradle", "cmake", "ctest", "make", "cc", "c++", "rust-analyzer", "scip-typescript", "scip-python"}
	for _, tool := range names {
		p, e := exec.LookPath(tool)
		tools[tool] = map[string]any{"available": e == nil, "path": p}
	}
	_, stateErr := project.Read(a.stateRoot)
	result := map[string]any{"version": Version, "root": a.root, "state_root": a.stateRoot, "initialized": stateErr == nil, "repository_identity": gitrepo.Identity(a.ctx, a.root), "capabilities": languages.Capabilities(), "external_tools": tools, "semantic_indexing": "unavailable: external tool presence does not enable semantic analysis", "import_resolution": "inferred from language path conventions (relative TS/JS, Python packages, go.mod modules, Rust crate/self/super paths, Java declared packages, C/C++ include directories and header/source naming)", "telemetry": false, "cloud_inference": false, "git": info}
	if e != nil {
		result["git_diagnostic"] = e.Error()
	}
	topLevel := info.Root == "" || filepath.Clean(info.Root) == a.root
	if !topLevel {
		result["git_diagnostic"] = "selected root is inside a larger Git repository; checkpoint analysis requires repository top-level root"
	}
	var proj *doctorProject
	if info.Root != "" && topLevel {
		proj = a.inspectProject()
		result["project"] = proj
	}
	a.report(result, func(w io.Writer) {
		p := paletteOf(w)
		line := func(label, value string) {
			fmt.Fprintf(w, "  %s%s %s\n", p.bold(label), strings.Repeat(" ", max(0, 11-len(label))), value)
		}
		more := func(value string) { fmt.Fprintf(w, "  %s %s\n", strings.Repeat(" ", 11), value) }
		fmt.Fprintf(w, "%s %s\n\n", brand(p, "Radar doctor"), p.dim(Version))
		if info.Root == "" {
			line("Repository", p.mark("✗")+" not a Git repository")
			fmt.Fprintf(w, "\n%s %s\n", p.bold("Next:"), "cd into a Git repository where your agents work, then run "+p.command("radar doctor"))
			return
		}
		state := "clean"
		if info.Dirty {
			state = p.yellow("uncommitted changes")
		}
		line("Repository", fmt.Sprintf("%s  %s @ %s (%s)", p.bold(filepath.Base(a.root)), p.cyan(info.Branch), short(info.Head), state))
		if d, ok := result["git_diagnostic"]; ok {
			line("Git note", fmt.Sprintf("%s %v", p.mark("!"), d))
		}
		if proj == nil {
			return
		}
		if proj.Base == "" {
			line("Base", p.mark("?")+" no main, master or trunk branch; pass --base REF to radar gate")
		} else {
			line("Base", p.cyan(proj.Base))
		}
		switch {
		case len(proj.Branches) > 0:
			combines := "radar gate combines these"
			if proj.Workspace != "" {
				combines = "in this repository; gate also checks the other workspace repositories"
			}
			line("Branches", fmt.Sprintf("%s  %s", p.cyan(listOrNone(proj.Branches)), p.dim(fmt.Sprintf("(%s with commits beyond %s; %s)", plural(len(proj.Branches), "worktree branch", "worktree branches"), proj.Base, combines))))
		case proj.Base != "":
			line("Branches", p.mark("?")+" no worktree branch has commits beyond "+proj.Base)
		}
		for i, s := range proj.Skipped {
			if i == 3 {
				more(p.dim(fmt.Sprintf("… %d more skipped", len(proj.Skipped)-3)))
				break
			}
			more(p.mark("·") + " skipped: " + s)
		}
		worktrees := plural(proj.Worktrees, "worktree", "worktrees") + " checked out"
		if proj.worktreesIncomplete {
			worktrees += "; inspection incomplete"
		} else if len(proj.Dirty) == 0 {
			worktrees += "; no uncommitted changes"
		}
		line("Worktrees", worktrees)
		for i, d := range proj.Dirty {
			if i == 5 {
				more(fmt.Sprintf("… %d more with uncommitted changes (radar gate --json lists them)", len(proj.Dirty)-5))
				break
			}
			name := d.Branch
			if name == "" {
				name = d.Path
			}
			more(fmt.Sprintf("%s %s: %d staged, %d unstaged, %d untracked — not checked until committed", p.mark("!"), p.cyan(name), d.Staged, d.Unstaged, d.Untracked))
		}
		for _, advisory := range proj.Resources {
			mark := "!"
			if advisory.Status == "unknown" {
				mark = "?"
			}
			line("Resources", fmt.Sprintf("%s %s: %s (%s)", p.mark(mark), advisory.Kind, advisory.Key, strings.Join(advisory.Worktrees, ", ")))
		}
		if len(proj.Languages) == 0 {
			line("Languages", p.dim("none Radar parses (TypeScript/JavaScript, Python, Go, Rust, Java, C/C++)"))
		} else {
			line("Languages", strings.Join(proj.Languages, ", ")+" "+p.dim("(structural parsing; no type checking)"))
		}
		switch {
		case proj.testsErr != nil:
			line("Tests", p.mark("?")+" test discovery failed: "+proj.testsErr.Error())
		case len(proj.Tests) == 0:
			line("Tests", p.mark("?")+" no tests found; radar gate --run will have nothing to run for code changes")
		}
		width := 0
		for _, r := range proj.Tests {
			width = max(width, len(r.Framework))
		}
		for i, r := range proj.Tests {
			mark := map[string]string{"available": "✓", "unverified": "?", "missing": "✗", "not_copied": "?", "unsupported": "?"}[r.Status]
			text := fmt.Sprintf("%s %s%s  %s", p.mark(mark), r.Framework, strings.Repeat(" ", width-len(r.Framework)), plural(r.Tests, "test file", "test files"))
			if r.Program != "" {
				text += "  " + p.cyan(r.Program)
			}
			if r.Note != "" {
				text += " " + p.dim("— "+r.Note)
			}
			if i == 0 {
				line("Tests", text)
			} else {
				more(text)
			}
		}
		notes := []string{}
		for _, problem := range proj.Problems {
			// Missing runners are already marked on their Tests rows.
			if !strings.HasSuffix(problem, "which is not on PATH") {
				notes = append(notes, problem)
			}
		}
		for i, n := range notes {
			if i == 0 {
				line("Notes", p.mark("!")+" "+n)
			} else {
				more(p.mark("!") + " " + n)
			}
		}
		if proj.Contracts {
			line("Contracts", ".radar/contracts.json declared")
		} else {
			line("Contracts", "none declared "+p.dim("(optional; radar discover proposes candidates)"))
		}
		agents := []string{}
		for _, name := range []string{"claude", "codex"} {
			label := map[string]string{"claude": "Claude Code", "codex": "Codex"}[name]
			if proj.Agents[name] {
				agents = append(agents, p.mark("✓")+" "+label)
			} else {
				agents = append(agents, p.mark("·")+" "+label+" "+p.dim("(radar setup "+name+")"))
			}
		}
		line("Agents", strings.Join(agents, "  "))
		if stateErr != nil {
			line("State", p.dim("not initialized (not needed for gate or watch)"))
		} else {
			line("State", a.stateRoot+"/.radar")
		}
		line("Limits", "candidate integrity: 10000 tracked files, 20000 filesystem inputs; no total byte limit")
		line("Snapshot", "radar test: 10000 tracked entries, 64 MiB total copied file content")
		line("Privacy", "local only: no telemetry, no cloud inference; runs repository code only with --run")
		fmt.Fprintf(w, "\n%s %s\n", p.bold("Next:"), p.next(proj.Next))
	})
	return 0
}

// inspectProject gathers doctor's repository facts with gate's own rules.
// Every step is read-only; a failing step is reported, not fatal.
func (a *app) inspectProject() *doctorProject {
	d := &doctorProject{Branches: []string{}, Skipped: []string{}, Dirty: []doctorDirty{}, Languages: []string{}, Tests: []doctorRunner{}, Agents: map[string]bool{}, Problems: []string{}}
	d.Base = gitrepo.DefaultBranch(a.ctx, a.root)
	branchErr := error(nil)
	if d.Base != "" {
		if baseSHA, err := gitrepo.Resolve(a.ctx, a.root, d.Base); err == nil {
			d.Branches, d.Skipped, branchErr = a.gateBranches(nil, d.Base, baseSHA)
			if branchErr != nil {
				d.Branches = []string{}
				if !strings.HasPrefix(branchErr.Error(), "no branches to combine:") {
					d.Problems = append(d.Problems, "branch listing failed: "+branchErr.Error())
				}
			}
			if d.Skipped == nil {
				d.Skipped = []string{}
			}
		}
	}
	if worktrees, err := gitrepo.InspectWorktrees(a.ctx, a.root); err != nil {
		d.worktreesIncomplete = true
		d.Problems = append(d.Problems, "worktree inspection unavailable: "+err.Error())
	} else {
		d.Worktrees = len(worktrees)
		roots := []string{}
		for _, w := range worktrees {
			roots = append(roots, w.Path)
		}
		d.Resources = inspectResources(roots)
		d.Resources = append(d.Resources, editableAdvisories(a.root, os.Getenv("VIRTUAL_ENV"))...)
		for _, w := range worktrees {
			if w.InspectionError != "" {
				d.worktreesIncomplete = true
				d.Problems = append(d.Problems, "worktree inspection unavailable for "+w.Path+": "+w.InspectionError)
			}
			if n := len(w.Staged) + len(w.Unstaged) + len(w.Untracked); n > 0 {
				d.Dirty = append(d.Dirty, doctorDirty{Path: w.Path, Branch: w.Branch, Staged: len(w.Staged), Unstaged: len(w.Unstaged), Untracked: len(w.Untracked)})
			}
		}
	}
	d.Resources = mergeDoctorResources(d.Resources)
	if files, err := gitrepo.Files(a.ctx, a.root, "HEAD"); err == nil {
		d.Languages = detectLanguages(files)
	}
	if inv, err := testselection.Discover(a.ctx, a.root, "HEAD"); err != nil {
		d.testsErr = err
		d.Problems = append(d.Problems, "test discovery failed: "+err.Error())
	} else {
		for _, r := range testselection.Runners(inv) {
			d.Tests = append(d.Tests, runnerStatus(r))
		}
		blocking, informational := 0, 0
		for _, diagnostic := range inv.Diagnostics {
			if testselection.InventoryDiagnosticBlocks(diagnostic) {
				blocking++
			} else {
				informational++
			}
		}
		if blocking > 0 {
			d.Problems = append(d.Problems, plural(blocking, "test or configuration file was", "test or configuration files were")+" skipped by discovery; radar gate --run will report the inventory as incomplete")
		}
		if informational > 0 {
			d.Problems = append(d.Problems, plural(informational, "non-test source file was", "non-test source files were")+" skipped by discovery due to its size; this omission does not block the test inventory")
		}
	}
	for _, r := range d.Tests {
		if r.Status == "missing" {
			d.Problems = append(d.Problems, r.Framework+" tests need "+r.Program+", which is not on PATH")
		}
	}
	_, err := os.Stat(filepath.Join(a.root, ".radar", "contracts.json"))
	d.Contracts = err == nil
	for name, skills := range map[string][]string{"claude": {".claude/skills/radar/SKILL.md"}, "codex": {".agents/skills/radar/SKILL.md", ".agents/skills/radar-architecture/SKILL.md"}} {
		for _, skill := range skills {
			_, err := os.Stat(filepath.Join(a.root, filepath.FromSlash(skill)))
			d.Agents[name] = d.Agents[name] || err == nil
		}
	}
	if inv, err := a.workspaceInvocation(options{}); err == nil && inv != nil {
		d.Workspace = inv.scope.Workspace
	}
	switch {
	case d.worktreesIncomplete:
		d.Next = "restore access to the worktrees and resolve the worktree inspection problems, then: radar doctor"
	case d.Workspace != "":
		d.Next = fmt.Sprintf("radar gate   (this repository is in workspace %q, so gate checks every repository in it; radar workspace show lists them)", d.Workspace)
	case d.Base == "":
		d.Next = "name the integration branch: radar gate --base REF"
	case len(d.Branches) == 0 && len(d.Dirty) > 0:
		d.Next = "commit the agents' work on their branches (uncommitted changes are not checked), then: radar gate"
	case len(d.Branches) == 0:
		d.Next = "start agents on their own branches (for example git worktree add -b agent-a ../agent-a " + shellArg(d.Base) + "), commit, then: radar gate"
	default:
		d.Next = "radar gate   (combines " + gateSubject(d.Branches) + " in private Git state; runs no code)"
	}
	return d
}

func gateSubject(branches []string) string {
	if len(branches) <= 3 {
		return strings.Join(branches, " + ")
	}
	return fmt.Sprintf("%d branches", len(branches))
}

// runnerStatus says whether a discovered framework's program can start in
// the private candidate, using PATH and the path shape only.
func runnerStatus(r testselection.Runner) doctorRunner {
	out := doctorRunner{Runner: r, Status: "available"}
	switch {
	case r.Program == "":
		out.Status, out.Note = "unsupported", "no safely recognized command; these tests are listed as not run"
	case strings.Contains(r.Program, "node_modules/"):
		out.Status, out.Note = "not_copied", "node_modules is not copied into the private combined tree, so --run reports these tests as not run"
	case strings.Contains(r.Program, "/"):
		out.Status, out.Note = "unverified", "repository wrapper; checked when tests run"
	default:
		if _, err := exec.LookPath(r.Program); err != nil {
			out.Status, out.Note = "missing", "not installed or not on PATH"
			break
		}
		// Only these frameworks are the program itself (unittest is in the
		// Python standard library); for the rest, e.g. pytest under python3,
		// a PATH hit says nothing about the framework.
		switch r.Framework {
		case "go", "cargo", "node-test", "unittest":
		default:
			out.Status, out.Note = "unverified", r.Program+" is on PATH; "+r.Framework+" itself is checked when tests run"
		}
	}
	return out
}

// detectLanguages names the parsed languages present in committed files,
// most files first.
func detectLanguages(files []string) []string {
	names := map[string]string{".ts": "TypeScript", ".tsx": "TypeScript", ".mts": "TypeScript", ".cts": "TypeScript", ".js": "JavaScript", ".jsx": "JavaScript", ".mjs": "JavaScript", ".cjs": "JavaScript", ".py": "Python", ".go": "Go", ".rs": "Rust", ".java": "Java", ".c": "C/C++", ".h": "C/C++", ".cc": "C/C++", ".cpp": "C/C++", ".cxx": "C/C++", ".hpp": "C/C++", ".hh": "C/C++", ".hxx": "C/C++"}
	count := map[string]int{}
	for _, f := range files {
		if name, ok := names[strings.ToLower(path.Ext(f))]; ok {
			count[name]++
		}
	}
	out := []string{}
	for name := range count {
		out = append(out, name)
	}
	sort.Slice(out, func(i, j int) bool {
		if count[out[i]] != count[out[j]] {
			return count[out[i]] > count[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}
