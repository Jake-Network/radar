package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/planning"
)

func run(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	code := Run(context.Background(), append(append([]string{}, args...), "--root", root), &out, &errs)
	return code, out.String(), errs.String()
}

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, e := exec.LookPath("git"); e != nil {
		t.Skip(e)
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	gitTest(t, root, "config", "user.email", "radar@example.invalid")
	gitTest(t, root, "config", "user.name", "Radar test")
	return root
}

func TestWorktreeSharesStateAndEvidence(t *testing.T) {
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip(e)
	}
	root := gitRepo(t)
	put(t, root, ".gitignore", ".radar/state*\n.radar/config.json\n")
	put(t, root, "test_real.py", "import unittest\nclass Case(unittest.TestCase):\n def test_real(self):\n  self.assertEqual(1+1,2)\n")
	put(t, root, "svc.py", "def run():\n    return 1\n")
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	sha := gitTest(t, root, "rev-parse", "HEAD")
	invoke(t, root, 0, "init")
	p := planning.Plan{SchemaVersion: "1", FeatureID: "wt", Intent: "agent worktree evidence", BaseRevision: sha,
		Requirements: []planning.Requirement{{ID: "r", Intent: "tests pass"}},
		Acceptance:   []planning.Criterion{{ID: "test", Requirement: "r", Intent: "unit test", Rule: &planning.Rule{Kind: "test_run", Command: []string{"python3", "-m", "unittest", "test_real"}}}},
		Tasks:        []planning.Task{{ID: "t", Intent: "implement", Requirements: []string{"r"}, Acceptance: []string{"test"}, Components: []string{"function:svc.py#run"}}}}
	raw, _ := json.Marshal(p)
	put(t, root, "candidate.json", string(raw))
	invoke(t, root, 0, "approve", "--plan", "candidate.json", "--reviewer", "fixture-declaration", "--output", "approved.json")
	approved := filepath.Join(root, "approved.json")

	worktree := filepath.Join(t.TempDir(), "agent")
	gitTest(t, root, "worktree", "add", "-q", "-b", "agent", worktree)
	// The worktree has no .radar of its own; it shares the main checkout's state.
	if code, out, errs := run(t, worktree, "doctor", "--json"); code != 0 || !strings.Contains(out, `"initialized": true`) {
		t.Fatalf("worktree state not shared: %s %s", out, errs)
	}
	// Component IDs from the plan resolve identically in the worktree.
	pre := invoke(t, worktree, 0, "preflight", "--plan", approved, "--ref", sha)
	for _, f := range pre["findings"].([]any) {
		if f.(map[string]any)["code"] == "unknown_component" {
			t.Fatal("entity identity depends on checkout path", f)
		}
	}
	var out, errs bytes.Buffer
	code := Run(context.Background(), []string{"test", "--plan", approved, "--ref", "HEAD", "--allow-execution", "--root", worktree, "--json", "--", "python3", "-m", "unittest", "test_real"}, &out, &errs)
	if code != 0 {
		t.Fatalf("test in worktree: %d %s %s", code, out.String(), errs.String())
	}
	var record map[string]any
	json.Unmarshal(out.Bytes(), &record)
	if !strings.HasPrefix(record["repository"].(string), "git:") {
		t.Fatal("evidence bound to checkout path", record)
	}
	good := invoke(t, root, 0, "verify", "--plan", approved, "--ref", sha, "--evidence", record["id"].(string))
	if good["status"] != "passed" || good["authoritative"] != true {
		t.Fatal("worktree evidence rejected in main checkout", good)
	}
}

func TestAffectedResolveAndContractLint(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "lib/b.ts", "export const b = 1;\n")
	put(t, root, "lib/a.ts", "import { b } from './b';\nexport const a = b;\n")
	put(t, root, "app/c.ts", "import { a } from '../lib/a';\nexport function view() { return a.total; }\n")
	put(t, root, "app/other.ts", "export const other = 1;\n")
	put(t, root, "schema.json", `{"type":"object","properties":{"total":{"type":"integer","format":"int64"}}}`)
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"summary","schema":"schema.json","pointer":"","producer":"lib/b.ts","consumer":"app/c.ts","direction":"response","fields":["total","currency"]}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	invoke(t, root, 0, "init")
	put(t, root, "lib/b.ts", "export const b = 2;\n")
	r := invoke(t, root, 0, "affected", "--base", "HEAD")
	affected := map[string]float64{}
	for _, f := range r["affected"].([]any) {
		m := f.(map[string]any)
		affected[m["path"].(string)] = m["depth"].(float64)
	}
	if affected["lib/a.ts"] != 1 || affected["app/c.ts"] != 2 || len(affected) != 2 {
		t.Fatalf("affected files: %v", r)
	}
	if contracts := r["contracts"].([]any); len(contracts) != 1 {
		t.Fatalf("affected contract missing: %v", r)
	}
	resolved := invoke(t, root, 0, "resolve", "view")
	matches := resolved["matches"].([]any)
	if len(matches) != 1 || matches[0].(map[string]any)["id"] != "function:app/c.ts#view" {
		t.Fatalf("resolve: %v", resolved)
	}
	lint := invoke(t, root, 0, "contracts")
	issues := lint["bindings"].([]any)[0].(map[string]any)["issues"].([]any)
	if lint["status"] != "warning" || len(issues) != 2 {
		t.Fatalf("lint should flag the undeclared schema field and stale consumer field: %v", lint)
	}
}

func TestCommandFlagsHelpAndHumanOutput(t *testing.T) {
	root := t.TempDir()
	if code, _, _ := run(t, root, "init", "--reviewer", "x"); code != 2 {
		t.Fatal("flag of another command accepted")
	}
	if code, out, _ := run(t, root, "help", "verify"); code != 0 || !strings.Contains(out, "-evidence") {
		t.Fatal("per-command help", out)
	}
	if code, out, _ := run(t, root, "verify", "--help"); code != 0 || !strings.Contains(out, "Usage: radar verify") {
		t.Fatal("--help", out)
	}
	if code, out, _ := run(t, root, "init"); code != 0 || !strings.Contains(out, "Initialized Radar state") {
		t.Fatal("human init output", out)
	}
	put(t, root, "x.py", "def run():\n    return 1\n")
	if code, out, _ := run(t, root, "index"); code != 0 || !strings.Contains(out, "Indexed") {
		t.Fatal("human index output", out)
	}
	code, out, _ := run(t, root, "plan", "--output", "p.json", "change run")
	if code != 0 || !strings.Contains(out, "radar preflight --plan") {
		t.Fatal("human plan output", out)
	}
	code, out, _ = run(t, root, "preflight", "--plan", "p.json")
	if code != 0 || !strings.Contains(out, "preflight: WARNING") || !strings.Contains(out, "Next steps:") {
		t.Fatal("human preflight output", code, out)
	}
	if code, out, _ = run(t, root, "graph", "--kind", "function"); code != 0 || !strings.Contains(out, "function:x.py#run") {
		t.Fatal("human graph output", out)
	}
}

func TestMCPServer(t *testing.T) {
	root := t.TempDir()
	put(t, root, "svc.py", "class Order:\n    def total(self):\n        return 1\n")
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"radar_init","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"radar_index","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"radar_resolve","arguments":{"query":"Order.total"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"radar_test","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"radar_resolve","arguments":{"bogus":1}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"unknown/method"}`,
	}
	var out bytes.Buffer
	a := &app{ctx: context.Background(), root: root, out: &out, errout: &out}
	serveMCP(a, strings.NewReader(strings.Join(requests, "\n")+"\n"), &out)
	responses := map[float64]map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(line, err)
		}
		responses[m["id"].(float64)] = m
	}
	if len(responses) != 8 {
		t.Fatalf("expected 8 responses (notification unanswered): %s", out.String())
	}
	if responses[1]["result"].(map[string]any)["protocolVersion"] != "2025-03-26" {
		t.Fatal("protocol negotiation", responses[1])
	}
	tools := responses[2]["result"].(map[string]any)["tools"].([]any)
	for _, tool := range tools {
		name := tool.(map[string]any)["name"].(string)
		if name == "radar_test" || name == "radar_approve" {
			t.Fatal("execution or approval exposed to agents")
		}
	}
	text := func(id float64) (string, bool) {
		result := responses[id]["result"].(map[string]any)
		return result["content"].([]any)[0].(map[string]any)["text"].(string), result["isError"].(bool)
	}
	if body, isErr := text(4); isErr || !strings.Contains(body, `"entities"`) || strings.Contains(body, `"nodes"`) {
		t.Fatal("index over MCP must return a summary, not the full snapshot", body)
	}
	if body, isErr := text(5); isErr || !strings.Contains(body, "function:svc.py#Order.total") {
		t.Fatal("resolve over MCP", body)
	}
	if _, isErr := text(6); !isErr {
		t.Fatal("unknown tool accepted")
	}
	if _, isErr := text(7); !isErr {
		t.Fatal("unknown argument accepted")
	}
	if responses[8]["error"] == nil {
		t.Fatal("unknown method accepted")
	}
}
