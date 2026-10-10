package integration

import (
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/model"
)

func TestExecutionRemediationFollowsObservation(t *testing.T) {
	ev := func(s model.Status, d *evidence.Diagnosis, execErr string) ExecutionEvidence {
		return ExecutionEvidence{Status: s, ExecutionError: execErr, Observation: evidence.CandidateObservation{Status: s, Diagnosis: d}}
	}
	for _, c := range []struct {
		name string
		ev   ExecutionEvidence
		want string
		not  string
	}{
		{"failed test", ev(model.StatusFailed, nil, ""), "reconcile producer/consumer", ""},
		{"missing runner", ev(model.StatusError, &evidence.Diagnosis{Kind: "runner_missing", Name: "pytest"}, ""), "Install or activate pytest", "reconcile"},
		{"missing module", ev(model.StatusError, &evidence.Diagnosis{Kind: "module_missing", Name: "click"}, ""), "renamed or removed it", "reconcile"},
		{"timeout", ev(model.StatusTimeout, nil, ""), "--timeout", "reconcile"},
		{"cannot run", ev(model.StatusError, nil, "candidate checkpoint mismatch"), "candidate checkpoint mismatch", "reconcile"},
		{"no result", ev(model.StatusError, nil, ""), "Run it yourself", "reconcile"},
		{"unrecognized", ev(model.StatusUnknown, nil, ""), "supported runner", "reconcile"},
	} {
		got := executionRemediation(c.ev)
		if !strings.Contains(got, c.want) || (c.not != "" && strings.Contains(got, c.not)) {
			t.Fatalf("%s: %s", c.name, got)
		}
	}
}
