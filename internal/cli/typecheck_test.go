package cli

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
)

func TestRenderTypecheckMarksHiddenImportsAsInferred(t *testing.T) {
	var out bytes.Buffer
	renderTypecheck(&out, integration.Report{TypecheckRequested: true, TypecheckExecutions: []integration.TypecheckExecution{{Tool: "mypy", CWD: ".", Status: model.StatusIncomplete, Command: []string{"python3", "-m", "mypy", "."}, HiddenImports: []integration.HiddenImport{{Path: "app/main.py", Line: 2, Module: "app.helpers"}}}}}, palette{})
	if !strings.Contains(out.String(), "incomplete: inferred imports") || !strings.Contains(out.String(), "app/main.py:2 imports deleted module app.helpers (inferred") || strings.Contains(out.String(), "no type errors") {
		t.Fatalf("output=%s", out.String())
	}
}

// typecheckRepo has two branches that each type-check, while their
// combination calls a function the other branch renamed.
func typecheckRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go unavailable")
	}
	root := gitRepo(t)
	put(t, root, "go.mod", "module example.com/m\n\ngo 1.21\n")
	put(t, root, "a/a.go", "package a\n\nfunc New() int { return 1 }\n")
	put(t, root, "b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc F() int { return a.New() }\n")
	put(t, root, "a/a_test.go", "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n")
	put(t, root, "b/b_test.go", "package b\n\nimport \"testing\"\n\nfunc TestF(t *testing.T) {\n\tif F() != 1 {\n\t\tt.Fatal(F())\n\t}\n}\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	gitTest(t, root, "branch", "baseline")
	gitTest(t, root, "checkout", "-qb", "rename")
	put(t, root, "a/a.go", "package a\n\nfunc Make() int { return 1 }\n")
	put(t, root, "b/b.go", "package b\n\nimport \"example.com/m/a\"\n\nfunc F() int { return a.Make() }\n")
	gitTest(t, root, "commit", "-qam", "rename New to Make")
	gitTest(t, root, "checkout", "-qb", "use", "baseline")
	put(t, root, "c/c.go", "package c\n\nimport \"example.com/m/a\"\n\nfunc G() int { return a.New() + 1 }\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "use New")
	gitTest(t, root, "checkout", "-q", "baseline")
	return root
}

func TestGateTypecheckFailsTheCombination(t *testing.T) {
	root := typecheckRepo(t)
	report := invoke(t, root, 1, "gate", "--base", "baseline", "--run", "--typecheck", "--timeout", "3m", "rename", "use")
	gate := report["gate"].(map[string]any)
	if gate["verdict"] != "fail" {
		t.Fatalf("verdict=%v", gate["verdict"])
	}
	var check map[string]any
	for _, c := range report["checks"].([]any) {
		if c.(map[string]any)["id"] == "combined_typecheck" {
			check = c.(map[string]any)
		}
	}
	if check == nil || check["status"] != "failed" || check["evidence"] != "verified_tool" {
		t.Fatalf("check=%v", check)
	}
	executions := report["typecheck_executions"].([]any)
	ev := executions[0].(map[string]any)
	newDiags := ev["new_diagnostics"].([]any)
	if len(executions) != 1 || len(newDiags) != 1 || newDiags[0].(map[string]any)["path"] != "c/c.go" || newDiags[0].(map[string]any)["symbol"] != "a.New" {
		t.Fatalf("executions=%v", executions)
	}

	code, out, errs := run(t, root, "gate", "--base", "baseline", "--run", "--typecheck", "--timeout", "3m", "rename", "use")
	if code != 1 || !strings.Contains(out, "Typecheck: ran 1 of 1 target") || !strings.Contains(out, "c/c.go:5  uses a.New, which the combined source does not define") || !strings.Contains(out, "1 new type error not on base") || !strings.Contains(out, "--typecheck") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
}

func TestGateTypecheckPassesEachBranchAlone(t *testing.T) {
	root := typecheckRepo(t)
	report := invoke(t, root, 0, "gate", "--base", "baseline", "--run", "--typecheck", "--timeout", "3m", "rename")
	required := false
	for _, c := range report["gate"].(map[string]any)["required_checks"].([]any) {
		if c.(map[string]any)["id"] == "combined_typecheck" {
			required = c.(map[string]any)["status"] == "passed"
		}
	}
	if !required || report["gate"].(map[string]any)["verdict"] != "pass" {
		t.Fatalf("gate=%v", report["gate"])
	}
}

func TestGateTypecheckRequiresRun(t *testing.T) {
	root := typecheckRepo(t)
	code, _, errs := run(t, root, "gate", "--base", "baseline", "--typecheck", "rename")
	if code != 2 || !strings.Contains(errs, "--typecheck runs repository tools; add --run") {
		t.Fatalf("exit %d: %s", code, errs)
	}
	code, _, errs = run(t, root, "gate", "--overlap", "--typecheck")
	if code != 2 || !strings.Contains(errs, "--overlap is a static query") {
		t.Fatalf("overlap exit %d: %s", code, errs)
	}
}

func TestGateWithoutTypecheckKeepsItsReport(t *testing.T) {
	root := typecheckRepo(t)
	// The static symbol check already fails this Go rename; the point here
	// is that no type checking happened without --typecheck.
	report := invoke(t, root, 1, "gate", "--base", "baseline", "rename", "use")
	if _, ok := report["typecheck_executions"]; ok {
		t.Fatal("static gate type-checked")
	}
	for _, c := range report["checks"].([]any) {
		if c.(map[string]any)["id"] == "combined_typecheck" {
			t.Fatal("static gate recorded a type-check check")
		}
	}
}

func TestGateTypecheckIsNotStatic(t *testing.T) {
	root := typecheckRepo(t)
	for _, flag := range []string{"--landing", "--conflict-brief"} {
		code, _, errs := run(t, root, "gate", flag, "--typecheck", "--base", "baseline", "rename", "use")
		if code != 2 || !strings.Contains(errs, "landing is static") {
			t.Fatalf("%s --typecheck: exit %d %s", flag, code, errs)
		}
	}
}

func TestGateTypecheckWithNothingToCheckSaysSo(t *testing.T) {
	root := typecheckRepo(t)
	gitTest(t, root, "checkout", "-qb", "py", "baseline")
	put(t, root, "tool.py", "print(1)\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "python only")
	gitTest(t, root, "checkout", "-q", "baseline")
	_, out, errs := run(t, root, "gate", "--base", "baseline", "--run", "--typecheck", "py")
	if !strings.Contains(out, "Typecheck: nothing checked — no supported configured project owns a changed file") || strings.Contains(out, "✓ Typecheck") {
		t.Fatalf("%s\n%s", out, errs)
	}
}

func TestWorkspaceNextKeepsTypecheck(t *testing.T) {
	if got := workspaceNext(gate.Fail, true, true, false, false); !strings.HasSuffix(got, "radar gate --again --run --typecheck") {
		t.Fatal(got)
	}
	if got := workspaceNext(gate.Fail, true, false, false, false); !strings.HasSuffix(got, "radar gate --again --run") {
		t.Fatal(got)
	}
}

func TestGateRerunKeepsRunForExecutionFlags(t *testing.T) {
	if got := gateCommandFor(options{verify: true}) + gateArgs(options{verify: true, typecheck: true, suite: "full", timeout: 10 * time.Minute}, "main", nil); got != "radar gate --run --suite full --typecheck" {
		t.Fatal(got)
	}
	if got := gateCommandFor(options{}); got != "radar gate " {
		t.Fatal(got)
	}
}

func TestGateTypecheckMovedProjectDoesNotClaimNewErrors(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go unavailable")
	}
	root := gitRepo(t)
	put(t, root, "old/go.mod", "module example.com/old\n\ngo 1.21\n")
	put(t, root, "old/x.go", "package old\n\nvar X int = \"pre-existing\"\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	gitTest(t, root, "branch", "baseline")
	gitTest(t, root, "checkout", "-qb", "move")
	gitTest(t, root, "mv", "old", "new")
	gitTest(t, root, "commit", "-qm", "move the project")
	gitTest(t, root, "checkout", "-q", "baseline")

	code, out, errs := run(t, root, "gate", "--base", "baseline", "--run", "--typecheck", "--timeout", "3m", "move")
	if code != 1 || strings.Contains(out, "new type errors") || !strings.Contains(out, "type errors on the combined tree; base comparison unavailable") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
}
