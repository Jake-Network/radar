package cli

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

// A change reaching more than 16 tests yields a bounded, actionable plan:
// required commands beyond the budget are reported and block the gate rather
// than crashing or being silently truncated.
func TestRecommendedSuiteBudgetBlocksInsteadOfTruncating(t *testing.T) {
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip(e)
	}
	root := gitRepo(t)
	put(t, root, "app.py", "def value():\n    return 1\n")
	for i := 0; i < 20; i++ {
		put(t, root, fmt.Sprintf("test_app_%02d.py", i), "import unittest\nimport app\nclass Case(unittest.TestCase):\n    def test_value(self):\n        self.assertEqual(app.value(), 1)\n")
	}
	put(t, root, ".radar/policy.json", `{"version":1,"require":["textual_merge","no_breaking_contracts","integration_execution"]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	base := gitTest(t, root, "rev-parse", "HEAD")
	put(t, root, "app.py", "def value():\n    return 1  # touched\n")
	gitTest(t, root, "commit", "-qam", "touch app")

	preview := invoke(t, root, 0, "check", "--base", base, "--head", "HEAD", "--suite", "targeted")
	sel := preview["selection"].(map[string]any)
	if len(sel["commands"].([]any)) != 16 || len(sel["omitted"].([]any)) != 4 || len(sel["blocking"].([]any)) != 4 {
		t.Fatalf("read-only preview: %v", sel)
	}

	args := []string{"merge-check", "--base", base, "--branches", "HEAD", "--policy", ".radar/policy.json", "--verify", "--allow-execution", "--suite", "recommended"}
	r := invoke(t, root, 1, args...)
	if r["gate"].(map[string]any)["verdict"] != "blocked" {
		t.Fatal(r["gate"])
	}
	if len(r["executions"].([]any)) != 16 {
		t.Fatal("budgeted commands not executed", len(r["executions"].([]any)))
	}
	omitted := r["selection"].(map[string]any)["omitted"].([]any)
	if len(omitted) != 4 || omitted[0].(map[string]any)["reason"] != "budget_exceeded" {
		t.Fatal(omitted)
	}
	r = invoke(t, root, 0, append(args, "--max-commands", "20")...)
	if r["gate"].(map[string]any)["verdict"] != "pass" || len(r["executions"].([]any)) != 20 {
		t.Fatal(r["gate"], r["checks"])
	}
	invoke(t, root, 2, append(args[:len(args)-2], "--suite", "everything")...)
}

// A runner absent from the private candidate is blocked, never executed as a
// failure and never counted as passed.
func TestUnavailableRunnerBlocksRequiredVerification(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "web/package.json", `{"name":"web","devDependencies":{"jest":"29.7.0"}}`)
	put(t, root, "web/sum.js", "module.exports = (a, b) => a + b;\n")
	put(t, root, "web/sum.test.js", "const sum = require('./sum');\ntest('adds', () => expect(sum(1, 2)).toBe(3));\n")
	put(t, root, ".radar/policy.json", `{"version":1,"require":["integration_execution"]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	base := gitTest(t, root, "rev-parse", "HEAD")
	put(t, root, "web/sum.js", "module.exports = (a, b) => b + a;\n")
	gitTest(t, root, "commit", "-qam", "change")
	r := invoke(t, root, 1, "merge-check", "--base", base, "--branches", "HEAD", "--policy", ".radar/policy.json", "--verify", "--allow-execution", "--suite", "targeted")
	if r["gate"].(map[string]any)["verdict"] != "blocked" || r["executions"] != nil {
		t.Fatal(r["gate"], r["executions"])
	}
	omitted := r["selection"].(map[string]any)["omitted"].([]any)
	if len(omitted) != 1 || omitted[0].(map[string]any)["reason"] != "environment_unavailable" || !strings.Contains(omitted[0].(map[string]any)["explanation"].(string), "node_modules") {
		t.Fatal(omitted)
	}
}

// Same-directory node:test files are grouped into one invocation and a real
// failure in the group fails the gate.
func TestGroupedSuiteObservesFailures(t *testing.T) {
	if _, e := exec.LookPath("node"); e != nil {
		t.Skip(e)
	}
	root := gitRepo(t)
	put(t, root, "lib.js", "exports.double = (x) => x * 2;\n")
	for _, name := range []string{"a", "b", "c"} {
		put(t, root, name+".test.js", "const test = require('node:test');\nconst assert = require('node:assert');\nconst {double} = require('./lib');\ntest('"+name+"', () => assert.strictEqual(double(2), 4));\n")
	}
	put(t, root, ".radar/policy.json", `{"version":1,"require":["integration_execution"]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	base := gitTest(t, root, "rev-parse", "HEAD")
	put(t, root, "lib.js", "exports.double = (x) => x * 3;\n")
	gitTest(t, root, "commit", "-qam", "regression")
	r := invoke(t, root, 1, "merge-check", "--base", base, "--branches", "HEAD", "--policy", ".radar/policy.json", "--verify", "--allow-execution", "--suite", "targeted")
	executions := r["executions"].([]any)
	if r["gate"].(map[string]any)["verdict"] != "fail" || len(executions) != 1 {
		t.Fatal(r["gate"], executions)
	}
	command := executions[0].(map[string]any)["command"].([]any)
	if len(command) != 6 {
		t.Fatal("not grouped", command)
	}
}
