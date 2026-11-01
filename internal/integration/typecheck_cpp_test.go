package integration

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/typecheck"
)

func TestCPPSyntaxCheckingFindsCombinedRename(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := goTypecheckRepo(t)
	padding := strings.Repeat("// separation\n", 15)
	source := "int old_name() { return 1; }\n" + padding + "int use() { return 0; }\n"
	put(t, root, "main.cpp", source)
	put(t, root, "compile_commands.json", `[{"directory":".","file":"main.cpp","arguments":["g++","-c","main.cpp","-o","main.o"]}]`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "cpp baseline")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "rename", "baseline", map[string]string{"main.cpp": strings.Replace(source, "old_name", "new_name", 1)})
	branch(t, root, "use", "baseline", map[string]string{"main.cpp": strings.Replace(source, "return 0", "return old_name()", 1)})
	for _, name := range []string{"rename", "use"} {
		r, err := Preview(context.Background(), root, typecheckOptions(name))
		if err != nil {
			t.Fatal(err)
		}
		if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusPassed {
			t.Fatalf("%s: %+v %+v", name, c, r.TypecheckExecutions)
		}
	}
	r, err := Preview(context.Background(), root, typecheckOptions("rename", "use"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusFailed {
		t.Fatalf("check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
}

func TestCPPHeaderChangesAreCheckedByCompilerDependencyInventory(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := goTypecheckRepo(t)
	put(t, root, "api.hpp", "inline int answer(){return 1;}\n")
	put(t, root, "main.cpp", "#include \"api.hpp\"\nint value(){return answer();}\n")
	put(t, root, "compile_commands.json", `[{"directory":".","file":"main.cpp","arguments":["g++","-c","main.cpp"]}]`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "cpp baseline")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "change", "baseline", map[string]string{"api.hpp": "inline int answer(){return 2;}\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("change"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusPassed {
		t.Fatalf("check=%+v gaps=%+v executions=%+v", c, r.TypecheckGaps, r.TypecheckExecutions)
	}
}

func TestCPPTemplateHeaderCannotHideBehindHealthyGoChange(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := goTypecheckRepo(t)
	put(t, root, "native/lib.tpp", "inline int answer(){return 1;}\n")
	put(t, root, "native/main.cpp", "#include \"lib.tpp\"\nint value(){return answer();}\n")
	put(t, root, "compile_commands.json", `[{"directory":"native","file":"main.cpp","arguments":["g++","-c","main.cpp"]}]`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "template baseline")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "break-template", "baseline", map[string]string{"native/lib.tpp": "inline int answer(){return missing_name;}\n", "a/a.go": "package a\n\nfunc New() int { return 2 }\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("break-template"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusFailed {
		t.Fatalf("check=%+v gaps=%+v executions=%+v", c, r.TypecheckGaps, r.TypecheckExecutions)
	}
}

func TestCPPUppercaseSourceCannotHideBehindHealthyGoChange(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := goTypecheckRepo(t)
	put(t, root, "native/api.H", "inline int answer(){return 1;}\n")
	put(t, root, "native/main.C", "#include \"api.H\"\nint value(){return answer();}\n")
	put(t, root, "compile_commands.json", `[{"directory":"native","file":"main.C","arguments":["g++","-c","main.C"]}]`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "uppercase baseline")
	gitTest(t, root, "branch", "-f", "baseline", "HEAD")
	branch(t, root, "break-uppercase", "baseline", map[string]string{"native/main.C": "#include \"api.H\"\nint value(){return missing_name;}\n", "a/a.go": "package a\n\nfunc New() int { return 2 }\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("break-uppercase"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusFailed {
		t.Fatalf("check=%+v gaps=%+v executions=%+v", c, r.TypecheckGaps, r.TypecheckExecutions)
	}
	branch(t, root, "break-uppercase-header", "baseline", map[string]string{"native/api.H": "inline int answer(){return missing_name;}\n", "a/a.go": "package a\n\nfunc New() int { return 2 }\n"})
	r, err = Preview(context.Background(), root, typecheckOptions("break-uppercase-header"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusFailed {
		t.Fatalf("uppercase header check=%+v gaps=%+v executions=%+v", c, r.TypecheckGaps, r.TypecheckExecutions)
	}

}
