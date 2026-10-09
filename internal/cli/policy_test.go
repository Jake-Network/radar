package cli

import (
	"os/exec"
	"testing"
)

func TestReadOnlyPolicyPassesWithLimitedCoverage(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "svc.py", "def value(): return 1\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	put(t, root, ".radar/policy.json", `{"version":1,"require":["dependency_impact","source_stability","no_breaking_contracts"]}`)
	r := invoke(t, root, 0, "check", "--base", "HEAD", "--policy", ".radar/policy.json")
	if r["gate"].(map[string]any)["verdict"] != "pass" || r["status"] != "incomplete" || len(r["coverage"].([]any)) == 0 {
		t.Fatal(r)
	}
	put(t, root, ".radar/policy.json", `{"version":1,"require":["integration_tests"]}`)
	r = invoke(t, root, 1, "check", "--base", "HEAD", "--policy", ".radar/policy.json")
	if r["gate"].(map[string]any)["verdict"] != "blocked" {
		t.Fatal("missing required evidence passed", r)
	}
	invoke(t, root, 2, "check", "--base", "HEAD", "--policy", ".radar/policy.json", "--require-complete")
}
func TestMergePolicyPassFailAndEnvironmentError(t *testing.T) {
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip(e)
	}
	root := gitRepo(t)
	put(t, root, "test_real.py", "import unittest\nclass Case(unittest.TestCase):\n def test_real(self): self.assertEqual(1+1,2)\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	put(t, root, ".radar/policy.json", `{"version":1,"require":["textual_merge","no_breaking_contracts","integration_execution"]}`)
	prefix := []string{"merge-check", "--base", "HEAD", "--branches", "HEAD", "--policy", ".radar/policy.json"}
	r := invoke(t, root, 1, prefix...)
	if r["gate"].(map[string]any)["verdict"] != "blocked" {
		t.Fatal(r)
	}
	args := append(append([]string{}, prefix...), "--verify", "--allow-execution", "--", "python3", "-m", "unittest", "test_real")
	r = invoke(t, root, 0, args...)
	if r["gate"].(map[string]any)["verdict"] != "pass" || r["status"] != "incomplete" {
		t.Fatal(r)
	}
	put(t, root, "test_real.py", "import unittest\nclass Case(unittest.TestCase):\n def test_real(self): self.assertEqual(1+1,3)\n")
	gitTest(t, root, "add", "test_real.py")
	gitTest(t, root, "commit", "-qm", "fail invariant")
	r = invoke(t, root, 1, args...)
	if r["gate"].(map[string]any)["verdict"] != "fail" {
		t.Fatal(r)
	}
	args = append(append([]string{}, prefix...), "--verify", "--allow-execution", "--", "radar_missing_test_tool")
	r = invoke(t, root, 2, args...)
	if r["gate"].(map[string]any)["verdict"] != "error" {
		t.Fatal(r)
	}
}
func TestSupportedContractViolationFailsPolicy(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "schema.json", `{"type":"object","properties":{"total":{"type":"integer"}}}`)
	put(t, root, "consumer.ts", "export function use(x:any){return x.total};\n")
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"summary","schema":"schema.json","pointer":"","consumer":"consumer.ts","fields":["total"],"direction":"response"}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	base := gitTest(t, root, "rev-parse", "HEAD")
	put(t, root, "schema.json", `{"type":"object","properties":{}}`)
	gitTest(t, root, "add", "schema.json")
	gitTest(t, root, "commit", "-qm", "remove declared response field")
	put(t, root, ".radar/policy.json", `{"version":1,"require":["no_breaking_contracts"]}`)
	r := invoke(t, root, 1, "check", "--base", base, "--head", "HEAD", "--policy", ".radar/policy.json")
	if r["gate"].(map[string]any)["verdict"] != "fail" {
		t.Fatal(r)
	}
}

func TestRequiredContractWithMissingSchemaIsBlocked(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "consumer.ts", "export const use = (x: any) => x.total;\n")
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"checkout","schema":"missing.json","pointer":"","consumer":"consumer.ts","direction":"response","fields":["total"]}]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "missing schema")
	put(t, root, ".radar/policy.json", `{"version":1,"require":["no_breaking_contracts"]}`)
	r := invoke(t, root, 1, "check", "--base", "HEAD", "--policy", ".radar/policy.json")
	if r["gate"].(map[string]any)["verdict"] != "blocked" {
		t.Fatal(r)
	}
}
