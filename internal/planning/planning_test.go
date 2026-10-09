package planning

import (
	"github.com/Jake-Network/radar/internal/model"
	"os"
	"path/filepath"
	"testing"
)

func validPlan() Plan {
	return Plan{SchemaVersion: "1", FeatureID: "f", Intent: "export", BaseRevision: "base", Requirements: []Requirement{{ID: "r", Intent: "preserve response"}}, Acceptance: []Criterion{{ID: "a", Requirement: "r", Intent: "preserve total", Rule: &Rule{Kind: "file_exists", Path: "schema.json"}}}, Tasks: []Task{{ID: "t", Intent: "implement", Requirements: []string{"r"}, Acceptance: []string{"a"}}}}
}
func TestDAGValidation(t *testing.T) {
	p := validPlan()
	p.Tasks[0].DependsOn = []string{"missing"}
	if _, e := Tasks(p); e == nil {
		t.Fatal("missing dependency accepted")
	}
	p.Tasks[0].DependsOn = []string{"t"}
	if _, e := Tasks(p); e == nil {
		t.Fatal("cycle accepted")
	}
}
func TestOwnership(t *testing.T) {
	p := validPlan()
	p.Tasks[0].Contracts = []string{"api"}
	b := p.Tasks[0]
	b.ID = "b"
	p.Tasks = append(p.Tasks, b)
	r := Validate(p, model.Snapshot{Revision: "base"})
	if r.Status != "failed" {
		t.Fatal(r)
	}
	schedule, e := Tasks(p)
	if e != nil || len(schedule.ParallelGroups) != 2 {
		t.Fatal(schedule, e)
	}
	p.Tasks[1].DependsOn = []string{"t"}
	if r := Validate(p, model.Snapshot{Revision: "base"}); r.Status != "passed" {
		t.Fatal(r)
	}
}
func TestSecurityAndRules(t *testing.T) {
	p := validPlan()
	p.Requirements[0].SecuritySensitive = true
	p.Acceptance[0].Rule = nil
	if Validate(p, model.Snapshot{Revision: "base"}).Status != "failed" {
		t.Fatal("security criterion accepted without evidence")
	}
	for _, r := range []Rule{{Kind: "shell", Path: "test.sh"}, {Kind: "file_exists", Path: "../secret"}, {Kind: "json_property", Path: "schema.json"}} {
		if ValidateRule(r) == nil {
			t.Fatal("invalid rule accepted", r)
		}
	}
}
func TestProjectionPreservesEvidence(t *testing.T) {
	s := model.Snapshot{Nodes: []model.Node{{ID: "old", Kind: "file", Provenance: model.Provenance{Evidence: model.VerifiedStatic}}}}
	p := validPlan()
	p.GraphDeltas = []Delta{{Operation: "add", Node: &model.Node{ID: "new", Kind: "symbol", Name: "new", Provenance: model.Provenance{Evidence: model.VerifiedStatic}}}}
	out, e := Project(p, s)
	if e != nil {
		t.Fatal(e)
	}
	for _, n := range out.Nodes {
		if n.ID == "new" && n.Provenance.Evidence != model.Proposed {
			t.Fatal("proposal represented as verified")
		}
	}
	if len(s.Nodes) != 1 {
		t.Fatal("baseline mutated")
	}
}
func TestApprovalDigest(t *testing.T) {
	p := validPlan()
	p.Approval = &Approval{Reviewer: "local", ReviewedAt: "2026-10-09T00:00:00Z", Checkpoint: p.BaseRevision, PlanDigest: Digest(p)}
	if !Approved(p) {
		t.Fatal("valid review rejected")
	}
	p.Intent = "changed"
	if Approved(p) {
		t.Fatal("changed plan accepted")
	}
}
func TestGenerateIncomplete(t *testing.T) {
	p := Generate("exports", model.Snapshot{Revision: "base"})
	if len(p.Incomplete) == 0 || Approved(p) {
		t.Fatal("generated context claimed complete design")
	}
}

func TestLoadRejectsMalformedArtifacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	for _, body := range []string{`{"unexpected":true}`, `{"tasks":"not an array"}`, `{} {}`, `{`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatal("invalid artifact accepted", body)
		}
	}
	if Validate(Plan{}, model.Snapshot{}).Status != "failed" {
		t.Fatal("empty plan accepted")
	}
}

func TestSharedComponentSerializesTaskPackets(t *testing.T) {
	p := Plan{BaseRevision: "base", Requirements: []Requirement{{ID: "r", Intent: "intent"}}, Acceptance: []Criterion{{ID: "a", Requirement: "r", Intent: "check"}}, Tasks: []Task{{ID: "one", Components: []string{"same-file"}, Requirements: []string{"r"}, Acceptance: []string{"a"}}, {ID: "two", Components: []string{"same-file"}}}}
	s, e := Tasks(p)
	if e != nil {
		t.Fatal(e)
	}
	if len(s.ParallelGroups) != 2 {
		t.Fatal("shared component scheduled in parallel", s)
	}
	if s.PlanDigest != Digest(p) || len(s.Instructions) != 2 || len(s.Instructions[0].Requirements) != 1 {
		t.Fatal("missing task proof packets")
	}
}
