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
	"github.com/Jake-Network/radar/internal/runners"
)

func TestPythonCandidateImports(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	external := t.TempDir()
	write := func(root, path, data string) {
		t.Helper()
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(external, "shared_modules/__init__.py", "VALUE = 'external'\n")
	venv := t.TempDir()
	if out, err := exec.Command("python3", "-m", "venv", "--without-pip", venv).CombinedOutput(); err != nil {
		t.Fatalf("venv: %s %v", out, err)
	}
	site, err := exec.Command(filepath.Join(venv, "bin/python"), "-c", "import site; print(site.getsitepackages()[0])").Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strings.TrimSpace(string(site)), "editable.pth"), []byte(external+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(venv, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := t.TempDir()
	write(root, "pyproject.toml", "[project]\nname = 'sample'\nversion = '0.0.0'\n")
	write(root, "libs/shared/pyproject.toml", "[project]\nname = 'shared-modules'\nversion = '0.0.0'\n")
	write(root, "libs/shared/src/shared_modules/__init__.py", "VALUE = 'candidate'\n")
	write(root, "test_example.py", "import unittest\nimport shared_modules\nclass Example(unittest.TestCase):\n def test_candidate(self): self.assertEqual(shared_modules.VALUE, 'candidate')\n")
	argv := []string{"python3", "-m", "unittest", "test_example"}
	got, err := ObserveCandidate(context.Background(), root, argv, time.Minute)
	if err != nil || got.Status != model.StatusError || got.TestsRun != 0 || got.Diagnosis == nil || got.Diagnosis.Kind != "python_import_unverified" || !got.Diagnosis.Environment() || len(got.FailedCases) != 0 || len(got.Locations) != 0 {
		t.Fatalf("external: %+v %v", got, err)
	}
	// A declared runner creates its own environment and editable path in the
	// candidate. The rewritten prefix must probe that interpreter too.
	profile := runners.Profile{Root: ".", Artifacts: []string{".venv"}, Lockfiles: []string{"uv.lock"}, Prefix: []string{".venv/bin/python"}, Setup: [][]string{{"python3", "-m", "venv", "--without-pip", ".venv"}, {"python3", "-c", `import os, glob; open(glob.glob('.venv/lib/python*/site-packages')[0]+'/editable.pth','w').write(os.path.abspath('libs/shared/src')+'\n')`}}}
	write(root, "uv.lock", "version = 1\n")
	commitRunnerFixture(t, root)
	prep, err := PrepareRunner(context.Background(), root, "repo", profile, runners.Cache{Dir: t.TempDir(), MaxBytes: 64 << 20}, time.Minute)
	if err != nil || prep.Status != model.StatusPassed {
		t.Fatalf("prepare: %+v %v", prep, err)
	}
	got, err = ObserveCandidate(context.Background(), root, profile.Rewrite(argv), time.Minute)
	if err != nil || got.Status != model.StatusPassed || got.TestsRun != 1 || got.Diagnosis != nil {
		t.Fatalf("candidate: %+v %v", got, err)
	}
}

func TestPythonImportProbePlainRepositoryAndNamespaces(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "test_plain.py"), []byte("import unittest\nclass Plain(unittest.TestCase):\n def test_ok(self): self.assertTrue(True)\n"), 0600)
	got, err := ObserveCandidate(context.Background(), root, []string{"python3", "-m", "unittest", "test_plain"}, time.Minute)
	if err != nil || got.Status != model.StatusPassed {
		t.Fatalf("plain: %+v %v", got, err)
	}
	os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname='ns'\n"), 0600)
	os.MkdirAll(filepath.Join(root, "src/namespace"), 0700)
	os.WriteFile(filepath.Join(root, "src/namespace/member.py"), nil, 0600)
	names, err := firstPartyPython(root)
	if err != nil || !strings.Contains(strings.Join(names, ","), "namespace") {
		t.Fatalf("namespace discovery: %v %v", names, err)
	}
}

func TestPythonImportProbeRejectsExternalNamespacePortions(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	for _, base := range []string{root, external} {
		if err := os.MkdirAll(filepath.Join(base, "src/namespace"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(base, "src/namespace/member.py"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname='ns'\n"), 0600)
	env := append(environment(t.TempDir(), nil), "PYTHONPATH="+filepath.Join(root, "src")+string(os.PathListSeparator)+filepath.Join(external, "src"))
	diagnosis := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "unittest"}, env)
	if diagnosis == nil || diagnosis.Name != "namespace" || !diagnosis.Environment() {
		t.Fatalf("namespace escaped check: %+v", diagnosis)
	}
}

func TestPythonImportProbeFlatNamespaces(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root, external := t.TempDir(), t.TempDir()
	write := func(base, path, data string) {
		t.Helper()
		name := filepath.Join(base, path)
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(root, "pyproject.toml", "[project]\nname='flat'\n")
	write(root, "app/module.py", "VALUE = 'candidate'\n")
	write(external, "app/module.py", "VALUE = 'external'\n")
	// Tooling and workspace containers must not introduce namespace probes.
	for _, directory := range []string{"tests", "docs", "scripts", "build"} {
		write(root, directory+"/helper.py", "")
	}
	write(root, "apps/member/pyproject.toml", "[project]\nname='member'\n")
	write(root, "apps/member/member/__init__.py", "")
	names, err := firstPartyPython(root)
	if err != nil || strings.Join(names, ",") != "app,member" {
		t.Fatalf("flat namespace discovery: %v %v", names, err)
	}
	write(root, "test_flat.py", "import unittest\nfrom app.module import VALUE\nclass Flat(unittest.TestCase):\n def test_candidate(self): self.assertEqual(VALUE, 'candidate')\n")
	argv := []string{"python3", "-m", "unittest", "test_flat"}
	got, err := ObserveCandidate(context.Background(), root, argv, time.Minute)
	if err != nil || got.Status != model.StatusPassed || got.TestsRun != 1 {
		t.Fatalf("candidate-local flat namespace: %+v %v", got, err)
	}
	// An external editable namespace can add search locations even when the
	// candidate contributes its own namespace portion.
	venv := t.TempDir()
	if out, err := exec.Command("python3", "-m", "venv", "--without-pip", venv).CombinedOutput(); err != nil {
		t.Fatalf("venv: %s %v", out, err)
	}
	site, err := exec.Command(filepath.Join(venv, "bin/python"), "-c", "import site; print(site.getsitepackages()[0])").Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strings.TrimSpace(string(site)), "editable.pth"), []byte(external+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(venv, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err = ObserveCandidate(context.Background(), root, argv, time.Minute)
	if err != nil || got.Status != model.StatusError || got.TestsRun != 0 || got.Diagnosis == nil || got.Diagnosis.Name != "app" || !got.Diagnosis.Environment() {
		t.Fatalf("external editable flat namespace accepted: %+v %v", got, err)
	}
}

func TestPythonConsoleAndVersionedLaunchers(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip(err)
	}
	root, external, venv := t.TempDir(), t.TempDir(), t.TempDir()
	put := func(base, name, data string) {
		t.Helper()
		p := filepath.Join(base, name)
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0700); err != nil {
			t.Fatal(err)
		}
	}
	put(root, "pyproject.toml", "[project]\nname='sample'\n")
	put(root, "src/shared_modules/__init__.py", "VALUE = 'candidate'\n")
	put(external, "shared_modules/__init__.py", "VALUE = 'external'\n")
	if out, e := exec.Command(python, "-m", "venv", "--without-pip", venv).CombinedOutput(); e != nil {
		t.Fatalf("%s %v", out, e)
	}
	interpreter := filepath.Join(venv, "bin/python")
	site, e := exec.Command(interpreter, "-c", "import site; print(site.getsitepackages()[0])").Output()
	if e != nil {
		t.Fatal(e)
	}
	pth := filepath.Join(strings.TrimSpace(string(site)), "editable.pth")
	if e = os.WriteFile(pth, []byte(external+"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	put(venv, "bin/pytest", "#!"+interpreter+"\nimport shared_modules\nprint('1 passed in 0.01s')\n")
	if e = os.Symlink(interpreter, filepath.Join(venv, "bin/python3.12")); e != nil {
		t.Skip(e)
	}
	t.Setenv("PATH", filepath.Join(venv, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	env := environment(t.TempDir(), nil)
	for _, argv := range [][]string{{"pytest"}, {"python3.12", "-m", "pytest"}} {
		if d := pythonImportCheck(context.Background(), root, root, argv, env); d == nil || d.Name != "shared_modules" {
			t.Fatalf("external %v: %+v", argv, d)
		}
	}
	// The uv context uses its own PATH, rather than the parent interpreter.
	uvbin := t.TempDir()
	put(uvbin, "uv", "#!/bin/sh\nshift\nPATH='"+filepath.Join(venv, "bin")+":'$PATH\nexport PATH\nexec \"$@\"\n")
	t.Setenv("PATH", uvbin+string(os.PathListSeparator)+os.Getenv("PATH"))
	env = environment(t.TempDir(), nil)
	argv := []string{"uv", "run", "pytest"}
	if d := pythonImportCheck(context.Background(), root, root, argv, env); d == nil || d.Name != "shared_modules" {
		t.Fatalf("uv external: %+v", d)
	}
	if e = os.WriteFile(pth, []byte(filepath.Join(root, "src")+"\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, cmd := range [][]string{{"pytest"}, argv} {
		got, e := ObserveCandidate(context.Background(), root, cmd, time.Minute)
		if e != nil || got.Status != model.StatusPassed || got.TestsRun != 1 {
			t.Fatalf("candidate local %v: %+v %v", cmd, got, e)
		}
	}
	put(venv, "bin/pytest", "#!/bin/sh\nprintf '1 passed in 0.01s\\n'\n")
	if d := pythonImportCheck(context.Background(), root, root, []string{"pytest"}, env); d == nil {
		t.Fatal("unsupported wrapper granted provenance")
	}
}

func TestPythonCommandProjectScopeAndTransitiveMembers(t *testing.T) {
	root := t.TempDir()
	put := func(name, data string) {
		t.Helper()
		p := filepath.Join(root, name)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	put("pyproject.toml", "[tool.uv.workspace]\nmembers=['apps/*','libs/*']\n")
	for _, project := range []string{"apps/selected", "apps/unrelated", "libs/shared", "libs/leaf"} {
		put(project+"/pyproject.toml", "[project]\nname='sample'\n")
	}
	put("apps/selected/app/__init__.py", "import shared_modules\n")
	put("apps/selected/tests/__init__.py", "")
	put("apps/selected/tests/test_selected.py", "from app import example\n")
	put("apps/unrelated/app/__init__.py", "")
	put("apps/unrelated/utils/__init__.py", "")
	put("libs/shared/src/shared_modules/__init__.py", "import leaf_modules\n")
	put("libs/leaf/src/leaf_modules/__init__.py", "")
	if d := pythonImportCheck(context.Background(), root, root, []string{"uv", "run", "pytest"}, os.Environ()); d == nil || d.Kind != "python_import_unverified" {
		t.Fatal("aggregate workspace command claimed empty package scope", d)
	}
	names, e := firstPartyPythonForCommand(root, root, []string{"python3", "-m", "pytest", "apps/selected/tests/test_selected.py"})
	if e != nil || strings.Join(names, ",") != "app,leaf_modules,shared_modules" {
		t.Fatalf("scope %v %v", names, e)
	}
	put("libs/leaf/src/leaf_modules/__init__.py", "import importlib\nimportlib.import_module('hidden')\n")
	if _, e = firstPartyPythonForCommand(root, filepath.Join(root, "apps/selected"), []string{"pytest", "tests/test_selected.py"}); e == nil {
		t.Fatal("dynamic transitive import marked verified")
	}
}

func TestPythonTestsOutsideMemberProject(t *testing.T) {
	root := t.TempDir()
	for name, data := range map[string]string{
		"backend/pyproject.toml":          "[project]\nname='backend'\n",
		"backend/src/service/__init__.py": "VALUE = 1\n",
		"integration/test_http.py":        "import unittest\nclass T(unittest.TestCase):\n def test_http(self): self.assertTrue(True)\n",
	} {
		p := filepath.Join(root, name)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	argv := []string{"python3", "-m", "unittest", "discover", "-s", "integration"}
	if names, e := firstPartyPythonForCommand(root, root, argv); e != nil || len(names) != 0 {
		t.Fatal("unrelated member packages contaminated explicit integration tests", names, e)
	}
	if e := os.WriteFile(filepath.Join(root, "integration/test_http.py"), []byte("import service\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if names, e := firstPartyPythonForCommand(root, root, argv); e != nil || strings.Join(names, ",") != "service" {
		t.Fatal("test import did not establish member scope", names, e)
	}
}

func TestPythonWorkspaceCommandModulesAndCommaImports(t *testing.T) {
	root := t.TempDir()
	put := func(name, data string) {
		t.Helper()
		p := filepath.Join(root, name)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	put("pyproject.toml", "[tool.uv.workspace]\nmembers=['libs/*']\n")
	put("libs/shared/pyproject.toml", "[project]\nname='shared'\n")
	put("libs/shared/src/shared_modules/__init__.py", "")
	for _, argv := range [][]string{{"pytest", "--pyargs", "shared_modules"}, {"python3", "-m", "unittest", "shared_modules.tests.test_example"}} {
		names, e := firstPartyPythonForCommand(root, root, argv)
		if e != nil || strings.Join(names, ",") != "shared_modules" {
			t.Fatalf("module command %v: %v %v", argv, names, e)
		}
	}
	for _, source := range []string{"import os, shared_modules\n", "import os, \\\n shared_modules\n", "import os; import shared_modules\n", "if True: import shared_modules\n"} {
		put("test_root.py", source)
		names, e := firstPartyPythonForCommand(root, root, []string{"pytest", "test_root.py"})
		if e != nil || strings.Join(names, ",") != "shared_modules" {
			t.Fatalf("comma import %q: %v %v", source, names, e)
		}
	}
	put("test_root.py", "from importlib import import_module as load\nload('shared_modules')\n")
	if _, e := firstPartyPythonForCommand(root, root, []string{"pytest", "test_root.py"}); e == nil {
		t.Fatal("dynamic alias import granted provenance")
	}
}

func TestPythonUnknownWrappersCannotBypassWorkspaceProvenance(t *testing.T) {
	root := t.TempDir()
	if e := os.MkdirAll(filepath.Join(root, "libs/member/src/example"), 0700); e != nil {
		t.Fatal(e)
	}
	for name, data := range map[string]string{"pyproject.toml": "[tool.uv.workspace]\nmembers=['libs/*']\n", "libs/member/pyproject.toml": "[project]\nname='example'\n", "libs/member/src/example/__init__.py": ""} {
		if e := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, argv := range [][]string{{"uv", "run", "--python", "3.12", "pytest"}, {"poetry", "run", "pytest"}, {"py", "-m", "pytest"}} {
		if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d == nil || d.Kind != "python_import_unverified" {
			t.Fatalf("unsupported %v granted verification", argv)
		}
	}
}

func TestPythonDeclaredStdlibNameDoesNotBypassProvenance(t *testing.T) {
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip(e)
	}
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname='sample'\n"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.MkdirAll(filepath.Join(root, "src/sys"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "src/sys/__init__.py"), nil, 0600); e != nil {
		t.Fatal(e)
	}
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d == nil || d.Name != "sys" {
		t.Fatalf("declared package silently hidden by stdlib: %+v", d)
	}
}

func TestPythonScopeIgnoresCommentsAndLiteralStrings(t *testing.T) {
	root := t.TempDir()
	put := func(name, data string) {
		t.Helper()
		p := filepath.Join(root, name)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	put("pyproject.toml", "[tool.uv.workspace]\nmembers=['libs/*']\n")
	put("libs/member/pyproject.toml", "[project]\nname='shared'\n")
	put("libs/member/src/shared_modules/__init__.py", "")
	for _, source := range []string{
		"# do not eval, __import__, importlib, exec, or runpy\nVALUE = 'do not eval'\n",
		"'''Module docs\nimport shared_modules\n__import__('hidden')\n'''\nVALUE = 1\n",
		"VALUE = r'''import shared_modules\neval('hidden')\n'''\n",
		"VALUE = \"import shared_modules; eval('hidden')\"\n",
		"VALUE = f'literal without interpolation'\n",
		"VALUE = f'{name}'\n",
		"VALUE = f'{name:eval}'\n",
		"VALUE = f'{name:{width}}'\n",
		"VALUE = f'{\"eval literal\"}'\n",
	} {
		put("test_root.py", source)
		names, e := firstPartyPythonForCommand(root, root, []string{"pytest", "test_root.py"})
		if e != nil || len(names) != 0 {
			t.Fatalf("literal source incorrectly changed scope %q: %v %v", source, names, e)
		}
	}
	for _, source := range []string{"VALUE = f'{eval(\"hidden\")}'\n", "VALUE = '''unterminated\n", "__import__('shared_modules')\n", "VALUE = f'{name:{__import__(\"shared_modules\")}}'\n"} {
		put("test_root.py", source)
		if _, e := firstPartyPythonForCommand(root, root, []string{"pytest", "test_root.py"}); e == nil {
			t.Fatalf("uncertain code silently masked: %q", source)
		}
	}
	put("test_root.py", "'''import nonexistent'''\nimport os, shared_modules # eval only in comment\n")
	names, e := firstPartyPythonForCommand(root, root, []string{"pytest", "test_root.py"})
	if e != nil || strings.Join(names, ",") != "shared_modules" {
		t.Fatalf("real import lost: %v %v", names, e)
	}
}

func TestPythonScopeSyntaxBudgetsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := pythonExecutableSourceContext(ctx, []byte("value = 1\n")); e == nil {
		t.Fatal("cancelled parser proceeded")
	}
	if _, e := firstPartyPythonForCommandContext(ctx, t.TempDir(), t.TempDir(), []string{"pytest"}); e == nil {
		t.Fatal("cancelled scope proceeded")
	}
	nested := "value = " + strings.Repeat("[", 300) + "0" + strings.Repeat("]", 300) + "\n"
	if _, e := pythonExecutableSource([]byte(nested)); e == nil {
		t.Fatal("unbounded AST depth accepted")
	}
	if _, e := pythonExecutableSource([]byte(strings.Repeat("value = 0\n", 40000))); e == nil {
		t.Fatal("unbounded AST node count accepted")
	}
}

func TestPythonDynamicCallsAreSyntaxAndAliasAware(t *testing.T) {
	for _, source := range []string{"model.eval()\nsession.exec('select 1')\n", "import importlib.metadata\nVERSION = importlib.metadata.version('example')\n", "from importlib import metadata as meta\nVERSION = meta.version('example')\n"} {
		root := t.TempDir()
		if e := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname='example'\n"), 0600); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(root, "example.py"), []byte(source), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e := firstPartyPythonForCommand(root, root, []string{"python3", "-m", "pytest", "example.py"}); e != nil {
			t.Fatalf("ordinary calls blocked: %q: %v", source, e)
		}
	}
}

func TestPythonConventionalNamesCanBeRealPackages(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "pyproject.toml"), []byte("[project]\nname='example'\n"), 0600); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"tests", "scripts", "build", "docs", "src"} {
		if e := os.MkdirAll(filepath.Join(root, name), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(root, name, "__init__.py"), nil, 0600); e != nil {
			t.Fatal(e)
		}
	}
	names, e := firstPartyPython(root)
	if e != nil || strings.Join(names, ",") != "build,docs,scripts,src,tests" {
		t.Fatalf("real packages excluded: %v %v", names, e)
	}
}

func TestPythonLoaderAliasesRemainUnverified(t *testing.T) {
	for _, source := range []string{
		"eval('code')\n", "exec('code')\n", "__import__('shared_modules')\n",
		"(eval)('code')\n", "__builtins__.__import__('shared_modules')\n",
		"import importlib as il\ngetattr(il, 'import_module')('shared_modules')\n",
		"import importlib as il\nil.import_module('shared_modules')\n",
		"from importlib import import_module as load\nload('shared_modules')\n",
		"import importlib\nload = importlib.import_module\nsecond = load\nsecond('shared_modules')\n",
		"import builtins as b\nb.eval('code')\n", "from builtins import __import__ as load\nload('shared_modules')\n",
		"import runpy as r\nr.run_module('shared_modules')\n",
		"import importlib as il\nVALUE = f'{il.import_module(\"shared_modules\")}'\n",
	} {
		if _, e := pythonExecutableSource([]byte(source)); e == nil {
			t.Fatalf("loader call granted provenance: %q", source)
		}
	}
}

func TestPythonRealConventionalPackageProvenanceIsScoped(t *testing.T) {
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip(e)
	}
	root, external := t.TempDir(), t.TempDir()
	put := func(base, name, data string) {
		t.Helper()
		p := filepath.Join(base, name)
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(p, []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	put(root, "pyproject.toml", "[project]\nname='sample'\n")
	// A literal src package is an ordinary first-party package, not just a layout.
	put(root, "src/__init__.py", "VALUE = 'candidate'\n")
	put(root, "tests/__init__.py", "VALUE = 'candidate'\n")
	put(root, "test_selected.py", "from src import VALUE\n")
	put(external, "src/__init__.py", "VALUE = 'external'\n")
	env := append(environment(t.TempDir(), nil), "PYTHONPATH="+external)
	// A member CWD makes the externally installed src visible ahead of its
	// parent, matching console-launcher and nested test execution layouts.
	if e := os.MkdirAll(filepath.Join(root, "nested"), 0700); e != nil {
		t.Fatal(e)
	}
	argv := []string{"python3", "-m", "pytest", "../test_selected.py"}
	if d := pythonImportCheck(context.Background(), root, filepath.Join(root, "nested"), argv, env); d == nil || d.Name != "src" {
		t.Fatalf("real src package omitted: %+v", d)
	}
	names, e := firstPartyPythonForCommand(root, root, []string{"pytest", "test_selected.py"})
	if e != nil || strings.Join(names, ",") != "src" {
		t.Fatalf("unrelated tests package included: %v %v", names, e)
	}
	names, e = firstPartyPythonForCommand(root, root, []string{"pytest", "--pyargs", "tests"})
	if e != nil || strings.Join(names, ",") != "src,tests" {
		t.Fatalf("explicit tests package omitted: %v %v", names, e)
	}
}

func TestPythonOrdinaryEvalMethodsExecuteNormally(t *testing.T) {
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip(e)
	}
	root := t.TempDir()
	files := map[string]string{
		"pyproject.toml":   "[project]\nname='sample'\n",
		"application.py":   "import importlib.metadata\nclass Model:\n def eval(self): return 1\nclass Session:\n def exec(self, query): return 2\nmodel = Model()\nsession = Session()\nVALUE = model.eval() + session.exec('select 1')\n",
		"test_selected.py": "import unittest\nfrom application import VALUE\nclass TestApplication(unittest.TestCase):\n def test_value(self): self.assertEqual(VALUE, 3)\n",
	}
	for name, data := range files {
		if e := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	got, e := ObserveCandidate(context.Background(), root, []string{"python3", "-m", "unittest", "test_selected"}, time.Minute)
	if e != nil || got.Status != model.StatusPassed || got.TestsRun != 1 || got.Diagnosis != nil {
		t.Fatalf("ordinary methods no longer runnable: %+v %v", got, e)
	}
}
