package integration

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/typecheck"
)

func TestCPPHeaderDeletedByOneBranchAndIncludedByAnotherFails(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := goTypecheckRepo(t)
	put(t, root, "native/util.hpp", "inline int answer(){return 1;}\n")
	put(t, root, "native/a.cpp", "#include \"util.hpp\"\nint a(){return answer();}\n")
	put(t, root, "native/b.cpp", "int b(){return 2;}\n")
	put(t, root, "compile_commands.json", `[{"directory":"native","file":"a.cpp","arguments":["g++","-c","a.cpp"]},{"directory":"native","file":"b.cpp","arguments":["g++","-c","b.cpp"]}]`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "native baseline")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	gitTest(t, root, "checkout", "-qb", "drop-header", "baseline")
	gitTest(t, root, "rm", "-q", "native/util.hpp")
	put(t, root, "native/a.cpp", "int a(){return 1;}\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "drop header")
	gitTest(t, root, "checkout", "-q", "baseline")
	branch(t, root, "use-header", "baseline", map[string]string{"native/b.cpp": "#include \"util.hpp\"\nint b(){return answer();}\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("drop-header", "use-header"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusFailed {
		t.Fatalf("deleted repository header was not a combined failure: check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
}

func TestGoBuildInputChangesAreNotUncompiledGaps(t *testing.T) {
	root := goTypecheckRepo(t)
	branch(t, root, "bump", "baseline", map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.21\n\n// bumped\n",
		"a/a.go": "package a\n\nfunc New() int { return 2 }\n",
	})
	r, err := Preview(context.Background(), root, typecheckOptions("bump"))
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range r.TypecheckGaps {
		if g.Path == "go.mod" {
			t.Fatalf("go.mod recorded as uncompiled: %+v", r.TypecheckGaps)
		}
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusPassed {
		t.Fatalf("check=%+v gaps=%+v executions=%+v", c, r.TypecheckGaps, r.TypecheckExecutions)
	}
}

func TestFailedSetupWithIntactSourceDoesNotSkipOtherProfiles(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := goTypecheckRepo(t)
	put(t, root, "one/go.mod", "module example.com/one\n\ngo 1.21\n")
	put(t, root, "one/one.go", "package one\n\nfunc One() int { return 1 }\n")
	put(t, root, "two/go.mod", "module example.com/two\n\ngo 1.21\n")
	put(t, root, "two/two.go", "package two\n\nfunc Two() int { return 2 }\n")
	put(t, root, ".radar/runners.json", `{"version":1,"profiles":[{"root":"one","setup":[["python3","-c","raise SystemExit(3)"]]},{"root":"two"}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "two modules")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "change", "baseline", map[string]string{
		"one/one.go": "package one\n\nfunc One() int { return 10 }\n",
		"two/two.go": "package two\n\nfunc Two() int { return 20 }\n",
	})
	r, err := Preview(context.Background(), root, typecheckOptions("change"))
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, o := range r.TypecheckOmitted {
		reasons[o.CWD] = o.Reason
	}
	if reasons["one"] != "runner_setup_unavailable" || reasons["two"] != "" {
		t.Fatalf("omitted=%+v", r.TypecheckOmitted)
	}
	ran := false
	for _, ev := range r.TypecheckExecutions {
		ran = ran || ev.CWD == "two" && ev.Status == model.StatusPassed
	}
	if !ran {
		t.Fatalf("unrelated profile was not checked: %+v", r.TypecheckExecutions)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status == model.StatusPassed {
		t.Fatalf("failed setup let the requirement pass: %+v", c)
	}
}

func TestTypecheckSelectionTimeoutIsNotAProfileError(t *testing.T) {
	root := goTypecheckRepo(t)
	put(t, root, ".radar/runners.json", `{"version":1,"profiles":[{"root":"."}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "profile")
	head := strings.TrimSpace(gitTest(t, root, "rev-parse", "HEAD"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &Report{CandidateCommit: head, Changed: []string{"a/a.go"}}
	if err := runTypecheck(ctx, root, r, Options{Typecheck: true, Verify: true, AllowExecution: true}); err != nil {
		t.Fatalf("expired budget became a hard error: %v", err)
	}
	if c := findCheck(*r, typecheck.CheckID); c == nil || c.Status != model.StatusTimeout {
		t.Fatalf("checks=%+v", r.Checks)
	}
}

func TestMypyInventoryBudgetIsReportedAsTimeout(t *testing.T) {
	_, _, err := mypyTarget(context.Background(), t.TempDir(), typecheck.Target{Tool: typecheck.ToolMypy, CWD: "."}, 0)
	if !errors.Is(err, errTypecheckBudget) {
		t.Fatalf("err=%v", err)
	}
}

func TestCPPHeaderReachedThroughIncFileIsStillChecked(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := goTypecheckRepo(t)
	put(t, root, "native/api.hpp", "int answer();\n")
	put(t, root, "native/wrap.inc", "#include \"api.hpp\"\n")
	put(t, root, "native/a.cpp", "#include \"api.hpp\"\nint a(){return answer();}\n")
	put(t, root, "native/b.cpp", "#include \"wrap.inc\"\nint b(){return answer();}\n")
	put(t, root, "compile_commands.json", `[{"directory":"native","file":"a.cpp","arguments":["g++","-c","a.cpp"]},{"directory":"native","file":"b.cpp","arguments":["g++","-c","b.cpp"]}]`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "native baseline")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "rename", "baseline", map[string]string{
		"native/api.hpp": "int reply();\n",
		"native/a.cpp":   "#include \"api.hpp\"\nint a(){return reply();}\n",
	})
	r, err := Preview(context.Background(), root, typecheckOptions("rename"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusFailed {
		t.Fatalf("unit including the header through .inc was skipped: check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
}

func TestDeletingUnrelatedHeaderOfSameNameIsNotACombinedFailure(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := goTypecheckRepo(t)
	// config.h is generated in real builds and never committed here.
	put(t, root, "native/a.cpp", "#include <config.h>\nint a(){return 1;}\n")
	put(t, root, "legacy/config.h", "#define OLD 1\n")
	put(t, root, "compile_commands.json", `[{"directory":"native","file":"a.cpp","arguments":["g++","-c","a.cpp"]}]`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "native baseline")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	gitTest(t, root, "checkout", "-qb", "cleanup", "baseline")
	gitTest(t, root, "rm", "-q", "legacy/config.h")
	put(t, root, "native/a.cpp", "#include <config.h>\nint a(){return 2;}\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "cleanup")
	gitTest(t, root, "checkout", "-q", "baseline")
	r, err := Preview(context.Background(), root, typecheckOptions("cleanup"))
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(r, typecheck.CheckID)
	if c == nil || c.Status == model.StatusFailed || c.Status == model.StatusPassed {
		t.Fatalf("missing header that base also lacks was judged: check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
}

func TestChangedCgoSourceRemainsAGoVetGap(t *testing.T) {
	root := goTypecheckRepo(t)
	// Excluded by a build tag, as cgo packages are when no C compiler is
	// available: go vet passes without compiling f.c.
	put(t, root, "c/c.go", "//go:build radar_never\n\npackage c\n\n// int f(void);\nimport \"C\"\n\nfunc F() int { return int(C.f()) }\n")
	put(t, root, "c/f.c", "int f(void) { return 1; }\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "cgo baseline")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "break-c", "baseline", map[string]string{"c/f.c": "int f(void) { return 1 +; }\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("break-c"))
	if err != nil {
		t.Fatal(err)
	}
	gap := false
	for _, g := range r.TypecheckGaps {
		gap = gap || g.Path == "c/f.c"
	}
	if c := findCheck(r, typecheck.CheckID); !gap || c == nil || c.Status == model.StatusPassed {
		t.Fatalf("unchecked cgo source passed: check=%+v gaps=%+v", c, r.TypecheckGaps)
	}
}

func TestMissingGeneratedHeaderIsNotBlamedOnUnrelatedDeletionWithoutBase(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := goTypecheckRepo(t)
	put(t, root, "legacy/config.h", "#define OLD 1\n")
	put(t, root, "native/old.cpp", "int old(){return 1;}\n")
	put(t, root, "compile_commands.json", `[{"directory":"native","file":"old.cpp","arguments":["g++","-c","old.cpp"]}]`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "native baseline")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	gitTest(t, root, "checkout", "-qb", "cleanup", "baseline")
	gitTest(t, root, "rm", "-q", "legacy/config.h")
	gitTest(t, root, "commit", "-qm", "cleanup")
	gitTest(t, root, "checkout", "-q", "baseline")
	// a.cpp does not exist on base, so no base comparison can be made.
	branch(t, root, "newlib", "baseline", map[string]string{
		"native/a.cpp":          "#include <config.h>\nint a(){return 1;}\n",
		"compile_commands.json": `[{"directory":"native","file":"old.cpp","arguments":["g++","-c","old.cpp"]},{"directory":"native","file":"a.cpp","arguments":["g++","-c","a.cpp"]}]`,
	})
	r, err := Preview(context.Background(), root, typecheckOptions("cleanup", "newlib"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status == model.StatusFailed || c.Status == model.StatusPassed {
		t.Fatalf("environment-missing header was judged: check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
}
