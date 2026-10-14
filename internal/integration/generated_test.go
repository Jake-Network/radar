package integration

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/contracts"
	"github.com/Jake-Network/radar/internal/model"
)

func TestGeneratedCommandsRequireAuthorizationAndPreserveCandidate(t *testing.T) {
	if _, e := exec.LookPath("sh"); e != nil {
		t.Skip("sh unavailable")
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, ".radar/generated.json", `{"sources":["spec"],"outputs":["out"],"command":["sh","-c","cat spec > out"]}`)
	put(t, root, "spec", "old")
	put(t, root, "out", "old")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	gitTest(t, root, "branch", "baseline")
	put(t, root, "spec", "new")
	gitTest(t, root, "commit", "-qam", "source")
	c, e := BuildCandidate(context.Background(), root, "baseline", []string{"HEAD"})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	makeReport := func() Report {
		r := newReport()
		r.Base = c.Base
		r.Inputs = c.Inputs
		r.CandidateCommit = c.Commit
		r.CandidateTree = c.Tree
		r.Changed = []string{"spec"}
		return r
	}
	r := makeReport()
	if e := analyzeGenerated(context.Background(), c.Dir, &r, Options{}); e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "generated_drift", model.StatusUnknown)
	if len(r.GeneratedExecutions) != 0 {
		t.Fatal("static generation executed")
	}
	before := gitTest(t, c.Dir, "status", "--porcelain")
	r = makeReport()
	if e := analyzeGenerated(context.Background(), c.Dir, &r, Options{Verify: true, AllowExecution: true, Timeout: 5 * time.Second}); e != nil {
		t.Fatal(e)
	}
	checkStatus(t, r, "generated_drift", model.StatusFailed)
	if len(r.GeneratedExecutions) != 1 || r.GeneratedExecutions[0].CandidateTree != c.Tree {
		t.Fatalf("unbound generator evidence %+v", r.GeneratedExecutions)
	}
	if gitTest(t, c.Dir, "status", "--porcelain") != before {
		t.Fatal("regeneration altered integration candidate")
	}
}
func TestContractGenerateComparesIsolatedBaseAndHead(t *testing.T) {
	if _, e := exec.LookPath("sh"); e != nil {
		t.Skip("sh unavailable")
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"api","schema":"api.json","fields":["name"],"direction":"response","consumer":"consumer.py","generate":["sh","-c","cp source.json api.json"]}]}`)
	put(t, root, "consumer.py", "name = 'field'\n")
	put(t, root, "source.json", `{"type":"object","properties":{"name":{"type":"string"}}}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	gitTest(t, root, "branch", "baseline")
	put(t, root, "source.json", `{"type":"object","properties":{}}`)
	gitTest(t, root, "commit", "-qam", "schema source")
	c, e := BuildCandidate(context.Background(), root, "baseline", []string{"HEAD"})
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	r := newReport()
	r.Base = c.Base
	r.CandidateCommit = c.Commit
	r.CandidateTree = c.Tree
	docs, e := generatedContractDocuments(context.Background(), c.Dir, &r, Options{})
	if e != nil || len(docs) != 0 {
		t.Fatalf("unauthorized spec generation: %v %v", docs, e)
	}
	docs, e = generatedContractDocuments(context.Background(), c.Dir, &r, Options{Verify: true, AllowExecution: true, Timeout: 5 * time.Second})
	if e != nil {
		t.Fatal(e)
	}
	impact, e := contracts.ImpactWithDocuments(context.Background(), c.Dir, r.Base, r.CandidateCommit, docs)
	if e != nil {
		t.Fatal(e)
	}
	if impact.Status != model.StatusFailed || len(impact.Findings) != 1 || impact.Findings[0].Code != "contract_field_removed" {
		t.Fatalf("generated contract comparison: %+v", impact)
	}
	if len(r.GeneratedExecutions) != 2 {
		t.Fatalf("base/candidate generator evidence missing %+v", r.GeneratedExecutions)
	}
	if gitTest(t, c.Dir, "status", "--porcelain") != "" {
		t.Fatal("spec generation altered candidate")
	}
}

func TestUnavailableGeneratorHasNoToolEvidence(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, ".radar/generated.json", `{"sources":["spec"],"outputs":["out"]}`)
	put(t, root, "spec", "old")
	put(t, root, "out", "old")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	c, e := BuildCandidate(context.Background(), root, "HEAD", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	r := newReport()
	r.Base = c.Base
	r.CandidateCommit = c.Commit
	r.CandidateTree = c.Tree
	if e := analyzeGenerated(context.Background(), c.Dir, &r, Options{Verify: true, AllowExecution: true}); e != nil {
		t.Fatal(e)
	}
	for _, check := range r.Checks {
		if check.ID == "generated_drift" || check.ID == "generated:generator-1" {
			if check.Status != model.StatusIncomplete || check.Evidence != model.Unknown {
				t.Fatalf("unexecuted generator claimed tool evidence %+v", check)
			}
		}
	}
}
