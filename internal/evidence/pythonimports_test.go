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
