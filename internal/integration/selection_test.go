package integration

import (
	"context"
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
	"testing"
	"time"
)

func TestRecommendedSuiteDetectsCombinedFailure(t *testing.T) {
	root := fixture(t)
	o := Options{Base: "baseline", Branches: []string{"backend", "frontend"}, SuggestTests: true}
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Executions) != 0 || r.VerificationProposal == nil || len(r.VerificationProposal.Commands) != 1 {
		t.Fatalf("read-only proposal: %+v", r)
	}
	o.Verify = true
	o.AllowExecution = true
	o.Suite = "recommended"
	o.Timeout = 10 * time.Second
	o.Policy = &gate.Policy{Version: 1, Require: []string{"textual_merge", "integration_execution"}}
	r, err = Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict != gate.Fail || len(r.Executions) != 1 || len(r.Selected) != 1 {
		t.Fatalf("combined selection did not fail: %+v", r)
	}
	checkStatus(t, r, "integration_execution", model.StatusFailed)
	o.Branches = []string{"backend", "repaired"}
	r, err = Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict != gate.Pass {
		t.Fatalf("repair: %+v", r)
	}
	o.AllowExecution = false
	if _, err = Preview(context.Background(), root, o); err == nil {
		t.Fatal("suite bypassed authorization")
	}
}

func TestRecommendedNestedDirectoryAndEmptySuite(t *testing.T) {
	root := fixture(t)
	put(t, root, "service/pyproject.toml", "[project]\nname='example'\n")
	put(t, root, "service/model.py", "VALUE=1\n")
	put(t, root, "service/test_model.py", "import unittest\nfrom model import VALUE\nclass Model(unittest.TestCase):\n def test_value(self): self.assertEqual(VALUE,1)\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "nested package")
	gitTest(t, root, "branch", "nested-base")
	put(t, root, "service/model.py", "VALUE=1 # source edit\n")
	gitTest(t, root, "commit", "-qam", "nested change")
	o := Options{Base: "nested-base", Branches: []string{"HEAD"}, Verify: true, AllowExecution: true, Suite: "recommended", Timeout: 10 * time.Second}
	r, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	nested := false
	for _, ev := range r.Executions {
		if ev.CWD == "service" && ev.Status == model.StatusPassed {
			nested = true
		}
	}
	if !nested {
		t.Fatalf("nested command not bound/executed: %+v", r)
	}
	o.Base = "HEAD"
	r, err = Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	checkStatus(t, r, "integration_execution", model.StatusUnknown)
	if r.Gate.Verdict == gate.Pass {
		t.Fatal("empty required suite passed")
	}
}
