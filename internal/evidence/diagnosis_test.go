package evidence

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/model"
)

func TestDiagnoseNamesMissingRunnerProgramAndModule(t *testing.T) {
	pytest := []string{"python3", "-m", "pytest", "./test_a.py"}
	for _, c := range []struct {
		name string
		argv []string
		exit int
		out  string
		kind string
		what string
	}{
		{"runner", pytest, 1, "/usr/bin/python3: No module named pytest\n", "runner_missing", "pytest"},
		{"shell 127", []string{"npm", "test"}, 127, "> jest\n\nsh: 1: jest: not found\n", "command_missing", "jest"},
		{"bash 127", []string{"make", "test"}, 127, "bash: cargo: command not found\n", "command_missing", "cargo"},
		{"import", pytest, 2, "E   ModuleNotFoundError: No module named 'click'\n=== 1 error in 0.10s ===\n", "module_missing", "click"},
		{"node", []string{"node", "--test"}, 1, "Error: Cannot find module 'zod'\n", "module_missing", "zod"},
	} {
		d := diagnose(c.argv, c.exit, []byte(c.out))
		if d == nil || d.Kind != c.kind || d.Name != c.what || !strings.Contains(d.Message, c.what) {
			t.Fatalf("%s: %+v", c.name, d)
		}
	}
	for _, c := range []struct {
		name string
		argv []string
		exit int
		out  string
	}{
		// Test output that says "not found" is not a missing program.
		{"not found without 127", []string{"npm", "test"}, 1, "Error: not found\n"},
		// A different module than the runner is not a missing runner.
		{"other module", pytest, 1, "/usr/bin/python3: No module named pytest_cov\n"},
		// Absolute or relative paths would put local paths into evidence.
		{"absolute module", []string{"node", "--test"}, 1, "Error: Cannot find module '/tmp/radar-x/a.js'\n"},
		{"relative module", []string{"node", "--test"}, 1, "Error: Cannot find module './a'\n"},
		{"nothing known", pytest, 1, "Segmentation fault\n"},
	} {
		if d := diagnose(c.argv, c.exit, []byte(c.out)); d != nil {
			t.Fatalf("%s: unexpected %+v", c.name, d)
		}
	}
	if (*Diagnosis)(nil).Environment() || (&Diagnosis{Kind: "module_missing"}).Environment() || !(&Diagnosis{Kind: "runner_missing"}).Environment() {
		t.Fatal("only a missing runner or program is about the environment")
	}
}

func TestObservedFailureCarriesNoDiagnosis(t *testing.T) {
	r := CandidateObservation{Status: model.StatusFailed, TestsRun: 1, TestsFailed: 1}
	argv := []string{"python3", "-m", "pytest"}
	decorateCandidateObservation(&r, argv, []byte("E   ModuleNotFoundError: No module named 'yaml'\n== 1 failed, 1 error in 0.2s ==\n"), t.TempDir(), t.TempDir(), ".")
	if r.Diagnosis != nil {
		t.Fatalf("observed failure diagnosed: %+v", r.Diagnosis)
	}
}

func TestObserveCandidateDiagnosesMissingPythonRunner(t *testing.T) {
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip(e)
	}
	r, e := ObserveCandidate(context.Background(), t.TempDir(), []string{"python3", "-m", "radar_missing_runner_xyz"}, 30*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if r.Status != model.StatusError || r.Diagnosis == nil || r.Diagnosis.Kind != "runner_missing" || r.Diagnosis.Name != "radar_missing_runner_xyz" {
		t.Fatalf("%+v %+v", r, r.Diagnosis)
	}
}
