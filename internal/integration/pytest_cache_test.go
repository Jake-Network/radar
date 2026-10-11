package integration

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
)

func TestSuiteGeneratedPytestMetadataPreservesCandidateEvidence(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	root := t.TempDir()
	gitTest(t, root, "init", "-q")
	// A trusted unittest fixture generates pytest's standard cache path. This
	// exercises the real suite/source guard without depending on installed pytest.
	code := "import unittest\nfrom pathlib import Path\nclass TestCache(unittest.TestCase):\n def test_cache(self):\n  cache = Path('.pytest_cache/v/cache')\n  cache.mkdir(parents=True, exist_ok=True)\n  (cache / 'nodeids').write_text('[\"test_cache.py::test_cache\"]')\n"
	put(t, root, "test_cache.py", code)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "baseline")
	gitTest(t, root, "branch", "baseline")
	put(t, root, "test_cache.py", code+"# harmless test change\n")
	gitTest(t, root, "commit", "-qam", "change test")
	r, err := Preview(context.Background(), root, Options{Base: "baseline", Branches: []string{"HEAD"}, Verify: true, AllowExecution: true, Suite: "full", Timeout: 10 * time.Second, Policy: &gate.Policy{Version: 1, Require: []string{"integration_execution", "test_selection"}}})
	if err != nil {
		t.Fatal(err)
	}
	checkStatus(t, r, "integration_execution", model.StatusPassed)
	checkStatus(t, r, "test_selection", model.StatusPassed)
	if r.Gate.Verdict != gate.Pass || len(r.Executions) != 1 || r.Executions[0].SourceAfterExecution != "unchanged" || r.Executions[0].Observation.TestsRun != 1 {
		t.Fatalf("generated pytest metadata invalidated passing suite: %+v", r)
	}
}
