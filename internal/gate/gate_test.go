package gate

import (
	"github.com/Jake-Network/radar/internal/model"
	"testing"
)

func TestRequiredEvidenceAndUnrelatedCoverage(t *testing.T) {
	p := Policy{Version: 1, Require: []string{"tests"}}
	for _, tc := range []struct {
		status model.Status
		want   Verdict
	}{{model.StatusPassed, Pass}, {model.StatusFailed, Fail}, {model.StatusUnknown, Blocked}, {model.StatusIncomplete, Blocked}, {model.StatusWarning, Blocked}, {model.StatusError, Error}, {model.StatusTimeout, Error}} {
		r := Evaluate(p, []Check{{ID: "tests", Status: tc.status}, {ID: "runtime", Status: model.StatusUnknown}})
		if r.Verdict != tc.want {
			t.Fatal(tc, r)
		}
	}
	if Evaluate(p, nil).Verdict != Blocked {
		t.Fatal("missing required evidence passed")
	}
	p.OnMissing = "fail"
	if Evaluate(p, nil).Verdict != Fail {
		t.Fatal("missing policy ignored")
	}
}
func TestInvalidAndAmbiguousPolicy(t *testing.T) {
	for _, p := range []Policy{{}, {Version: 1, Require: []string{"a", "a"}}, {Version: 1, Require: []string{"a"}, OnMissing: "ignore"}} {
		if Evaluate(p, nil).Verdict != Error {
			t.Fatal(p)
		}
	}
	p := Policy{Version: 1, Require: []string{"a"}}
	if Evaluate(p, []Check{{ID: "a"}, {ID: "a"}}).Verdict != Error {
		t.Fatal("ambiguous evidence accepted")
	}
}

func TestNoBreakingRequiresEstablishedObligations(t *testing.T) {
	if c := NoBreaking(nil, nil); c.Status != model.StatusPassed {
		t.Fatal(c)
	}
	if c := NoBreaking(nil, []string{"binding removed"}); c.Status != model.StatusUnknown || c.Evidence != model.Unknown {
		t.Fatal("absence of findings passed with unverified obligations", c)
	}
	failing := []model.Finding{{Contract: "x", Severity: model.SeverityError, Explanation: "removed"}}
	if c := NoBreaking(failing, []string{"other"}); c.Status != model.StatusFailed {
		t.Fatal("confirmed failure masked by unverified coverage", c)
	}
}
