package cli

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// agentRepo commits a baseline on main and returns a function that creates a
// worktree branch with the given file contents committed.
func agentRepo(t *testing.T, files map[string]string) (string, func(branch string, changes map[string]string) string) {
	t.Helper()
	root := gitRepo(t)
	gitTest(t, root, "checkout", "-q", "-b", "main")
	for p, content := range files {
		put(t, root, p, content)
	}
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	worktrees := t.TempDir()
	return root, func(branch string, changes map[string]string) string {
		t.Helper()
		dir := filepath.Join(worktrees, branch)
		gitTest(t, root, "worktree", "add", "-q", "-b", branch, dir, "main")
		if len(changes) == 0 {
			return dir
		}
		for p, content := range changes {
			put(t, dir, p, content)
		}
		gitTest(t, dir, "add", ".")
		gitTest(t, dir, "commit", "-qm", branch)
		return dir
	}
}

const checkoutTest = "import re, unittest\nfrom pathlib import Path\nfrom backend import price\nclass Checkout(unittest.TestCase):\n    def test_budget(self):\n        q = int(re.search(r'QUANTITY = (\\d+)', Path('frontend.ts').read_text())[1])\n        self.assertLessEqual(price() * q, 2)\n"

func checkoutRepo(t *testing.T) (string, func(string, map[string]string) string) {
	return agentRepo(t, map[string]string{
		"backend.py":       "def price():\n    return 1\n",
		"frontend.ts":      "export const QUANTITY = 1;\n",
		"test_checkout.py": checkoutTest,
	})
}

func TestGateCombinesWorktreeBranchesWithoutConfiguration(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-frontend", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})
	agent("agent-idle", nil)

	// No init, no manifest, no flags: branches come from the worktrees.
	r := invoke(t, root, 0, "gate")
	if r["base_ref"] != "main" {
		t.Fatal("default base", r["base_ref"])
	}
	branches := r["branches"].([]any)
	if len(branches) != 2 || branches[0].(map[string]any)["ref"] != "agent-backend" || branches[1].(map[string]any)["ref"] != "agent-frontend" {
		t.Fatal("worktree branches", branches)
	}
	if skipped := r["skipped_branches"].([]any); len(skipped) != 1 || !strings.HasPrefix(skipped[0].(string), "agent-idle") {
		t.Fatal("idle worktree must be skipped", skipped)
	}
	if r["gate"].(map[string]any)["verdict"] != "pass" || r["next"] != "radar gate --run" {
		t.Fatal("static gate", r["gate"], r["next"])
	}
	if r["verification_proposal"] == nil || r["executions"] != nil {
		t.Fatal("static gate must propose tests without executing them", r["executions"])
	}
	if code, out, _ := run(t, root, "gate"); code != 0 || !strings.Contains(out, "combined tree not tested yet") || !strings.Contains(out, "Next: radar gate --run") {
		t.Fatal("human static gate", out)
	}
}

func TestGateRunFailsCombinedTreeAndPointsAtBranch(t *testing.T) {
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip(e)
	}
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-frontend", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})
	for _, branch := range []string{"agent-backend", "agent-frontend"} {
		// The test reads frontend.ts at runtime, which import analysis cannot
		// relate. A reviewed full inventory run is required for that branch.
		if r := invoke(t, root, 0, "gate", "--run", "--suite", "full", branch); r["gate"].(map[string]any)["verdict"] != "pass" {
			t.Fatal("each branch passes alone", branch, r["gate"])
		}
	}
	r := invoke(t, root, 1, "gate", "--run")
	if r["gate"].(map[string]any)["verdict"] != "fail" {
		t.Fatal(r["gate"])
	}
	var failure map[string]any
	for _, raw := range r["findings"].([]any) {
		if f := raw.(map[string]any); f["code"] == "integration_execution_failed" {
			failure = f
		}
	}
	if failure == nil {
		t.Fatal("combined failure missing", r["findings"])
	}
	lead := r["attribution"].(map[string]any)[failure["id"].(string)].(map[string]any)
	leads := lead["leads"].([]any)
	if len(leads) != 1 || leads[0].(map[string]any)["branch"] != "agent-backend" || !strings.Contains(lead["basis"].(string), "inferred") {
		t.Fatal("failing test imports backend.py from agent-backend", lead)
	}
	if !strings.HasPrefix(r["next"].(string), "repair") || !strings.HasSuffix(r["next"].(string), "radar gate --run") {
		t.Fatal(r["next"])
	}
}

func TestGateConflictNamesBothBranches(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("left", map[string]string{"backend.py": "def price():\n    return 3\n"})
	agent("right", map[string]string{"backend.py": "def price():\n    return 4\n"})
	r := invoke(t, root, 1, "gate", "left,right")
	for _, raw := range r["findings"].([]any) {
		f := raw.(map[string]any)
		if f["code"] != "integration_textual_conflict" {
			continue
		}
		leads := r["attribution"].(map[string]any)[f["id"].(string)].(map[string]any)["leads"].([]any)
		if len(leads) != 2 {
			t.Fatal("both branches touched the conflicted path", leads)
		}
		return
	}
	t.Fatal("conflict missing", r["findings"])
}

func TestGateDiscoveredContractBreakWithoutManifest(t *testing.T) {
	schema := `{"openapi":"3.0.0","paths":{"/users":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/User"}}}}}}}},"components":{"schemas":{"User":{"type":"object","properties":{"email":{"type":"string"}}}}}}`
	root, agent := agentRepo(t, map[string]string{
		"openapi.json": schema,
		"types.ts":     "export interface User { email: string; }\n",
		"client.ts":    "import type { User } from './types';\nasync function load() { const user: User = await (await fetch('/users')).json(); return user.email; }\n",
	})
	agent("api-agent", map[string]string{"openapi.json": strings.Replace(schema, `"email"`, `"display"`, 1)})
	agent("ui-agent", map[string]string{"client.ts": "import type { User } from './types';\nasync function load() { const user: User = await (await fetch('/users')).json(); return 'Hi ' + user.email; }\n"})
	code, out, errs := run(t, root, "gate")
	if code != 0 || !strings.Contains(out, "discovered_response_field_removed") || !strings.Contains(out, "api-agent (openapi.json)") {
		t.Fatalf("discovered contract warning with branch lead: %d\n%s\n%s", code, out, errs)
	}
}

func TestGateInvocationErrors(t *testing.T) {
	root, _ := checkoutRepo(t)
	if code, _, errs := run(t, root, "gate"); code != 2 || !strings.Contains(errs, "no branches to combine") {
		t.Fatal("no worktree branches", code, errs)
	}
	if code, _, errs := run(t, root, "gate", "--suite", "full", "main"); code != 2 || !strings.Contains(errs, "--run") {
		t.Fatal("--suite without --run", code, errs)
	}
	gitTest(t, root, "branch", "-m", "main", "develop")
	if code, _, errs := run(t, root, "gate", "x"); code != 2 || !strings.Contains(errs, "--base") {
		t.Fatal("no default base", code, errs)
	}
}

func TestHelpShowsEverydayCommandsFirst(t *testing.T) {
	root := t.TempDir()
	code, out, _ := run(t, root, "help")
	if code != 0 || !strings.Contains(out, "  gate ") || strings.Contains(out, "  approve ") || !strings.Contains(out, "radar help --all") {
		t.Fatal("default help", out)
	}
	if code, out, _ = run(t, root, "help", "--all"); code != 0 || !strings.Contains(out, "  approve ") || !strings.Contains(out, "  merge-check ") {
		t.Fatal("help --all", out)
	}
	// Advanced commands remain directly usable.
	if code, out, _ = run(t, root, "help", "approve"); code != 0 || !strings.Contains(out, "Usage: radar approve") {
		t.Fatal("advanced command help", out)
	}
}

func TestMCPGateIsBoundedAndStatic(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-frontend", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})
	a := &app{ctx: context.Background(), root: root}
	result := a.callTool("radar_gate", map[string]any{"branches": "agent-backend,agent-frontend"})
	text := result["content"].([]map[string]any)[0]["text"].(string)
	if result["isError"] != false {
		t.Fatal(text)
	}
	var brief map[string]any
	if err := json.Unmarshal([]byte(text), &brief); err != nil {
		t.Fatal(err, text)
	}
	branches := brief["branches"].([]any)
	if len(branches) != 2 || branches[0].(map[string]any)["changed_count"] != 1.0 || brief["next"] == nil || brief["agent_brief"] == nil {
		t.Fatal("bounded gate summary", text)
	}
	if strings.Contains(text, `"executions":[{`) {
		t.Fatal("MCP gate executed repository code", text)
	}
}
