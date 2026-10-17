package evidence

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
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
	site, err := exec.Command(pythonVenvInterpreter(venv), "-c", "import site; print(site.getsitepackages()[0])").Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strings.TrimSpace(string(site)), "editable.pth"), []byte(external+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pythonVenvAlias(t, venv)
	t.Setenv("PATH", filepath.Dir(pythonVenvInterpreter(venv))+string(os.PathListSeparator)+os.Getenv("PATH"))
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
	profile := runners.Profile{Root: ".", Artifacts: []string{".venv"}, Lockfiles: []string{"uv.lock"}, Prefix: []string{filepath.ToSlash(pythonVenvInterpreter(".venv"))}, Setup: [][]string{{"python3", "-m", "venv", "--without-pip", ".venv"}, {"python3", "-c", `import os, glob; paths=glob.glob('.venv/lib/python*/site-packages')+glob.glob('.venv/Lib/site-packages'); open(os.path.join(paths[0], 'editable.pth'),'w').write(os.path.abspath('libs/shared/src')+'\n')`}}}
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
	names, err := firstPartyPythonContext(context.Background(), root)
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
	names, err := firstPartyPythonContext(context.Background(), root)
	if err != nil || strings.Join(names, ",") != "app,app.module,member" {
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
	site, err := exec.Command(pythonVenvInterpreter(venv), "-c", "import site; print(site.getsitepackages()[0])").Output()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(strings.TrimSpace(string(site)), "editable.pth"), []byte(external+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pythonVenvAlias(t, venv)
	t.Setenv("PATH", filepath.Dir(pythonVenvInterpreter(venv))+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err = ObserveCandidate(context.Background(), root, argv, time.Minute)
	if err != nil || got.Status != model.StatusError || got.TestsRun != 0 || got.Diagnosis == nil || got.Diagnosis.Name != "app" || !got.Diagnosis.Environment() {
		t.Fatalf("external editable flat namespace accepted: %+v %v", got, err)
	}
}

func TestPythonConsoleAndVersionedLaunchers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shebang launchers are not native Windows executables")
	}
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
	interpreter := pythonVenvInterpreter(venv)
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
	pythonVenvAlias(t, venv)
	t.Setenv("PATH", filepath.Dir(pythonVenvInterpreter(venv))+string(os.PathListSeparator)+os.Getenv("PATH"))
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
	names, e := firstPartyPythonContext(context.Background(), root)
	if e != nil || strings.Join(names, ",") != "build,docs,scripts,src,tests" {
		t.Fatalf("real packages excluded: %v %v", names, e)
	}
}

// Each source loads the workspace member by name. Scope must either probe it or
// refuse; returning no first-party names would accept external evidence.
// pytest plugins, configuration, ancestor conftest files and string loaders can
// import any first-party package, so every one is probed regardless of what the
// command's sources appear to import. Static references only explain a refusal.
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
	// The runner puts test packages on sys.path itself, so their startup
	// resolution is not probed by name.
	in, e := pythonProbeNames(context.Background(), root, root, []string{"pytest", "test_selected.py"})
	if e != nil || strings.Join(in.Command, ",") != "src" || len(in.Other) != 0 {
		t.Fatalf("probe names: %+v %v", in, e)
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

// pythonTestVenv creates a venv without pip and puts it first on PATH. It
// returns the interpreter and its site-packages directory.
func pythonTestVenv(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	venv := t.TempDir()
	if out, err := exec.Command("python3", "-m", "venv", "--without-pip", venv).CombinedOutput(); err != nil {
		t.Fatalf("venv: %s %v", out, err)
	}
	interpreter := pythonVenvInterpreter(venv)
	site, err := exec.Command(interpreter, "-c", "import site; print(site.getsitepackages()[0])").Output()
	if err != nil {
		t.Fatal(err)
	}
	pythonVenvAlias(t, venv)
	t.Setenv("PATH", filepath.Dir(pythonVenvInterpreter(venv))+string(os.PathListSeparator)+os.Getenv("PATH"))
	return interpreter, strings.TrimSpace(string(site))
}

func pythonTestPut(t *testing.T, base, name, data string) {
	t.Helper()
	p := filepath.Join(base, name)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

// A merge that deletes or renames a package leaves no candidate name to probe.
// An editable install of the original checkout still provides it, so the
// location itself must be refused.
func TestPythonDeletedPackageFromOriginalCheckoutIsRefused(t *testing.T) {
	_, site := pythonTestVenv(t)
	root, original := t.TempDir(), t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	pythonTestPut(t, root, "kept/__init__.py", "")
	pythonTestPut(t, root, "test_removed.py", "import unittest\nimport removed\nclass T(unittest.TestCase):\n def test_removed(self): self.assertTrue(removed.VALUE)\n")
	pythonTestPut(t, original, "kept/__init__.py", "")
	pythonTestPut(t, original, "removed/__init__.py", "VALUE = True\n")
	pth := filepath.Join(site, "original.pth")
	if err := os.WriteFile(pth, []byte(original+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	argv := []string{"python3", "-m", "unittest", "test_removed"}
	got, err := ObserveCandidate(context.Background(), root, argv, time.Minute)
	if err != nil || got.Status != model.StatusError || got.TestsRun != 0 || got.Diagnosis == nil || got.Diagnosis.Name != "sys.path" || !got.Diagnosis.Environment() {
		t.Fatalf("deleted package loaded from the original checkout: %+v %v", got, err)
	}
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d == nil || d.Name != "sys.path" {
		t.Fatalf("pytest: %+v", d)
	}
	// Isolated mode omits the working directory, so kept itself resolves to
	// the original checkout.
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-I", "-m", "pytest"}, environment(t.TempDir(), nil)); d == nil || d.Name != "kept" {
		t.Fatalf("isolated: %+v", d)
	}
	// Paths inside the candidate and paths that do not exist cannot load code
	// from elsewhere.
	if err := os.WriteFile(pth, []byte(root+"\n"+filepath.Join(original, "missing")+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("candidate path refused: %+v", d)
	}
}

func TestPythonImportHooksAreChecked(t *testing.T) {
	_, site := pythonTestVenv(t)
	root, original := t.TempDir(), t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	pythonTestPut(t, original, "removed/__init__.py", "")
	finder := func(target string) string {
		return "import sys\nMAPPING = {'removed': " + strconvQuote(target) + "}\nNAMESPACES = {}\nclass _EditableFinder:\n @classmethod\n def find_spec(cls, fullname, path=None, target=None): return None\ndef install():\n sys.meta_path.append(_EditableFinder)\n"
	}
	pythonTestPut(t, site, "__editable___removed_0_1_finder.py", finder(filepath.Join(original, "removed")))
	pythonTestPut(t, site, "__editable__.removed-0.1.pth", "import __editable___removed_0_1_finder; __editable___removed_0_1_finder.install()\n")
	argv := []string{"python3", "-m", "pytest"}
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d == nil || d.Name != "sys.meta_path" {
		t.Fatalf("editable mapping outside the candidate accepted: %+v", d)
	}
	pythonTestPut(t, site, "__editable___removed_0_1_finder.py", finder(filepath.Join(root, "app")))
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("candidate editable mapping refused: %+v", d)
	}
	// An unknown hook can redirect any name; its targets cannot be inspected.
	pythonTestPut(t, site, "custom_hook.py", "import sys\nclass Finder:\n def find_spec(self, fullname, path=None, target=None): return None\nsys.meta_path.insert(0, Finder())\n")
	pythonTestPut(t, site, "custom_hook.pth", "import custom_hook\n")
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d == nil || d.Name != "sys.meta_path" {
		t.Fatalf("unknown import hook accepted: %+v", d)
	}
}

func strconvQuote(s string) string {
	return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", `\'`) + "'"
}

func TestPythonLocalDistributionInstallsAreChecked(t *testing.T) {
	_, site := pythonTestVenv(t)
	root, original := t.TempDir(), t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	// A non-editable install copies the original checkout into site-packages.
	pythonTestPut(t, site, "removed/__init__.py", "")
	pythonTestPut(t, site, "removed-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: removed\nVersion: 1.0\n")
	direct := filepath.Join(site, "removed-1.0.dist-info/direct_url.json")
	argv := []string{"python3", "-m", "pytest"}
	check := func(url string) *Diagnosis {
		t.Helper()
		if err := os.WriteFile(direct, []byte(`{"url": "`+url+`", "dir_info": {}}`), 0600); err != nil {
			t.Fatal(err)
		}
		return pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil))
	}
	if d := check(pythonFileURL(original)); d == nil || d.Name != "removed" || !strings.Contains(d.Message, "distribution removed") {
		t.Fatalf("local install of the original checkout accepted: %+v", d)
	}
	if d := check("file://remote-host/share/removed"); d == nil || d.Name != "removed" {
		t.Fatalf("remote file URL accepted: %+v", d)
	}
	if d := check(pythonFileURL(root)); d != nil {
		t.Fatalf("candidate install refused: %+v", d)
	}
	if d := check("https://example.com/removed-1.0.tar.gz"); d != nil {
		t.Fatalf("remote archive refused: %+v", d)
	}
	if err := os.Remove(direct); err != nil {
		t.Fatal(err)
	}
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("index install refused: %+v", d)
	}
}

func TestPythonModulesLoadedAtStartupAreChecked(t *testing.T) {
	_, site := pythonTestVenv(t)
	root, original := t.TempDir(), t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	pythonTestPut(t, original, "preloaded/__init__.py", "")
	pythonTestPut(t, site, "preload.pth", "import sys; sys.path.insert(0, "+strconvQuote(original)+"); import preloaded; sys.path.pop(0)\n")
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d == nil || d.Name != "preloaded" {
		t.Fatalf("module preloaded from outside accepted: %+v", d)
	}
}

// The probe imports its own modules before the working directory is on
// sys.path, so candidate code cannot print a passing result for it.
func TestPythonCandidateCannotAnswerForTheProbe(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root, external := t.TempDir(), t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	// The working directory is not a package root, so these are not
	// first-party names; they only shadow the probe's own imports.
	for _, name := range []string{"json.py", "sysconfig.py", "zipimport.py"} {
		pythonTestPut(t, root, "work/"+name, "import sys\nprint('{\"checked\": true, \"reason\": \"\", \"outside\": \"\"}')\nsys.exit(0)\n")
	}
	env := append(environment(t.TempDir(), nil), "PYTHONPATH="+external)
	if d := pythonImportCheck(context.Background(), root, filepath.Join(root, "work"), []string{"python3", "-m", "pytest"}, env); d == nil || d.Name != "sys.path" {
		t.Fatalf("candidate module answered for the probe: %+v", d)
	}
}

func TestPythonProbeNamesFollowCommandProjects(t *testing.T) {
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='Root_Project'\n")
	pythonTestPut(t, root, "root_modules/__init__.py", "")
	pythonTestPut(t, root, "libs/a/pyproject.toml", "[project]\nname='a.lib'\n")
	pythonTestPut(t, root, "libs/a/src/a_modules/__init__.py", "")
	pythonTestPut(t, root, "libs/b/pyproject.toml", "[project]\nname = NAME\n")
	pythonTestPut(t, root, "libs/b/src/b_modules/__init__.py", "")
	pythonTestPut(t, root, "libs/c/pyproject.toml", "[tool.poetry]\nname = 'Poetry.C'\n")
	pythonTestPut(t, root, "tests/__init__.py", "")
	pythonTestPut(t, root, "tests/fixtures/sample/pyproject.toml", "[project]\nname='fixture'\n")
	pythonTestPut(t, root, "tests/fixtures/sample/json/__init__.py", "")
	names := func(dir string, argv ...string) pythonProbeInput {
		t.Helper()
		in, err := pythonProbeNames(context.Background(), root, filepath.Join(root, dir), argv)
		if err != nil {
			t.Fatalf("%s %v: %v", dir, argv, err)
		}
		return in
	}
	in := names(".", "pytest")
	if strings.Join(in.Command, ",") != "root_modules" || strings.Join(in.Other, ",") != "a_modules,b_modules,json" || strings.Join(in.Projects, ",") != "a-lib,fixture,poetry-c,root-project" {
		t.Fatalf("root command: %+v", in)
	}
	if strings.Join(in.Unnamed, ",") != "b_modules" {
		t.Fatalf("unnamed project packages: %+v", in)
	}
	if in = names("libs/a/src", "pytest"); strings.Join(in.Command, ",") != "a_modules,root_modules" {
		t.Fatalf("member command: %+v", in)
	}
	if in = names(".", "pytest", "tests/fixtures/sample/test_x.py::test", "libs/b"); strings.Join(in.Command, ",") != "b_modules,root_modules" || strings.Join(in.Other, ",") != "a_modules,json" || len(in.Unnamed) != 0 {
		t.Fatalf("path arguments: %+v", in)
	}
	if in, err := pythonProbeNames(context.Background(), t.TempDir(), t.TempDir(), []string{"pytest"}); err != nil || len(in.Command)+len(in.Other)+len(in.Projects) != 0 {
		t.Fatalf("plain repository: %+v %v", in, err)
	}
}

// Test fixtures and stray installed test packages are not the command's
// first-party code and must not block every command.
func TestPythonFixtureAndTestPackageNamesDoNotBlock(t *testing.T) {
	_, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	pythonTestPut(t, root, "tests/__init__.py", "")
	pythonTestPut(t, root, "tests/fixtures/sample/pyproject.toml", "[project]\nname='fixture-sample'\n")
	pythonTestPut(t, root, "tests/fixtures/sample/json/__init__.py", "")
	pythonTestPut(t, root, "tests/fixtures/sample/packaging/__init__.py", "")
	pythonTestPut(t, site, "tests/__init__.py", "")
	// An index-installed dependency that shares a fixture's package name.
	pythonTestPut(t, site, "packaging/__init__.py", "")
	pythonTestPut(t, site, "packaging-24.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: packaging\nVersion: 24.0\n")
	pythonTestPut(t, site, "packaging-24.0.dist-info/RECORD", "packaging/__init__.py,,\npackaging-24.0.dist-info/METADATA,,\n")
	for dir, argv := range map[string][]string{"tests": {"python3", "-m", "pytest"}, ".": {"python3", "-m", "pytest", "tests"}} {
		if d := pythonImportCheck(context.Background(), root, filepath.Join(root, dir), argv, environment(t.TempDir(), nil)); d != nil {
			t.Fatalf("%s %v: %+v", dir, argv, d)
		}
	}
	// A fixture named after the installed distribution may be its source.
	pythonTestPut(t, root, "tests/fixtures/sample/pyproject.toml", "[project]\nname='packaging'\n")
	if d := pythonImportCheck(context.Background(), root, filepath.Join(root, "tests"), []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d == nil || d.Name != "packaging" {
		t.Fatalf("fixture namesake accepted: %+v", d)
	}
}

// Names travel on stdin; a large workspace must not hit the argument limit.
func TestPythonProbeNamesAreNotPassedAsArguments(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	for i := 0; i < 3000; i++ {
		pythonTestPut(t, root, fmt.Sprintf("module_with_a_long_descriptive_name_number_%04d.py", i), "")
	}
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("large name set: %+v", d)
	}
}

// A monorepo without a uv workspace declaration gives no member list. A stale
// copy of a member package installed from an index must still be refused.
func TestPythonStaleCopyOfUndeclaredMemberIsRefused(t *testing.T) {
	_, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[tool.ruff]\nline-length = 100\n")
	pythonTestPut(t, root, "libs/core/pyproject.toml", "[project]\nname='core'\n")
	pythonTestPut(t, root, "libs/core/src/core/__init__.py", "VALUE = 'candidate'\n")
	pythonTestPut(t, root, "test_core.py", "import unittest\nimport core\nclass T(unittest.TestCase):\n def test_core(self): self.assertEqual(core.VALUE, 'stale')\n")
	pythonTestPut(t, site, "core/__init__.py", "VALUE = 'stale'\n")
	record := filepath.Join(site, "core-1.0.dist-info/RECORD")
	pythonTestPut(t, site, "core-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: core\nVersion: 1.0\n")
	pythonTestPut(t, site, "core-1.0.dist-info/RECORD", "core/__init__.py,,\ncore-1.0.dist-info/METADATA,,\n")
	got, err := ObserveCandidate(context.Background(), root, []string{"python3", "-m", "unittest", "test_core"}, time.Minute)
	if err != nil || got.Status != model.StatusError || got.TestsRun != 0 || got.Diagnosis == nil || got.Diagnosis.Name != "core" {
		t.Fatalf("stale member copy accepted: %+v %v", got, err)
	}
	// A copy that no distribution claims is unknown, not unrelated.
	if err := os.Remove(record); err != nil {
		t.Fatal(err)
	}
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "unittest", "test_core"}, environment(t.TempDir(), nil)); d == nil || d.Name != "core" {
		t.Fatalf("unowned copy accepted: %+v", d)
	}
}

func TestPythonRepositoryWithoutPyprojectIsStillChecked(t *testing.T) {
	_, site := pythonTestVenv(t)
	root, original := t.TempDir(), t.TempDir()
	pythonTestPut(t, root, "setup.py", "from setuptools import setup\nsetup(name='sample')\n")
	pythonTestPut(t, root, "kept/__init__.py", "")
	pythonTestPut(t, original, "removed/__init__.py", "")
	pythonTestPut(t, site, "original.pth", original+"\n")
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d == nil || d.Name != "sys.path" {
		t.Fatalf("setup.py-only repository skipped: %+v", d)
	}
}

func TestPythonSymlinksInSitePackagesAreChecked(t *testing.T) {
	_, site := pythonTestVenv(t)
	root, original := t.TempDir(), t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	pythonTestPut(t, original, "removed/__init__.py", "")
	if err := os.Symlink(filepath.Join(original, "removed"), filepath.Join(site, "removed")); err != nil {
		t.Skip(err)
	}
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d == nil || d.Name != "sys.path" {
		t.Fatalf("symlinked package outside the candidate accepted: %+v", d)
	}
}

func TestPythonSymlinkedConsoleScriptsAndScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shebang launchers are not native Windows executables")
	}
	interpreter, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	pythonTestPut(t, filepath.Dir(interpreter), "pytest", "#!"+interpreter+"\nprint('1 passed in 0.01s')\n")
	if err := os.Chmod(filepath.Join(filepath.Dir(interpreter), "pytest"), 0700); err != nil {
		t.Fatal(err)
	}
	// pipx and `uv tool install` link console scripts into another directory.
	links := t.TempDir()
	if err := os.Symlink(filepath.Join(filepath.Dir(interpreter), "pytest"), filepath.Join(links, "pytest")); err != nil {
		t.Skip(err)
	}
	t.Setenv("PATH", links+string(os.PathListSeparator)+os.Getenv("PATH"))
	if d := pythonImportCheck(context.Background(), root, root, []string{"pytest"}, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("symlinked console script refused: %+v", d)
	}
	// A script puts its own directory, not the CWD, first on sys.path.
	pythonTestPut(t, root, "tools/run.py", "import app\n")
	pythonTestPut(t, site, "app/__init__.py", "")
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "tools/run.py"}, environment(t.TempDir(), nil)); d == nil || d.Name != "app" {
		t.Fatalf("script directory not probed: %+v", d)
	}
}

// setuptools adds a path hook for namespace packages of a flat-layout
// editable install. Mapped inside the candidate, it cannot load from outside.
func TestPythonEditableNamespaceHookInsideCandidate(t *testing.T) {
	_, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	pythonTestPut(t, root, "app/templates/page.html", "")
	finder := "import sys\nMAPPING = {'app': " + strconvQuote(filepath.Join(root, "app")) + "}\nNAMESPACES = {'app.templates': [" + strconvQuote(filepath.Join(root, "app/templates")) + "]}\n" +
		"class _EditableFinder:\n @classmethod\n def find_spec(cls, fullname, path=None, target=None): return None\n" +
		"class _EditableNamespaceFinder:\n @classmethod\n def _path_hook(cls, path):\n  raise ImportError\n" +
		"def install():\n sys.meta_path.append(_EditableFinder)\n sys.path_hooks.append(_EditableNamespaceFinder._path_hook)\n"
	pythonTestPut(t, site, "__editable___sample_0_1_finder.py", finder)
	pythonTestPut(t, site, "__editable__.sample-0.1.pth", "import __editable___sample_0_1_finder; __editable___sample_0_1_finder.install()\n")
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("candidate namespace hook refused: %+v", d)
	}
	pythonTestPut(t, site, "other_hook.py", "import sys\nsys.path_hooks.append(lambda path: (_ for _ in ()).throw(ImportError()))\n")
	pythonTestPut(t, site, "other_hook.pth", "import other_hook\n")
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d == nil || d.Name != "sys.meta_path" {
		t.Fatalf("unknown path hook accepted: %+v", d)
	}
}

// Distributions built elsewhere (conda-build, Homebrew) record a build
// directory that no longer exists; that copy is not the repository.
func TestPythonBuildDirectoryURLsOfUnrelatedDistributions(t *testing.T) {
	_, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	pythonTestPut(t, site, "thirdparty/__init__.py", "")
	pythonTestPut(t, site, "thirdparty-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: thirdparty\nVersion: 1.0\n")
	pythonTestPut(t, site, "thirdparty-1.0.dist-info/direct_url.json", `{"url": "file:///home/conda/feedstock_root/build_artifacts/thirdparty_1/work", "dir_info": {}}`)
	argv := []string{"python3", "-m", "pytest"}
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("missing build directory refused: %+v", d)
	}
	// The same missing path is refused for a distribution of this repository.
	pythonTestPut(t, site, "thirdparty-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: Sample\nVersion: 1.0\n")
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d == nil || d.Name != "Sample" {
		t.Fatalf("installed copy of the repository accepted: %+v", d)
	}
}

// The probe's own standard library imports must not hide candidate packages,
// nor may candidate modules answer for the launcher probe.
func TestPythonProbeImportsDoNotShadowOrHijack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shebang launchers are not native Windows executables")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	pythonTestPut(t, root, "calendar/__init__.py", "")
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "unittest"}, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("candidate package shadowed by the probe's imports: %+v", d)
	}
	work, external, uvbin := filepath.Join(t.TempDir(), "work"), t.TempDir(), t.TempDir()
	pythonTestPut(t, work, "pyproject.toml", "[project]\nname='sample'\n")
	for _, name := range []string{"shutil.py", "re.py", "os.py", "_sysconfigdata__linux_x86_64-linux-gnu.py"} {
		pythonTestPut(t, work, "cwd/"+name, "import sys\nprint('{\"checked\": true, \"reason\": \"\", \"outside\": \"\"}')\nsys.exit(0)\n")
	}
	python, _ := exec.LookPath("python3")
	pythonTestPut(t, uvbin, "bin/pytest", "#!"+python+"\nprint('1 passed in 0.01s')\n")
	pythonTestPut(t, uvbin, "bin/python", "#!/bin/sh\nexec "+python+" \"$@\"\n")
	pythonTestPut(t, uvbin, "uv", "#!/bin/sh\nshift\nPATH='"+filepath.Join(uvbin, "bin")+":'$PATH\nexport PATH\nexec \"$@\"\n")
	for _, name := range []string{"uv", "bin/pytest", "bin/python"} {
		if err := os.Chmod(filepath.Join(uvbin, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", uvbin+string(os.PathListSeparator)+os.Getenv("PATH"))
	env := append(environment(t.TempDir(), nil), "PYTHONPATH="+external)
	for _, argv := range [][]string{{"uv", "run", "pytest"}, {"python3", "-m", "pytest"}} {
		if d := pythonImportCheck(context.Background(), work, filepath.Join(work, "cwd"), argv, env); d == nil || d.Name != "sys.path" {
			t.Fatalf("%v: candidate module answered for the probe: %+v", argv, d)
		}
	}
}

// The probe shares the command's deadline. Running out of time there is a
// timeout, not an import provenance problem.
func TestPythonProbeTimeoutIsReportedAsTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shebang launchers are not native Windows executables")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	bin := t.TempDir()
	pythonTestPut(t, bin, "python3", "#!/bin/sh\nif [ \"$1\" = \"-c\" ]; then sleep 5; fi\nexec "+python+" \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "python3"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := fixture(t, map[string]string{"test_example.py": "import unittest\nclass T(unittest.TestCase):\n def test_ok(self): pass\n"})
	argv := []string{"python3", "-m", "unittest", "test_example"}
	got, err := ObserveCandidate(context.Background(), root, argv, 300*time.Millisecond)
	if err != nil || got.Status != model.StatusTimeout || got.Diagnosis != nil {
		t.Fatalf("observe: %+v %v", got, err)
	}
	c := candidateCheckpoint(t, root)
	p := reviewedPlan(root, c, argv, ".")
	review(&p)
	run, err := RunCandidate(context.Background(), root, c, p, argv, ".", 300*time.Millisecond)
	if err != nil || run.Record.Status != model.StatusTimeout || run.Observation.Diagnosis != nil {
		t.Fatalf("plan run: %+v %v", run.Record, err)
	}
}

// A wrapper hides which interpreter runs, so no location can be checked.
func TestPythonWrappersAreUnverifiedWithoutPackages(t *testing.T) {
	root := t.TempDir()
	pythonTestPut(t, root, "test_plain.py", "")
	for _, argv := range [][]string{{"env", "python3", "-m", "unittest"}, {"poetry", "run", "python", "-m", "unittest"}, {"env", "python3", "-m", "pytest"}, {"uv", "run", "--with", "x", "pytest"}} {
		if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d == nil || d.Kind != "python_import_unverified" {
			t.Fatalf("%v granted provenance: %+v", argv, d)
		}
	}
	for _, argv := range [][]string{{"go", "test", "./..."}, {"npm", "test"}} {
		if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d != nil {
			t.Fatalf("%v: %+v", argv, d)
		}
	}
}

// The payload is UTF-8 whatever the interpreter's stdin encoding.
func TestPythonProbePayloadEncoding(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := filepath.Join(t.TempDir(), "작업 공간")
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	env := append(environment(t.TempDir(), nil), "PYTHONIOENCODING=cp1252")
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, env); d != nil {
		t.Fatalf("non-ASCII candidate root refused: %+v", d)
	}
}

// A sibling whose distribution name cannot be read from its pyproject (or
// that was renamed) is still refused when a stale installed copy hides it.
func TestPythonStaleCopyOfUnnamedSiblingIsRefused(t *testing.T) {
	_, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='app'\n")
	pythonTestPut(t, root, "libs/utils/pyproject.toml", "[build-system]\nrequires = ['setuptools']\n")
	pythonTestPut(t, root, "libs/utils/acme_utils/__init__.py", "VALUE = 'candidate'\n")
	pythonTestPut(t, root, "test_app.py", "import unittest\nimport acme_utils\nclass T(unittest.TestCase):\n def test_stale(self): self.assertEqual(acme_utils.VALUE, 'stale')\n")
	pythonTestPut(t, site, "acme_utils/__init__.py", "VALUE = 'stale'\n")
	pythonTestPut(t, site, "acme_utils-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: acme-utils\nVersion: 1.0\n")
	pythonTestPut(t, site, "acme_utils-1.0.dist-info/RECORD", "acme_utils/__init__.py,,\n")
	got, err := ObserveCandidate(context.Background(), root, []string{"python3", "-m", "unittest", "test_app"}, time.Minute)
	if err != nil || got.Status != model.StatusError || got.Diagnosis == nil || got.Diagnosis.Name != "acme_utils" {
		t.Fatalf("stale sibling copy accepted: %+v %v", got, err)
	}
}

// A venv inside the candidate can still hold an index-installed copy of a
// first-party package. Only a copy built from the candidate itself counts.
func TestPythonCandidateVenvCopiesMustComeFromTheCandidate(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	if out, err := exec.Command("python3", "-m", "venv", "--without-pip", filepath.Join(root, ".venv")).CombinedOutput(); err != nil {
		t.Fatalf("venv: %s %v", out, err)
	}
	interpreter := pythonVenvInterpreter(filepath.Join(root, ".venv"))
	out, err := exec.Command(interpreter, "-c", "import site; print(site.getsitepackages()[0])").Output()
	if err != nil {
		t.Fatal(err)
	}
	site := strings.TrimSpace(string(out))
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='app'\n")
	pythonTestPut(t, root, "libs/utils/pyproject.toml", "[project]\nname='acme-utils'\n")
	pythonTestPut(t, root, "libs/utils/src/acme_utils/__init__.py", "VALUE = 'candidate'\n")
	pythonTestPut(t, site, "acme_utils/__init__.py", "VALUE = 'stale'\n")
	pythonTestPut(t, site, "acme_utils-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: acme-utils\nVersion: 1.0\n")
	pythonTestPut(t, site, "acme_utils-1.0.dist-info/RECORD", "acme_utils/__init__.py,,\n")
	argv := []string{interpreter, "-m", "unittest", "discover", "-s", "libs/utils"}
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d == nil || d.Name != "acme_utils" {
		t.Fatalf("index copy in the candidate venv accepted: %+v", d)
	}
	// A non-editable build of the candidate's own source is candidate code.
	pythonTestPut(t, site, "acme_utils-1.0.dist-info/direct_url.json", `{"url": "`+pythonFileURL(filepath.Join(root, "libs/utils"))+`", "dir_info": {}}`)
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("candidate build refused: %+v", d)
	}
}

// Only a launcher's command position names an interpreter; a Go package or a
// test filter called python is not one.
func TestPythonWrapperDetectionUsesCommandPosition(t *testing.T) {
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='app'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	for _, argv := range [][]string{{"go", "test", "-json", "./internal/python"}, {"cargo", "test", "python"}, {"make", "-C", "bindings/python", "test"}, {"ctest", "-R", "python3"}} {
		if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d != nil {
			t.Fatalf("%v: %+v", argv, d)
		}
	}
	for _, argv := range [][]string{{"timeout", "60", "python3", "-m", "unittest"}, {"coverage", "run", "-m", "pytest"}, {"env", "FOO=1", "python3", "-m", "unittest"}} {
		if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d == nil {
			t.Fatalf("%v granted provenance", argv)
		}
	}
}

// firstPartyPythonContext lists the conventional packages of every project.
func firstPartyPythonContext(ctx context.Context, root string) ([]string, error) {
	projects, err := pythonProjectsContext(ctx, root)
	names := map[string]bool{}
	for _, project := range projects {
		if err == nil {
			err = pythonProjectPackages(project, names)
		}
	}
	result := make([]string, 0, len(names))
	for name := range names {
		result = append(result, name)
	}
	sort.Strings(result)
	return result, err
}

// A PEP 420 namespace shared with registry distributions (acme-*, google-*)
// is checked member by member: other distributions may install into it, but
// a first-party member must still come from the candidate.
func TestPythonNamespaceSharedWithRegistryDistributions(t *testing.T) {
	_, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='acme-foo'\n")
	pythonTestPut(t, root, "src/acme/foo/__init__.py", "")
	pythonTestPut(t, root, "src/acme/util.py", "")
	pythonTestPut(t, site, "acme/bar/__init__.py", "")
	pythonTestPut(t, site, "acme_bar-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: acme-bar\nVersion: 1.0\n")
	pythonTestPut(t, site, "acme_bar-1.0.dist-info/RECORD", "acme/bar/__init__.py,,\n")
	argv := []string{"python3", "-m", "pytest"}
	check := func() *Diagnosis {
		t.Helper()
		return pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil))
	}
	// An editable path into the candidate.
	pythonTestPut(t, site, "candidate.pth", filepath.Join(root, "src")+"\n")
	if d := check(); d != nil {
		t.Fatalf("editable namespace member refused: %+v", d)
	}
	// A stale copy of a first-party member in the shared namespace.
	pythonTestPut(t, site, "acme/foo/__init__.py", "")
	pythonTestPut(t, site, "acme_foo_old-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: acme-foo-old\nVersion: 1.0\n")
	pythonTestPut(t, site, "acme_foo_old-1.0.dist-info/RECORD", "acme/foo/__init__.py,,\n")
	if d := check(); d == nil || d.Name != "acme.foo" || !strings.Contains(d.Message, "package acme.foo") {
		t.Fatalf("stale namespace member accepted: %+v", d)
	}
	// A non-editable build of the candidate into the shared namespace.
	for _, name := range []string{"candidate.pth", "acme_foo_old-1.0.dist-info"} {
		if err := os.RemoveAll(filepath.Join(site, name)); err != nil {
			t.Fatal(err)
		}
	}
	pythonTestPut(t, site, "acme/util.py", "")
	pythonTestPut(t, site, "acme_foo-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: acme-foo\nVersion: 1.0\n")
	pythonTestPut(t, site, "acme_foo-1.0.dist-info/RECORD", "acme/foo/__init__.py,,\nacme/util.py,,\n")
	pythonTestPut(t, site, "acme_foo-1.0.dist-info/direct_url.json", `{"url": "`+pythonFileURL(root)+`", "dir_info": {}}`)
	if d := check(); d != nil {
		t.Fatalf("candidate build into a shared namespace refused: %+v", d)
	}
}

// A package of another project (an example, fixture or vendored tree) can share
// a top-level name with an installed dependency. Only a copy that may be the
// project's own distribution is refused.
func TestPythonOtherProjectNamesSharedWithDependencies(t *testing.T) {
	_, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='app'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	pythonTestPut(t, root, "examples/demo/pyproject.toml", "[project]\nname='pygments-demo'\n")
	pythonTestPut(t, root, "examples/demo/pygments.py", "")
	pythonTestPut(t, site, "pygments/__init__.py", "")
	pythonTestPut(t, site, "Pygments-2.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: Pygments\nVersion: 2.0\n")
	pythonTestPut(t, site, "Pygments-2.0.dist-info/RECORD", "pygments/__init__.py,,\n")
	argv := []string{"python3", "-m", "pytest"}
	check := func() *Diagnosis {
		t.Helper()
		return pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil))
	}
	if d := check(); d != nil {
		t.Fatalf("dependency sharing an example's module name refused: %+v", d)
	}
	// Without a readable name the copy may be the project's own.
	pythonTestPut(t, root, "examples/demo/pyproject.toml", "[build-system]\nrequires = ['hatchling']\n")
	if d := check(); d == nil || d.Name != "pygments" {
		t.Fatalf("unnamed project accepted: %+v", d)
	}
	// A vendored fork is the installed distribution's namesake.
	pythonTestPut(t, root, "examples/demo/pyproject.toml", "[project]\nname='Pygments'\n")
	if d := check(); d == nil || d.Name != "pygments" {
		t.Fatalf("vendored fork accepted: %+v", d)
	}
}

// Packages outside conventional layouts (maturin python-source, lib/) are not
// found by name. An installed distribution of a repository project must still
// come from the candidate.
func TestPythonInstalledCopyOfRepositoryDistribution(t *testing.T) {
	_, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='My.Lib'\n[tool.maturin]\npython-source = 'python'\n")
	pythonTestPut(t, root, "python/my_lib/__init__.py", "VALUE = 'candidate'\n")
	pythonTestPut(t, site, "my_lib/__init__.py", "VALUE = 'stale'\n")
	pythonTestPut(t, site, "my_lib-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: my-lib\nVersion: 1.0\n")
	pythonTestPut(t, site, "my_lib-1.0.dist-info/RECORD", "my_lib/__init__.py,,\n")
	direct := filepath.Join(site, "my_lib-1.0.dist-info/direct_url.json")
	argv := []string{"python3", "-m", "pytest"}
	check := func() *Diagnosis {
		t.Helper()
		return pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil))
	}
	if d := check(); d == nil || d.Name != "my-lib" || !strings.Contains(d.Message, "distribution my-lib") {
		t.Fatalf("index copy of the repository accepted: %+v", d)
	}
	pythonTestPut(t, site, "my_lib-1.0.dist-info/direct_url.json", `{"url": "https://example.com/my_lib-1.0.tar.gz", "archive_info": {}}`)
	if d := check(); d == nil || d.Name != "my-lib" {
		t.Fatalf("remote copy of the repository accepted: %+v", d)
	}
	pythonTestPut(t, site, "my_lib-1.0.dist-info/direct_url.json", `{"url": "`+pythonFileURL(root)+`", "dir_info": {}}`)
	if d := check(); d != nil {
		t.Fatalf("candidate build refused: %+v", d)
	}
	// Legacy develop installs keep their metadata in the candidate source tree.
	if err := os.Remove(direct); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(site, "my_lib-1.0.dist-info")); err != nil {
		t.Fatal(err)
	}
	pythonTestPut(t, root, "python/my_lib.egg-info/PKG-INFO", "Metadata-Version: 2.1\nName: my-lib\nVersion: 1.0\n")
	pythonTestPut(t, site, "develop.pth", filepath.Join(root, "python")+"\n")
	if d := check(); d != nil {
		t.Fatalf("develop install metadata in the candidate refused: %+v", d)
	}
}

// Nested namespace members are located without importing their parents.
func TestPythonNestedNamespaceMembers(t *testing.T) {
	_, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='svc'\n")
	pythonTestPut(t, root, "app/routers/users.py", "VALUE = 'candidate'\n")
	pythonTestPut(t, root, "test_users.py", "import unittest\nfrom app.routers import users\nclass T(unittest.TestCase):\n def test_users(self): self.assertEqual(users.VALUE, 'candidate')\n")
	got, err := ObserveCandidate(context.Background(), root, []string{"python3", "-m", "unittest", "test_users"}, time.Minute)
	if err != nil || got.Status != model.StatusPassed || got.TestsRun != 1 || got.Diagnosis != nil {
		t.Fatalf("nested namespace: %+v %v", got, err)
	}
	// A stale copy deep in a shared namespace is still found.
	pythonTestPut(t, site, "app/routers/users.py", "VALUE = 'stale'\n")
	pythonTestPut(t, site, "app_old-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: app-old\nVersion: 1.0\n")
	pythonTestPut(t, site, "app_old-1.0.dist-info/RECORD", "app/routers/users.py,,\n")
	if err := os.Rename(filepath.Join(root, "app"), filepath.Join(root, "src")); err != nil {
		t.Fatal(err)
	}
	pythonTestPut(t, root, "src/app/routers/users.py", "VALUE = 'candidate'\n")
	if err := os.RemoveAll(filepath.Join(root, "src/routers")); err != nil {
		t.Fatal(err)
	}
	pythonTestPut(t, site, "candidate.pth", filepath.Join(root, "src")+"\n")
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "unittest", "test_users"}, environment(t.TempDir(), nil)); d == nil || d.Name != "app.routers.users" {
		t.Fatalf("stale nested member accepted: %+v", d)
	}
}

// Members too deep to list cannot be checked by name.
func TestPythonDeepNamespaceIsUnverified(t *testing.T) {
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='deep'\n")
	pythonTestPut(t, root, strings.Repeat("deep/", 40)+"leaf.py", "")
	if _, err := pythonProbeNames(context.Background(), root, root, []string{"pytest"}); err == nil {
		t.Fatal("deep namespace members silently omitted")
	}
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d == nil || d.Name != "python" {
		t.Fatalf("deep namespace: %+v", d)
	}
}

// pythonCandidateVenv creates a venv inside the candidate and returns its
// interpreter and site-packages directory.
func pythonCandidateVenv(t *testing.T, root string) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	if out, err := exec.Command("python3", "-m", "venv", "--without-pip", filepath.Join(root, ".venv")).CombinedOutput(); err != nil {
		t.Fatalf("venv: %s %v", out, err)
	}
	interpreter := pythonVenvInterpreter(filepath.Join(root, ".venv"))
	out, err := exec.Command(interpreter, "-c", "import site; print(site.getsitepackages()[0])").Output()
	if err != nil {
		t.Fatal(err)
	}
	return interpreter, strings.TrimSpace(string(out))
}

// pip checks out `-e git+…` requirements under the venv's src directory. That
// is inside the candidate directory but is not candidate source.
func TestPythonCheckoutsInsideTheCandidateVenvAreRefused(t *testing.T) {
	root := t.TempDir()
	interpreter, site := pythonCandidateVenv(t, root)
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='app'\n")
	pythonTestPut(t, root, "libs/mylib/pyproject.toml", "[project]\nname='mylib'\n")
	pythonTestPut(t, root, "libs/mylib/mylib/__init__.py", "VALUE = 'candidate'\n")
	checkout := filepath.Join(root, ".venv/src/mylib")
	pythonTestPut(t, checkout, "mylib/__init__.py", "VALUE = 'v1.0-tag'\n")
	pythonTestPut(t, site, "__editable__.mylib-1.0.pth", checkout+"\n")
	argv := []string{interpreter, "-m", "unittest"}
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d == nil || d.Name != "mylib" {
		t.Fatalf("venv checkout accepted: %+v", d)
	}
	// The same checkout recorded only as a distribution.
	pythonTestPut(t, site, "mylib-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: mylib\nVersion: 1.0\n")
	pythonTestPut(t, site, "mylib-1.0.dist-info/direct_url.json", `{"url": "`+pythonFileURL(checkout)+`", "dir_info": {"editable": true}}`)
	if err := os.Remove(filepath.Join(site, "__editable__.mylib-1.0.pth")); err != nil {
		t.Fatal(err)
	}
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d == nil || d.Name != "mylib" {
		t.Fatalf("venv checkout distribution accepted: %+v", d)
	}
}

// A committed wheel or sdist in the candidate may be an old release, and a
// distribution owning the name does not vouch for a different file.
func TestPythonCandidateBuildsMustBeTheLoadedSourceTree(t *testing.T) {
	root := t.TempDir()
	interpreter, site := pythonCandidateVenv(t, root)
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='app'\n")
	pythonTestPut(t, root, "libs/utils/pyproject.toml", "[project]\nname='acme-utils'\n")
	pythonTestPut(t, root, "libs/utils/src/acme_utils/__init__.py", "VALUE = 'candidate'\n")
	pythonTestPut(t, site, "acme_utils/__init__.py", "VALUE = 'old-release'\n")
	pythonTestPut(t, site, "acme_utils-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: acme-utils\nVersion: 1.0\n")
	pythonTestPut(t, site, "acme_utils-1.0.dist-info/RECORD", "acme_utils/__init__.py,,\n")
	argv := []string{interpreter, "-m", "unittest", "discover", "-s", "libs/utils"}
	pythonTestPut(t, site, "acme_utils-1.0.dist-info/direct_url.json", `{"url": "`+pythonFileURL(filepath.Join(root, "wheelhouse/acme_utils-1.0-py3-none-any.whl"))+`", "archive_info": {}}`)
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d == nil || d.Name != "acme_utils" {
		t.Fatalf("committed archive accepted: %+v", d)
	}
	// The candidate build owns a module; an unowned stale package wins.
	pythonTestPut(t, site, "acme_utils-1.0.dist-info/direct_url.json", `{"url": "`+pythonFileURL(filepath.Join(root, "libs/utils"))+`", "dir_info": {}}`)
	pythonTestPut(t, site, "acme_utils-1.0.dist-info/RECORD", "acme_utils.py,,\n")
	pythonTestPut(t, site, "acme_utils.py", "VALUE = 'candidate'\n")
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d == nil || d.Name != "acme_utils" {
		t.Fatalf("unowned file accepted on another file's ownership: %+v", d)
	}
	if err := os.RemoveAll(filepath.Join(site, "acme_utils")); err != nil {
		t.Fatal(err)
	}
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("candidate build refused: %+v", d)
	}
}

// A namespace's site-packages portion may hold other distributions' members,
// but not files that nothing installed.
func TestPythonUnownedNamespaceMembersAreRefused(t *testing.T) {
	_, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='acme-foo'\n")
	pythonTestPut(t, root, "src/acme/foo/__init__.py", "")
	pythonTestPut(t, site, "candidate.pth", filepath.Join(root, "src")+"\n")
	pythonTestPut(t, site, "acme/bar/__init__.py", "")
	pythonTestPut(t, site, "acme_bar-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: acme-bar\nVersion: 1.0\n")
	pythonTestPut(t, site, "acme_bar-1.0.dist-info/RECORD", "acme/bar/__init__.py,,\n")
	pythonTestPut(t, site, "acme/old.py", "V = 'stale'\n")
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d == nil || d.Name != "acme" {
		t.Fatalf("unowned namespace member accepted: %+v", d)
	}
}

// Unreadable or legacy metadata of an unrelated distribution must not abort
// the probe.
func TestPythonUnusualDistributionMetadata(t *testing.T) {
	_, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='app'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	pythonTestPut(t, root, "examples/demo/pyproject.toml", "[project]\nname='demo'\n")
	pythonTestPut(t, root, "examples/demo/thirdparty.py", "")
	pythonTestPut(t, site, "thirdparty/__init__.py", "")
	pythonTestPut(t, site, "thirdparty-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: thirdparty\nVersion: 1.0\n")
	pythonTestPut(t, site, "thirdparty-1.0.dist-info/RECORD", "thirdparty/__init__.py,,\n")
	pythonTestPut(t, site, "legacy-1.0.egg-info/PKG-INFO", "Metadata-Version: 1.0\nName: legacy\nVersion: 1.0\n")
	pythonTestPut(t, site, "legacy-1.0.egg-info/installed-files.txt", "../legacy/__init__.py\n../../../../bin/legacy\n")
	pythonTestPut(t, site, "broken-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nVersion: 1.0\n")
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("unusual metadata aborted the probe: %+v", d)
	}
	// An unreadable install record cannot establish where the copy came from.
	pythonTestPut(t, site, "corrupt-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: corrupt\nVersion: 1.0\n")
	pythonTestPut(t, site, "corrupt-1.0.dist-info/direct_url.json", "[]")
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d == nil || d.Name != "corrupt" {
		t.Fatalf("unreadable direct_url accepted: %+v", d)
	}
}

// Environments inside the repository are not projects of the repository.
func TestPythonEnvironmentsInsideTheRepositoryAreNotProjects(t *testing.T) {
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='app'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	pythonTestPut(t, root, "env/pyvenv.cfg", "home = /usr/bin\n")
	pythonTestPut(t, root, "env/bin/activate_this.py", "")
	pythonTestPut(t, root, "env/lib/python3.12/site-packages/pandas/pyproject.toml", "[project]\nname='pandas'\n")
	pythonTestPut(t, root, "env/lib/python3.12/site-packages/pandas/__init__.py", "")
	pythonTestPut(t, root, ".conda/conda-meta/history", "")
	pythonTestPut(t, root, ".conda/lib/python3.12/site-packages/numpy/pyproject.toml", "[project]\nname='numpy'\n")
	in, err := pythonProbeNames(context.Background(), root, root, []string{"pytest"})
	if err != nil || strings.Join(in.Command, ",") != "app" || len(in.Other) != 0 || strings.Join(in.Projects, ",") != "app" {
		t.Fatalf("environment treated as a project: %+v %v", in, err)
	}
}

// An environment marker in a project directory does not hide the project.
func TestPythonEnvironmentMarkersDoNotHideProjects(t *testing.T) {
	_, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='acme-app'\n")
	pythonTestPut(t, root, "pyvenv.cfg", "home = /usr/bin\n")
	pythonTestPut(t, root, "src/acme_app/__init__.py", "VALUE = 'candidate'\n")
	pythonTestPut(t, root, "libs/acme/pyproject.toml", "[project]\nname='acme-lib'\n")
	pythonTestPut(t, root, "libs/acme/conda-meta/history", "")
	pythonTestPut(t, root, "libs/acme/src/acme_lib/__init__.py", "")
	pythonTestPut(t, site, "acme_app/__init__.py", "VALUE = 'stale'\n")
	pythonTestPut(t, site, "acme_app-0.9.dist-info/METADATA", "Metadata-Version: 2.1\nName: acme-app\nVersion: 0.9\n")
	pythonTestPut(t, site, "acme_app-0.9.dist-info/RECORD", "acme_app/__init__.py,,\n")
	for _, dir := range []string{".", "libs/acme"} {
		if d := pythonImportCheck(context.Background(), root, filepath.Join(root, dir), []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d == nil || d.Name != "acme_app" && d.Name != "acme-app" {
			t.Fatalf("%s: marked project hidden: %+v", dir, d)
		}
	}
	in, err := pythonProbeNames(context.Background(), root, filepath.Join(root, "libs/acme"), []string{"pytest"})
	if err != nil || strings.Join(in.Command, ",") != "acme_app,acme_lib" || strings.Join(in.Projects, ",") != "acme-app,acme-lib" {
		t.Fatalf("names: %+v %v", in, err)
	}
}

// A venv whose base interpreter is installed inside the candidate directory
// still has a standard library that is not candidate source. This needs a
// relocatable CPython (uv's managed installs) linkable into the test tree.
func TestPythonBaseInterpreterInsideTheCandidate(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	installs, _ := filepath.Glob(filepath.Join(home, ".local/share/uv/python/cpython-3.*-linux-*"))
	if len(installs) == 0 {
		t.Skip("no relocatable CPython")
	}
	root := t.TempDir()
	base := filepath.Join(root, ".python")
	err = filepath.WalkDir(installs[0], func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(installs[0], p)
		target := filepath.Join(base, rel)
		switch {
		case d.Type()&os.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case d.IsDir():
			return os.MkdirAll(target, 0700)
		}
		return os.Link(p, target)
	})
	if err != nil {
		t.Skip(err)
	}
	if out, err := exec.Command(filepath.Join(base, "bin/python3"), "-m", "venv", "--without-pip", filepath.Join(root, ".venv")).CombinedOutput(); err != nil {
		t.Skipf("venv: %s %v", out, err)
	}
	interpreter := pythonVenvInterpreter(filepath.Join(root, ".venv"))
	out, err := exec.Command(interpreter, "-c", "import site; print(site.getsitepackages()[0])").Output()
	if err != nil {
		t.Fatal(err)
	}
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	pythonTestPut(t, root, "src/calendar/__init__.py", "")
	pythonTestPut(t, strings.TrimSpace(string(out)), "candidate.pth", filepath.Join(root, "src")+"\n")
	if d := pythonImportCheck(context.Background(), root, root, []string{interpreter, "-m", "pytest"}, environment(t.TempDir(), nil)); d == nil || d.Name != "calendar" {
		t.Fatalf("standard library inside the candidate treated as candidate source: %+v", d)
	}
}

// Sourceless bytecode is importable; an unowned .pyc in a namespace portion is
// an unowned member.
func TestPythonUnownedBytecodeNamespaceMember(t *testing.T) {
	_, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='acme-foo'\n")
	pythonTestPut(t, root, "src/acme/foo/__init__.py", "")
	pythonTestPut(t, site, "candidate.pth", filepath.Join(root, "src")+"\n")
	pythonTestPut(t, site, "acme/bar/__init__.py", "")
	pythonTestPut(t, site, "acme_bar-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: acme-bar\nVersion: 1.0\n")
	pythonTestPut(t, site, "acme_bar-1.0.dist-info/RECORD", "acme/bar/__init__.py,,\n")
	argv := []string{"python3", "-m", "pytest"}
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("baseline: %+v", d)
	}
	if out, err := exec.Command("python3", "-c", "import py_compile, sys; py_compile.compile(sys.argv[1], cfile=sys.argv[2])", filepath.Join(root, "src/acme/foo/__init__.py"), filepath.Join(site, "acme/old.pyc")).CombinedOutput(); err != nil {
		t.Fatalf("%s %v", out, err)
	}
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d == nil || d.Name != "acme" {
		t.Fatalf("unowned bytecode accepted: %+v", d)
	}
}

// One unrelated distribution's unreadable top_level.txt does not abort the probe.
func TestPythonUndecodableTopLevelOfUnrelatedDistribution(t *testing.T) {
	_, site := pythonTestVenv(t)
	root := t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='app'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	pythonTestPut(t, root, "examples/demo/pyproject.toml", "[project]\nname='demo'\n")
	pythonTestPut(t, root, "examples/demo/thirdparty.py", "")
	pythonTestPut(t, site, "thirdparty/__init__.py", "")
	pythonTestPut(t, site, "thirdparty-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: thirdparty\nVersion: 1.0\n")
	pythonTestPut(t, site, "thirdparty-1.0.dist-info/RECORD", "thirdparty/__init__.py,,\n")
	pythonTestPut(t, site, "odd-1.0.dist-info/METADATA", "Metadata-Version: 2.1\nName: odd\nVersion: 1.0\n")
	pythonTestPut(t, site, "odd-1.0.dist-info/top_level.txt", "odd\xff\n")
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("unreadable top_level.txt aborted the probe: %+v", d)
	}
}

// The distutils and virtualenv shims are trusted only to exist. They are not
// asked for candidate names, which could import candidate code.
func TestPythonShimFindersAreNotAskedForCandidateNames(t *testing.T) {
	_, site := pythonTestVenv(t)
	root, marker := t.TempDir(), filepath.Join(t.TempDir(), "called")
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='app'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	pythonTestPut(t, site, "_distutils_hack/__init__.py", "import sys\nclass DistutilsMetaFinder:\n def find_spec(self, fullname, path, target=None):\n  if fullname == 'app': open("+strconvQuote(marker)+", 'w').close()\n  return None\nsys.meta_path.insert(0, DistutilsMetaFinder())\n")
	pythonTestPut(t, site, "distutils-precedence.pth", "import _distutils_hack\n")
	if d := pythonImportCheck(context.Background(), root, root, []string{"python3", "-m", "pytest"}, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("shim refused: %+v", d)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("shim finder was asked for a candidate name")
	}
}

// hatch dev-mode-exact installs the editables redirector, whose mapping is
// checked like a setuptools editable mapping.
func TestPythonEditablesRedirectorMapping(t *testing.T) {
	_, site := pythonTestVenv(t)
	root, original := t.TempDir(), t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='app'\n")
	pythonTestPut(t, root, "app/__init__.py", "")
	pythonTestPut(t, original, "app/__init__.py", "")
	pythonTestPut(t, site, "editables/__init__.py", "")
	pythonTestPut(t, site, "editables/redirector.py", "import importlib.abc, importlib.util, sys\nclass RedirectingFinder(importlib.abc.MetaPathFinder):\n _redirections = {}\n @classmethod\n def map_module(cls, name, path): cls._redirections[name] = path\n @classmethod\n def find_spec(cls, fullname, path=None, target=None):\n  if fullname in cls._redirections: return importlib.util.spec_from_file_location(fullname, cls._redirections[fullname])\n  return None\n @classmethod\n def install(cls):\n  if cls not in sys.meta_path: sys.meta_path.append(cls)\n")
	mapping := func(target string) {
		pythonTestPut(t, site, "_app.py", "from editables.redirector import RedirectingFinder as F\nF.install()\nF.map_module('app', "+strconvQuote(target)+")\n")
	}
	pythonTestPut(t, site, "_app.pth", "import _app\n")
	argv := []string{"python3", "-m", "pytest"}
	mapping(filepath.Join(root, "app/__init__.py"))
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("candidate redirection refused: %+v", d)
	}
	mapping(filepath.Join(original, "app/__init__.py"))
	if d := pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil)); d == nil || d.Name != "sys.meta_path" {
		t.Fatalf("outside redirection accepted: %+v", d)
	}
}

func pythonVenvInterpreter(root string) string {
	if runtime.GOOS == "windows" {
		return filepath.ToSlash(filepath.Join(root, "Scripts", "python.exe"))
	}
	return filepath.Join(root, "bin", "python")
}

func pythonVenvAlias(t *testing.T, root string) {
	t.Helper()
	if runtime.GOOS != "windows" {
		return
	}
	data, err := os.ReadFile(pythonVenvInterpreter(root))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Scripts", "python3.exe"), data, 0700); err != nil {
		t.Fatal(err)
	}
}

func pythonFileURL(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func TestPythonCandidateRootAliasChecksCanonicalLocations(t *testing.T) {
	interpreter, site := pythonTestVenv(t)
	root, outside := t.TempDir(), t.TempDir()
	alias := filepath.Join(t.TempDir(), "candidate-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	pythonTestPut(t, root, "src/local_app/__init__.py", "")
	pythonTestPut(t, site, "candidate.pth", filepath.Join(root, "src")+"\n")
	argv := []string{interpreter, "-m", "unittest"}
	if d := pythonImportCheck(context.Background(), alias, alias, argv, environment(t.TempDir(), nil)); d != nil {
		t.Fatalf("candidate alias refused: %+v", d)
	}
	pythonTestPut(t, outside, "local_app/__init__.py", "")
	pythonTestPut(t, site, "candidate.pth", outside+"\n")
	if d := pythonImportCheck(context.Background(), alias, alias, argv, environment(t.TempDir(), nil)); d == nil || d.Name != "local_app" && d.Name != "sys.path" {
		t.Fatalf("candidate alias concealed external imports: %+v", d)
	}
}

func TestPythonVenvStandardLibraryExtensionOrigins(t *testing.T) {
	interpreter, site := pythonTestVenv(t)
	root, outside := t.TempDir(), t.TempDir()
	pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
	// Force the extension to load during startup on every platform, including
	// Windows where its trusted location is base_prefix/DLLs/_socket.pyd.
	pythonTestPut(t, site, "socket.pth", "import _socket\n")
	argv := []string{interpreter, "-m", "unittest"}
	check := func() *Diagnosis {
		return pythonImportCheck(context.Background(), root, root, argv, environment(t.TempDir(), nil))
	}
	if d := check(); d != nil {
		t.Fatalf("interpreter standard-library extension refused: %+v", d)
	}
	origin, err := exec.Command(interpreter, "-c", `import importlib
for name in ("_socket", "_ssl", "_ctypes", "_decimal"):
    try:
        module = importlib.import_module(name)
    except ImportError:
        continue
    if getattr(module, "__file__", None):
        print(name)
        print(module.__file__)
        break
`).Output()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimSpace(string(origin)), "\n")
	if len(parts) != 2 {
		t.Fatal("no file-backed interpreter extension available")
	}
	extension, original := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	binary, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, filepath.Base(original)), binary, 0600); err != nil {
		t.Fatal(err)
	}
	pythonTestPut(t, site, "socket.pth", "import sys; sys.path.insert(0, "+strconvQuote(outside)+"); import "+extension+"; sys.path.pop(0)\n")
	if d := check(); d == nil || d.Name != extension {
		t.Fatalf("external extension accepted: %+v", d)
	}
	pythonTestPut(t, outside, "unrelated_external.py", "VALUE = 1\n")
	pythonTestPut(t, site, "socket.pth", "import sys; sys.path.insert(0, "+strconvQuote(outside)+"); import unrelated_external; sys.path.pop(0)\n")
	if d := check(); d == nil || d.Name != "unrelated_external" {
		t.Fatalf("external Python module accepted: %+v", d)
	}
}

func TestPythonVenvDLLAliasCannotTrustExternalCode(t *testing.T) {
	for _, aliasFile := range []bool{false, true} {
		t.Run(fmt.Sprintf("file=%v", aliasFile), func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			interpreter, site := pythonCandidateVenv(t, root)
			pythonTestPut(t, outside, "unrelated_external.py", "VALUE = 1\n")
			target := outside
			if aliasFile {
				target = filepath.Join(outside, "unrelated_external.py")
			}
			if err := os.Symlink(target, filepath.Join(root, ".venv", "DLLs")); err != nil {
				t.Fatal(err)
			}
			pythonTestPut(t, root, "pyproject.toml", "[project]\nname='sample'\n")
			pythonTestPut(t, site, "outside.pth", "import sys; sys.path.insert(0, "+strconvQuote(outside)+"); import unrelated_external; sys.path.pop(0)\n")
			if d := pythonImportCheck(context.Background(), root, root, []string{interpreter, "-m", "unittest"}, environment(t.TempDir(), nil)); d == nil || (d.Name != "unrelated_external" && d.Name != "sys.path") {
				t.Fatalf("venv DLLs alias concealed external code: %+v", d)
			}
		})
	}
}
