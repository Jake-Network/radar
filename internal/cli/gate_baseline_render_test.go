package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/testselection"
)

func TestGateTextSeparatesBaseFailuresFromCandidateFailures(t *testing.T) {
	existing := integration.ExecutionEvidence{Status: model.StatusFailed, Command: []string{"python3", "-m", "unittest", "test_old"}, CWD: ".", Observation: evidence.CandidateObservation{TestsRun: 1, TestsFailed: 1, FailedCases: []string{"test_old.C.test_old"}}, BaseComparison: &integration.BaseComparison{AllExisting: true, ExistingCases: []string{"test_old.C.test_old"}}}
	for _, mixed := range []bool{false, true} {
		t.Run(map[bool]string{false: "only base failures", true: "new failure remains"}[mixed], func(t *testing.T) {
			r := integration.Report{Selection: &testselection.Selection{Mode: "targeted"}, Executions: []integration.ExecutionEvidence{existing}}
			status := model.StatusIncomplete
			if mixed {
				r.Executions = append(r.Executions, integration.ExecutionEvidence{Status: model.StatusFailed, Command: []string{"python3", "-m", "unittest", "test_new"}, Observation: evidence.CandidateObservation{TestsRun: 1, TestsFailed: 1}})
				status = model.StatusFailed
			}
			r.Gate = gate.Evaluate(gate.Policy{Version: 1, Require: []string{"integration_execution"}}, []gate.Check{{ID: "integration_execution", Status: status}})
			var out bytes.Buffer
			renderGate(&out, gateReport{Report: r}, true)
			text := out.String()
			if !strings.Contains(text, "? python3 -m unittest test_old") || !strings.Contains(text, "already fails on base; integration not verified") || strings.Contains(text, "✗ python3 -m unittest test_old") {
				t.Fatal(text)
			}
			if mixed {
				if !strings.Contains(text, "1 of 2 test commands failed on the combined tree") || !strings.Contains(text, "✗ python3 -m unittest test_new") {
					t.Fatal(text)
				}
			} else if !strings.Contains(text, "NOT VERIFIED") || strings.Contains(text, "failed on the combined tree") {
				t.Fatal(text)
			}
			// Keep the raw observed failure in JSON while its integration check
			// and human marker explain the baseline comparison.
			data, err := json.Marshal(r)
			if err != nil || !strings.Contains(string(data), `"status":"failed"`) || r.Executions[0].Status != model.StatusFailed {
				t.Fatal(err, string(data))
			}
		})
	}
}

func TestUnidentifiedFailuresRemainVisibleAfterBaseComparison(t *testing.T) {
	ev := integration.ExecutionEvidence{Status: model.StatusFailed,
		Observation:    evidence.CandidateObservation{Harness: "pytest", TestsFailed: 2, FailedCases: []string{"tests/test_old.py::test_old"}},
		BaseComparison: &integration.BaseComparison{ExistingCases: []string{"tests/test_old.py::test_old"}},
	}
	detail := executionDetail(ev)
	if !strings.Contains(detail, "1 failure lacks a reliable identity; candidate failure retained") || strings.Contains(detail, "0 remaining") {
		t.Fatal(detail)
	}
}
