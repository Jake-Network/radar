package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
)

func TestFullMultilingualSuiteReportsExecutionAndCoverageGaps(t *testing.T) {
	for _, tool := range []string{"node", "python3"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip(tool + " unavailable")
		}
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, "backend/pyproject.toml", "[project]\nname='example'\n")
	put(t, root, "backend/model.py", "VALUE=1\n")
	put(t, root, "backend/test_model.py", "import unittest\nfrom model import VALUE\nclass Model(unittest.TestCase):\n def test_value(self): self.assertEqual(VALUE, 1)\n")
	put(t, root, "frontend/package.json", `{"name":"example","type":"module"}`)
	put(t, root, "frontend/model.js", "export const value = 1;\n")
	put(t, root, "frontend/model.test.js", "import test from 'node:test';\nimport assert from 'node:assert/strict';\nimport { value } from './model.js';\ntest('model value', () => assert.equal(value, 1));\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "multilingual baseline")
	gitTest(t, root, "branch", "baseline")
	put(t, root, "backend/model.py", "VALUE=1 # candidate edit\n")
	gitTest(t, root, "commit", "-qam", "candidate")
	o := Options{Base: "baseline", Branches: []string{"HEAD"}, Verify: true, AllowExecution: true, Suite: "full", Timeout: 10 * time.Second}
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict != gate.Pass || len(r.Executions) != 2 || r.Selection.Inventory != 2 {
		t.Fatalf("multilingual suite did not pass with both frameworks: %+v", r)
	}
	seen := map[string]string{}
	for _, ev := range r.Executions {
		if ev.Observation.TestsRun != 1 || ev.Status != model.StatusPassed {
			t.Fatalf("no recognized test execution: %+v", ev)
		}
		seen[ev.Observation.Harness] = ev.CWD
	}
	if seen["unittest"] != "backend" || seen["node-test"] != "frontend" {
		t.Fatalf("framework roots not preserved: %+v", seen)
	}
	t.Run("missing runner", func(t *testing.T) {
		bin := t.TempDir()
		for _, tool := range []string{"git", "python3"} {
			resolved, err := exec.LookPath(tool)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Symlink(resolved, filepath.Join(bin, tool)); err != nil {
				t.Skip("controlled runner PATH requires symlinks: " + err.Error())
			}
		}
		t.Setenv("PATH", bin)
		r, err := Preview(context.Background(), root, o)
		if err != nil {
			t.Fatal(err)
		}
		if r.Gate.Verdict != gate.Blocked || len(r.Executions) != 1 || len(r.Selection.Omitted) != 1 || r.Selection.Omitted[0].Reason != "environment_unavailable" {
			t.Fatalf("missing required node runner did not block: %+v", r)
		}
	})
	put(t, root, "frontend/unsupported.test.js", "describe('unknown framework', () => {});\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "unsupported discovered suite")
	r, err = Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict != gate.Blocked || len(r.Executions) != 2 || len(r.Selection.Omitted) != 1 || r.Selection.Omitted[0].Reason != "unsupported_configuration" || r.Selection.Omitted[0].TestFiles[0] != "frontend/unsupported.test.js" {
		t.Fatalf("unsupported test silently disappeared: %+v", r)
	}
}

func TestConfirmedExecutionFailureSurvivesLaterCoverageErrors(t *testing.T) {
	for _, later := range []model.Status{model.StatusUnknown, model.StatusIncomplete, model.StatusError, model.StatusTimeout} {
		if got := aggregateExecution(model.StatusFailed, later); got != model.StatusFailed {
			t.Errorf("confirmed failure followed by %s became %s", later, got)
		}
		if got := aggregateExecution(later, model.StatusFailed); got != model.StatusFailed {
			t.Errorf("%s followed by confirmed failure became %s", later, got)
		}
	}
}

func TestConfirmedContractFailureSurvivesConfigurationGap(t *testing.T) {
	r := Report{
		Checks:   []Check{{ID: "declared_contract_configuration", Status: model.StatusError, Evidence: model.Unknown, Explanation: "another declared schema is unreadable"}},
		Findings: []model.Finding{{Contract: "accepted-api", Severity: model.SeverityError, Explanation: "consumed field removed"}},
	}
	p := gate.Policy{Version: 1, Require: []string{"no_breaking_contracts"}}
	applyGate(&r, Options{Policy: &p})
	if r.Gate.Verdict != gate.Fail || len(r.Gate.Required) != 1 || r.Gate.Required[0].Status != model.StatusFailed {
		t.Fatalf("confirmed incompatibility was replaced by configuration gap: %+v", r.Gate)
	}
}

func TestBlockedObservationCannotEstablishExecutionSuccess(t *testing.T) {
	if got := aggregateExecution(model.StatusPassed, model.StatusBlocked); got != model.StatusBlocked {
		t.Fatalf("blocked observation became %s", got)
	}
}
