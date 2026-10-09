package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
)

func invoke(t *testing.T, root string, want int, args ...string) map[string]any {
	t.Helper()
	var out, errs bytes.Buffer
	all := append([]string{"--root", root, "--json"}, args...)
	code := Run(context.Background(), all, &out, &errs)
	if code != want {
		t.Fatalf("%v: exit %d want %d\n%s\n%s", args, code, want, out.String(), errs.String())
	}
	var v map[string]any
	if e := json.Unmarshal(out.Bytes(), &v); e != nil {
		t.Fatalf("non JSON %v: %s %s", e, out.String(), errs.String())
	}
	return v
}
func put(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if e := os.MkdirAll(filepath.Dir(full), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(full, []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestCLIArchitectureLifecycle(t *testing.T) {
	root := t.TempDir()
	invoke(t, root, 0, "init")
	for path, source := range map[string]string{"backend/api.py": "from models import Summary\ndef export():\n    return 1\n", "frontend/client.ts": "export interface Summary { total: number }; export function read(s:Summary){return s.total}", "worker/main.go": "package main\nfunc main() {}\n", "format/lib.rs": "pub struct Summary { pub total: i64 }\npub fn format() {}"} {
		put(t, root, path, source)
	}
	put(t, root, "openapi.json", `{"components":{"schemas":{"Summary":{"type":"object","properties":{"total":{"type":"integer"}}}}}}`)
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"summary","schema":"openapi.json","pointer":"/components/schemas/Summary","producer":"backend/api.py","consumer":"frontend/client.ts","direction":"response","fields":["total"]}]}`)
	indexed := invoke(t, root, 0, "index")
	raw, _ := json.Marshal(indexed)
	var s model.Snapshot
	if e := json.Unmarshal(raw, &s); e != nil {
		t.Fatal(e)
	}
	langs := map[string]bool{}
	for _, n := range s.Nodes {
		langs[n.Language] = true
	}
	for _, l := range []string{"typescript", "python", "go", "rust"} {
		if !langs[l] {
			t.Fatalf("missing language %s", l)
		}
	}
	if len(s.Edges) < 5 {
		t.Fatal("graph lacks relationships")
	}
	query := invoke(t, root, 0, "graph", "--kind", "schema_field")
	if len(query["nodes"].([]any)) != 1 {
		t.Fatal("schema graph absent")
	}
	generated := invoke(t, root, 0, "plan", "Implement asynchronous organization-scoped exports")
	if generated["status"] != "incomplete" {
		t.Fatal("fabricated complete design")
	}
	if _, e := os.Stat(generated["context_path"].(string)); e != nil {
		t.Fatal(e)
	}
	p := planning.Generate("Preserve summary contract", s)
	p.Incomplete = []string{}
	p.Assumptions = []planning.Assumption{}
	p.Acceptance = []planning.Criterion{{ID: "total", Requirement: "request", Intent: "retain total", Rule: &planning.Rule{Kind: "json_property", Path: "openapi.json", Pointer: "/components/schemas/Summary/properties", Property: "total"}}}
	p.Tasks = []planning.Task{{ID: "producer", Intent: "preserve response", Requirements: []string{"request"}, Acceptance: []string{"total"}, Contracts: []string{"summary"}}}
	b, _ := json.Marshal(p)
	put(t, root, "plan.json", string(b))
	invoke(t, root, 0, "preflight", "--plan", "plan.json")
	schedule := invoke(t, root, 0, "tasks", "--plan", "plan.json")
	if len(schedule["order"].([]any)) != 1 {
		t.Fatal("DAG missing")
	}
	good := invoke(t, root, 0, "verify", "--plan", "plan.json")
	checks := good["checks"].([]any)
	found := false
	for _, c := range checks {
		m := c.(map[string]any)
		if m["id"] == "total" && m["status"] == "passed" {
			found = true
		}
	}
	if !found || good["authoritative"] != false {
		t.Fatal("incorrect compliant report", good)
	}
	p.Tasks = append(p.Tasks, planning.Task{ID: "consumer", Intent: "retain display", Requirements: []string{"request"}, Acceptance: []string{"total"}, Contracts: []string{"summary"}})
	b, _ = json.Marshal(p)
	put(t, root, "bad-plan.json", string(b))
	bad := invoke(t, root, 1, "preflight", "--plan", "bad-plan.json")
	found = false
	for _, f := range bad["findings"].([]any) {
		if f.(map[string]any)["code"] == "contract_owner_conflict" {
			found = true
		}
	}
	if !found {
		t.Fatal("missing design conflict")
	}
	put(t, root, "openapi.json", `{"components":{"schemas":{"Summary":{"type":"object","properties":{"total_cents":{"type":"integer"}}}}}}`)
	failed := invoke(t, root, 1, "verify", "--plan", "plan.json")
	if failed["status"] != "failed" {
		t.Fatal("noncompliant accepted")
	}
	findings := failed["findings"].([]any)
	id := findings[len(findings)-1].(map[string]any)["id"].(string)
	invoke(t, root, 0, "explain", id)
}
func gitTest(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", root}, args...)...)
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
	return strings.TrimSpace(string(b))
}
func TestCLICommittedConflictAndWIP(t *testing.T) {
	if _, e := exec.LookPath("git"); e != nil {
		t.Skip(e)
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	gitTest(t, root, "config", "user.email", "radar@example.invalid")
	gitTest(t, root, "config", "user.name", "Radar test")
	put(t, root, "producer.py", "class Invoice:\n    total: int\n")
	put(t, root, "consumer.ts", "export interface Invoice { total: number }\n")
	put(t, root, "schema.json", `{"type":"object","properties":{"total":{"type":"integer"}}}`)
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"invoice","schema":"schema.json","pointer":"","producer":"producer.py","consumer":"consumer.ts","direction":"response","fields":["total"]}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	base := gitTest(t, root, "rev-parse", "HEAD")
	invoke(t, root, 0, "init")
	gitTest(t, root, "checkout", "-qb", "producer")
	put(t, root, "schema.json", `{"type":"object","properties":{"total_cents":{"type":"integer"}}}`)
	wip := invoke(t, root, 0, "impact", "--base", base, "--head", "WORKTREE")
	if wip["checkpoint"] != "informational" {
		t.Fatal("WIP promoted")
	}
	gitTest(t, root, "add", "schema.json")
	gitTest(t, root, "commit", "-qm", "producer rename")
	gitTest(t, root, "checkout", "-qb", "consumer", base)
	put(t, root, "consumer.ts", "export interface Invoice { total: number }; export const label='Summary';")
	gitTest(t, root, "add", "consumer.ts")
	gitTest(t, root, "commit", "-qm", "consumer display")
	r := invoke(t, root, 1, "scan", "--base", base, "--branches", "producer,consumer")
	if len(r["findings"].([]any)) == 0 {
		t.Fatal("conflict undetected")
	}
	invoke(t, root, 0, "impact", "--base", base, "--head", "consumer")
	invoke(t, root, 2, "impact", "--base", base, "--head", "not-a-ref")
}
func TestCLIBoundsAndErrors(t *testing.T) {
	root := t.TempDir()
	invoke(t, root, 2, "index")
	invoke(t, root, 0, "init")
	invoke(t, root, 0, "index")
	invoke(t, root, 2, "preflight")
	put(t, root, "invalid.json", `{"unexpected":true}`)
	invoke(t, root, 2, "preflight", "--plan", "invalid.json")
	invoke(t, root, 2, "plan", "--output", "../escape.json", "a feature")
	invoke(t, root, 2, "unknown")
	invoke(t, root, 2, "graph", "--format", "unsupported")
	invoke(t, root, 0, "plan", "--output", "existing.json", "a feature")
	invoke(t, root, 2, "plan", "--output", "existing.json", "a feature")
}

func TestPreflightRejectsChangedBaseline(t *testing.T) {
	root := t.TempDir()
	invoke(t, root, 0, "init")
	put(t, root, "x.ts", "export const initial=1;")
	indexed := invoke(t, root, 0, "index")
	raw, _ := json.Marshal(indexed)
	var s model.Snapshot
	json.Unmarshal(raw, &s)
	p := planning.Generate("change", s)
	b, _ := json.Marshal(p)
	put(t, root, "plan.json", string(b))
	put(t, root, "x.ts", "export const changed=2;")
	r := invoke(t, root, 1, "preflight", "--plan", "plan.json")
	found := false
	for _, f := range r["findings"].([]any) {
		if f.(map[string]any)["code"] == "stale_context" {
			found = true
		}
	}
	if !found {
		t.Fatal("stale context undetected")
	}
}
func TestFilteredGraphHasNoDanglingEdges(t *testing.T) {
	root := t.TempDir()
	invoke(t, root, 0, "init")
	put(t, root, "x.py", "class Service:\n    def Run(self):\n        return 1\n")
	invoke(t, root, 0, "index")
	r := invoke(t, root, 0, "graph", "--kind", "function")
	ids := map[string]bool{}
	for _, n := range r["nodes"].([]any) {
		ids[n.(map[string]any)["id"].(string)] = true
	}
	for _, e := range r["edges"].([]any) {
		m := e.(map[string]any)
		if !ids[m["from"].(string)] || !ids[m["to"].(string)] {
			t.Fatal("dangling filtered relationship")
		}
	}
	var out, errs bytes.Buffer
	if code := Run(context.Background(), []string{"graph", "--kind", "function", "--format", "mermaid", "--root", root}, &out, &errs); code != 0 {
		t.Fatal(errs.String())
	}
	if strings.Contains(out.String(), "class:") {
		t.Fatal("Mermaid ignored graph filters")
	}
}

func TestWorkingTreeApprovalAndIntentGraph(t *testing.T) {
	root := t.TempDir()
	invoke(t, root, 0, "init")
	put(t, root, "api.py", "def api():\n return 1\n")
	indexed := invoke(t, root, 0, "index")
	raw, _ := json.Marshal(indexed)
	var s model.Snapshot
	json.Unmarshal(raw, &s)
	p := planning.Plan{SchemaVersion: "1", FeatureID: "f", Intent: "Small local change", BaseRevision: s.Revision, Requirements: []planning.Requirement{{ID: "r", Intent: "Preserve file"}}, Acceptance: []planning.Criterion{{ID: "a", Requirement: "r", Intent: "File remains", Rule: &planning.Rule{Kind: "file_exists", Path: "api.py"}}}, Tasks: []planning.Task{{ID: "t", Intent: "Preserve file", Requirements: []string{"r"}, Acceptance: []string{"a"}}}}
	raw, _ = json.Marshal(p)
	put(t, root, "candidate.json", string(raw))
	invoke(t, root, 0, "approve", "--plan", "candidate.json", "--reviewer", "fixture-declaration", "--output", "approved.json")
	g := invoke(t, root, 0, "graph", "--plan", "approved.json", "--kind", "implementation_task")
	if len(g["nodes"].([]any)) != 1 {
		t.Fatal("planning entities absent from graph", g)
	}
	put(t, root, "api.py", "def changed():\n return 2\n")
	invoke(t, root, 2, "approve", "--plan", "candidate.json", "--reviewer", "fixture-declaration", "--output", "stale.json")
}
