package integration

import (
	"context"
	"os/exec"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/typecheck"
)

func TestCompilerConfigurationChangeCannotHideBehindHealthyGo(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := goTypecheckRepo(t)
	source := "#ifdef BREAK\nint broken(){ return undefined_name; }\n#endif\nint value(){return 1;}\n"
	put(t, root, "native/main.cpp", source)
	put(t, root, "a/a_test.go", "package a\nimport \"testing\"\nfunc TestNew(t *testing.T){if New()<1{t.Fatal(\"bad\")}}\n")
	put(t, root, "compile_commands.json", `[{"directory":"native","file":"main.cpp","arguments":["g++","-c","main.cpp"]}]`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "native baseline")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "break-flags", "baseline", map[string]string{"compile_commands.json": `[{"directory":"native","file":"main.cpp","arguments":["g++","-DBREAK","-c","main.cpp"]}]`, "a/a.go": "package a\nfunc New() int { return 2 }\n"})
	cmd := exec.Command("g++", "-DBREAK", "-fsyntax-only", "main.cpp")
	cmd.Dir = root + "/native"
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatal("native fixture did not fail")
	} else {
		t.Logf("native compiler rejects candidate flags: %s", out)
	}
	o := typecheckOptions("break-flags")
	o.Command = []string{"go", "test", "-json", "./..."}
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range r.TypecheckExecutions {
		t.Logf("tool=%s status=%s", e.Tool, e.Status)
	}
	c := findCheck(r, typecheck.CheckID)
	t.Logf("check=%+v gaps=%+v gate=%s", c, r.TypecheckGaps, r.Gate.Verdict)
	if c == nil || c.Status != model.StatusFailed || r.Gate.Verdict != "fail" {
		t.Fatalf("expected source failure for changed compiler flags, check=%+v gate=%s", c, r.Gate.Verdict)
	}
	branch(t, root, "healthy-flags", "baseline", map[string]string{"compile_commands.json": `[{"directory":"native","file":"main.cpp","arguments":["g++","-DHEALTHY","-c","main.cpp"]}]`, "a/a.go": "package a\nfunc New() int { return 2 }\n"})
	o = typecheckOptions("healthy-flags")
	o.Command = []string{"go", "test", "-json", "./..."}
	r, err = Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	c = findCheck(r, typecheck.CheckID)
	if c == nil || c.Status != model.StatusPassed || r.Gate.Verdict != "pass" || len(r.TypecheckGaps) != 0 {
		t.Fatalf("healthy compiler config did not pass: check=%+v gate=%s gaps=%+v", c, r.Gate.Verdict, r.TypecheckGaps)
	}
}

func TestCompilerConfigurationAmbiguousBaseRemainsUnverified(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := goTypecheckRepo(t)
	put(t, root, "native/main.cpp", "#ifdef BREAK\nint broken(){return undefined_name;}\n#endif\nint value(){return 1;}\n")
	put(t, root, "compile_commands.json", `[{"directory":"native","file":"main.cpp","arguments":["g++","-DVARIANT_A","-c","main.cpp"]},{"directory":"native","file":"main.cpp","arguments":["g++","-DVARIANT_B","-c","main.cpp"]}]`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "multiple native compile variants")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "flags", "baseline", map[string]string{"compile_commands.json": `[{"directory":"native","file":"main.cpp","arguments":["g++","-DBREAK","-c","main.cpp"]}]`})
	r, err := Preview(context.Background(), root, typecheckOptions("flags"))
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(r, typecheck.CheckID)
	if c == nil || c.Status != model.StatusIncomplete || len(r.TypecheckExecutions) != 1 {
		t.Fatalf("ambiguous base compile variant became definitive: check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
	cmp := r.TypecheckExecutions[0].BaseComparison
	if cmp == nil || cmp.Compared || cmp.Unresolved != 1 {
		t.Fatalf("ambiguous base provenance missing: %+v", cmp)
	}
}

func TestDeclaredCompilerCommandRetainsItsBaseArguments(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := goTypecheckRepo(t)
	put(t, root, "native/main.cpp", "#ifdef BREAK\nint broken(){return undefined_name;}\n#endif\nint value(){return 1;}\n")
	put(t, root, "compile_commands.json", `[{"directory":"native","file":"main.cpp","arguments":["g++","-c","main.cpp"]}]`)
	put(t, root, ".radar/runners.json", `{"version":1,"profiles":[{"root":"native","typecheck":[{"tool":"cpp","command":["g++","-DBREAK","-fsyntax-only","main.cpp"]}]}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "declared native command already fails on base")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "flags", "baseline", map[string]string{"compile_commands.json": `[{"directory":"native","file":"main.cpp","arguments":["g++","-DOTHER","-c","main.cpp"]}]`})
	r, err := Preview(context.Background(), root, typecheckOptions("flags"))
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(r, typecheck.CheckID)
	if c == nil || c.Status != model.StatusIncomplete || len(r.TypecheckExecutions) != 1 {
		t.Fatalf("declared command incorrectly borrowed database flags: check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
	cmp := r.TypecheckExecutions[0].BaseComparison
	if cmp == nil || !cmp.Compared || cmp.Existing != 1 {
		t.Fatalf("pre-existing declared-command failure was not compared: %+v", cmp)
	}
}

func TestUnrelatedCompilationDatabaseDoesNotHideHeaderFailure(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := goTypecheckRepo(t)
	put(t, root, "native/api.hpp", "inline int value(){return 1;}\n")
	put(t, root, "native/main.cpp", "#include \"api.hpp\"\nint use(){return value();}\n")
	put(t, root, "native/compile_commands.json", `[{"directory":"native","file":"main.cpp","arguments":["g++","-c","main.cpp"]}]`)
	put(t, root, "other/main.cpp", "int value(){return 1;}\n")
	put(t, root, "other/compile_commands.json", `[{"directory":"other","file":"main.cpp","arguments":["g++","-c","main.cpp"]}]`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "independent native projects")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "change", "baseline", map[string]string{
		"native/api.hpp":              "inline int value(){return undefined_name;}\n",
		"other/compile_commands.json": `[{"directory":"other","file":"main.cpp","arguments":["g++","-DHEALTHY","-c","main.cpp"]}]`,
	})
	r, err := Preview(context.Background(), root, typecheckOptions("change"))
	if err != nil {
		t.Fatal(err)
	}
	c := findCheck(r, typecheck.CheckID)
	if c == nil || c.Status != model.StatusFailed {
		t.Fatalf("unrelated database hid new header failure: check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
}
