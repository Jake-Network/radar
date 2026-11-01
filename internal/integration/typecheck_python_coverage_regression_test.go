package integration

import (
	"context"
	"os/exec"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/typecheck"
)

func TestRuffListingCannotSatisfyCombinedTypecheck(t *testing.T) {
	if _, err := exec.LookPath("ruff"); err != nil {
		t.Skip("ruff unavailable")
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, "app.py", "value = 1\n")
	put(t, root, ".radar/runners.json", `{"version":1,"profiles":[{"root":".","typecheck":[{"tool":"ruff","command":["ruff","check","--show-files","."]}]}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	gitTest(t, root, "branch", "baseline")
	branch(t, root, "broken", "baseline", map[string]string{"app.py": "value = missing_name\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("broken"))
	if err != nil {
		t.Fatal(err)
	}
	if check := findCheck(r, typecheck.CheckID); check == nil || check.Status == model.StatusPassed {
		t.Fatalf("listing-only command satisfied typechecking: check=%+v executions=%+v", check, r.TypecheckExecutions)
	}
	if ev := pythonExecution(r, "ruff"); ev == nil || ev.Status != model.StatusIncomplete {
		t.Fatalf("listing-only execution must remain incomplete: %+v", ev)
	}
}

func TestMypyModuleIgnoreIsNotCoverage(t *testing.T) {
	requirePythonTools(t)
	for _, tc := range []struct {
		name, source string
		gap          bool
		status       model.Status
	}{
		{"module ignore", "# type: ignore\ncount: int = 'bad'\n", true, model.StatusPassed},
		{"module ignore after ordinary comments", "#!/usr/bin/env python3\n# explanatory comment\n\n# type: ignore\ncount: int = 'bad'\n", true, model.StatusPassed},
		{"decorator is first statement", "@missing_decorator # type: ignore\ndef show() -> int:\n    return 'wrong'\n", false, model.StatusFailed},
		{"line ignore", "count: int = 'bad' # type: ignore\n", false, model.StatusPassed},
		{"string lookalike", "text = '# type: ignore'\ncount: int = 1\n", false, model.StatusPassed},
		{"after module docstring", "'''module documentation'''\n# type: ignore\ncount: int = 'bad'\n", false, model.StatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			gitTest(t, root, "init", "-q")
			put(t, root, "mypy.ini", "[mypy]\n")
			put(t, root, "app.py", "count: int = 1\n")
			gitTest(t, root, "add", ".")
			gitTest(t, root, "commit", "-qm", "base")
			gitTest(t, root, "branch", "baseline")
			branch(t, root, "edit", "baseline", map[string]string{"app.py": tc.source})
			r, err := Preview(context.Background(), root, typecheckOptions("edit"))
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, gap := range r.TypecheckGaps {
				found = found || gap.Path == "app.py"
			}
			if found != tc.gap {
				t.Fatalf("gap=%v, want %v: %+v", found, tc.gap, r.TypecheckGaps)
			}
			if ev := pythonExecution(r, "mypy"); ev == nil || ev.Status != tc.status {
				t.Fatalf("execution=%+v, want %s", ev, tc.status)
			}
			if check := findCheck(r, typecheck.CheckID); check == nil || tc.gap && check.Status == model.StatusPassed {
				t.Fatalf("module ignored source counted as checked: %+v", check)
			}
		})
	}
}
