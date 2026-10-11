package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/testselection"
)

// A documentation-only change selects no test, so `gate --run` has no test
// evidence and stays NOT VERIFIED: Radar cannot show that no test reads the
// documents. The text says so plainly and points at the static check; the
// JSON and exit code are unchanged.
func TestGateRunDocumentationOnlyChangeStaysNotVerified(t *testing.T) {
	files := map[string]string{
		"app.py":      "def price():\n    return 1\n",
		"test_app.py": "import unittest\nfrom app import price\nclass T(unittest.TestCase):\n    def test(self): self.assertEqual(price(), 1)\n",
		"README.md":   "# app\n",
	}
	policy := filepath.Join(t.TempDir(), "strict.json")
	if err := os.WriteFile(policy, []byte(`{"version":1,"name":"explicit-strict","require":["textual_merge","no_breaking_contracts","integration_execution","test_selection"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	root, agent := agentRepo(t, files)
	agent("agent-docs", map[string]string{"README.md": "# app\n\nUsage.\n", "docs/guide.md": "guide\n"})
	for _, args := range [][]string{{"gate", "--run"}, {"gate", "--run", "--policy", policy}} {
		code, r := reliabilityReport(t, root, args...)
		if code != 1 || r.Gate.Verdict != gate.Blocked || len(r.Executions) != 0 {
			t.Fatalf("%v: code=%d gate=%+v", args, code, r.Gate)
		}
		assertScenarioCheck(t, r, "integration_execution", model.StatusUnknown)
		code, out, _ := run(t, root, args...)
		wants := []string{
			"? Tests           not verified — only documentation changed",
			"? not verified: only documentation changed; Radar selects no test for documentation and cannot show that no test reads it",
			"fix: For a documentation-only change, the static result (radar gate) is what Radar can verify.",
			"a policy applies to every change",
		}
		if len(args) == 2 {
			wants = append(wants, "Radar gate: NOT VERIFIED — only documentation changed, so no test was selected and none ran",
				"Next: documentation changes have no test evidence; the static check is what Radar verifies: radar gate")
		} else if strings.Contains(out, "the static check is what Radar verifies") || strings.Contains(out, "NOT VERIFIED — only documentation changed") {
			// An explicit policy may require test evidence even statically;
			// the static rerun would not answer it.
			t.Fatalf("explicit policy must not get the static next step:\n%s", out)
		}
		for _, want := range wants {
			if code != 1 || !strings.Contains(out, want) {
				t.Fatalf("%v: missing %q (code %d):\n%s", args, want, code, out)
			}
		}
		if strings.Contains(out, "PASS") {
			t.Fatalf("a documentation-only run must not pass:\n%s", out)
		}
	}
	code, out, _ := run(t, root, "gate")
	if code != 0 || !strings.Contains(out, "PASS (static)") || strings.Contains(out, "only documentation changed") {
		t.Fatalf("static gate is unchanged: %d\n%s", code, out)
	}
	mixedRoot, mixed := agentRepo(t, files)
	mixed("agent-mixed", map[string]string{"README.md": "# app\n\nUsage.\n", "app.py": "def price():\n    return 1  # same\n"})
	code, out, _ = run(t, mixedRoot, "gate", "--run")
	if code != 0 || strings.Contains(out, "only documentation changed") {
		t.Fatalf("code changes run tests as before: %d\n%s", code, out)
	}
}

func TestDocumentationNextStepOnlyWhenItIsTheWholeBlock(t *testing.T) {
	r := integration.Report{Changed: []string{"README.md"}, Selection: &testselection.Selection{}}
	r.Gate = gate.Result{Verdict: gate.Blocked, Policy: gate.Policy{Name: "supported-integration"}, Required: []gate.Check{{ID: "textual_merge", Status: model.StatusPassed}, {ID: "integration_execution", Status: model.StatusUnknown}}}
	again := rerunWith(options{verify: true, timeout: 10 * time.Minute, maxCommands: 16}, "main", nil)
	if got := blockedNext(r, gateGaps(r), again, options{}); got != "documentation changes have no test evidence; the static check is what Radar verifies: radar gate" {
		t.Fatal(got)
	}
	r.Gate.Required = append(r.Gate.Required, gate.Check{ID: "dependency_compatibility", Status: model.StatusUnknown})
	if got := blockedNext(r, gateGaps(r), again, options{}); strings.Contains(got, "static check") {
		t.Fatalf("another unmet requirement remains: %s", got)
	}
	if got := gateHeadline(gateReport{Report: r}, true); strings.Contains(got, "only documentation") || !strings.Contains(got, "dependency_compatibility") {
		t.Fatalf("headline must keep the other requirement: %s", got)
	}
}
