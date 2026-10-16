package evidence

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/model"
)

// Exercise the PATH interpreter directly, including its startup configuration.
// Replacing it with a venv would hide interpreter-installation false positives.
func TestPythonDefaultInterpreterCandidateProvenance(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatal("default python3 unavailable: ", err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pythonTestPut(t, root, "test_default.py", "import unittest\nclass Default(unittest.TestCase):\n def test_candidate(self): self.assertEqual(2 + 2, 4)\n")
	argv := []string{"python3", "-m", "unittest", "test_default"}
	got, err := ObserveCandidate(context.Background(), root, argv, time.Minute)
	if err != nil || got.Status != model.StatusPassed || got.TestsRun != 1 || got.Diagnosis != nil {
		logDefaultPythonProvenance(t, root)
		t.Fatalf("default interpreter did not verify candidate: status=%s tests=%d diagnosis=%+v error=%v", got.Status, got.TestsRun, got.Diagnosis, err)
	}
	// A valid interpreter installation must not authorize an unrelated source
	// path merely because that path was added to the same interpreter.
	external := t.TempDir()
	pythonTestPut(t, external, "unrelated_external.py", "VALUE = 1\n")
	env := append(environment(t.TempDir(), nil), "PYTHONPATH="+external)
	if d := pythonImportCheck(context.Background(), root, root, argv, env); d == nil || d.Kind != "python_import_unverified" || d.Name != "sys.path" {
		t.Fatalf("default interpreter concealed an unrelated source path: %+v", d)
	}
}

func logDefaultPythonProvenance(t *testing.T, root string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "python3", "-c", `import json, os, site, sys, sysconfig
print(json.dumps({"executable": sys.executable, "canonical_executable": os.path.realpath(sys.executable), "version": sys.version, "prefix": sys.prefix, "base_prefix": sys.base_prefix, "paths": sysconfig.get_paths(), "sys_path": sys.path, "site_packages": site.getsitepackages(), "startup_modules": {name: getattr(sys.modules.get(name), "__file__", None) for name in ("sitecustomize", "usercustomize")}}, indent=2))`)
	cmd.Dir = root
	cmd.Env = environment(t.TempDir(), nil)
	out, err := cmd.CombinedOutput()
	if len(out) > 16<<10 {
		out = out[:16<<10]
	}
	t.Logf("default Python in the sanitized candidate environment (diagnostic only, not persisted evidence): %s; error=%v", out, err)
}
