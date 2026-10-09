package verification

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
)

func driftGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", root}, args...)...)
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
	return string(b)
}
func driftWrite(t *testing.T, root, path, body string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}
func driftCommit(t *testing.T, root string) string {
	t.Helper()
	driftGit(t, root, "add", ".")
	driftGit(t, root, "commit", "-qm", "fixture")
	sha, err := gitSHA(root)
	if err != nil {
		t.Fatal(err)
	}
	return sha
}
func gitSHA(root string) (string, error) { return gitrepo.Resolve(context.Background(), root, "HEAD") }
func approveDrift(p planning.Plan) planning.Plan {
	p.Approval = &planning.Approval{Reviewer: "test", ReviewedAt: "2026-10-09T00:00:00Z", Checkpoint: p.BaseRevision, PlanDigest: planning.Digest(p)}
	return p
}
func TestCommittedContractProjection(t *testing.T) {
	const original = `{"type":"object","properties":{"total":{"type":"integer"}}}`
	const renamed = `{"type":"object","properties":{"total_cents":{"type":"integer"}}}`
	const added = `{"type":"object","properties":{"total":{"type":"integer"},"currency":{"type":"string"}}}`
	const manifest = `{"version":1,"bindings":[{"id":"invoice","schema":"schema.json","pointer":"","producer":"api.py","consumer":"client.ts","fields":["total"]}]}`
	for _, tc := range []struct {
		name, actual, expected, operation, want string
		declared                                bool
	}{
		{"approved rename", renamed, renamed, "modify", "passed", true},
		{"approved addition", added, added, "modify", "passed", true},
		{"missing approved change", original, renamed, "modify", "failed", true},
		{"unexpected removal", renamed, "", "", "failed", false},
		{"compatible addition still drift", added, "", "", "failed", false},
		{"unchanged", original, "", "", "passed", false},
		{"malformed expected", `{"type":"object","properties":{"x":false}}`, `{"type":"object","properties":{"x":false}}`, "modify", "unknown", true},
		{"constraint keywords compare exactly", `{"type":"object","additionalProperties":false}`, `{"type":"object","additionalProperties":false}`, "modify", "passed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			driftGit(t, root, "init", "-q")
			driftGit(t, root, "config", "user.email", "fixture@example.com")
			driftGit(t, root, "config", "user.name", "fixture")
			driftWrite(t, root, "schema.json", original)
			driftWrite(t, root, ".radar/contracts.json", manifest)
			base := driftCommit(t, root)
			driftWrite(t, root, "schema.json", tc.actual)
			driftWrite(t, root, "change", tc.name)
			head := driftCommit(t, root)
			p := planning.Plan{BaseRevision: base}
			if tc.declared {
				p.ContractDeltas = []planning.ContractDelta{{Contract: "invoice", Operation: tc.operation, Schema: "schema.json", Expected: json.RawMessage(tc.expected)}}
			}
			p = approveDrift(p)
			checks := VerifyContracts(context.Background(), root, p, model.Snapshot{Repository: root, Revision: head})
			if len(checks) != 1 || string(checks[0].Status) != tc.want {
				t.Fatalf("%+v", checks)
			}
			if tc.declared {
				p.Intent = "tampered"
				checks = VerifyContracts(context.Background(), root, p, model.Snapshot{Revision: head})
				if len(checks) != 1 || checks[0].Status != "unknown" {
					t.Fatal(checks)
				}
			}
		})
	}
}

func TestTaskCompletionEvidence(t *testing.T) {
	p := planning.Plan{Tasks: []planning.Task{{ID: "a", Acceptance: []string{"one"}}, {ID: "b", Acceptance: []string{"two"}, DependsOn: []string{"a"}}, {ID: "c"}}}
	r := planning.Report{Checks: []planning.Check{{ID: "one", Status: "unknown"}, {ID: "two", Status: "passed"}}}
	ApplyTaskResults(p, &r)
	status := map[string]string{}
	for _, task := range r.Tasks {
		status[task.ID] = string(task.Status)
	}
	if status["a"] != "unknown" || status["b"] != "blocked" || status["c"] != "unknown" {
		t.Fatal(r.Tasks)
	}
	r.Checks[0].Status = "passed"
	ApplyTaskResults(p, &r)
	for _, task := range r.Tasks {
		if task.ID != "c" && task.Status != "passed" {
			t.Fatal(r.Tasks)
		}
	}
	r.Checks[1].Status = "failed"
	ApplyTaskResults(p, &r)
	if len(r.Feedback) != 2 {
		t.Fatal(r.Feedback)
	}
}

func TestContractPresenceAndReferenceDrift(t *testing.T) {
	const schema = `{"$defs":{"Invoice":{"type":"object","properties":{"total":{"type":"integer"}}}},"response":{"$ref":"#/$defs/Invoice"}}`
	const binding = `{"version":1,"bindings":[{"id":"invoice","schema":"schema.json","pointer":"/response"}]}`
	for _, tc := range []struct {
		name, actual, manifest, want, op, expected string
		remove                                     bool
	}{
		{name: "local ref changed", actual: `{"$defs":{"Invoice":{"type":"object","properties":{"total_cents":{"type":"integer"}}}},"response":{"$ref":"#/$defs/Invoice"}}`, manifest: binding, want: "failed"},
		{name: "missing pointer", actual: `{"type":"object"}`, manifest: binding, want: "failed"},
		{name: "missing file", manifest: binding, want: "failed", remove: true},
		{name: "binding removed", actual: schema, manifest: `{"version":1,"bindings":[]}`, want: "failed"},
		{name: "approved remove", actual: `{"type":"object"}`, manifest: `{"version":1,"bindings":[]}`, want: "passed", op: "remove"},
		{name: "remove binding retains object", actual: schema, manifest: `{"version":1,"bindings":[]}`, want: "failed", op: "remove"},
		{name: "approved referenced projection", actual: schema, manifest: binding, want: "passed", op: "modify", expected: `{"type":"object","properties":{"total":{"type":"integer"}}}`},
		{name: "malformed cannot prove absence", actual: `{`, manifest: binding, want: "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			driftGit(t, root, "init", "-q")
			driftGit(t, root, "config", "user.email", "fixture@example.com")
			driftGit(t, root, "config", "user.name", "fixture")
			driftWrite(t, root, "schema.json", schema)
			driftWrite(t, root, ".radar/contracts.json", binding)
			base := driftCommit(t, root)
			if tc.remove {
				if err := os.Remove(filepath.Join(root, "schema.json")); err != nil {
					t.Fatal(err)
				}
			} else {
				driftWrite(t, root, "schema.json", tc.actual)
			}
			driftWrite(t, root, ".radar/contracts.json", tc.manifest)
			driftWrite(t, root, "change", tc.name)
			head := driftCommit(t, root)
			p := planning.Plan{BaseRevision: base}
			if tc.op != "" {
				p.ContractDeltas = []planning.ContractDelta{{Contract: "invoice", Operation: tc.op, Schema: "schema.json", Pointer: "/response", Expected: json.RawMessage(tc.expected)}}
			}
			p = approveDrift(p)
			checks := VerifyContracts(context.Background(), root, p, model.Snapshot{Revision: head})
			if len(checks) != 1 || string(checks[0].Status) != tc.want {
				t.Fatalf("%+v", checks)
			}
			if tc.op != "" {
				checks = VerifyContracts(context.Background(), root, p, model.Snapshot{Revision: "WORKTREE:hash"})
				if len(checks) != 1 || checks[0].Status != "unknown" {
					t.Fatal(checks)
				}
			}
		})
	}
}
