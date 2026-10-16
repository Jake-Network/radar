package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/testselection"
)

// gateWithExecution is a --run report whose one selected command, testing
// backend.py through test_backend.py, ended with status and diagnosis d.
func gateWithExecution(status model.Status, d *evidence.Diagnosis) gateReport {
	cmd := []string{"python3", "-m", "pytest", "./test_backend.py"}
	f := model.NewFinding("integration_execution_"+string(status), fmt.Sprintf("Combined verification command %q in %q returned %s (exit 1).", cmd, ".", status), model.ObservedTest)
	f.Severity = model.SeverityError
	ev := integration.ExecutionEvidence{Command: cmd, CWD: ".", Status: status, SelectionID: "t1", Observation: evidence.CandidateObservation{Status: status, Diagnosis: d}}
	if d != nil {
		f.Explanation += " Cause: " + d.Message + "."
	}
	f.Remediation = "fix it"
	verdict := gate.Fail
	if status != model.StatusFailed {
		verdict = gate.Error
	}
	g := gateReport{BaseRef: "main", Branches: []gateBranch{{Ref: "agent-backend", Changed: []string{"backend.py"}}}, Attribution: map[string]gateAttribution{}}
	g.Report = integration.Report{
		Findings:   []model.Finding{f},
		Executions: []integration.ExecutionEvidence{ev},
		Selection:  &testselection.Selection{Mode: "balanced", Commands: []testselection.Command{{ID: "t1", Command: cmd, CWD: ".", TestFiles: []string{"test_backend.py"}}}},
		AffectedBy: map[string][]string{"test_backend.py": {"backend.py"}},
		Gate: gate.Result{Verdict: verdict, Policy: gate.Policy{Name: "supported-integration", Require: []string{"integration_execution"}},
			Required: []gate.Check{{ID: "integration_execution", Status: status}}, Explanation: "Selected policy requirements are not satisfied; inspect required_checks."},
	}
	g.attribute()
	return g
}

func TestGateNamesMissingRunnerInsteadOfBlamingBranches(t *testing.T) {
	g := gateWithExecution(model.StatusError, &evidence.Diagnosis{Kind: "runner_missing", Name: "pytest", Message: `pytest is not installed for python3 (it reported "No module named pytest")`})
	if len(g.Attribution) != 0 {
		t.Fatalf("a missing runner must not point at a branch: %+v", g.Attribution)
	}
	if got := gateHeadline(g, true); got != "the tests could not run; not installed: pytest" {
		t.Fatal(got)
	}
	var out bytes.Buffer
	renderGate(&out, g, true)
	text := out.String()
	if !strings.Contains(text, `    cause: pytest is not installed for python3 (it reported "No module named pytest")`+"\n") || strings.Contains(text, "look at") {
		t.Fatal(text)
	}
}

func TestGateKeepsLeadsOnlyForObservedSourceFailures(t *testing.T) {
	g := gateWithExecution(model.StatusFailed, nil)
	if len(g.Attribution) != 1 {
		t.Fatalf("observed failure lead dropped: %+v", g.Report.Executions[0].Observation)
	}
	// A missing module can be environmental; its name alone does not establish
	// an observed source failure, even when that file changed in a branch.
	g = gateWithExecution(model.StatusError, &evidence.Diagnosis{Kind: "module_missing", Name: "backend", Message: "Python could not import backend"})
	if len(g.Attribution) != 0 {
		t.Fatalf("inconclusive import blamed branch: %+v", g.Attribution)
	}
	if got := gateHeadline(gateWithExecution(model.StatusError, nil), true); got != "could not complete: tests (see Problems)" {
		t.Fatal(got)
	}
}

func TestGateExternalPythonImportHasNoLeadOrMissingRunnerClaim(t *testing.T) {
	g := gateWithExecution(model.StatusError, &evidence.Diagnosis{Kind: "python_import_unverified", Name: "backend", Message: "Python resolves first-party package backend outside the private candidate"})
	if len(g.Attribution) != 0 {
		t.Fatalf("external import blamed branch: %+v", g.Attribution)
	}
	var out bytes.Buffer
	renderGate(&out, g, true)
	text := out.String()
	if strings.Contains(text, "look at") || strings.Contains(text, "not installed") || !strings.Contains(text, "outside the private candidate") {
		t.Fatal(text)
	}
}

func TestGateMetadataDeadlineReportsUnstartedVerification(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{
		"backend.py":      "VALUE = 1\n",
		"test_backend.py": "import unittest\nfrom backend import VALUE\nclass Backend(unittest.TestCase):\n def test_backend(self): self.assertEqual(VALUE, 1)\n",
	})
	agent("agent-backend", map[string]string{"backend.py": "VALUE = 2\n"})
	report := invoke(t, root, 2, "gate", "--run", "--timeout", "1ns")
	if report["gate"].(map[string]any)["verdict"] != "error" || report["execution"] != nil || report["executions"] != nil || report["observed_attributions"] != nil {
		t.Fatal("metadata deadline invented execution or passed the gate", report)
	}
	if attribution, ok := report["attribution"].(map[string]any); ok && len(attribution) != 0 {
		t.Fatal("metadata deadline blamed a branch", attribution)
	}
	found := false
	for _, raw := range report["checks"].([]any) {
		check := raw.(map[string]any)
		if check["id"] == "integration_execution" {
			found = true
			if check["status"] != "timeout" || check["evidence"] != "unknown" {
				t.Fatal("unstarted verification claimed observed evidence", check)
			}
		}
	}
	if !found {
		t.Fatal("missing structured timeout check", report)
	}
	code, output, _ := run(t, root, "gate", "--run", "--timeout", "1ns")
	if code != 2 || !strings.Contains(output, "Verification budget expired") || strings.Contains(output, "look at:") || strings.Contains(output, "runner profiles:") {
		t.Fatal("human output lost metadata timeout scope", code, output)
	}
}
