package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/coverage"
	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/runners"
	"github.com/Jake-Network/radar/internal/testselection"
)

func runtimeFixture(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python unavailable")
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	put(t, root, "config/limits.json", `{"limit":2}`)
	// Test constructs the path at runtime: static literal-file matching cannot
	// establish this relationship, but the audit observer records the read.
	put(t, root, "tests/test_runtime.py", "import unittest, json, os\nclass Runtime(unittest.TestCase):\n def test_read(self):\n  name = os.path.join('config', 'limits' + '.json')\n  with open(name) as f: self.assertGreater(json.load(f)['limit'],0)\n")
	put(t, root, "tests/__init__.py", "")
	put(t, root, "app.py", "VALUE = 1\n")
	put(t, root, "uv.lock", "lock-one")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	gitTest(t, root, "branch", "baseline")
	return root
}

func selectionForRuntime(r Report, mode string) (testselection.Selection, error) {
	return testselection.Plan(*r.VerificationProposal, r.Changed, mode, 16)
}

func TestRuntimeProfileCacheAndLockfileInvalidation(t *testing.T) {
	t.Setenv("RADAR_STATE_DIR", t.TempDir())
	root := runtimeFixture(t)
	cfg := runners.Config{Version: 1, Profiles: []runners.Profile{{Root: ".", Prefix: []string{".venv/bin/python"}, Setup: [][]string{{"python3", "-m", "venv", "--without-pip", "--copies", ".venv"}}, Lockfiles: []string{"uv.lock"}, Artifacts: []string{".venv"}}}}
	data, _ := json.Marshal(cfg)
	put(t, root, runners.Manifest, string(data))
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "declare runner")
	gitTest(t, root, "branch", "-f", "baseline")
	gitTest(t, root, "checkout", "-qb", "feature")
	put(t, root, "app.py", "VALUE = 2\n")
	gitTest(t, root, "commit", "-qam", "change app")
	gitTest(t, root, "checkout", "baseline")
	o := Options{Base: "baseline", Branches: []string{"feature"}, Verify: true, AllowExecution: true, Suite: "full", Timeout: 30 * time.Second, MaxCommands: 4}
	before := gitTest(t, root, "status", "--porcelain")
	first, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	checkStatus(t, first, "integration_execution", model.StatusPassed)
	if len(first.RunnerPreparations) != 1 || first.RunnerPreparations[0].CacheHit || first.RunnerPreparations[0].SetupCommands != 1 {
		t.Fatal(first.RunnerPreparations)
	}
	second, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	checkStatus(t, second, "integration_execution", model.StatusPassed)
	if !second.RunnerPreparations[0].CacheHit || second.RunnerPreparations[0].SetupCommands != 0 {
		t.Fatal(second.RunnerPreparations)
	}
	gitTest(t, root, "checkout", "feature")
	put(t, root, "uv.lock", "lock-two")
	gitTest(t, root, "commit", "-qam", "change lock")
	gitTest(t, root, "checkout", "baseline")
	third, err := Preview(context.Background(), root, o)
	if err != nil {
		t.Fatal(err)
	}
	checkStatus(t, third, "integration_execution", model.StatusPassed)
	if third.RunnerPreparations[0].CacheHit || third.RunnerPreparations[0].SetupCommands != 1 {
		t.Fatal(third.RunnerPreparations)
	}
	if after := gitTest(t, root, "status", "--porcelain"); after != before {
		t.Fatal("source checkout changed", before, after)
	}
	if _, err := os.Stat(filepath.Join(root, ".venv")); !os.IsNotExist(err) {
		t.Fatal("dependency cache leaked into user checkout")
	}
}

func TestRuntimeDataCoverageSelectsTestsAndRejectsStaleBaseline(t *testing.T) {
	t.Setenv("RADAR_STATE_DIR", t.TempDir())
	root := runtimeFixture(t)
	ctx := context.Background()
	gitTest(t, root, "checkout", "-qb", "data")
	put(t, root, "config/limits.json", `{"limit":3}`)
	gitTest(t, root, "commit", "-qam", "change data")
	gitTest(t, root, "checkout", "baseline")
	static := Options{Base: "baseline", Branches: []string{"data"}, SuggestTests: true, Timeout: 30 * time.Second}
	initial, err := Preview(ctx, root, static)
	if err != nil {
		t.Fatal(err)
	}
	initialPlan, err := selectionForRuntime(initial, "targeted")
	if err != nil || len(initialPlan.Uncovered) != 1 {
		t.Fatal(initialPlan, err)
	}
	// Observe the baseline through a separate source-only branch so the report's
	// read files still match baseline contents when data changes later.
	gitTest(t, root, "checkout", "-qb", "observe")
	put(t, root, "app.py", "VALUE = 2\n")
	gitTest(t, root, "commit", "-qam", "observation branch")
	gitTest(t, root, "checkout", "baseline")
	observed, err := Preview(ctx, root, Options{Base: "baseline", Branches: []string{"observe"}, Suite: "full", Verify: true, AllowExecution: true, ObserveCoverage: true, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	checkStatus(t, observed, "integration_execution", model.StatusPassed)
	after, err := Preview(ctx, root, static)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := selectionForRuntime(after, "targeted")
	if err != nil || len(selected.Uncovered) != 0 || len(selected.Commands) == 0 {
		t.Fatal(selected, err)
	}
	// Updating a previously observed test invalidates its relationship.
	gitTest(t, root, "checkout", "baseline")
	put(t, root, "tests/test_runtime.py", "import unittest\nclass Changed(unittest.TestCase):\n def test_no_read(self): self.assertTrue(True)\n")
	gitTest(t, root, "commit", "-qam", "replace observed test")
	stale, err := Preview(ctx, root, static)
	if err != nil {
		t.Fatal(err)
	}
	stalePlan, err := selectionForRuntime(stale, "targeted")
	if err != nil || len(stalePlan.Uncovered) != 1 {
		t.Fatal("stale observation selected data test", stalePlan, err)
	}
}

// An unrelated baseline is explicitly stale even if recorded bytes match.
func TestRuntimeCoverageUnrelatedBaselineRejected(t *testing.T) {
	state := t.TempDir()
	t.Setenv("RADAR_STATE_DIR", state)
	root := runtimeFixture(t)
	ctx := context.Background()
	base := gitTest(t, root, "rev-parse", "baseline")
	identity, err := evidence.RepositoryIdentity(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	store, err := coverage.Open(filepath.Join(state, "coverage.db"))
	if err != nil {
		t.Fatal(err)
	}
	provider := indexer.NewCommit(root, base)
	defer provider.Close()
	if err := store.RecordAtBaseline(ctx, provider, identity, "ffffffffffffffffffffffffffffffffffffffff", base, "command", []string{"tests/test_runtime.py"}, []string{"config/limits.json"}); err != nil {
		t.Fatal(err)
	}
	store.Close()
	r := Report{Base: base}
	edges := loadCoverage(ctx, root, root, &r)
	if len(edges) != 1 || !edges[0].Stale {
		t.Fatal(edges, r.Diagnostics)
	}
}
