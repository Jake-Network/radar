package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/typecheck"
)

func TestDeclaredTypecheckReplacesAutomaticTargetAtProfileRoot(t *testing.T) {
	root := goTypecheckRepo(t)
	put(t, root, ".radar/runners.json", `{"version":1,"profiles":[{"root":".","typecheck":[{"tool":"go","command":["go","vet","-buildtag","./..."],"files":["a/**/*.go"]}]}]}`)
	gitTest(t, root, "add", ".radar/runners.json")
	gitTest(t, root, "commit", "-qm", "declare checks")
	p := indexer.NewCommit(root, "HEAD")
	defer p.Close()
	plan, err := typecheck.Select(context.Background(), p, []string{"a/a.go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := typecheckProfiles(context.Background(), p, &plan, []string{"a/a.go"}); err != nil {
		t.Fatal(err)
	}
	if len(plan.Targets) != 1 || !plan.Targets[0].Custom || plan.Targets[0].CWD != "." || len(plan.Targets[0].Changed) != 1 {
		t.Fatalf("plan=%+v", plan)
	}
}

func TestTypecheckDeclaredSetupAndPrefixRunOnCandidateAndBase(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := goTypecheckRepo(t)
	put(t, root, "a/a.go", "package a\nvar bad int = \"old\"\nfunc New() int { return 1 }\n")
	put(t, root, "launch.py", "from pathlib import Path\nimport subprocess,sys\nassert Path('node_modules/ready').read_text() == 'ready'\nsys.exit(subprocess.call(sys.argv[1:]))\n")
	profile := map[string]any{"version": 1, "profiles": []any{map[string]any{
		"root": ".", "prefix": []string{"python3", "launch.py"}, "exclude": []string{"--test-only-exclusion"},
		"setup":     [][]string{{"python3", "-c", "from pathlib import Path; Path('node_modules').mkdir(); Path('node_modules/ready').write_text('ready')"}},
		"typecheck": []any{map[string]any{"tool": "go", "command": []string{"go", "vet", "-buildtag", "./..."}, "files": []string{"**/*.go"}}},
	}}}
	data, _ := json.Marshal(profile)
	put(t, root, ".radar/runners.json", string(data))
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline runner")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "change", "baseline", map[string]string{"b/b.go": "package b\nimport \"example.com/m/a\"\nfunc F() int { return a.New() + 1 }\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("change"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.TypecheckExecutions) != 1 {
		t.Fatalf("executions=%+v", r.TypecheckExecutions)
	}
	ev := r.TypecheckExecutions[0]
	if ev.Status != model.StatusIncomplete || ev.BaseComparison == nil || !ev.BaseComparison.Compared || ev.BaseComparison.Existing == 0 {
		t.Fatalf("base comparison did not use declared setup: %+v cmp=%+v", ev, ev.BaseComparison)
	}
	if len(r.RunnerPreparations) != 1 || r.RunnerPreparations[0].SetupCommands != 1 {
		t.Fatalf("candidate setup=%+v", r.RunnerPreparations)
	}
	if strings.Contains(strings.Join(ev.Command, " "), "test-only-exclusion") {
		t.Fatalf("test options leaked into typecheck: %v", ev.Command)
	}
}

func TestTypecheckSetupSourceMutationInvalidatesEvidence(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := goTypecheckRepo(t)
	put(t, root, ".radar/runners.json", `{"version":1,"profiles":[{"root":".","setup":[["python3","-c","open('a/a.go','w').write('package a\\n')"]]}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "declare setup")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "change", "baseline", map[string]string{"b/b.go": "package b\nfunc F() int { return 1 }\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("change"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.TypecheckExecutions) != 0 || len(r.TypecheckOmitted) != 1 || r.TypecheckOmitted[0].Reason != "runner_setup_unavailable" {
		t.Fatalf("source-mutating setup was accepted: executions=%+v omitted=%+v", r.TypecheckExecutions, r.TypecheckOmitted)
	}
}

func TestDeclaredNarrowGoCommandDoesNotBorrowWholeModuleCoverage(t *testing.T) {
	root := goTypecheckRepo(t)
	put(t, root, ".radar/runners.json", `{"version":1,"profiles":[{"root":".","typecheck":[{"tool":"go","command":["go","vet","-buildtag","./a"]}]}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "declare narrow check")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "break-unchecked", "baseline", map[string]string{"b/b.go": "package b\nvar bad int = \"wrong\"\nfunc F() int { return bad }\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("break-unchecked"))
	if err != nil {
		t.Fatal(err)
	}
	if check := findCheck(r, typecheck.CheckID); check == nil || check.Status == model.StatusPassed {
		t.Fatalf("a narrow declared check covered another package: check=%+v executions=%+v", check, r.TypecheckExecutions)
	}
}

func TestTypecheckDeclaredSetupSharesCommandBudget(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := goTypecheckRepo(t)
	put(t, root, ".radar/runners.json", `{"version":1,"profiles":[{"root":".","setup":[["python3","-c","pass"]]}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "declare setup")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "change", "baseline", map[string]string{"a/a.go": "package a\nfunc New() int { return 2 }\n"})
	o := typecheckOptions("change")
	o.MaxCommands = 1
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.RunnerPreparations) != 1 || r.RunnerPreparations[0].SetupCommands != 1 || len(r.TypecheckOmitted) != 1 || r.TypecheckOmitted[0].Reason != "command_budget_exhausted" {
		t.Fatalf("setup did not share command budget: prep=%+v omitted=%+v", r.RunnerPreparations, r.TypecheckOmitted)
	}
}

func TestInvalidTypecheckPrefixCannotStartDeclaredSetup(t *testing.T) {
	root := goTypecheckRepo(t)
	marker := t.TempDir() + "/setup-ran"
	setup := []string{"python3", "-c", "import pathlib; pathlib.Path(" + strconv.Quote(marker) + ").write_text('ran')"}
	manifest, _ := json.Marshal(map[string]any{"version": 1, "profiles": []any{map[string]any{"root": ".", "prefix": []string{"/invalid/absolute-prefix"}, "setup": [][]string{setup}}}})
	put(t, root, ".radar/runners.json", string(manifest))
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "invalid declaration")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "change", "baseline", map[string]string{"a/a.go": "package a\nfunc New() int { return 2 }\n"})
	if _, err := Preview(context.Background(), root, typecheckOptions("change")); err == nil {
		t.Fatal("invalid prefix accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("invalid prefix allowed setup side effect: %v", err)
	}
}
