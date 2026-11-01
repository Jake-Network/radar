package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/typecheck"
)

// goTypecheckRepo commits a module whose package a exports New and whose
// package b calls it, on branch "baseline".
func goTypecheckRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go unavailable")
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, "go.mod", "module example.com/m\n\ngo 1.21\n")
	put(t, root, "a/a.go", "package a\n\nfunc New() int { return 1 }\n")
	put(t, root, "b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc F() int { return a.New() }\n")
	put(t, root, "README.md", "module\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	gitTest(t, root, "branch", "baseline")
	return root
}

func branch(t *testing.T, root, name, from string, files map[string]string) {
	t.Helper()
	gitTest(t, root, "checkout", "-qb", name, from)
	for p, content := range files {
		put(t, root, p, content)
	}
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", name)
	gitTest(t, root, "checkout", "-q", "baseline")
}

func typecheckOptions(branches ...string) Options {
	return Options{Base: "baseline", Branches: branches, Verify: true, AllowExecution: true, Typecheck: true, Command: []string{"go", "version"}, Timeout: 2 * time.Minute}
}

func findCheck(r Report, id string) *Check {
	for i := range r.Checks {
		if r.Checks[i].ID == id {
			return &r.Checks[i]
		}
	}
	return nil
}

func findingCodes(r Report) []string {
	var codes []string
	for _, f := range r.Findings {
		codes = append(codes, f.Code)
	}
	return codes
}

func TestTypecheckFailsOnlyTheCombination(t *testing.T) {
	root := goTypecheckRepo(t)
	branch(t, root, "rename", "baseline", map[string]string{
		"a/a.go": "package a\n\nfunc Make() int { return 1 }\n",
		"b/b.go": "package b\n\nimport \"example.com/m/a\"\n\nfunc F() int { return a.Make() }\n",
	})
	branch(t, root, "use", "baseline", map[string]string{
		"c/c.go": "package c\n\nimport \"example.com/m/a\"\n\nfunc G() int { return a.New() + 1 }\n",
	})
	ctx := context.Background()
	for _, alone := range []string{"rename", "use"} {
		r, err := Preview(ctx, root, typecheckOptions(alone))
		if err != nil {
			t.Fatal(err)
		}
		if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusPassed || c.Evidence != model.VerifiedTool {
			t.Fatalf("%s alone: %+v %+v", alone, c, r.TypecheckExecutions)
		}
	}
	r, err := Preview(ctx, root, typecheckOptions("rename", "use"))
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(r, typecheck.CheckID)
	if c == nil || c.Status != model.StatusFailed || !slices.Contains(required(r), typecheck.CheckID) || r.Gate.Verdict != gate.Fail {
		t.Fatalf("combined: check=%+v gate=%+v", c, r.Gate)
	}
	if len(r.TypecheckExecutions) != 1 {
		t.Fatalf("executions=%+v", r.TypecheckExecutions)
	}
	ev := r.TypecheckExecutions[0]
	if ev.CandidateCommit != r.CandidateCommit || ev.SourceAfterExecution != "unchanged" || ev.BaseComparison == nil || !ev.BaseComparison.Compared || ev.BaseComparison.New != 1 || len(ev.NewDiagnostics) != 1 || ev.NewDiagnostics[0].Path != "c/c.go" || ev.NewDiagnostics[0].Symbol != "a.New" {
		t.Fatalf("execution=%+v base=%+v", ev, ev.BaseComparison)
	}
	var located bool
	for _, f := range r.Findings {
		if f.Code == "typecheck_failed" && f.Severity == model.SeverityError && f.Evidence == model.VerifiedTool && len(f.Locations) == 1 && f.Locations[0].Path == "c/c.go" && f.Locations[0].Revision == r.CandidateCommit {
			located = true
		}
	}
	if !located {
		t.Fatalf("findings=%+v", r.Findings)
	}
}

func TestTypecheckExistingBaseErrorIsNotTheCombinations(t *testing.T) {
	root := goTypecheckRepo(t)
	put(t, root, "b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc F() int { return a.Broken() }\n")
	gitTest(t, root, "commit", "-qam", "base already broken")
	gitTest(t, root, "branch", "-f", "baseline")
	// The branch shifts the existing error's line without changing it.
	branch(t, root, "shift", "baseline", map[string]string{
		"b/b.go": "package b\n\nimport \"example.com/m/a\"\n\n// F is documented.\n\nfunc F() int { return a.Broken() }\n",
	})
	r, err := Preview(context.Background(), root, typecheckOptions("shift"))
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(r, typecheck.CheckID)
	if c == nil || c.Status != model.StatusIncomplete || r.Gate.Verdict == gate.Fail || r.Gate.Verdict == gate.Pass {
		t.Fatalf("check=%+v gate=%+v", c, r.Gate)
	}
	ev := r.TypecheckExecutions[0]
	if ev.BaseComparison == nil || ev.BaseComparison.Existing != 1 || ev.BaseComparison.New != 0 || len(ev.NewDiagnostics) != 0 {
		t.Fatalf("base comparison=%+v", ev.BaseComparison)
	}
	if !slices.Contains(findingCodes(r), "typecheck_base_failure") || slices.Contains(findingCodes(r), "typecheck_failed") {
		t.Fatalf("findings=%v", findingCodes(r))
	}
}

// go vet stops at a failing package, so once base fails anywhere in the
// module a new-looking error may have been hidden there: not verified.
func TestTypecheckNewErrorInAModuleBaseAlreadyFailsIsNotVerified(t *testing.T) {
	root := goTypecheckRepo(t)
	put(t, root, "b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc F() int { return a.Broken() }\n")
	gitTest(t, root, "commit", "-qam", "base already broken")
	gitTest(t, root, "branch", "-f", "baseline")
	branch(t, root, "other", "baseline", map[string]string{
		"c/c.go": "package c\n\nfunc G() int { return missing() }\n",
	})
	r, err := Preview(context.Background(), root, typecheckOptions("other"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusIncomplete || r.Gate.Verdict == gate.Fail {
		t.Fatalf("check=%+v verdict=%s", c, r.Gate.Verdict)
	}
	ev := r.TypecheckExecutions[0]
	if ev.BaseComparison.New != 0 || ev.BaseComparison.Existing != 1 || ev.BaseComparison.Unresolved != 1 || len(ev.NewDiagnostics) != 0 {
		t.Fatalf("comparison=%+v new=%+v", ev.BaseComparison, ev.NewDiagnostics)
	}
}

func TestTypecheckErrorBaseMayHideIsNotBlamed(t *testing.T) {
	root := goTypecheckRepo(t)
	// go vet reports only b.go's first error on base; the second is hidden.
	put(t, root, "b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc F() int { return a.First() }\n\nfunc H() int { return a.Second() }\n")
	gitTest(t, root, "commit", "-qam", "base with two errors")
	gitTest(t, root, "branch", "-f", "baseline")
	branch(t, root, "fix-first", "baseline", map[string]string{
		"b/b.go": "package b\n\nimport \"example.com/m/a\"\n\nfunc F() int { return a.New() }\n\nfunc H() int { return a.Second() }\n",
	})
	r, err := Preview(context.Background(), root, typecheckOptions("fix-first"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusIncomplete {
		t.Fatalf("an error base hid was blamed on the combination: %+v %+v", c, r.TypecheckExecutions)
	}
	if cmp := r.TypecheckExecutions[0].BaseComparison; cmp.Unresolved != 1 || cmp.New != 0 {
		t.Fatalf("comparison=%+v", cmp)
	}
}

func TestTypecheckWithoutGoChangeRecordsNoCheck(t *testing.T) {
	root := goTypecheckRepo(t)
	branch(t, root, "docs", "baseline", map[string]string{"README.md": "more\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("docs"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c != nil || slices.Contains(required(r), typecheck.CheckID) || len(r.TypecheckExecutions) != 0 {
		t.Fatalf("nothing to type-check must not become a check: %+v", c)
	}
}

func TestTypecheckIsOptIn(t *testing.T) {
	root := goTypecheckRepo(t)
	branch(t, root, "use", "baseline", map[string]string{"c/c.go": "package c\n\nfunc G() int { return missing() }\n"})
	o := typecheckOptions("use")
	o.Typecheck = false
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if findCheck(r, typecheck.CheckID) != nil || len(r.TypecheckExecutions) != 0 {
		t.Fatal("type checking ran without --typecheck")
	}
	if _, err := Preview(context.Background(), root, Options{Base: "baseline", Branches: []string{"use"}, Typecheck: true}); err == nil {
		t.Fatal("type checking accepted without execution opt-in")
	}
}

func TestTypecheckBudgetExhaustionBlocks(t *testing.T) {
	root := goTypecheckRepo(t)
	branch(t, root, "two", "baseline", map[string]string{
		"c/c.go":          "package c\n\nfunc G() int { return 1 }\n",
		"tools/go.mod":    "module example.com/tools\n\ngo 1.21\n",
		"tools/t/main.go": "package main\n\nfunc main() {}\n",
	})
	o := typecheckOptions("two")
	o.MaxCommands = 1
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(r, typecheck.CheckID)
	if c == nil || c.Status != model.StatusIncomplete || len(r.TypecheckOmitted) != 1 || r.TypecheckOmitted[0].Reason != "command_budget_exhausted" || r.Gate.Verdict == gate.Pass {
		t.Fatalf("check=%+v omitted=%+v gate=%s", c, r.TypecheckOmitted, r.Gate.Verdict)
	}
}

func TestTypecheckCommandThatChangesSourceIsDiscarded(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts are not executable on Windows")
	}
	root := goTypecheckRepo(t)
	put(t, root, "tc.sh", "#!/bin/sh\necho changed >> a/a.go\necho 'vet: b/b.go:5:22: undefined: a.New'\nexit 1\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "update-index", "--chmod=+x", "tc.sh")
	gitTest(t, root, "commit", "-qm", "script")
	head := gitTest(t, root, "rev-parse", "HEAD")
	c, err := BuildCandidate(context.Background(), root, head, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	identity, err := evidence.RepositoryIdentity(context.Background(), c.Dir)
	if err != nil {
		t.Fatal(err)
	}
	cp := evidence.CandidateCheckpoint{Repository: identity, Revision: c.Commit, Tree: c.Tree}
	ev := observeTypecheck(context.Background(), c.Dir, cp, typecheck.Target{ID: "t", Tool: typecheck.ToolGo, Root: ".", CWD: ".", Command: []string{"./tc.sh"}}, nil, Report{CandidateCommit: c.Commit, CandidateTree: c.Tree}, nil, time.Minute)
	if ev.SourceAfterExecution != "modified" || ev.Status != model.StatusIncomplete || ev.Observation.Status != model.StatusFailed {
		t.Fatalf("a failure observed on a changed tree was kept: %+v", ev)
	}
}

func TestCompareTypecheck(t *testing.T) {
	d := func(p, fp string) evidence.TypecheckDiagnostic {
		return evidence.TypecheckDiagnostic{Path: p, Line: 1, Fingerprint: fp}
	}
	cases := []struct {
		name                 string
		partial              bool // the base run stopped early
		candidate, base      []evidence.TypecheckDiagnostic
		fresh                []evidence.TypecheckDiagnostic
		existing, unresolved int
	}{
		{name: "all new", candidate: []evidence.TypecheckDiagnostic{d("a/a.go", "1")}, fresh: []evidence.TypecheckDiagnostic{d("a/a.go", "1")}},
		{name: "same error matches", partial: true, candidate: []evidence.TypecheckDiagnostic{d("a/a.go", "1")}, base: []evidence.TypecheckDiagnostic{d("a/a.go", "1")}, existing: 1},
		{name: "duplicate is matched once", candidate: []evidence.TypecheckDiagnostic{d("a/a.go", "1"), d("a/a.go", "1")}, base: []evidence.TypecheckDiagnostic{d("a/a.go", "1")}, fresh: []evidence.TypecheckDiagnostic{d("a/a.go", "1")}, existing: 1},
		{name: "partial base hides an error in the package", partial: true, candidate: []evidence.TypecheckDiagnostic{d("a/x.go", "2")}, base: []evidence.TypecheckDiagnostic{d("a/a.go", "1")}, unresolved: 1},
		{name: "partial base hides an error elsewhere", partial: true, candidate: []evidence.TypecheckDiagnostic{d("y/y.go", "2"), d("a/a.go", "1")}, base: []evidence.TypecheckDiagnostic{d("a/a.go", "1")}, existing: 1, unresolved: 1},
		{name: "a complete base run reports every error", candidate: []evidence.TypecheckDiagnostic{d("a/x.go", "2")}, base: []evidence.TypecheckDiagnostic{d("a/a.go", "1")}, fresh: []evidence.TypecheckDiagnostic{d("a/x.go", "2")}},
		{name: "same file different message is new", candidate: []evidence.TypecheckDiagnostic{d("a/a.go", "2")}, base: []evidence.TypecheckDiagnostic{d("a/a.go", "1")}, fresh: []evidence.TypecheckDiagnostic{d("a/a.go", "2")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fresh, existing, unresolved := compareTypecheck(c.partial, c.candidate, c.base, nil)
			if !reflect.DeepEqual(fresh, c.fresh) || existing != c.existing || unresolved != c.unresolved {
				t.Fatalf("fresh=%v existing=%d unresolved=%d", fresh, existing, unresolved)
			}
		})
	}
}

// Base fails in package b, so go vet never checks a, which imports b. A
// branch repairs b, revealing a's existing error: not the combination's.
func TestTypecheckErrorHiddenBehindAFailingImportIsNotBlamed(t *testing.T) {
	root := goTypecheckRepo(t)
	put(t, root, "b/b.go", "package b\n\nfunc X() int { return undefinedY }\n")
	put(t, root, "a/a.go", "package a\n\nimport \"example.com/m/b\"\n\nfunc New() int { return b.X() + oldBug }\n")
	gitTest(t, root, "commit", "-qam", "base with a hidden error")
	gitTest(t, root, "branch", "-f", "baseline")
	branch(t, root, "fix", "baseline", map[string]string{"b/b.go": "package b\n\nfunc X() int { return 1 }\n"})
	branch(t, root, "other", "baseline", map[string]string{"c/c.go": "package c\n\nfunc G() int { return 1 }\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("fix", "other"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusIncomplete || r.Gate.Verdict == gate.Fail || slices.Contains(findingCodes(r), "typecheck_failed") {
		t.Fatalf("check=%+v verdict=%s findings=%v", c, r.Gate.Verdict, findingCodes(r))
	}
}

func TestTypecheckFileExcludedOnThisHostIsNotAPass(t *testing.T) {
	root := goTypecheckRepo(t)
	other := "windows"
	if runtime.GOOS == "windows" {
		other = "linux"
	}
	branch(t, root, "elsewhere", "baseline", map[string]string{"a/use_" + other + ".go": "package a\n\nvar x int = \"s\"\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("elsewhere"))
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(r, typecheck.CheckID)
	if c == nil || c.Status != model.StatusIncomplete || len(r.TypecheckGaps) != 1 || r.Gate.Verdict == gate.Pass {
		t.Fatalf("a file go vet never compiled counted as type-checked: check=%+v gaps=%+v", c, r.TypecheckGaps)
	}
}

func TestTypecheckWithoutBaseComparisonDoesNotClaimNewErrors(t *testing.T) {
	root := goTypecheckRepo(t)
	// Base cannot be type-checked: it requires a module that is unavailable.
	put(t, root, "go.mod", "module example.com/m\n\ngo 1.21\n\nrequire example.invalid/missing v1.0.0\n")
	put(t, root, "d/d.go", "package d\n\nimport _ \"example.invalid/missing\"\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base needs an unavailable module")
	gitTest(t, root, "branch", "-f", "baseline")
	branch(t, root, "dropdep", "baseline", map[string]string{"go.mod": "module example.com/m\n\ngo 1.21\n", "d/d.go": "package d\n", "c/c.go": "package c\n\nfunc G() int { return missing() }\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("dropdep"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusFailed {
		t.Fatalf("check=%+v", c)
	}
	ev := r.TypecheckExecutions[0]
	if ev.BaseComparison == nil || ev.BaseComparison.Compared || len(ev.NewDiagnostics) != 0 {
		t.Fatalf("an unavailable base comparison was reported as new errors: %+v %+v", ev.BaseComparison, ev.NewDiagnostics)
	}
	for _, f := range r.Findings {
		if f.Code == "typecheck_failed" && (strings.Contains(f.Explanation, "that base does not have") || !strings.Contains(f.Explanation, "base comparison was unavailable") || len(f.Locations) != 1) {
			t.Fatalf("finding=%+v", f)
		}
	}
}

func TestTypecheckFollowsRelativeReplaceDependents(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go unavailable")
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, "a/go.mod", "module example.com/a\n\ngo 1.21\n")
	put(t, root, "a/a.go", "package a\n\nfunc Old() int { return 1 }\n")
	put(t, root, "b/go.mod", "module example.com/b\n\ngo 1.21\n\nrequire example.com/a v0.0.0\n\nreplace example.com/a => ../a\n")
	put(t, root, "b/b.go", "package b\n\nimport \"example.com/a\"\n\nfunc F() int { return a.Old() }\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	gitTest(t, root, "branch", "baseline")
	branch(t, root, "rename", "baseline", map[string]string{"a/a.go": "package a\n\nfunc New() int { return 1 }\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("rename"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusFailed || len(r.TypecheckExecutions) != 2 {
		t.Fatalf("check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
	var dependent *TypecheckExecution
	for i := range r.TypecheckExecutions {
		if r.TypecheckExecutions[i].Root == "b" {
			dependent = &r.TypecheckExecutions[i]
		}
	}
	if dependent == nil || dependent.Status != model.StatusFailed || len(dependent.NewDiagnostics) != 1 || dependent.NewDiagnostics[0].Path != "b/b.go" {
		t.Fatalf("dependent=%+v", dependent)
	}
}

func TestTypecheckDeletedRepositoryPackageIsTheSources(t *testing.T) {
	root := goTypecheckRepo(t)
	gitTest(t, root, "checkout", "-qb", "drop", "baseline")
	gitTest(t, root, "rm", "-q", "b/b.go")
	gitTest(t, root, "commit", "-qm", "drop b")
	gitTest(t, root, "checkout", "-q", "baseline")
	branch(t, root, "use", "baseline", map[string]string{"c/c.go": "package c\n\nimport \"example.com/m/b\"\n\nfunc G() int { return b.F() }\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("drop", "use"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusFailed {
		t.Fatalf("a package one branch deleted was blamed on the environment: check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
	if ev := r.TypecheckExecutions[0]; len(ev.NewDiagnostics) != 1 || ev.NewDiagnostics[0].Path != "c/c.go" {
		t.Fatalf("new=%+v", ev.NewDiagnostics)
	}
}

func TestTypecheckWithNothingToCheckSaysSo(t *testing.T) {
	root := goTypecheckRepo(t)
	branch(t, root, "py", "baseline", map[string]string{"tool.py": "print(1)\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("py"))
	if err != nil {
		t.Fatal(err)
	}
	if findCheck(r, typecheck.CheckID) != nil || !r.TypecheckRequested || !slices.ContainsFunc(r.Limitations, func(l string) bool { return strings.Contains(l, "nothing was type-checked") }) {
		t.Fatalf("limitations=%v", r.Limitations)
	}
}

func TestTypecheckOnlyUncheckableFilesIsIncomplete(t *testing.T) {
	root := goTypecheckRepo(t)
	branch(t, root, "loose", "baseline", map[string]string{"vendor/x/x.go": "package x\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("loose"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusIncomplete || !slices.Contains(required(r), typecheck.CheckID) || len(r.TypecheckExecutions) != 0 {
		t.Fatalf("check=%+v", c)
	}
}

// The toolchain that runs decides which files it compiles. Without a C
// compiler on PATH, go turns cgo off and skips //go:build cgo files, even
// though Radar itself was built with cgo.
func TestTypecheckCgoFileSkippedByTheToolchainIsNotAPass(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH symlinks")
	}
	root := goTypecheckRepo(t)
	bin := t.TempDir()
	for _, tool := range []string{"go", "git"} {
		target, err := exec.LookPath(tool)
		if err != nil {
			t.Skip(tool + " unavailable")
		}
		if err := os.Symlink(target, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	branch(t, root, "cgo", "baseline", map[string]string{"a/a_cgo.go": "//go:build cgo\n\npackage a\n\nvar broken int = \"s\"\n"})
	t.Setenv("PATH", bin)
	r, err := Preview(context.Background(), root, typecheckOptions("cgo"))
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(r, typecheck.CheckID)
	if c == nil || c.Status == model.StatusPassed || !slices.ContainsFunc(r.TypecheckGaps, func(g typecheck.Gap) bool { return g.Path == "a/a_cgo.go" }) {
		t.Fatalf("a file go vet skipped counted as type-checked: check=%+v gaps=%+v", c, r.TypecheckGaps)
	}
}

func TestTypecheckFileForAFutureReleaseIsNotAPass(t *testing.T) {
	root := goTypecheckRepo(t)
	branch(t, root, "future", "baseline", map[string]string{"a/future.go": "//go:build go1.999\n\npackage a\n\nvar broken int = \"s\"\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("future"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusIncomplete || len(r.TypecheckGaps) != 1 {
		t.Fatalf("check=%+v gaps=%+v", c, r.TypecheckGaps)
	}
}

func TestTypecheckImportCycleFromTwoBranchesFails(t *testing.T) {
	root := goTypecheckRepo(t)
	branch(t, root, "a-imports-b", "baseline", map[string]string{"a/a.go": "package a\n\nimport _ \"example.com/m/b\"\n\nfunc New() int { return 1 }\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("a-imports-b"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusFailed {
		t.Fatalf("an import cycle was not a source failure: check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
	ev := r.TypecheckExecutions[0]
	if ev.Observation.Diagnosis == nil || ev.Observation.Diagnosis.Kind != "typecheck_failed" {
		t.Fatalf("execution=%+v", ev)
	}
	// The base edge b -> a and the branch's a -> b are both located, so
	// either side of the cycle can be inspected.
	located := map[string]bool{}
	for _, d := range ev.NewDiagnostics {
		located[d.Path] = true
	}
	if len(ev.NewDiagnostics) != 2 || !located["a/a.go"] || !located["b/b.go"] {
		t.Fatalf("new diagnostics=%+v", ev.NewDiagnostics)
	}
}

func TestTypecheckProjectMissingOnBaseIsNotCompared(t *testing.T) {
	root := goTypecheckRepo(t)
	branch(t, root, "newmod", "baseline", map[string]string{"svc/go.mod": "module example.com/svc\n\ngo 1.21\n", "svc/s.go": "package svc\n\nfunc S() int { return missing() }\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("newmod"))
	if err != nil {
		t.Fatal(err)
	}
	var ev *TypecheckExecution
	for i := range r.TypecheckExecutions {
		if r.TypecheckExecutions[i].Root == "svc" {
			ev = &r.TypecheckExecutions[i]
		}
	}
	if ev == nil || ev.Status != model.StatusFailed || ev.BaseComparison == nil || ev.BaseComparison.Compared || ev.BaseComparison.Observation != nil || !strings.Contains(ev.BaseComparison.Explanation, "does not exist on base") {
		t.Fatalf("execution=%+v", ev)
	}
}
