package evidence

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/planning"
)

func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for path, data := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(data), 0700); err != nil {
			t.Fatal(err)
		}
	}
	git(t, root, "init", "-q")
	git(t, root, "add", ".")
	git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture")
	return root
}
func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	b, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git: %v %s", err, b)
	}
	return strings.TrimSpace(string(b))
}
func plan(command []string) planning.Plan {
	return planning.Plan{SchemaVersion: "1", FeatureID: "test", Intent: "verification", Acceptance: []planning.Criterion{{ID: "criterion", Intent: "real tests pass", Rule: &planning.Rule{Kind: "test_run", Command: command}}}}
}
func needPython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required")
	}
}

const pythonTest = "import unittest\nclass Example(unittest.TestCase):\n def test_actual(self):\n  self.assertEqual(2+2,4)\n"

func TestImmutableRunAndBinding(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"test_example.py": pythonTest})
	argv := []string{"python3", "-m", "unittest", "discover"}
	p := plan(argv)
	sha := git(t, root, "rev-parse", "HEAD")
	// A dirty checkout does not change the immutable snapshot being verified.
	if err := os.WriteFile(filepath.Join(root, "test_example.py"), []byte("BROKEN UNCOMMITTED CODE"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := Run(context.Background(), root, "HEAD", p, argv, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "passed" || r.TestsRun != 1 || r.Revision != sha {
		t.Fatalf("unexpected record: %+v", r)
	}
	if err = Validate(r, root, sha, p, "criterion", argv); err != nil {
		t.Fatal(err)
	}
	if err = Validate(r, root, "different", p, "criterion", argv); err == nil {
		t.Fatal("accepted stale commit")
	}
	changed := p
	changed.Intent = "changed"
	if err = Validate(r, root, sha, changed, "criterion", argv); err == nil {
		t.Fatal("accepted altered plan")
	}
	if err = Validate(r, root, sha, p, "undeclared", argv); err == nil {
		t.Fatal("accepted unrelated criterion")
	}
	tampered := r
	tampered.ExitCode = 23
	if err = Validate(tampered, root, sha, p, "criterion", argv); err == nil {
		t.Fatal("accepted edited evidence")
	}
	data, _ := os.ReadFile(filepath.Join(root, "test_example.py"))
	if string(data) != "BROKEN UNCOMMITTED CODE" {
		t.Fatal("runner modified checkout")
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), "BROKEN") || strings.Contains(string(encoded), "test_actual") {
		t.Fatal("record persisted raw test output")
	}
}
func TestFailedTests(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"test_example.py": strings.Replace(pythonTest, "2+2,4", "2+2,5", 1)})
	argv := []string{"python3", "-m", "unittest", "discover"}
	r, err := Run(context.Background(), root, "HEAD", plan(argv), argv, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "failed" || r.ExitCode == 0 {
		t.Fatalf("failure accepted %+v", r)
	}
}
func TestNoTestsAndGenericSuccessUnknown(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"empty.py": "pass\n"})
	for _, argv := range [][]string{{"python3", "-m", "unittest", "discover"}, {"python3", "-c", "print('not a test')"}} {
		r, err := Run(context.Background(), root, "HEAD", plan(argv), argv, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != "unknown" || r.TestsRun != 0 {
			t.Fatalf("empty success accepted %+v", r)
		}
	}
}
func TestTimeoutAndCancellation(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"empty.py": "pass\n"})
	argv := []string{"python3", "-c", "import time; time.sleep(30)"}
	start := time.Now()
	r, err := Run(context.Background(), root, "HEAD", plan(argv), argv, 80*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "timeout" || time.Since(start) > 3*time.Second {
		t.Fatalf("not bounded: %+v", r)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = Run(ctx, root, "HEAD", plan(argv), argv, time.Second); err == nil {
		t.Fatal("cancelled analysis accepted")
	}
}
func TestUndeclaredAndUnsafeCommandsRejected(t *testing.T) {
	root := fixture(t, map[string]string{"file": "data"})
	argv := []string{"python3", "-c", "print('never executed')"}
	if _, err := Run(context.Background(), root, "HEAD", plan([]string{"different"}), argv, time.Second); err == nil {
		t.Fatal("undeclared command accepted")
	}
	if _, err := Run(context.Background(), root, "WORKTREE", plan(argv), argv, time.Second); err == nil {
		t.Fatal("mutable checkpoint accepted")
	}
	unsafe := []string{"../program"}
	if _, err := Run(context.Background(), root, "HEAD", plan(unsafe), unsafe, time.Second); err == nil {
		t.Fatal("escaping executable accepted")
	}
}
func TestSnapshotOmitsSymlinks(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"test_example.py": pythonTest})
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "outside")); err != nil {
		t.Skip(err)
	}
	git(t, root, "add", "outside")
	git(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "link")
	argv := []string{"python3", "-c", "import os; assert not os.path.exists('outside')"}
	r, err := Run(context.Background(), root, "HEAD", plan(argv), argv, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.ExitCode != 0 || r.Status != "unknown" {
		t.Fatalf("symlink materialized %+v", r)
	}
}
func TestHarnessCounts(t *testing.T) {
	cases := []struct {
		argv              []string
		output            string
		run, failed, skip int
	}{
		{[]string{"go", "test", "-json", "./..."}, "{\"Action\":\"pass\",\"Test\":\"TestA\"}\n{\"Action\":\"pass\",\"Test\":\"TestA/sub\"}\n{\"Action\":\"fail\",\"Test\":\"TestB\"}\n{\"Action\":\"skip\",\"Test\":\"TestC\"}\n{\"Action\":\"pass\",\"Package\":\"empty\"}\n", 2, 1, 1},
		{[]string{"go", "test", "./..."}, "ok package 0.01s", 0, 0, 0},
		{[]string{"python3", "-m", "unittest"}, "Ran 2 tests in 0.001s\n\nOK (skipped=2)\n", 0, 0, 2},
		{[]string{"python3", "-m", "unittest"}, "Ran 3 tests in 0.001s\n\nFAILED (failures=1, errors=1)\n", 3, 2, 0},
		{[]string{"python3", "-m", "pytest", "-q"}, "..F\n1 failed, 2 passed, 1 skipped in 0.12s\n", 3, 1, 1},
		{[]string{"uv", "run", "pytest"}, "============ 5 passed, 1 error in 1.01s ============\n", 6, 1, 0},
		{[]string{"npx", "jest", "--ci"}, "Tests:       1 failed, 2 skipped, 4 passed, 7 total\n", 5, 1, 2},
		{[]string{"npx", "vitest", "run"}, " \x1b[32m Test Files  2 passed (2)\x1b[0m\n      Tests  4 passed | 1 failed (5)\n", 5, 1, 0},
		{[]string{"node", "--test"}, "# tests 5\n# pass 3\n# fail 0\n# skipped 2\n", 3, 0, 2},
		{[]string{"cargo", "test"}, "test result: ok. 4 passed; 0 failed; 1 ignored; 0 measured\ntest result: ok. 0 passed; 0 failed; 0 ignored;", 4, 0, 1},
		{[]string{"true"}, "Ran 500 tests in 1s", 0, 0, 0},
	}
	for _, tc := range cases {
		got := harnessCounts(tc.argv, []byte(tc.output))
		if got.Run != tc.run || got.Failed != tc.failed || got.Skipped != tc.skip {
			t.Errorf("%v got %+v want run=%d failed=%d skipped=%d", tc.argv, got, tc.run, tc.failed, tc.skip)
		}
	}
	junit := `<testsuites><testsuite name="s"><testcase name="a"/><testcase name="b"><failure message="x"/></testcase><testcase name="c"><skipped/></testcase></testsuite></testsuites>`
	r, err := parseJUnit(strings.NewReader(junit))
	if err != nil || r.Run != 2 || r.Failed != 1 || r.Skipped != 1 {
		t.Fatalf("junit %+v %v", r, err)
	}
	if _, err = parseJUnit(strings.NewReader("<testsuites/>")); err == nil {
		t.Fatal("empty junit accepted")
	}
}
func TestBuildFailureIsErrorNotTestFailure(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"test_example.py": "import missing_dependency\n"})
	argv := []string{"python3", "test_example.py"}
	r, err := Run(context.Background(), root, "HEAD", plan(argv), argv, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "error" || r.TestsRun != 0 || !strings.Contains(r.OutputTail, "missing_dependency") {
		t.Fatalf("environment failure misclassified: %+v", r)
	}
	encoded, _ := json.Marshal(r)
	if strings.Contains(string(encoded), "missing_dependency") {
		t.Fatal("output tail persisted")
	}
}
func TestSetupLinkJUnitAndEnv(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{".gitignore": "deps/\n", "check.py": "import os, sys\nsys.path.insert(0, 'deps')\nimport helper\nassert os.environ.get('RADAR_FIXTURE_TOKEN') == 'ok'\nassert os.path.exists('prepared')\nopen('report.xml','w').write('<testsuite><testcase name=\"a\"/><testcase name=\"b\"/></testsuite>')\n"})
	if err := os.MkdirAll(filepath.Join(root, "deps"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "deps", "helper.py"), []byte("VALUE = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RADAR_FIXTURE_TOKEN", "ok")
	t.Setenv("RADAR_UNDECLARED_SECRET", "leak")
	argv := []string{"python3", "check.py"}
	p := plan(argv)
	p.Acceptance[0].Rule.Setup = [][]string{{"python3", "-c", "import os; assert 'RADAR_UNDECLARED_SECRET' not in os.environ; open('prepared','w').close()"}}
	p.Acceptance[0].Rule.Env = []string{"RADAR_FIXTURE_TOKEN"}
	p.Acceptance[0].Rule.Link = []string{"deps"}
	p.Acceptance[0].Rule.JUnit = "report.xml"
	r, err := Run(context.Background(), root, "HEAD", p, argv, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "passed" || r.TestsRun != 2 || r.Harness != "junit" || len(r.Env) != 1 {
		t.Fatalf("%+v\n%s", r, r.OutputTail)
	}
	p.Acceptance[0].Rule.Setup = [][]string{{"python3", "-c", "raise SystemExit(3)"}}
	r, err = Run(context.Background(), root, "HEAD", p, argv, 10*time.Second)
	if err != nil || r.Status != "error" || r.Phase != "setup" || r.ExitCode != 3 {
		t.Fatalf("setup failure: %+v %v", r, err)
	}
	conflicting := p
	conflicting.Constraints = []planning.Constraint{{ID: "c", Intent: "same command", Rule: &planning.Rule{Kind: "test_run", Command: argv}}}
	if _, err = Run(context.Background(), root, "HEAD", conflicting, argv, time.Second); err == nil {
		t.Fatal("conflicting execution declarations accepted")
	}
}
func TestGoModuleDependenciesResolveFromLocalCache(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip(err)
	}
	// Radar's own dependencies are in the local module cache when this test runs.
	out, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}|{{.Version}}", "github.com/google/uuid").Output()
	if err != nil || !strings.Contains(string(out), "|") {
		t.Skip("uuid module not cached")
	}
	version := strings.TrimSpace(strings.Split(string(out), "|")[1])
	sum, err := os.ReadFile(filepath.Join("..", "..", "go.sum"))
	if err != nil {
		t.Skip(err)
	}
	var lines []string
	for _, line := range strings.Split(string(sum), "\n") {
		if strings.HasPrefix(line, "github.com/google/uuid "+version) {
			lines = append(lines, line)
		}
	}
	root := fixture(t, map[string]string{
		"go.mod":     "module example.com/fixture\n\ngo 1.23\n\nrequire github.com/google/uuid " + version + "\n",
		"go.sum":     strings.Join(lines, "\n") + "\n",
		"id.go":      "package fixture\n\nimport \"github.com/google/uuid\"\n\nfunc New() string { return uuid.NewString() }\n",
		"id_test.go": "package fixture\n\nimport \"testing\"\n\nfunc TestNew(t *testing.T) { if New() == \"\" { t.Fatal(\"empty\") } }\n",
	})
	argv := []string{"go", "test", "-json", "./..."}
	r, err := Run(context.Background(), root, "HEAD", plan(argv), argv, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "passed" || r.TestsRun != 1 {
		t.Fatalf("module dependency not resolved offline: %+v\n%s", r, r.OutputTail)
	}
}
func TestOutputBounded(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"empty.py": "pass"})
	argv := []string{"python3", "-c", "print('x' * 2000000)"}
	r, err := Run(context.Background(), root, "HEAD", plan(argv), argv, time.Second*10)
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "unknown" || r.OutputDigest == "" {
		t.Fatalf("unbounded output accepted %+v", r)
	}
}

func TestConstraintProofBinding(t *testing.T) {
	needPython(t)
	root := fixture(t, map[string]string{"test_example.py": pythonTest})
	argv := []string{"python3", "-m", "unittest", "discover"}
	p := plan(argv)
	p.Constraints = []planning.Constraint{{ID: "isolation", Intent: "declared test constraint", Rule: &planning.Rule{Kind: "test_run", Command: argv}}}
	r, e := Run(context.Background(), root, "HEAD", p, argv, 10*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	if e = Validate(r, root, r.Revision, p, "constraint:isolation", argv); e != nil {
		t.Fatal("constraint coverage not bound", e)
	}
}
