package evidence

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/model"
)

func TestParsePythonTypecheck(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"app/models.py", "app/report.py", "svc/app/x.py"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, f)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	scope := TypecheckScope{PythonPackages: []string{"app"}}
	cases := []struct {
		name    string
		tool    string
		cwd     string
		output  string
		paths   []string
		symbols []string
		env     string
	}{
		{name: "mypy errors, notes skipped", tool: "mypy", output: "app/models.py:5:1: error: Name \"f\" already defined on line 3  [no-redef]\napp/report.py:6:11: error: Name \"undefined_thing\" is not defined  [name-defined]\napp/report.py:6:11: note: Did you mean \"x\"?\napp/report.py:7:12: error: \"User\" has no attribute \"name\"  [attr-defined]\napp/report.py:4:1: error: Module \"app.models\" has no attribute \"Old\"  [attr-defined]\napp/report.py:7:30: error: Argument 1 to \"f\" has incompatible type \"str\"; expected \"int\"  [arg-type]\n", paths: []string{"app/models.py", "app/report.py", "app/report.py", "app/report.py", "app/report.py"}, symbols: []string{"", "undefined_thing", "User.name", "app.models.Old", ""}},
		{name: "mypy in a project directory", tool: "mypy", cwd: "svc", output: "app/x.py:1:1: error: Incompatible types in assignment  [assignment]\n", paths: []string{"svc/app/x.py"}, symbols: []string{""}},
		{name: "mypy missing project module is the source's", tool: "mypy", output: "app/report.py:2:1: error: Cannot find implementation or library stub for module named \"app.gone\"  [import-not-found]\napp/report.py:2:1: note: See https://mypy.readthedocs.io/en/stable/running_mypy.html#missing-imports\n", paths: []string{"app/report.py"}, symbols: []string{""}},
		{name: "mypy missing third-party module is the environment", tool: "mypy", output: "app/report.py:6:11: error: Name \"x\" is not defined  [name-defined]\napp/report.py:2:1: error: Cannot find implementation or library stub for module named \"pydantic\"  [import-not-found]\n", env: "pydantic"},
		{name: "mypy missing stubs are the environment", tool: "mypy", output: "app/report.py:1:1: error: Library stubs not installed for \"requests\"  [import-untyped]\napp/report.py:1:1: note: Hint: \"python3 -m pip install types-requests\"\n", env: "requests"},
		{name: "mypy installed package without types is the environment", tool: "mypy", output: "app/report.py:1:1: error: Skipping analyzing \"yaml\": module is installed, but missing library stubs or py.typed marker  [import-untyped]\n", env: "yaml"},
		{name: "mypy error outside the repository is dropped", tool: "mypy", output: "/usr/lib/python3/site-packages/x.py:1:1: error: broken  [misc]\n"},
		{name: "mypy syntax error is located", tool: "mypy", output: "app/report.py:1:13: error: Expected a parameter or the end of the parameter list  [syntax]\n", paths: []string{"app/report.py"}, symbols: []string{""}},
		{name: "ruff undefined and redefined names", tool: "ruff", output: "app/models.py:5:5: F811 Redefinition of unused `f` from line 3: `f` redefined here\napp/report.py:6:11: F821 Undefined name `undefined_thing`\nFound 2 errors.\n", paths: []string{"app/models.py", "app/report.py"}, symbols: []string{"", "undefined_thing"}},
		{name: "ruff fixable marker and syntax errors", tool: "ruff", output: "app/models.py:1:1: F811 [*] Redefinition of unused `os` from line 1\napp/report.py:1:12: invalid-syntax: Expected a parameter\napp/report.py:2:1: SyntaxError: unexpected EOF\n", paths: []string{"app/models.py", "app/report.py", "app/report.py"}, symbols: []string{"", "", ""}},
		{name: "ruff summary only", tool: "ruff", output: "All checks passed!\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := root
			if c.cwd != "" {
				dir = filepath.Join(root, c.cwd)
			}
			p := parseTypecheck(c.tool, []byte(c.output), root, dir, c.cwd, scope)
			if c.env != "" {
				if p.environment == nil || p.environment.Kind != "module_missing" || p.environment.Name != c.env || len(p.diagnostics) != 0 {
					t.Fatalf("environment=%+v diagnostics=%+v", p.environment, p.diagnostics)
				}
				return
			}
			if p.environment != nil || len(p.diagnostics) != len(c.paths) {
				t.Fatalf("parse=%+v", p)
			}
			for i, d := range p.diagnostics {
				if d.Path != c.paths[i] || d.Symbol != c.symbols[i] || len(d.Fingerprint) != 24 {
					t.Fatalf("diagnostic %d = %+v", i, d)
				}
			}
		})
	}
}

func TestPythonFingerprintIgnoresLineReferences(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "m.py"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint := func(tool, output string) string {
		p := parseTypecheck(tool, []byte(output), root, root, ".", TypecheckScope{})
		if len(p.diagnostics) != 1 {
			t.Fatalf("%q: %+v", output, p)
		}
		return p.diagnostics[0].Fingerprint
	}
	if fingerprint("ruff", "m.py:9:5: F811 Redefinition of unused `f` from line 3\n") != fingerprint("ruff", "m.py:19:5: F811 Redefinition of unused `f` from line 13\n") {
		t.Fatal("a moved earlier definition changed the ruff fingerprint")
	}
	if fingerprint("mypy", "m.py:5:1: error: Name \"f\" already defined on line 3  [no-redef]\n") == fingerprint("mypy", "m.py:5:1: error: Name \"g\" already defined on line 3  [no-redef]\n") {
		t.Fatal("different names share a fingerprint")
	}
	if fingerprint("mypy", "m.py:5:1: error: Incompatible types  [assignment]\n") == fingerprint("mypy", "m.py:5:1: error: Incompatible types  [return-value]\n") {
		t.Fatal("the error code is not part of the fingerprint")
	}
}

func TestObserveMypyStatuses(t *testing.T) {
	cases := []struct {
		name    string
		output  string
		code    int
		status  model.Status
		partial bool
	}{
		{name: "errors", output: "m.py:1:1: error: Name \"x\" is not defined  [name-defined]\n", code: 1, status: model.StatusFailed},
		{name: "a blocking error makes the run partial", output: "m.py:1:5: error: invalid syntax  [syntax]\n", code: 2, status: model.StatusFailed, partial: true},
		{name: "a crash is an error", output: "Traceback (most recent call last):\nINTERNAL ERROR\n", code: 2, status: model.StatusError},
		{name: "unreadable file is an error", output: "m.py: error: Cannot read file: No such file or directory\n", code: 2, status: model.StatusError},
		{name: "clean", output: "", code: 0, status: model.StatusPassed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "m.py"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			argv := fakeTool(t, root, c.output, c.code)
			r, err := ObserveTypecheck(context.Background(), root, ".", "mypy", TypecheckScope{}, argv, 10*time.Second)
			if err != nil || r.Status != c.status || r.Partial != c.partial {
				t.Fatalf("err=%v observation=%+v", err, r)
			}
		})
	}
}

// pythonTool reports whether python3 can run module, so real-tool tests
// skip where it is not installed.
func pythonTool(t *testing.T, module string) {
	t.Helper()
	if err := exec.Command("python3", "-m", module, "--version").Run(); err != nil {
		t.Skipf("python3 -m %s unavailable", module)
	}
}

func TestMypyShadowOnlyTypedProjectCopies(t *testing.T) {
	site := t.TempDir()
	pythonTestPut(t, site, "project/py.typed", "")
	pythonTestPut(t, site, "thirdparty/__init__.py", "")
	pythonTestPut(t, site, "stubs-stubs/__init__.pyi", "")
	if got := MypyShadow([]string{site}, []string{"unrelated", "thirdparty"}); got != "" {
		t.Fatalf("unrelated untyped package=%s", got)
	}
	if got := MypyShadow([]string{site}, []string{"project"}); got != "project" {
		t.Fatalf("typed shadow=%s", got)
	}
	if got := MypyShadow([]string{site}, []string{"stubs"}); got != "stubs" {
		t.Fatalf("stub shadow=%s", got)
	}
}

func TestObserveRealMypyAndRuff(t *testing.T) {
	pythonTool(t, "mypy")
	if _, err := exec.LookPath("ruff"); err != nil {
		t.Skip("ruff unavailable")
	}
	root := t.TempDir()
	for name, content := range map[string]string{"mypy.ini": "[mypy]\n", "app/__init__.py": "", "app/m.py": "def f(x: int) -> int:\n    return x\n\n\ndef g() -> str:\n    return f(1) + missing()\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mypy, err := ObserveTypecheck(context.Background(), root, ".", "mypy", TypecheckScope{PythonPackages: []string{"app"}}, []string{"python3", "-m", "mypy", "--no-error-summary", "--show-error-codes", "--show-column-numbers", "--no-pretty", "--no-color-output", "--cache-dir=/dev/null", "--", "app/m.py"}, 2*time.Minute)
	if err != nil || mypy.Status != model.StatusFailed || mypy.DiagnosticCount != 1 || mypy.Diagnostics[0].Symbol != "missing" || mypy.Partial {
		t.Fatalf("err=%v mypy=%+v", err, mypy)
	}
	ruff, err := ObserveTypecheck(context.Background(), root, ".", "ruff", TypecheckScope{}, []string{"ruff", "check", "--no-cache", "--output-format", "concise", "--select", "F821,F811,F822,F823", "--", "app/m.py"}, time.Minute)
	if err != nil || ruff.Status != model.StatusFailed || ruff.DiagnosticCount != 1 || ruff.Diagnostics[0].Symbol != "missing" {
		t.Fatalf("err=%v ruff=%+v", err, ruff)
	}
	if _, err := os.Stat(filepath.Join(root, ".mypy_cache")); err == nil {
		t.Fatal("mypy wrote a cache into the checked tree")
	}
}

func TestPythonTypecheckReviewRegressions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "m.py"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	crash := parseTypecheck("mypy", []byte("m.py:3: error: INTERNAL ERROR -- Please try using mypy master on GitHub:\nTraceback (most recent call last):\n"), root, root, ".", TypecheckScope{})
	if !crash.crashed || len(crash.diagnostics) != 0 {
		t.Fatalf("a mypy crash was a finding: %+v", crash)
	}
	long := "pkg" + strings.Repeat("x", 140) + ".sub"
	missing := parseTypecheck("mypy", []byte("m.py:1:1: error: Cannot find implementation or library stub for module named \""+long+"\"  [import-not-found]\n"), root, root, ".", TypecheckScope{})
	if missing.environment == nil || len(missing.environment.Name) > 100 {
		t.Fatalf("diagnosis=%+v", missing.environment)
	}
	none := parseTypecheck("ruff", []byte("warning: No Python files found under the given path(s)\nAll checks passed!\n"), root, root, ".", TypecheckScope{})
	if !none.noInputs {
		t.Fatalf("ruff checking nothing was not reported: %+v", none)
	}
	syntax := parseTypecheck("ruff", []byte("m.py:1:7: invalid-syntax: Expected a parameter\n"), root, root, ".", TypecheckScope{})
	if !syntax.partial {
		t.Fatalf("a ruff syntax error did not make the run partial: %+v", syntax)
	}
	argv := fakeTool(t, root, "warning: No Python files found under the given path(s)\n", 0)
	if r, err := ObserveTypecheck(context.Background(), root, ".", "ruff", TypecheckScope{}, argv, 10*time.Second); err != nil || r.Status != model.StatusUnknown {
		t.Fatalf("err=%v observation=%+v", err, r)
	}
}
