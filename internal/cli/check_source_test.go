package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
	"github.com/Jake-Network/radar/internal/testselection"
)

func TestCheckStabilityIncludesRecommendationReads(t *testing.T) {
	for _, required := range []string{"source_stability", "dependency_impact", "no_breaking_contracts"} {
		for _, change := range []string{"test_service.py", "pytest.ini", ".radar/contracts.json"} {
			t.Run(required+"/"+change, func(t *testing.T) {
				root := gitRepo(t)
				put(t, root, "service.py", "VALUE = 1\n")
				put(t, root, "test_service.py", "from service import VALUE\ndef test_value(): assert VALUE == 1\n")
				put(t, root, "pytest.ini", "[pytest]\naddopts = -q\n")
				gitTest(t, root, "add", ".")
				gitTest(t, root, "commit", "-qm", "baseline")
				put(t, root, "policy.json", `{"version":1,"require":["`+required+`"]}`)
				var out, errout bytes.Buffer
				a := &app{ctx: context.Background(), root: root, out: &out, errout: &errout, machine: true}
				code := a.checkWithRecommendation(options{base: "HEAD", head: "WORKTREE", suggestTests: true, policy: "policy.json"}, func(ctx context.Context, root, ref string, s model.Snapshot, changed []string, p *planning.Plan) (testselection.Proposal, error) {
					// Deterministic agent write during the recommendation stage, after the
					// former stability boundary. Configuration has no indexed source node.
					switch change {
					case "test_service.py":
						put(t, root, change, "def test_changed(): assert False\n")
					case "pytest.ini":
						put(t, root, change, "[pytest]\naddopts = --ignore=test_service.py\n")
					default:
						put(t, root, change, `{"version":1,"bindings":[]}`)
					}
					return testselection.Recommend(ctx, root, ref, s, changed, p)
				})
				if code != 1 {
					t.Fatalf("stale analysis exit %d: %s %s", code, &out, &errout)
				}
				var r checkReport
				if err := json.Unmarshal(out.Bytes(), &r); err != nil {
					t.Fatal(err)
				}
				if r.Gate.Verdict != "blocked" {
					t.Fatal("changing source passed", r.Gate)
				}
				for _, c := range r.Checks {
					if c.ID == "source_stability" && c.Status != model.StatusIncomplete {
						t.Fatal(c)
					}
				}
			})
		}
	}
}

func TestCheckPinnedCommitUnaffectedByWorktreeMutation(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "service.py", "VALUE = 1\n")
	put(t, root, "test_service.py", "from service import VALUE\ndef test_value(): assert VALUE == 1\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	sha := strings.TrimSpace(gitTest(t, root, "rev-parse", "HEAD"))
	put(t, root, "policy.json", `{"version":1,"require":["source_stability"]}`)
	var out, errout bytes.Buffer
	a := &app{ctx: context.Background(), root: root, out: &out, errout: &errout, machine: true}
	code := a.checkWithRecommendation(options{base: "HEAD", head: "HEAD", suggestTests: true, policy: "policy.json"}, func(ctx context.Context, root, ref string, s model.Snapshot, changed []string, p *planning.Plan) (testselection.Proposal, error) {
		if ref != sha || s.Revision != sha {
			t.Fatalf("revision not pinned: %s %s", ref, s.Revision)
		}
		put(t, root, "test_service.py", "def test_mutated(): assert False\n")
		return testselection.Recommend(ctx, root, ref, s, changed, p)
	})
	if code != 0 {
		t.Fatalf("immutable revision exit %d: %s %s", code, &out, &errout)
	}
	var r checkReport
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict != "pass" || r.Head != sha || r.VerificationProposal.Inventory.Revision != sha {
		t.Fatal(r)
	}
	if len(r.VerificationProposal.Inventory.Tests) != 1 || r.VerificationProposal.Inventory.Tests[0].Evidence.Revision != sha {
		t.Fatal(r.VerificationProposal)
	}
}

func TestCheckSourceStabilityStableWorktree(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "service.py", "VALUE = 1\n")
	put(t, root, "pytest.ini", "[pytest]\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	put(t, root, "policy.json", `{"version":1,"require":["source_stability"]}`)
	report := invoke(t, root, 0, "check", "--base", "HEAD", "--suite", "full", "--policy", "policy.json")
	if report["gate"].(map[string]any)["verdict"] != "pass" {
		t.Fatal(report)
	}
}

func TestCheckSourceObservationBudgetCannotPass(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "service.py", "VALUE = 1\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	put(t, root, "policy.json", `{"version":1,"require":["source_stability"]}`)
	filename := filepath.Join(root, "oversized-input.dat")
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate((64 << 20) + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	report := invoke(t, root, 1, "check", "--base", "HEAD", "--policy", "policy.json")
	if report["gate"].(map[string]any)["verdict"] != "blocked" {
		t.Fatal(report)
	}
}

func TestCheckUnstableSourceDoesNotHideKnownContractFailure(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "schema.json", `{"type":"object","properties":{"total":{"type":"integer"}}}`)
	put(t, root, "client.ts", "export function read(value: {total: number}) { return value.total; }\n")
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"total","schema":"schema.json","pointer":"","consumer":"client.ts","fields":["total"],"direction":"response"}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	put(t, root, ".radar/contracts.json", "{malformed")
	put(t, root, "policy.json", `{"version":1,"require":["manifest_lint","source_stability"]}`)
	var out, errout bytes.Buffer
	a := &app{ctx: context.Background(), root: root, out: &out, errout: &errout, machine: true}
	code := a.checkWithRecommendation(options{base: "HEAD", head: "WORKTREE", suggestTests: true, policy: "policy.json"}, func(ctx context.Context, root, ref string, s model.Snapshot, changed []string, p *planning.Plan) (testselection.Proposal, error) {
		put(t, root, "client.ts", "export const changed = true;\n")
		return testselection.Recommend(ctx, root, ref, s, changed, p)
	})
	if code != 1 {
		t.Fatalf("exit %d: %s %s", code, &out, &errout)
	}
	var r checkReport
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict != "fail" || r.Status != model.StatusFailed {
		t.Fatal("actual failure overwritten by stability gap", r.Gate, r.Status)
	}
}
