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

func TestSuggestedTestsUsesTargetedSelectionWithoutChangingEvidence(t *testing.T) {
	proposal := testselection.Proposal{Commands: []testselection.Command{
		{ID: "fallback", CWD: ".", Command: []string{"python3", "-m", "unittest", "discover"}, EvidenceReasons: []testselection.Reason{{Code: "package_fallback"}}},
		{ID: "direct", CWD: ".", Command: []string{"go", "test", "./svc"}, Framework: "go", ToolAvailable: true, TestFiles: []string{"svc/app_test.go"}, EvidenceReasons: []testselection.Reason{{Code: "dependency_impact"}}},
	}}
	report := gateReport{Report: integration.Report{VerificationProposal: &proposal, Changed: []string{"svc/app.go"}, Gate: gate.Result{Verdict: gate.Blocked}}}
	before, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	renderGate(&output, report, false)
	if !strings.Contains(output.String(), "go test ./svc") || !strings.Contains(output.String(), "1 package fallback available; review with --run --suite balanced") || strings.Contains(output.String(), "python3 -m unittest discover") {
		t.Fatal(output.String())
	}
	after, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("human rendering changed serialized evidence")
	}
}

func TestRunnerPreparationCauseRendered(t *testing.T) {
	preparation := evidence.RunnerPreparation{Status: model.StatusIncomplete, Error: "candidate integrity inspection failed: input limit reached"}
	var output bytes.Buffer
	renderGate(&output, gateReport{Report: integration.Report{RunnerPreparations: []evidence.RunnerPreparation{preparation}, Gate: gate.Result{Verdict: gate.Blocked}}}, true)
	if !strings.Contains(output.String(), "Runner setup: "+preparation.Error) {
		t.Fatal(output.String())
	}
	output.Reset()
	renderWorkspaceGate(&output, workspaceReport{Repos: []workspaceRepo{{ID: "app", Report: &integration.Report{RunnerPreparations: []evidence.RunnerPreparation{preparation}}}}})
	if !strings.Contains(output.String(), "Runner setup: "+preparation.Error) {
		t.Fatal(output.String())
	}
}
