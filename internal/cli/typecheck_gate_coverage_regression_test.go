package cli

import (
	"os/exec"
	"strings"
	"testing"
)

func TestGateCompilerConfigurationChangeCannotHideBehindGo(t *testing.T) {
	if _, err := exec.LookPath("g++"); err != nil {
		t.Skip("g++ unavailable")
	}
	root := typecheckRepo(t)
	put(t, root, "native/main.cpp", "#ifdef BREAK\nint broken(){return missing_name;}\n#endif\nint value(){return 1;}\n")
	put(t, root, "compile_commands.json", `[{"directory":"native","file":"main.cpp","arguments":["g++","-c","main.cpp"]}]`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "native baseline")
	gitTest(t, root, "checkout", "-qb", "compiler-flags")
	put(t, root, "compile_commands.json", `[{"directory":"native","file":"main.cpp","arguments":["g++","-DBREAK","-c","main.cpp"]}]`)
	put(t, root, "a/a.go", "package a\n\n// New returns the value.\nfunc New() int { return 1 }\n")
	gitTest(t, root, "commit", "-qam", "enable native feature")
	args := []string{"gate", "--base", "baseline", "--run", "--typecheck", "compiler-flags"}
	report := invoke(t, root, 1, args...)
	if report["gate"].(map[string]any)["verdict"] != "fail" {
		t.Fatalf("gate=%v", report["gate"])
	}
	found := false
	for _, raw := range report["typecheck_executions"].([]any) {
		ev := raw.(map[string]any)
		if ev["tool"] == "cpp" && ev["status"] == "failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("executions=%v", report["typecheck_executions"])
	}
	code, out, errs := run(t, root, args...)
	if code != 1 || !strings.Contains(out, "Radar gate: FAIL") || !strings.Contains(out, "native/main.cpp") {
		t.Fatalf("exit=%d\n%s\n%s", code, out, errs)
	}
}

func TestGateAlembicConfiguredDirectoriesDetectCombinedHeads(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "alembic.ini", "[alembic]\nscript_location = alembic\nversion_locations = alembic/core;alembic/ext\npath_separator = ;\n")
	put(t, root, "alembic/core/root.py", "revision = 'root'\ndown_revision = None\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "alembic baseline")
	gitTest(t, root, "branch", "baseline")
	for _, name := range []string{"left", "right"} {
		gitTest(t, root, "checkout", "-qb", name, "baseline")
		put(t, root, "alembic/ext/"+name+".py", "revision = '"+name+"'\ndown_revision = 'root'\n")
		gitTest(t, root, "add", ".")
		gitTest(t, root, "commit", "-qm", "add revision")
	}
	args := []string{"gate", "--base", "baseline", "left", "right"}
	report := invoke(t, root, 1, args...)
	if report["gate"].(map[string]any)["verdict"] != "fail" {
		t.Fatalf("gate=%v", report["gate"])
	}
	found := false
	for _, raw := range report["checks"].([]any) {
		check := raw.(map[string]any)
		found = found || check["id"] == "migration_order" && check["status"] == "failed"
	}
	if !found {
		t.Fatalf("checks=%v", report["checks"])
	}
	code, out, errs := run(t, root, args...)
	if code != 1 || !strings.Contains(out, "Radar gate: FAIL") || !strings.Contains(out, "Migration") {
		t.Fatalf("exit=%d\n%s\n%s", code, out, errs)
	}
}
