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

func TestGateKeepsLeadsForFailedTestsAndAmbiguousImports(t *testing.T) {
	for _, g := range []gateReport{
		gateWithExecution(model.StatusFailed, nil),
		// A module that cannot be imported may be one a branch removed.
		gateWithExecution(model.StatusError, &evidence.Diagnosis{Kind: "module_missing", Name: "backend", Message: "Python could not import backend"}),
	} {
		if len(g.Attribution) != 1 {
			t.Fatalf("lead dropped: %+v", g.Report.Executions[0].Observation)
		}
	}
	if got := gateHeadline(gateWithExecution(model.StatusError, nil), true); got != "could not complete: tests (see Problems)" {
		t.Fatal(got)
	}
}
