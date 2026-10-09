package verification

import (
	"context"
	"github.com/radar-engine/radar/internal/model"
	"github.com/radar-engine/radar/internal/planning"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContractCriterion(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "schema.json")
	p := planning.Plan{SchemaVersion: "1", FeatureID: "f", Intent: "preserve total", BaseRevision: "baseline", Requirements: []planning.Requirement{{ID: "r", Intent: "preserve total"}}, Acceptance: []planning.Criterion{{ID: "a", Requirement: "r", Intent: "total exists", Rule: &planning.Rule{Kind: "json_property", Path: "schema.json", Pointer: "/properties", Property: "total"}}}}
	s := model.Snapshot{Repository: root, Revision: "WORKTREE"}
	for _, tc := range []struct{ body, status string }{{`{"properties":{"total":{"type":"integer"}}}`, "passed"}, {`{"properties":{"total_cents":{"type":"integer"}}}`, "failed"}, {`{`, "unknown"}} {
		if e := os.WriteFile(path, []byte(tc.body), 0600); e != nil {
			t.Fatal(e)
		}
		r := VerifyWithEvidence(context.Background(), p, s, root, nil)
		if r.Authoritative {
			t.Fatal("working tree authoritative")
		}
		var actual string
		for _, c := range r.Checks {
			if c.ID == "a" {
				actual = string(c.Status)
			}
		}
		if actual != tc.status {
			t.Fatal(r.Checks)
		}
	}
}
func TestUnsafeEvidence(t *testing.T) {
	root := t.TempDir()
	s := model.Snapshot{Revision: "WORKTREE"}
	for _, r := range []planning.Rule{{Kind: "file_exists", Path: "../secret"}, {Kind: "file_exists", Path: "missing"}, {Kind: "graph_entity", Entity: "missing"}} {
		c := evaluate(context.Background(), "x", &r, s, root)
		if c.Status == "passed" {
			t.Fatal("unsupported evidence passed", c)
		}
	}
	s.Diagnostics = []model.Diagnostic{{Message: "parse failure", Severity: "error"}}
	if c := evaluate(context.Background(), "x", &planning.Rule{Kind: "graph_entity", Entity: "missing"}, s, root); c.Status != "unknown" {
		t.Fatal("incomplete index proved absence", c)
	}
}
func TestGraphDrift(t *testing.T) {
	p := planning.Plan{SchemaVersion: "1", Requirements: []planning.Requirement{{ID: "r"}}, GraphDeltas: []planning.Delta{{Operation: "add", Node: &model.Node{ID: "new", Kind: "function", Name: "export"}}}}
	s := model.Snapshot{Revision: "WORKTREE", Nodes: []model.Node{{ID: "new", Kind: "function", Name: "different", Provenance: model.Provenance{Evidence: model.VerifiedStatic}}}}
	r := VerifyWithEvidence(context.Background(), p, s, t.TempDir(), nil)
	if r.Status != "failed" {
		t.Fatal("graph drift missed", r)
	}
	s.Nodes[0].Name = "export"
	r = VerifyWithEvidence(context.Background(), p, s, t.TempDir(), nil)
	if r.Checks[len(r.Checks)-1].Status != "passed" {
		t.Fatal(r)
	}
}

func TestHashedWorkingTree(t *testing.T) {
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "exists"), []byte("ok"), 0600); e != nil {
		t.Fatal(e)
	}
	c := evaluate(context.Background(), "x", &planning.Rule{Kind: "file_exists", Path: "exists"}, model.Snapshot{Revision: "WORKTREE:abc"}, root)
	if c.Status != "passed" {
		t.Fatal(c)
	}
}

func TestIncompleteGraphCannotProveRemoval(t *testing.T) {
	d := planning.Delta{Operation: "remove", Node: &model.Node{ID: "gone", Provenance: model.Provenance{Path: "broken.py"}}}
	s := model.Snapshot{Diagnostics: []model.Diagnostic{{Path: "broken.py", Severity: "warning", Message: "syntax errors"}}}
	if c := evaluateDelta(d, s); c.Status != "unknown" {
		t.Fatal("partial analysis proved removal", c)
	}
	s.Diagnostics[0].Severity = "info"
	if c := evaluateDelta(d, s); c.Status != "passed" {
		t.Fatal("informational notice blocked structural proof", c)
	}
	s.Diagnostics[0].Severity = "warning"
	s.Diagnostics[0].Path = "unrelated.py"
	if c := evaluateDelta(d, s); c.Status != "passed" {
		t.Fatal("unrelated source blocked scoped absence", c)
	}
	if c := evaluate(context.Background(), "x", &planning.Rule{Kind: "graph_entity", Entity: "gone"}, s, t.TempDir()); c.Status != "unknown" {
		t.Fatal("warning index proved missing entity", c)
	}
}
func TestDeltaShapesAndEvidence(t *testing.T) {
	n := model.Node{ID: "new", Kind: "function", Name: "export", Provenance: model.Provenance{Evidence: model.Inferred}}
	s := model.Snapshot{Nodes: []model.Node{n}}
	d := planning.Delta{Operation: "add", Node: &n}
	if c := evaluateDelta(d, s); c.Status != "unknown" {
		t.Fatal("inferred entity proved delta", c)
	}
	d.Edge = &model.Edge{ID: "edge"}
	if c := evaluateDelta(d, s); c.Status != "unknown" {
		t.Fatal("both payloads accepted", c)
	}
	d.Node = nil
	d.Edge.Kind = "CALLS"
	if c := evaluateDelta(d, s); c.Status != "unknown" {
		t.Fatal("edge without endpoints accepted", c)
	}
}
func TestReviewedPlanKeepsAssumptions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "exists"), []byte("ok"), 0600); err != nil {
		t.Fatal(err)
	}
	p := planning.Plan{SchemaVersion: "1", FeatureID: "f", Intent: "export", BaseRevision: "base", Requirements: []planning.Requirement{{ID: "r", Intent: "file"}}, Acceptance: []planning.Criterion{{ID: "a", Requirement: "r", Intent: "file", Rule: &planning.Rule{Kind: "file_exists", Path: "exists"}}}, Assumptions: []planning.Assumption{{Text: "tenant isolation remains unverified"}}}
	p.Approval = &planning.Approval{Reviewer: "local", ReviewedAt: "2026-10-09T00:00:00Z", Checkpoint: "base", PlanDigest: planning.Digest(p)}
	r := VerifyWithEvidence(context.Background(), p, model.Snapshot{Revision: "WORKTREE"}, root, nil)
	if r.Status == "passed" {
		t.Fatal("assumption disappeared from reviewed plan", r)
	}
	found := false
	for _, c := range r.Checks {
		if strings.Contains(c.Explanation, "tenant isolation") {
			found = true
			if c.Status != "unknown" {
				t.Fatal(c)
			}
		}
	}
	if !found {
		t.Fatal("assumption absent from report", r)
	}
}
