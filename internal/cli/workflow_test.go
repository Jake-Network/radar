package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Jake-Network/radar/internal/planning"
	"os/exec"
	"testing"
)

func testCommand(t *testing.T, root, plan, sha string, allow bool, want int) map[string]any {
	t.Helper()
	args := []string{"test", "--plan", plan, "--ref", sha, "--root", root, "--json"}
	if allow {
		args = append(args, "--allow-execution")
	}
	args = append(args, "--", "python3", "-m", "unittest", "test_real")
	var out, stderr bytes.Buffer
	code := Run(context.Background(), args, &out, &stderr)
	if code != want {
		t.Fatalf("test exit %d want %d %s %s", code, want, out.String(), stderr.String())
	}
	var record map[string]any
	if e := json.Unmarshal(out.Bytes(), &record); e != nil {
		t.Fatal(e, out.String())
	}
	return record
}
func TestApprovedCheckpointEvidenceLifecycle(t *testing.T) {
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip(e)
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	gitTest(t, root, "config", "user.name", "Radar Fixture")
	gitTest(t, root, "config", "user.email", "test@example.invalid")
	put(t, root, ".gitignore", ".radar/state*\n.radar/config.json\n*.plan.json\n")
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[]}`)
	put(t, root, "test_real.py", "import unittest\nclass Case(unittest.TestCase):\n def test_real(self):\n  self.assertEqual(1+1,2)\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	sha := gitTest(t, root, "rev-parse", "HEAD")
	invoke(t, root, 0, "init")
	invoke(t, root, 0, "index", "--ref", sha)
	p := planning.Plan{SchemaVersion: "1", FeatureID: "real-test", Intent: "Verify observed test", BaseRevision: sha, Requirements: []planning.Requirement{{ID: "r", Intent: "Regression test passes"}}, Acceptance: []planning.Criterion{{ID: "test", Requirement: "r", Intent: "execute real test", Rule: &planning.Rule{Kind: "test_run", Command: []string{"python3", "-m", "unittest", "test_real"}}}}, Tasks: []planning.Task{{ID: "t", Intent: "Verify change", Requirements: []string{"r"}, Acceptance: []string{"test"}, DependsOn: []string{}}}}
	raw, _ := json.Marshal(p)
	put(t, root, "candidate.plan.json", string(raw))
	invoke(t, root, 0, "approve", "--plan", "candidate.plan.json", "--reviewer", "fixture-declaration", "--output", "approved.plan.json")
	testCommand(t, root, "approved.plan.json", sha, false, 2)
	record := testCommand(t, root, "approved.plan.json", sha, true, 0)
	if record["status"] != "passed" || record["tests_run"] != float64(1) {
		t.Fatal(record)
	}
	id := record["id"].(string)
	good := invoke(t, root, 0, "verify", "--plan", "approved.plan.json", "--ref", sha, "--evidence", id)
	if good["status"] != "passed" || good["authoritative"] != true {
		t.Fatal(good)
	}
	approved, e := planning.Load(root + "/approved.plan.json")
	if e != nil {
		t.Fatal(e)
	}
	approved.Acceptance[0].Rule.EvidenceID = id
	raw, _ = json.Marshal(approved)
	put(t, root, "attached.plan.json", string(raw))
	attached := invoke(t, root, 0, "verify", "--plan", "attached.plan.json", "--ref", sha)
	if attached["status"] != "passed" {
		t.Fatal("attached proof not resolved", attached)
	}
	// A success at the old checkpoint is not evidence for the new implementation.
	put(t, root, "test_real.py", "import unittest\nclass Case(unittest.TestCase):\n def test_real(self):\n  self.assertEqual(1+1,3)\n")
	gitTest(t, root, "add", "test_real.py")
	gitTest(t, root, "commit", "-qm", "regression")
	broken := gitTest(t, root, "rev-parse", "HEAD")
	stale := invoke(t, root, 0, "verify", "--plan", "approved.plan.json", "--ref", broken, "--evidence", id)
	if stale["status"] != "unknown" {
		t.Fatal("stale evidence reused", stale)
	}
	badRecord := testCommand(t, root, "approved.plan.json", broken, true, 1)
	bad := invoke(t, root, 1, "verify", "--plan", "approved.plan.json", "--ref", broken, "--evidence", badRecord["id"].(string))
	if bad["status"] != "failed" || bad["authoritative"] != true {
		t.Fatal(bad)
	}
	// Unrelated history cannot become an authoritative implementation checkpoint.
	gitTest(t, root, "checkout", "--orphan", "unrelated")
	gitTest(t, root, "commit", "-qm", "unrelated history")
	unrelated := gitTest(t, root, "rev-parse", "HEAD")
	unbound := invoke(t, root, 0, "verify", "--plan", "approved.plan.json", "--ref", unrelated)
	if unbound["authoritative"] != false {
		t.Fatal("unrelated history authoritative", unbound)
	}
}
