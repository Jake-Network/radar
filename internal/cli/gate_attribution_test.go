package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func requirePython(t *testing.T) {
	t.Helper()
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip(e)
	}
}

// executionFailure returns the attribution of the gate's failed test command.
func executionFailure(t *testing.T, r map[string]any) map[string]any {
	t.Helper()
	for _, raw := range r["findings"].([]any) {
		f := raw.(map[string]any)
		if f["code"] != "integration_execution_failed" {
			continue
		}
		a, ok := r["attribution"].(map[string]any)[f["id"].(string)].(map[string]any)
		if !ok {
			t.Fatal("failed command has no attribution", r["attribution"])
		}
		return a
	}
	t.Fatal("no failed test command", r["findings"])
	return nil
}

func leadBranches(a map[string]any) []string {
	out := []string{}
	for _, l := range a["leads"].([]any) {
		out = append(out, l.(map[string]any)["branch"].(string))
	}
	return out
}

func results(o map[string]any) []string {
	out := []string{}
	for _, run := range o["runs"].([]any) {
		out = append(out, run.(map[string]any)["result"].(string))
	}
	return out
}

func TestGateObservesThatOnlyTheCombinationFails(t *testing.T) {
	requirePython(t)
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-frontend", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})

	a := executionFailure(t, invoke(t, root, 1, "gate", "--run", "--attribution-runs", "8"))
	o := a["observed"].(map[string]any)
	// Imports reach only backend.py; the test reads frontend.ts at runtime.
	// Reruns observe that both branches are needed.
	if o["outcome"] != "isolated" || o["evidence"] != "observed_test" || !strings.HasPrefix(a["basis"].(string), "observed") {
		t.Fatal("conclusive observation", a)
	}
	if got := strings.Join(leadBranches(a), ","); got != "agent-backend,agent-frontend" {
		t.Fatal("both branches are needed", got)
	}
	if got := strings.Join(results(o), ","); got != "passed,passed,passed" {
		t.Fatal("base and each branch alone pass", got)
	}
	code, out, _ := run(t, root, "gate", "--run", "--attribution-runs", "8")
	if code != 1 || !strings.Contains(out, "observed: fails with agent-backend + agent-frontend; each smaller combination passes (3 reruns)") || !strings.Contains(out, "look at: agent-backend (backend.py)") {
		t.Fatal("human report", out)
	}
	if strings.Contains(out, "import-based lead") {
		t.Fatal("a conclusive observation replaces the inferred lead", out)
	}
}

func TestGateAttributionRunsZeroKeepsTheInferredLead(t *testing.T) {
	requirePython(t)
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-frontend", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})
	r := invoke(t, root, 1, "gate", "--run", "--attribution-runs", "0")
	a := executionFailure(t, r)
	if a["observed"] != nil || r["observed_attributions"] != nil || !strings.Contains(a["basis"].(string), "inferred") {
		t.Fatal("no reruns", a)
	}
	if !strings.HasSuffix(r["next"].(string), "radar gate --run") {
		t.Fatal("rerun keeps the setting", r["next"])
	}
}

func TestGateObservesABranchThatFailsAlone(t *testing.T) {
	requirePython(t)
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 3\n"})
	agent("agent-docs", map[string]string{"NOTES.md": "notes\n"})
	a := executionFailure(t, invoke(t, root, 1, "gate", "--run", "--suite", "full", "--attribution-runs", "8"))
	if a["observed"].(map[string]any)["outcome"] != "isolated" || strings.Join(leadBranches(a), ",") != "agent-backend" {
		t.Fatal("agent-backend breaks the test by itself", a)
	}
	if _, out, _ := run(t, root, "gate", "--run", "--suite", "full", "--attribution-runs", "8"); !strings.Contains(out, "observed: fails with agent-backend alone; passes on the base alone") {
		t.Fatal("human report", out)
	}
}

func TestGateObservesAFailureAlreadyOnTheBase(t *testing.T) {
	requirePython(t)
	root, agent := agentRepo(t, map[string]string{
		"backend.py":    "def price():\n    return 1\n",
		"test_price.py": "import unittest\nfrom backend import price\nclass Price(unittest.TestCase):\n    def test_price(self):\n        self.assertEqual(price(), 5)\n",
	})
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 1\n\ndef tax():\n    return 0\n"})
	agent("agent-docs", map[string]string{"NOTES.md": "notes\n"})
	a := executionFailure(t, invoke(t, root, 1, "gate", "--run", "--attribution-runs", "8"))
	if a["observed"].(map[string]any)["outcome"] != "base" || len(a["leads"].([]any)) != 0 {
		t.Fatal("a failure on the base names no branch", a)
	}
	if got := results(a["observed"].(map[string]any)); strings.Join(got, ",") != "reproduced" {
		t.Fatal("the base run reproduces it and the search stops", got)
	}
	if _, out, _ := run(t, root, "gate", "--run", "--attribution-runs", "8"); !strings.Contains(out, "observed: already fails on main without any branch") {
		t.Fatal("human report", out)
	}
}

func TestGateDifferentSubsetFailureIsNotAReproduction(t *testing.T) {
	requirePython(t)
	root, agent := agentRepo(t, map[string]string{
		"a.py": "A = 0\n",
		"b.py": "B = 0\n",
		// test_x fails only with both flags; test_y fails with A but not B.
		"test_flags.py": "import unittest\nfrom a import A\nfrom b import B\nclass Flags(unittest.TestCase):\n    def test_x(self):\n        self.assertFalse(A and B)\n    def test_y(self):\n        self.assertTrue(B or not A)\n",
	})
	agent("agent-a", map[string]string{"a.py": "A = 1\n"})
	agent("agent-b", map[string]string{"b.py": "B = 1\n"})
	a := executionFailure(t, invoke(t, root, 1, "gate", "--run", "--attribution-runs", "8"))
	o := a["observed"].(map[string]any)
	if got := strings.Join(results(o), ","); got != "passed,different_failure,passed" {
		t.Fatal("agent-a alone fails test_y, not test_x", got)
	}
	if o["outcome"] != "inconclusive" || !strings.Contains(a["basis"].(string), "inferred") {
		t.Fatal("minimality is not established, so the inferred lead stays", a)
	}
	if _, out, _ := run(t, root, "gate", "--run", "--attribution-runs", "8"); !strings.Contains(out, "observed: reproduced with agent-a + agent-b, but not shown minimal: 1 smaller combination failed differently or could not run (3 reruns)") {
		t.Fatal("human report", out)
	}
}

func TestGateAttributionStopsAtItsBudget(t *testing.T) {
	requirePython(t)
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-frontend", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})
	a := executionFailure(t, invoke(t, root, 1, "gate", "--run", "--attribution-runs", "1"))
	o := a["observed"].(map[string]any)
	if o["outcome"] != "inconclusive" || len(o["runs"].([]any)) != 1 || len(o["reproducing"].([]any)) != 0 {
		t.Fatal("one run cannot attribute two branches", o)
	}
	if strings.Join(leadBranches(a), ",") != "agent-backend" || !strings.Contains(a["basis"].(string), "inferred") {
		t.Fatal("the inferred lead stays", a)
	}
	if _, out, _ := run(t, root, "gate", "--run", "--attribution-runs", "1"); !strings.Contains(out, "observed: no smaller combination tried reproduced it before the budget ran out (1 rerun; --attribution-runs)") {
		t.Fatal("human report", out)
	}
}

func TestGateRejectsAttributionRunsOutOfRange(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	if code, _, errs := run(t, root, "gate", "--run", "--attribution-runs", "65"); code != 2 || !strings.Contains(errs, "attribution runs") {
		t.Fatal(code, errs)
	}
}

func TestGateBaseRunThatWritesSourceIsNotABaseFailure(t *testing.T) {
	requirePython(t)
	// On the base the test rewrites backend.py before failing; that run is
	// not trustworthy, so the failure must not be called pre-existing.
	root, agent := agentRepo(t, map[string]string{
		"backend.py":    "def price():\n    return 1\n",
		"test_price.py": "import unittest\nfrom pathlib import Path\nfrom backend import price\nclass Price(unittest.TestCase):\n    def test_price(self):\n        if price() == 1:\n            Path('backend.py').write_text('def price():\\n    return 9\\n')\n        self.assertEqual(price(), 5)\n",
	})
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-docs", map[string]string{"NOTES.md": "notes\n"})
	a := executionFailure(t, invoke(t, root, 1, "gate", "--run", "--attribution-runs", "8"))
	o := a["observed"].(map[string]any)
	if o["outcome"] == "base" || results(o)[0] != "not_comparable" {
		t.Fatal("a source-modifying base run is not comparable", o)
	}
}

func TestGateBaseFailureDoesNotHideABranchFailure(t *testing.T) {
	requirePython(t)
	// The base already fails test_old; agent-backend adds a test_new failure.
	root, agent := agentRepo(t, map[string]string{
		"backend.py":    "def price():\n    return 1\n",
		"test_price.py": "import unittest\nfrom backend import price\nclass Price(unittest.TestCase):\n    def test_old(self):\n        self.fail('known')\n    def test_new(self):\n        self.assertEqual(price(), 1)\n",
	})
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-docs", map[string]string{"NOTES.md": "notes\n"})
	a := executionFailure(t, invoke(t, root, 1, "gate", "--run", "--attribution-runs", "8"))
	o := a["observed"].(map[string]any)
	if o["outcome"] == "base" || results(o)[0] != "different_failure" {
		t.Fatal("the base shows only part of the failure", o)
	}
	if !strings.Contains(strings.Join(leadBranches(a), ","), "agent-backend") {
		t.Fatal("agent-backend stays a lead", a)
	}
}

func TestGateAttributionDefaultsToNoAdditionalExecutions(t *testing.T) {
	requirePython(t)
	for _, tc := range []struct {
		name       string
		args       []string
		executions int
	}{
		{"default", nil, 1},
		{"explicit zero", []string{"--attribution-runs", "0"}, 1},
		{"opt in", []string{"--attribution-runs", "8"}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			counter := filepath.Join(t.TempDir(), "executions")
			root, agent := agentRepo(t, map[string]string{
				"backend.py":    "def price():\n    return 1\n",
				"test_price.py": fmt.Sprintf("import unittest\nfrom pathlib import Path\nfrom backend import price\nclass Price(unittest.TestCase):\n    def test_price(self):\n        counter = Path(%q)\n        with counter.open('a') as log:\n            log.write('run\\n')\n        self.assertEqual(price(), 1)\n", counter),
			})
			agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
			args := append([]string{"gate", "--run"}, tc.args...)
			report := invoke(t, root, 1, args...)
			lead := executionFailure(t, report)
			observed := lead["observed"] != nil
			if observed != (tc.executions > 1) {
				t.Fatalf("observed attribution = %v, report: %v", observed, lead)
			}
			if !observed && !strings.Contains(lead["basis"].(string), "inferred") {
				t.Fatalf("default retains inferred repair lead: %v", lead)
			}
			data, err := os.ReadFile(counter)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(data), "run\n"); got != tc.executions {
				t.Fatalf("executions = %d, want %d", got, tc.executions)
			}
			want := "radar gate --run"
			if observed {
				want += " --attribution-runs 8"
			}
			if !strings.HasSuffix(report["next"].(string), want) {
				t.Fatalf("rerun command: %v", report["next"])
			}
		})
	}
}
