package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/typecheck"
)

// Python round-3 review regressions: mypy projects that reach a changed
// project, search bases, and imports mypy is configured to ignore.

func mypyExecutionAt(r Report, root string) *TypecheckExecution {
	for i := range r.TypecheckExecutions {
		if ev := &r.TypecheckExecutions[i]; ev.Tool == typecheck.ToolMypy && ev.Root == root {
			return ev
		}
	}
	return nil
}

func TestMypyChecksAnotherProjectThatReachesTheChangeThroughMypyPath(t *testing.T) {
	root := pyTypecheckRepo(t, map[string]string{
		"pyproject.toml":      "[project]\nname = \"svc\"\nversion = \"0\"\n\n[tool.mypy]\nmypy_path = \"lib\"\nexclude = \"^lib/\"\n",
		"lib/pyproject.toml":  "[tool.mypy]\nstrict_optional = true\n",
		"lib/pkg/__init__.py": "",
		"lib/pkg/util.py":     "def f(x: int) -> int:\n    return x\n",
		"app/main.py":         "from pkg.util import f\n\n\ndef run() -> int:\n    return f(1)\n",
	})
	branch(t, root, "narrow", "baseline", map[string]string{"lib/pkg/util.py": "def f(x: str) -> str:\n    return x\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("narrow"))
	if err != nil {
		t.Fatal(err)
	}
	ev := mypyExecutionAt(r, ".")
	if ev == nil || ev.Status != model.StatusFailed || len(ev.NewDiagnostics) == 0 || ev.NewDiagnostics[0].Path != "app/main.py" {
		t.Fatalf("a project using the change through mypy_path was not checked: %+v all=%+v", ev, r.TypecheckExecutions)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusFailed {
		t.Fatalf("check=%+v", c)
	}
}

func TestMypyChecksASiblingProjectThatImportsTheChange(t *testing.T) {
	root := pyTypecheckRepo(t, map[string]string{
		"core/pyproject.toml":      "[tool.mypy]\nstrict_optional = true\n",
		"core/corelib/__init__.py": "",
		"core/corelib/calc.py":     "def total(x: int) -> int:\n    return x\n",
		"api/pyproject.toml":       "[tool.mypy]\nmypy_path = \"../core\"\n",
		"api/handler.py":           "from corelib.calc import total\n\n\ndef handle() -> int:\n    return total(2)\n",
	})
	branch(t, root, "rename", "baseline", map[string]string{"core/corelib/calc.py": "def grand_total(x: int) -> int:\n    return x\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("rename"))
	if err != nil {
		t.Fatal(err)
	}
	ev := mypyExecutionAt(r, "api")
	if ev == nil || ev.Status != model.StatusFailed || len(ev.NewDiagnostics) == 0 || ev.NewDiagnostics[0].Path != "api/handler.py" {
		t.Fatalf("a sibling project importing the change was not checked: %+v all=%+v", ev, r.TypecheckExecutions)
	}
}

func TestMypyDoesNotSelectAProjectThatDoesNotImportTheChange(t *testing.T) {
	root := pyTypecheckRepo(t, map[string]string{
		"core/pyproject.toml":      "[tool.mypy]\nstrict_optional = true\n",
		"core/corelib/__init__.py": "",
		"core/corelib/calc.py":     "def total(x: int) -> int:\n    return x\n",
		"api/pyproject.toml":       "[tool.mypy]\nmypy_path = \"../core\"\n",
		"api/handler.py":           "def handle() -> int:\n    return 2\n",
	})
	branch(t, root, "rename", "baseline", map[string]string{"core/corelib/calc.py": "def grand_total(x: int) -> int:\n    return x\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("rename"))
	if err != nil {
		t.Fatal(err)
	}
	if ev := mypyExecutionAt(r, "api"); ev != nil {
		t.Fatalf("a project that does not import the change was checked: %+v", ev)
	}
	if ev := mypyExecutionAt(r, "core"); ev == nil || ev.Status != model.StatusPassed {
		t.Fatalf("core execution=%+v", ev)
	}
}

func TestMypyMissingThirdPartyModuleNamedLikeANestedPackageIsTheEnvironment(t *testing.T) {
	root := pyTypecheckRepo(t, map[string]string{
		"app/integrations/__init__.py":                  "",
		"app/integrations/radar_absent_xyz/__init__.py": "",
		"app/integrations/radar_absent_xyz/client.py":   "class Client:\n    pass\n",
	})
	branch(t, root, "dep", "baseline", map[string]string{"app/cache.py": "from radar_absent_xyz.client import Redis\n\n\ndef get() -> Redis:\n    return Redis()\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("dep"))
	if err != nil {
		t.Fatal(err)
	}
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusError || ev.Observation.Diagnosis == nil || ev.Observation.Diagnosis.Kind != "module_missing" {
		t.Fatalf("a missing dependency was blamed on the source: %+v", ev)
	}
}

func TestMypyMissingSingleFileModuleOnMypyPathIsTheSources(t *testing.T) {
	root := pyTypecheckRepo(t, map[string]string{
		"mypy.ini":             "[mypy]\nmypy_path = shared\n",
		"shared/radar_util.py": "def h() -> int:\n    return 1\n",
	})
	gitTest(t, root, "checkout", "-qb", "drop", "baseline")
	gitTest(t, root, "rm", "-q", "shared/radar_util.py")
	gitTest(t, root, "commit", "-qm", "drop util")
	gitTest(t, root, "checkout", "-q", "baseline")
	branch(t, root, "use", "baseline", map[string]string{"app/use.py": "from radar_util import h\n\n\ndef run() -> int:\n    return h()\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("drop", "use"))
	if err != nil {
		t.Fatal(err)
	}
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusFailed || len(ev.NewDiagnostics) == 0 || ev.NewDiagnostics[0].Path != "app/use.py" {
		t.Fatalf("a deleted module on mypy_path was blamed on the environment: %+v", ev)
	}
}

func dropHelpersRepo(t *testing.T, config string, user map[string]string) (string, Report) {
	t.Helper()
	root := pyTypecheckRepo(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"svc\"\nversion = \"0\"\n\n" + config,
		"app/helpers.py": "def slug(s: str) -> str:\n    return s.lower()\n",
	})
	gitTest(t, root, "checkout", "-qb", "drop", "baseline")
	gitTest(t, root, "rm", "-q", "app/helpers.py")
	gitTest(t, root, "commit", "-qm", "drop helpers")
	gitTest(t, root, "checkout", "-q", "baseline")
	branches := []string{"drop"}
	if user != nil {
		branch(t, root, "use", "baseline", user)
		branches = append(branches, "use")
	}
	r, err := Preview(context.Background(), root, typecheckOptions(branches...))
	if err != nil {
		t.Fatal(err)
	}
	return root, r
}

func TestMypyIgnoredMissingImportOfADeletedModuleIsNotAPass(t *testing.T) {
	for name, config := range map[string]string{
		"ignore_missing_imports": "[tool.mypy]\nignore_missing_imports = true\n",
		"per-module override":    "[tool.mypy]\n\n[[tool.mypy.overrides]]\nmodule = \"app.*\"\nignore_missing_imports = true\n",
		"disabled error code":    "[tool.mypy]\ndisable_error_code = [\"import-not-found\"]\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, r := dropHelpersRepo(t, config, map[string]string{"app/slugs.py": "from app.helpers import slug\n\n\ndef key(s: str) -> str:\n    return slug(s)\n"})
			ev := pythonExecution(r, typecheck.ToolMypy)
			if ev == nil || ev.Status == model.StatusPassed || ev.Status == model.StatusFailed {
				t.Fatalf("an ignored import of a deleted module passed: %+v", ev)
			}
			if len(ev.HiddenImports) != 1 || ev.HiddenImports[0].Path != "app/slugs.py" || ev.HiddenImports[0].Module != "app.helpers" || ev.HiddenImports[0].Line != 1 {
				t.Fatalf("hidden imports=%+v", ev.HiddenImports)
			}
			if c := findCheck(r, typecheck.CheckID); c == nil || c.Status == model.StatusPassed {
				t.Fatalf("check=%+v", c)
			}
			var found *model.Finding
			for i := range r.Findings {
				if r.Findings[i].Code == "typecheck_hidden_missing_import" {
					found = &r.Findings[i]
				}
			}
			if found == nil || found.Evidence != model.Inferred || len(found.Locations) != 1 || found.Locations[0].Path != "app/slugs.py" {
				t.Fatalf("finding=%+v", found)
			}
		})
	}
}

func TestMypyIgnoredMissingImportRelativeImportOfADeletedModule(t *testing.T) {
	_, r := dropHelpersRepo(t, "[tool.mypy]\nignore_missing_imports = true\n", map[string]string{"app/slugs.py": "from . import helpers\n\n\ndef key(s: str) -> str:\n    return helpers.slug(s)\n"})
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status == model.StatusPassed || len(ev.HiddenImports) != 1 || ev.HiddenImports[0].Module != "app.helpers" {
		t.Fatalf("a relative import of a deleted module passed: %+v", ev)
	}
}

func TestMypyIgnoredMissingImportWithoutImportersPassesWithALimitation(t *testing.T) {
	_, r := dropHelpersRepo(t, "[tool.mypy]\nignore_missing_imports = true\n", nil)
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusPassed || len(ev.HiddenImports) != 0 {
		t.Fatalf("execution=%+v", ev)
	}
	if !strings.Contains(strings.Join(r.Limitations, "\n"), "ignores missing imports of app.helpers") {
		t.Fatalf("limitations=%v", r.Limitations)
	}
}

func TestMypyIgnoredMissingImportOfAMovedModuleIsNotHidden(t *testing.T) {
	root := pyTypecheckRepo(t, map[string]string{
		"pyproject.toml": "[project]\nname = \"svc\"\nversion = \"0\"\n\n[tool.mypy]\nignore_missing_imports = true\n",
		"app/helpers.py": "def slug(s: str) -> str:\n    return s.lower()\n",
		"app/slugs.py":   "from app.helpers import slug\n\n\ndef key(s: str) -> str:\n    return slug(s)\n",
	})
	gitTest(t, root, "checkout", "-qb", "pkg", "baseline")
	gitTest(t, root, "rm", "-q", "app/helpers.py")
	put(t, root, "app/helpers/__init__.py", "def slug(s: str) -> str:\n    return s.lower()\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "package")
	gitTest(t, root, "checkout", "-q", "baseline")
	r, err := Preview(context.Background(), root, typecheckOptions("pkg"))
	if err != nil {
		t.Fatal(err)
	}
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusPassed || len(ev.HiddenImports) != 0 {
		t.Fatalf("a module that still exists as a package was reported hidden: %+v", ev)
	}
}

func TestMypySearchPathOutsideTheCandidateIsBlocked(t *testing.T) {
	outside := t.TempDir()
	root := pyTypecheckRepo(t, map[string]string{"mypy.ini": "[mypy]\nmypy_path = " + outside + "\n"})
	branch(t, root, "edit", "baseline", map[string]string{"app/report.py": "def line() -> str:\n    return \"x\"\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("edit"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.TypecheckOmitted) != 1 || r.TypecheckOmitted[0].Reason != "environment_unavailable" || !strings.Contains(r.TypecheckOmitted[0].Explanation, "outside the private candidate") {
		t.Fatalf("omitted=%+v executions=%+v", r.TypecheckOmitted, r.TypecheckExecutions)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status == model.StatusPassed {
		t.Fatalf("check=%+v", c)
	}
}

func TestMypyInstalledTypedShadowOfDeletedModuleIsBlocked(t *testing.T) {
	requirePythonTools(t)
	venv := t.TempDir()
	if out, err := exec.Command("python3", "-m", "venv", "--without-pip", venv).CombinedOutput(); err != nil {
		t.Fatalf("venv: %s %v", out, err)
	}
	python := filepath.Join(venv, "bin", "python")
	if runtime.GOOS == "windows" {
		python = filepath.Join(venv, "Scripts", "python.exe")
	}
	out, err := exec.Command(python, "-c", "import site; print(site.getsitepackages()[0])").Output()
	if err != nil {
		t.Fatal(err)
	}
	site := strings.TrimSpace(string(out))
	for name, content := range map[string]string{"__init__.py": "", "helpers.py": "def slug(s: str) -> str:\n    return s.lower()\n", "py.typed": ""} {
		file := filepath.Join(site, "app", name)
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root := pyTypecheckRepo(t, map[string]string{"mypy.ini": "[mypy]\npython_executable = " + python + "\n", "app/helpers.py": "def slug(s: str) -> str:\n    return s.lower()\n"})
	gitTest(t, root, "checkout", "-qb", "drop", "baseline")
	gitTest(t, root, "rm", "-q", "app/helpers.py")
	gitTest(t, root, "commit", "-qm", "drop helper")
	gitTest(t, root, "checkout", "-q", "baseline")
	branch(t, root, "use", "baseline", map[string]string{"app/slugs.py": "from app.helpers import slug\n\ndef key(s: str) -> str:\n    return slug(s)\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("drop", "use"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status == model.StatusPassed || c.Status == model.StatusFailed {
		t.Fatalf("check=%+v", c)
	}
	if len(r.TypecheckOmitted) != 1 || !strings.Contains(r.TypecheckOmitted[0].Explanation, "installed copy of project package app") {
		t.Fatalf("omitted=%+v", r.TypecheckOmitted)
	}
}

func TestMypyDeletedImportInDocstringDoesNotBlock(t *testing.T) {
	_, r := dropHelpersRepo(t, "[tool.mypy]\nignore_missing_imports = true\n", map[string]string{"app/slugs.py": "doc = '''\nfrom app.helpers import slug\n'''\n"})
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusPassed || len(ev.HiddenImports) != 0 {
		t.Fatalf("execution=%+v", ev)
	}
}

func TestMypyModulesConfigurationHasARealSourceInventory(t *testing.T) {
	root := pyTypecheckRepo(t, map[string]string{"mypy.ini": "[mypy]\nmodules = app.views\n"})
	branch(t, root, "edit", "baseline", map[string]string{"app/views.py": "from app.models import User\n\ndef show(u: User) -> str:\n    return u.name + '!'\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("edit"))
	if err != nil {
		t.Fatal(err)
	}
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusPassed || len(r.TypecheckGaps) != 0 {
		t.Fatalf("execution=%+v gaps=%+v", ev, r.TypecheckGaps)
	}
}

func TestMypyDisabledErrorsDoNotCountAsCheckedSource(t *testing.T) {
	for _, tc := range []struct{ name, config, header string }{
		{"global", "[mypy]\nignore_errors = True\n", ""},
		{"module override", "[mypy]\n[mypy-app.views]\nignore_errors = True\n", ""},
		{"file directive", "[mypy]\n", "# mypy: ignore-errors\n"},
		{"inline false under global ignore", "[mypy]\nignore_errors = True\n", "# mypy: ignore-errors=False\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pyTypecheckRepo(t, map[string]string{"mypy.ini": tc.config})
			branch(t, root, "silenced", "baseline", map[string]string{"app/views.py": tc.header + "def show() -> int:\n    return 'wrong'\n"})
			r, err := Preview(context.Background(), root, typecheckOptions("silenced"))
			if err != nil {
				t.Fatal(err)
			}
			if check := findCheck(r, typecheck.CheckID); check == nil || check.Status == model.StatusPassed {
				t.Fatalf("disabled mypy errors counted as checked: check=%+v ev=%+v gaps=%+v", check, r.TypecheckExecutions, r.TypecheckGaps)
			}
			found := false
			for _, g := range r.TypecheckGaps {
				found = found || g.Path == "app/views.py"
			}
			if !found {
				t.Fatalf("disabled source missing coverage gap: %+v", r.TypecheckGaps)
			}
		})
	}
}

func TestMypyDirectiveLookalikesAndUntypedCodeRemainCovered(t *testing.T) {
	for _, tc := range []struct {
		name, config, source string
		fail                 bool
	}{
		{"ordinary string", "[mypy]\n", "doc = '# mypy: ignore-errors'\ndef show():\n    return 'ordinary untyped function'\n", false},
		{"multiline string", "[mypy]\n", "doc = '''\n# mypy: ignore-errors\n'''\ndef show() -> int:\n    return 'wrong'\n", true},
		{"trailing comment", "[mypy]\n", "def show() -> int:\n    return 'wrong' # mypy: ignore-errors\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := pyTypecheckRepo(t, map[string]string{"mypy.ini": tc.config})
			branch(t, root, "edit", "baseline", map[string]string{"app/views.py": tc.source})
			r, err := Preview(context.Background(), root, typecheckOptions("edit"))
			if err != nil {
				t.Fatal(err)
			}
			ev := pythonExecution(r, typecheck.ToolMypy)
			if ev == nil || len(r.TypecheckGaps) != 0 || tc.fail && ev.Status != model.StatusFailed || !tc.fail && ev.Status != model.StatusPassed {
				t.Fatalf("execution=%+v gaps=%+v", ev, r.TypecheckGaps)
			}
		})
	}
}
