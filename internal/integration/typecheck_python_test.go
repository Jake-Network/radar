package integration

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/typecheck"
)

func requirePythonTools(t *testing.T) {
	t.Helper()
	if err := exec.Command("python3", "-m", "mypy", "--version").Run(); err != nil {
		t.Skip("python3 -m mypy unavailable")
	}
	if _, err := exec.LookPath("ruff"); err != nil {
		t.Skip("ruff unavailable")
	}
}

// pyTypecheckRepo is a Python project configured for mypy and ruff.
func pyTypecheckRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	requirePythonTools(t)
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	base := map[string]string{
		"pyproject.toml":  "[project]\nname = \"svc\"\nversion = \"0\"\n\n[tool.mypy]\nstrict_optional = true\n\n[tool.ruff]\nline-length = 100\n",
		"app/__init__.py": "",
		"app/models.py":   "class User:\n    def __init__(self, name: str) -> None:\n        self.name = name\n",
		"app/views.py":    "from app.models import User\n\n\ndef show(u: User) -> str:\n    return u.name\n",
	}
	for p, content := range files {
		base[p] = content
	}
	for p, content := range base {
		put(t, root, p, content)
	}
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	gitTest(t, root, "branch", "baseline")
	return root
}

func pythonExecution(r Report, tool string) *TypecheckExecution {
	for i := range r.TypecheckExecutions {
		if r.TypecheckExecutions[i].Tool == tool {
			return &r.TypecheckExecutions[i]
		}
	}
	return nil
}

func TestMypyFailsOnlyTheCombination(t *testing.T) {
	root := pyTypecheckRepo(t, nil)
	branch(t, root, "rename", "baseline", map[string]string{
		"app/models.py": "class User:\n    def __init__(self, full_name: str) -> None:\n        self.full_name = full_name\n",
		"app/views.py":  "from app.models import User\n\n\ndef show(u: User) -> str:\n    return u.full_name\n",
	})
	branch(t, root, "report", "baseline", map[string]string{"app/report.py": "from app.models import User\n\n\ndef line(u: User) -> str:\n    return \"user: \" + u.name\n"})
	for _, alone := range []string{"rename", "report"} {
		r, err := Preview(context.Background(), root, typecheckOptions(alone))
		if err != nil {
			t.Fatal(err)
		}
		if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusPassed || c.Evidence != model.VerifiedTool {
			t.Fatalf("%s alone: check=%+v executions=%+v", alone, c, r.TypecheckExecutions)
		}
	}
	r, err := Preview(context.Background(), root, typecheckOptions("rename", "report"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusFailed {
		t.Fatalf("check=%+v", c)
	}
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusFailed || len(ev.NewDiagnostics) != 1 || ev.NewDiagnostics[0].Path != "app/report.py" || ev.NewDiagnostics[0].Symbol != "User.name" {
		t.Fatalf("mypy execution=%+v", ev)
	}
	// mypy checks the whole project on both sides.
	if cmp := ev.BaseComparison; cmp == nil || !cmp.Compared || cmp.Command != nil || ev.Command[len(ev.Command)-1] != "." {
		t.Fatalf("base comparison=%+v", ev.BaseComparison)
	}
	if ruff := pythonExecution(r, typecheck.ToolRuff); ruff == nil || ruff.Status != model.StatusPassed {
		t.Fatalf("ruff execution=%+v", ruff)
	}
}

func TestMypyErrorInAnImportedModuleThatBaseHasIsNotBlamed(t *testing.T) {
	root := pyTypecheckRepo(t, map[string]string{"app/legacy.py": "count: int = \"many\"\n"})
	branch(t, root, "uses-legacy", "baseline", map[string]string{"app/views.py": "from app import legacy\nfrom app.models import User\n\n\ndef show(u: User) -> str:\n    return u.name + str(legacy.count)\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("uses-legacy"))
	if err != nil {
		t.Fatal(err)
	}
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusIncomplete || ev.BaseComparison == nil || !ev.BaseComparison.Compared || ev.BaseComparison.Existing != 1 || len(ev.NewDiagnostics) != 0 {
		t.Fatalf("an existing error in an imported module was blamed: %+v base=%+v", ev, ev.BaseComparison)
	}
}

func TestMypyMissingThirdPartyModuleIsTheEnvironment(t *testing.T) {
	root := pyTypecheckRepo(t, nil)
	branch(t, root, "dep", "baseline", map[string]string{"app/client.py": "import radar_absent_dependency_xyz\n\n\ndef get() -> int:\n    return radar_absent_dependency_xyz.value\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("dep"))
	if err != nil {
		t.Fatal(err)
	}
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusError || ev.Observation.Diagnosis == nil || ev.Observation.Diagnosis.Kind != "module_missing" || ev.Observation.Diagnosis.Name != "radar_absent_dependency_xyz" {
		t.Fatalf("execution=%+v", ev)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status == model.StatusPassed {
		t.Fatalf("check=%+v", c)
	}
}

func TestMypyDeletedProjectModuleIsTheSources(t *testing.T) {
	root := pyTypecheckRepo(t, map[string]string{"app/helpers.py": "def slug(s: str) -> str:\n    return s.lower()\n"})
	gitTest(t, root, "checkout", "-qb", "drop-helpers", "baseline")
	gitTest(t, root, "rm", "-q", "app/helpers.py")
	gitTest(t, root, "commit", "-qm", "drop helpers")
	gitTest(t, root, "checkout", "-q", "baseline")
	branch(t, root, "use-helpers", "baseline", map[string]string{"app/slugs.py": "import app.helpers\n\n\ndef key(s: str) -> str:\n    return app.helpers.slug(s)\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("drop-helpers", "use-helpers"))
	if err != nil {
		t.Fatal(err)
	}
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusFailed || len(ev.NewDiagnostics) == 0 || ev.NewDiagnostics[0].Path != "app/slugs.py" {
		t.Fatalf("execution=%+v", ev)
	}
}

func TestRuffFindsANameOneBranchRemovedAndAnotherCalls(t *testing.T) {
	requirePythonTools(t)
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, "ruff.toml", "line-length = 100\n")
	put(t, root, "util.py", "def helper() -> int:\n    return 1\n\n\ndef a() -> int:\n    return helper()\n\n\ndef b() -> int:\n    return 2\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	gitTest(t, root, "branch", "baseline")
	branch(t, root, "remove", "baseline", map[string]string{"util.py": "def a() -> int:\n    return 1\n\n\ndef b() -> int:\n    return 2\n"})
	branch(t, root, "call", "baseline", map[string]string{"util.py": "def helper() -> int:\n    return 1\n\n\ndef a() -> int:\n    return helper()\n\n\ndef b() -> int:\n    return helper() + 1\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("remove", "call"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.TypecheckExecutions) != 1 {
		t.Fatalf("only ruff is configured: %+v", r.TypecheckExecutions)
	}
	ev := r.TypecheckExecutions[0]
	if ev.Tool != typecheck.ToolRuff || ev.Status != model.StatusFailed || len(ev.NewDiagnostics) != 1 || ev.NewDiagnostics[0].Symbol != "helper" {
		t.Fatalf("execution=%+v", ev)
	}
}

func TestPythonWithoutConfigurationIsNotTypeChecked(t *testing.T) {
	root := goTypecheckRepo(t)
	branch(t, root, "script", "baseline", map[string]string{"scripts/run.py": "print(undefined)\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("script"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c != nil || len(r.TypecheckExecutions) != 0 {
		t.Fatalf("check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
	if !strings.Contains(strings.Join(r.Limitations, "\n"), "no mypy or ruff configuration encloses: scripts/run.py") {
		t.Fatalf("limitations=%v", r.Limitations)
	}
}

// Review regressions: each case was a wrong verdict before its fix.

func TestMypyChecksUnchangedImportersOfChangedCode(t *testing.T) {
	root := pyTypecheckRepo(t, map[string]string{
		"app/lib.py":      "def f() -> str:\n    return \"a\"\n",
		"app/mid.py":      "def g() -> str:\n    return \"b\"\n",
		"app/consumer.py": "from app import mid\n\n\ndef shout() -> str:\n    return mid.g().upper()\n",
	})
	branch(t, root, "int", "baseline", map[string]string{"app/lib.py": "def f() -> int:\n    return 1\n"})
	branch(t, root, "alias", "baseline", map[string]string{"app/mid.py": "from app.lib import f as g\n\n__all__ = [\"g\"]\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("int", "alias"))
	if err != nil {
		t.Fatal(err)
	}
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusFailed || len(ev.NewDiagnostics) != 1 || ev.NewDiagnostics[0].Path != "app/consumer.py" {
		t.Fatalf("an unchanged importer's new error was missed: %+v", ev)
	}
}

func TestPythonRenamedFileKeepsItsBaseError(t *testing.T) {
	for _, c := range []struct{ from, to string }{{"old.py", "new.py"}, {"app/old.py", "app/new.py"}} {
		t.Run(c.to, func(t *testing.T) {
			root := pyTypecheckRepo(t, map[string]string{c.from: "count: int = \"many\"\n"})
			gitTest(t, root, "checkout", "-qb", "move", "baseline")
			gitTest(t, root, "mv", c.from, c.to)
			gitTest(t, root, "commit", "-qm", "move")
			gitTest(t, root, "checkout", "-q", "baseline")
			r, err := Preview(context.Background(), root, typecheckOptions("move"))
			if err != nil {
				t.Fatal(err)
			}
			if c := findCheck(r, typecheck.CheckID); c == nil || c.Status == model.StatusFailed || c.Status == model.StatusPassed {
				t.Fatalf("a moved base error was blamed or passed: check=%+v executions=%+v", c, r.TypecheckExecutions)
			}
		})
	}
}

func TestMypyExcludedDirectoryIsAGapAndDoesNotStopTheRun(t *testing.T) {
	root := pyTypecheckRepo(t, nil)
	gitTest(t, root, "checkout", "-q", "baseline")
	put(t, root, "mypy.ini", "[mypy]\nexclude = ^migrations/\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "exclude migrations")
	gitTest(t, root, "branch", "-f", "base2")
	branch(t, root, "both", "base2", map[string]string{
		"migrations/m1.py": "x: int = \"unchecked\"\n",
		"app/report.py":    "from app.models import User\n\n\ndef line(u: User) -> str:\n    return u.name\n",
	})
	o := typecheckOptions("both")
	o.Base = "base2"
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusPassed || strings.Contains(strings.Join(ev.Command, " "), "migrations") {
		t.Fatalf("mypy execution=%+v", ev)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusIncomplete || len(r.TypecheckGaps) != 1 || r.TypecheckGaps[0].Path != "migrations/m1.py" {
		t.Fatalf("check=%+v gaps=%+v", c, r.TypecheckGaps)
	}
}

func ruffRepo(t *testing.T, config string, files map[string]string) string {
	t.Helper()
	requirePythonTools(t)
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, "ruff.toml", config)
	for p, content := range files {
		put(t, root, p, content)
	}
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	gitTest(t, root, "branch", "baseline")
	return root
}

func TestRuffForceExcludeDoesNotSkipChangedFiles(t *testing.T) {
	root := ruffRepo(t, "force-exclude = true\nextend-exclude = [\"legacy\"]\n", map[string]string{"legacy/old.py": "X = 1\n"})
	branch(t, root, "use", "baseline", map[string]string{"legacy/use.py": "def f() -> int:\n    return totl(2)\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("use"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusFailed {
		t.Fatalf("an excluded changed file passed: check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
}

func TestRuffErrorHiddenByABaseSyntaxErrorIsNotBlamed(t *testing.T) {
	root := ruffRepo(t, "", map[string]string{"a.py": "def f(:\n    pass\n\n\ndef g():\n    return undefined_x\n"})
	branch(t, root, "fix", "baseline", map[string]string{"a.py": "def f():\n    pass\n\n\ndef g():\n    return undefined_x\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("fix"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusIncomplete {
		t.Fatalf("an error base hid behind a syntax error was blamed: check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
}

// Round-2 review regressions.

func TestMypyChecksImportersInOtherTopLevelDirectories(t *testing.T) {
	root := pyTypecheckRepo(t, map[string]string{
		"lib/__init__.py": "", "app/__init__.py": "",
		"lib/types.py": "from typing import Union\n\n\ndef f(x: Union[int, str]) -> int:\n    return 1\n",
		"lib/impl.py":  "def g() -> int:\n    return 1\n",
		"app/use.py":   "from lib.impl import g\nfrom lib.types import f\n\n\ndef run() -> int:\n    return f(g())\n",
	})
	branch(t, root, "narrow", "baseline", map[string]string{"lib/types.py": "def f(x: int) -> int:\n    return x\n"})
	branch(t, root, "string", "baseline", map[string]string{"lib/impl.py": "def g() -> str:\n    return \"1\"\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("narrow", "string"))
	if err != nil {
		t.Fatal(err)
	}
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusFailed || len(ev.NewDiagnostics) != 1 || ev.NewDiagnostics[0].Path != "app/use.py" {
		t.Fatalf("an importer in another directory was missed: %+v", ev)
	}
}

func TestMypyChecksImportersOfADeletedTopLevelPackage(t *testing.T) {
	root := pyTypecheckRepo(t, map[string]string{"helpers/__init__.py": "def x() -> int:\n    return 1\n", "app/main.py": "from helpers import x\n\n\ndef run() -> int:\n    return x()\n"})
	gitTest(t, root, "checkout", "-qb", "drop", "baseline")
	gitTest(t, root, "rm", "-rq", "helpers")
	gitTest(t, root, "commit", "-qm", "drop helpers")
	gitTest(t, root, "checkout", "-q", "baseline")
	r, err := Preview(context.Background(), root, typecheckOptions("drop"))
	if err != nil {
		t.Fatal(err)
	}
	if c := findCheck(r, typecheck.CheckID); c == nil || c.Status != model.StatusFailed {
		t.Fatalf("deleting an imported package passed: check=%+v executions=%+v", c, r.TypecheckExecutions)
	}
}

func TestMypyNewErrorIsNotMatchedToAFixedBaseError(t *testing.T) {
	root := pyTypecheckRepo(t, map[string]string{"app/a.py": "def a() -> int:\n    return zz\n"})
	branch(t, root, "fix", "baseline", map[string]string{"app/a.py": "def a() -> int:\n    return 1\n"})
	branch(t, root, "add", "baseline", map[string]string{"app/b.py": "def b() -> int:\n    return zz\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("fix", "add"))
	if err != nil {
		t.Fatal(err)
	}
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusFailed || len(ev.NewDiagnostics) != 1 || ev.NewDiagnostics[0].Path != "app/b.py" {
		t.Fatalf("a new error was matched to a fixed one: %+v", ev)
	}
}

func TestMypyMissingModuleUnderMypyPathIsTheSources(t *testing.T) {
	root := pyTypecheckRepo(t, map[string]string{
		"mypy.ini":                     "[mypy]\nmypy_path = services/api\nexplicit_package_bases = True\n",
		"services/api/svc/__init__.py": "",
		"services/api/svc/helpers.py":  "def h() -> int:\n    return 1\n",
	})
	gitTest(t, root, "checkout", "-qb", "drop", "baseline")
	gitTest(t, root, "rm", "-q", "services/api/svc/helpers.py")
	gitTest(t, root, "commit", "-qm", "drop helpers")
	gitTest(t, root, "checkout", "-q", "baseline")
	branch(t, root, "use", "baseline", map[string]string{"services/api/svc/use.py": "from svc.helpers import h\n\n\ndef run() -> int:\n    return h()\n"})
	r, err := Preview(context.Background(), root, typecheckOptions("drop", "use"))
	if err != nil {
		t.Fatal(err)
	}
	ev := pythonExecution(r, typecheck.ToolMypy)
	if ev == nil || ev.Status != model.StatusFailed {
		t.Fatalf("a deleted project module under mypy_path was blamed on the environment: %+v", ev)
	}
}
