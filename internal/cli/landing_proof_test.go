package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
)

func TestLandingCLIPrivateRefAndMCP(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-api", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-docs", map[string]string{"NOTES.md": "notes\n"})
	before := gitTest(t, root, "status", "--porcelain=v1")
	branches := gitTest(t, root, "branch", "--format=%(refname):%(objectname)")
	report := invoke(t, root, 0, "land", "agent-api", "agent-docs", "--base", "main", "--apply")
	if report["landing"] == nil || report["integration_ref"] == nil {
		t.Fatal(report)
	}
	ref := report["integration_ref"].(map[string]any)["ref"].(string)
	if !strings.HasPrefix(ref, "refs/radar/integration/") {
		t.Fatal(ref)
	}
	if gitTest(t, root, "status", "--porcelain=v1") != before || gitTest(t, root, "branch", "--format=%(refname):%(objectname)") != branches {
		t.Fatal("landing changed source state")
	}
	if _, out, _ := run(t, root, "land", "agent-api", "agent-docs"); !strings.Contains(out, "Land order:") || !strings.Contains(out, "Any time:") {
		t.Fatal(out)
	}
	a := &app{root: root, ctx: context.Background()}
	tool := a.callTool("radar_land", map[string]any{"branches": "agent-api,agent-docs"})
	data, _ := json.Marshal(tool)
	if tool["isError"] == true || !strings.Contains(string(data), "landing") {
		t.Fatal(string(data))
	}
}

func TestTrailerRecordsHistoricalGate(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-api", map[string]string{"backend.py": "def price():\n    return 2\n"})
	code, out, errout := run(t, root, "gate", "agent-api", "--run", "--trailer")
	if code != 0 || !strings.HasPrefix(out, "Radar-Verified:") || !strings.Contains(out, "selected commands") {
		t.Fatal(code, out, errout)
	}
	_, id, ok := strings.Cut(strings.TrimSpace(out), "run=")
	if !ok {
		t.Fatal(out)
	}
	report := invoke(t, root, 0, "show", id)
	if report["gate"].(map[string]any)["verdict"] != "pass" {
		t.Fatal(report)
	}
	tree := report["candidate_tree"]
	put(t, root, "later.txt", "later\n")
	gitTest(t, root, "add", "later.txt")
	gitTest(t, root, "commit", "-qm", "later main commit")
	if newer := invoke(t, root, 0, "show", id); newer["candidate_tree"] != tree {
		t.Fatal("historical report rebound to HEAD")
	}
	if _, out, _ := run(t, root, "gate", "agent-api", "--json", "--trailer"); !strings.Contains(out, "error") {
		t.Fatal(out)
	}
	if code, _, _ := run(t, root, "show", "../escape"); code != 2 {
		t.Fatal("unsafe ID accepted")
	}
}

func TestTrailerNeverClaimsMissingContractsOrTests(t *testing.T) {
	r := integration.Report{Checks: []integration.Check{{ID: "textual_merge", Status: model.StatusPassed, Evidence: model.VerifiedTool}, {ID: "no_breaking_contracts", Status: model.StatusPassed, Evidence: model.VerifiedStatic}}, Gate: gate.Result{Verdict: gate.Pass}}
	text := gateTrailer(r, false, "run")
	if strings.Contains(text, "contracts") || !strings.Contains(text, "tests(not_run)") {
		t.Fatal(text)
	}
}

func TestWorkspaceTrailerRecordsFullHistoricalReport(t *testing.T) {
	s := stage2Shop(t)
	s.agent(s.orders, "agent/compatible", map[string]string{"marker.txt": "api"})
	code, out, errs := run(t, s.orders, "gate", "orders:agent/compatible", "payments:agent/client", "--trailer")
	if code != 0 || !strings.HasPrefix(out, "Radar-Verified:") {
		t.Fatal(code, out, errs)
	}
	_, id, ok := strings.Cut(strings.TrimSpace(out), "run=")
	if !ok {
		t.Fatal(out)
	}
	report := invoke(t, s.orders, 0, "show", id)
	if report["verdict"] != "pass" || len(report["repos"].([]any)) != 2 {
		t.Fatal(report)
	}
}

func TestFailedOrUnverifiedGateCannotEmitTrailer(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-api", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-client", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})
	if code, out, errs := run(t, root, "gate", "agent-api", "agent-client", "--run", "--trailer"); code != 1 || strings.Contains(out, "Radar-Verified:") {
		t.Fatal(code, out, errs)
	}
	agent("agent-docs", map[string]string{"NOTES.md": "notes\n"})
	if code, out, errs := run(t, root, "gate", "agent-docs", "--run", "--trailer"); code != 1 || strings.Contains(out, "Radar-Verified:") {
		t.Fatal(code, out, errs)
	}
}

func TestConflictBriefMCPRejectsExecution(t *testing.T) {
	for _, name := range []string{"radar_land", "radar_conflict_brief", "radar_overlap"} {
		for _, spec := range mcpTools {
			if spec.name != name {
				continue
			}
			args, err := spec.args(map[string]any{"branches": "a,b", "apply": true})
			if err == nil {
				t.Fatal(name, args)
			}
		}
	}
	root, agent := checkoutRepo(t)
	agent("a", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("b", map[string]string{"backend.py": "def price():\n    return 3\n"})
	report := invoke(t, root, 1, "gate", "a", "b", "--conflict-brief")
	b, _ := json.Marshal(report["conflict_brief"])
	if !strings.Contains(string(b), "backend.py") || !strings.Contains(string(b), "hunks") {
		t.Fatal(string(b))
	}
}
