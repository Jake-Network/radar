package landing

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
)

func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", root, "-c", "user.name=test", "-c", "user.email=test@invalid", "-c", "core.hooksPath=" + os.DevNull}, args...)...)
	out, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, out)
	}
	return strings.TrimSpace(string(out))
}

func TestApplyImportsOnlyNewCandidateObjectsAndRepeatsWithoutPack(t *testing.T) {
	root := fixture(t)
	for i := 0; i < 18; i++ {
		put(t, root, "history.txt", strings.Repeat(fmt.Sprintf("history-%02d\n", i), 1024))
		git(t, root, "add", "history.txt")
		git(t, root, "commit", "-qm", fmt.Sprintf("history %d", i))
	}
	git(t, root, "branch", "-f", "baseline")
	branch(t, root, "a", "baseline", "a.md", "a\n")
	branch(t, root, "b", "baseline", "b.md", "b\n")
	git(t, root, "checkout", "-q", "baseline")
	git(t, root, "repack", "-ad")
	existing, err := filepath.Glob(filepath.Join(root, ".git", "objects", "pack", "*.idx"))
	if err != nil {
		t.Fatal(err)
	}
	old := map[string]bool{}
	for _, p := range existing {
		old[p] = true
	}
	plan, err := Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := Apply(context.Background(), root, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Outcome.Published || !applied.Outcome.ObjectsImported {
		t.Fatalf("outcome: %+v", applied.Outcome)
	}
	after, err := filepath.Glob(filepath.Join(root, ".git", "objects", "pack", "*.idx"))
	if err != nil {
		t.Fatal(err)
	}
	newPacks := 0
	objects := 0
	for _, p := range after {
		if old[p] {
			continue
		}
		newPacks++
		for _, line := range strings.Split(git(t, root, "verify-pack", "-v", p), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 5 && (fields[1] == "commit" || fields[1] == "tree" || fields[1] == "blob") {
				objects++
			}
		}
	}
	if newPacks != 1 || objects < 2 || objects > 6 {
		t.Fatalf("history repacked: new packs=%d new objects=%d", newPacks, objects)
	}
	again, err := Apply(context.Background(), root, plan)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Outcome.Published || again.Outcome.ObjectsImported {
		t.Fatalf("repeat imported objects: %+v", again.Outcome)
	}
	last, err := filepath.Glob(filepath.Join(root, ".git", "objects", "pack", "*.idx"))
	if err != nil || !reflect.DeepEqual(after, last) {
		t.Fatalf("repeat changed pack set: before=%v after=%v error=%v", after, last, err)
	}
}

func TestApplyOutcomePreservesRefusalReasonAndMovingInput(t *testing.T) {
	root := fixture(t)
	branch(t, root, "a", "baseline", "a.md", "a\n")
	branch(t, root, "b", "baseline", "b.md", "b\n")
	git(t, root, "checkout", "-q", "baseline")
	plan, err := Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ApplyWithOptions(context.Background(), root, plan, Options{Finalize: func(ctx context.Context, r *integration.Report) error {
		git(t, root, "update-ref", "refs/heads/a", git(t, root, "rev-parse", "b"))
		return nil
	}})
	if err == nil || !strings.Contains(result.Outcome.Reason, "changed before publication") || result.Outcome.Published || !result.Outcome.ObjectsImported || result.Ref != "" {
		t.Fatalf("moving input outcome: %+v %v", result, err)
	}
	if refs := git(t, root, "for-each-ref", "--format=%(refname)", "refs/radar/integration/"); refs != "" {
		t.Fatal("published moving input", refs)
	}
}

func TestApplyEnforcesSelectedPolicyAndFinalConfigurationBeforePublication(t *testing.T) {
	root := fixture(t)
	branch(t, root, "a", "baseline", "a.md", "a\n")
	git(t, root, "checkout", "-q", "baseline")
	plan, err := Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	policy := gate.Policy{Version: 1, Name: "requires-runtime", Require: []string{"textual_merge", "integration_execution"}}
	applied, err := ApplyWithOptions(context.Background(), root, plan, Options{Analysis: integration.Options{Policy: &policy}})
	if err == nil || applied.Report.Gate.Verdict != gate.Blocked {
		t.Fatalf("custom required policy bypassed: %+v %v", applied, err)
	}
	if refs := git(t, root, "for-each-ref", "--format=%(refname)", "refs/radar/integration/"); refs != "" {
		t.Fatalf("blocked policy published %s", refs)
	}
	calls := 0
	applied, err = ApplyWithOptions(context.Background(), root, plan, Options{Finalize: func(ctx context.Context, r *integration.Report) error {
		calls++
		// Finalization occurs after object import and before public ref visibility.
		git(t, root, "cat-file", "-e", r.CandidateCommit)
		if refs := git(t, root, "for-each-ref", "--format=%(refname)", "refs/radar/integration/"); refs != "" {
			t.Fatalf("finalizer ran after publication: %s", refs)
		}
		check := gate.Check{ID: "configuration_stability", Status: model.StatusIncomplete, Evidence: model.Unknown, Explanation: "Selected policy changed after capture."}
		r.Checks = append(r.Checks, check)
		p := r.Gate.Policy
		p.Require = append(p.Require, check.ID)
		r.Gate = gate.Evaluate(p, r.Checks)
		return nil
	}})
	if calls != 1 || err == nil || applied.Report.Gate.Verdict != gate.Blocked {
		t.Fatalf("unstable config published: calls=%d report=%+v err=%v", calls, applied, err)
	}
	if refs := git(t, root, "for-each-ref", "--format=%(refname)", "refs/radar/integration/"); refs != "" {
		t.Fatalf("unstable config published %s", refs)
	}
	stablePolicy := gate.Policy{Version: 1, Require: []string{"textual_merge", "configuration_stability"}}
	applied, err = ApplyWithOptions(context.Background(), root, plan, Options{Analysis: integration.Options{Policy: &stablePolicy}, Finalize: func(ctx context.Context, r *integration.Report) error {
		calls++
		r.Checks = append(r.Checks, gate.Check{ID: "configuration_stability", Status: model.StatusPassed, Evidence: model.VerifiedStatic})
		r.Gate = gate.Evaluate(stablePolicy, r.Checks)
		return nil
	}})
	if err != nil || applied.Ref == "" || calls != 2 {
		t.Fatalf("stable finalizer rejected: %+v %v calls=%d", applied, err, calls)
	}
}

func TestParseHunksRequiresExactSeparator(t *testing.T) {
	hunks := parseHunks("<<<<<<< HEAD\n=======not a separator\nleft\n=======\nright\n>>>>>>> branch\n")
	if len(hunks) != 1 || hunks[0].Left != "=======not a separator\nleft\n" || hunks[0].Right != "right\n" {
		t.Fatalf("hunks: %+v", hunks)
	}
	crlf := parseHunks("<<<<<<< HEAD\r\nleft\r\n=======\r\nright\r\n>>>>>>> branch\r\n")
	if len(crlf) != 1 || crlf[0].Left != "left\r\n" {
		t.Fatalf("CRLF hunks: %+v", crlf)
	}
}

func TestLandingAnalysisCannotAuthorizeRepositoryExecution(t *testing.T) {
	root := fixture(t)
	branch(t, root, "a", "baseline", "a.md", "a\n")
	plan, err := Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"a"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, analysis := range []integration.Options{{Verify: true, AllowExecution: true, Command: []string{"python3", "-c", "raise Exception('must not run')"}}, {AllowExecution: true}, {Command: []string{"echo"}}, {Suite: "balanced"}, {ObserveCoverage: true}, {AttributionRuns: 1}} {
		if _, err = ApplyWithOptions(context.Background(), root, plan, Options{Analysis: analysis}); err == nil {
			t.Fatalf("accepted execution options: %+v", analysis)
		}
		if _, err = PreviewWithOptions(context.Background(), root, Options{Base: "baseline", Branches: []string{"a"}, Analysis: analysis}); err == nil {
			t.Fatalf("preview accepted execution options: %+v", analysis)
		}
	}
}
func put(t *testing.T, root, path, text string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, path), []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	git(t, root, "init", "-q")
	put(t, root, "service.py", "def charge():\n return 1\n")
	put(t, root, "test_service.py", "from service import charge\ndef test_charge():\n assert charge() == 1\n")
	put(t, root, "test_other.py", "import service\ndef test_other():\n assert service.charge()\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "base")
	git(t, root, "branch", "baseline")
	return root
}
func branch(t *testing.T, root, name, base, path, text string) {
	t.Helper()
	git(t, root, "checkout", "-qB", name, base)
	put(t, root, path, text)
	git(t, root, "add", "--", path)
	git(t, root, "commit", "-qm", name)
}
func TestMatrixConflictBriefAndAncestry(t *testing.T) {
	root := fixture(t)
	branch(t, root, "left", "baseline", "service.py", "def charge():\n return 2\n")
	branch(t, root, "right", "baseline", "service.py", "def charge():\n return 3\n")
	branch(t, root, "docs", "baseline", "notes.md", "hello\n")
	branch(t, root, "stack", "left", "stack.md", "stack\n")
	git(t, root, "checkout", "-q", "baseline")
	r, e := Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"stack", "right", "docs", "left"}})
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Pairs) != 6 {
		t.Fatalf("pairs: %+v", r)
	}
	if !reflect.DeepEqual(r.Independent, []string{"docs"}) {
		t.Fatalf("independent: %+v", r.Independent)
	}
	pos := map[string]int{}
	for i, s := range r.Order {
		pos[s] = i
	}
	if pos["left"] >= pos["stack"] {
		t.Fatalf("ancestry: %v", r.Order)
	}
	if r.EstimatedRebases != 1 {
		t.Fatalf("estimate: %d", r.EstimatedRebases)
	}
	b, e := Brief(context.Background(), root, "baseline", "left", "right")
	if e != nil {
		t.Fatal(e)
	}
	if len(b.Files) != 1 || len(b.Files[0].Hunks) != 1 || !strings.Contains(b.Files[0].Base, "return 1") {
		t.Fatalf("brief: %+v", b)
	}
	if len(b.Files[0].Tests) != 2 {
		t.Fatalf("tests: %+v", b.Files[0])
	}
	found := false
	for _, s := range b.Files[0].Symbols {
		if s == "charge" {
			found = true
		}
	}
	if !found {
		t.Fatalf("symbol: %+v", b.Files[0])
	}
	again, e := Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"left", "docs", "right", "stack"}})
	if e != nil || !reflect.DeepEqual(r, again) {
		t.Fatalf("nondeterministic plan: %v", e)
	}
}
func TestApplyPreservesUserStateAndRejectsStale(t *testing.T) {
	root := fixture(t)
	branch(t, root, "a", "baseline", "a.md", "a\n")
	branch(t, root, "b", "baseline", "b.md", "b\n")
	git(t, root, "checkout", "-q", "baseline")
	put(t, root, "untracked.txt", "leave me")
	put(t, root, "service.py", "dirty\n")
	git(t, root, "add", "service.py")
	before := git(t, root, "status", "--porcelain=v1")
	heads := git(t, root, "show-ref", "--heads")
	index := git(t, root, "write-tree")
	r, e := Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"a", "b"}})
	if e != nil {
		t.Fatal(e)
	}
	a, e := Apply(context.Background(), root, r)
	if e != nil {
		t.Fatal(e)
	}
	if a.Ref == "" || len(a.FastForward) == 0 {
		t.Fatalf("applied: %+v", a)
	}
	if git(t, root, "status", "--porcelain=v1") != before || git(t, root, "show-ref", "--heads") != heads || git(t, root, "write-tree") != index {
		t.Fatal("user state changed")
	}
	if got := git(t, root, "rev-parse", a.Ref); got != a.Commit {
		t.Fatalf("ref: %s", got)
	}
	if again, e := Apply(context.Background(), root, r); e != nil || again.Ref != a.Ref {
		t.Fatalf("idempotent apply: %+v %v", again, e)
	}
	git(t, root, "update-ref", "refs/heads/a", git(t, root, "rev-parse", "b"))
	if _, e = Apply(context.Background(), root, r); e == nil {
		t.Fatal("accepted stale input")
	}
}
func TestApplyConflictAndUnsupportedAreNotPass(t *testing.T) {
	root := fixture(t)
	branch(t, root, "a", "baseline", "service.py", "def charge():\n return 2\n")
	branch(t, root, "b", "baseline", "service.py", "def charge():\n return 3\n")
	r, e := Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"a", "b"}})
	if e != nil {
		t.Fatal(e)
	}
	if r.Pairs[0].Status != model.StatusFailed {
		t.Fatalf("status %+v", r)
	}
	if _, e = Apply(context.Background(), root, r); e == nil {
		t.Fatal("published conflicting candidate")
	}
	branch(t, root, "unsafe", "baseline", "target", "safe\n")
	if e = os.Symlink("target", filepath.Join(root, "link")); e != nil {
		t.Skip(e)
	}
	git(t, root, "add", "link")
	git(t, root, "commit", "-qm", "symlink")
	r, e = Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"a", "unsafe"}})
	if e != nil {
		t.Fatal(e)
	}
	if r.Complete || r.Pairs[0].Status != model.StatusUnknown {
		t.Fatalf("unsupported pair passed: %+v", r)
	}
}
func TestPairBudgetAndDriverOptIn(t *testing.T) {
	root := fixture(t)
	for _, name := range []string{"a", "b", "c"} {
		branch(t, root, name, "baseline", name+".md", name)
	}
	r, e := Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"a", "b", "c"}, MaxPairs: 1})
	if e != nil {
		t.Fatal(e)
	}
	if r.Complete || len(r.Pairs) != 3 || r.Pairs[1].Status != model.StatusUnknown || len(r.Independent) > 0 {
		t.Fatalf("omission: %+v", r)
	}
	if _, e = Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"a"}, Driver: "mergiraf"}); e == nil {
		t.Fatal("driver lacked opt-in")
	}
	if _, e = Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"a"}, Driver: "shell", AllowExecution: true}); e == nil {
		t.Fatal("accepted arbitrary driver")
	}
}
func TestExactOrderMinimizesLaterRebases(t *testing.T) {
	n := 4
	anc := make([][]bool, n)
	conf := make([][]bool, n)
	for i := range anc {
		anc[i] = make([]bool, n)
		conf[i] = make([]bool, n)
	}
	for i := 1; i < n; i++ {
		conf[0][i] = true
		conf[i][0] = true
	}
	order, cost := orderBranches(n, anc, conf)
	if cost != 1 || order[len(order)-1] != 0 {
		t.Fatalf("order %v cost %d", order, cost)
	}
}
func TestAuthorizedDriverResolvesPrivately(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX fixture")
	}
	root := fixture(t)
	branch(t, root, "left", "baseline", "service.py", "def charge():\n return 2\n")
	branch(t, root, "right", "baseline", "service.py", "def charge():\n return 3\n")
	branch(t, root, "third", "baseline", "third.md", "third\n")
	git(t, root, "checkout", "-q", "baseline")
	driverDir := t.TempDir()
	script := "#!/bin/sh\n[ \"$1\" = merge ] || exit 2\nshift\nprintf 'def charge():\\n return 4\\n' > \"$5\"\n"
	if e := os.WriteFile(filepath.Join(driverDir, "mergiraf"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", driverDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	o := Options{Base: "baseline", Branches: []string{"left", "right", "third"}, Driver: "mergiraf", AllowExecution: true}
	r, e := Plan(context.Background(), root, o)
	if e != nil {
		t.Fatal(e)
	}
	resolved := false
	for _, p := range r.Pairs {
		if p.ResolvedBy == "mergiraf" {
			resolved = true
			if p.Status != model.StatusPassed {
				t.Fatalf("resolved pair: %+v", p)
			}
		}
	}
	if !resolved {
		t.Fatalf("no resolution: %+v", r)
	}
	if _, e = Apply(context.Background(), root, r); e == nil {
		t.Fatal("apply inferred driver authorization")
	}
	full, e := PreviewWithOptions(context.Background(), root, o)
	if e != nil || len(full.Conflicts) > 0 {
		t.Fatalf("full driver preview: %+v %v", full, e)
	}
	a, e := ApplyWithOptions(context.Background(), root, r, o)
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(git(t, root, "show", a.Ref+":service.py"), "return 4") {
		t.Fatal("driver output not published")
	}
	if git(t, root, "status", "--porcelain") != "" {
		t.Fatal("driver changed source")
	}
}

func TestStandaloneRestackHonorsAuthorizedDriver(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX driver fixture")
	}
	root := fixture(t)
	branch(t, root, "stale", "baseline", "service.py", "def charge():\n return 2\n")
	branch(t, root, "new-base", "baseline", "service.py", "def charge():\n return 3\n")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mergiraf"), []byte("#!/bin/sh\n[ \"$1\" = merge ] || exit 2\nprintf 'def charge():\\n return 4\\n' > \"$6\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	before := git(t, root, "show-ref")
	o := Options{Base: "new-base", Branches: []string{"stale"}, Driver: "mergiraf", AllowExecution: true}
	p, err := Plan(context.Background(), root, o)
	if err != nil || len(p.RestackFirst) != 0 || len(p.Order) != 1 {
		t.Fatalf("authorized driver resolution excluded: %+v %v", p, err)
	}
	r, err := PreviewPlan(context.Background(), root, p, o)
	if err != nil || r.Gate.Verdict != gate.Pass {
		t.Fatalf("driver preview: %+v %v", r, err)
	}
	if before != git(t, root, "show-ref") {
		t.Fatal("standalone driver mutated source refs")
	}
	a, err := ApplyWithOptions(context.Background(), root, p, o)
	if err != nil || !a.Outcome.Published || !strings.Contains(git(t, root, "show", a.Ref+":service.py"), "return 4") {
		t.Fatalf("authorized standalone apply: %+v %v", a, err)
	}
}

func TestMigrationPairUsesExistingDetectorAndIgnoresBaseExisting(t *testing.T) {
	root := fixture(t)
	put(t, root, "db/env.py", "# alembic\n")
	put(t, root, "db/versions/r0.py", "revision = 'r0'\ndown_revision = None\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "migration base")
	git(t, root, "branch", "-f", "baseline")
	branch(t, root, "a", "baseline", "db/versions/a.py", "revision = 'a'\ndown_revision = 'r0'\n")
	branch(t, root, "b", "baseline", "db/versions/b.py", "revision = 'b'\ndown_revision = 'r0'\n")
	r, e := Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"a", "b"}})
	if e != nil {
		t.Fatal(e)
	}
	if r.Pairs[0].Status != model.StatusFailed {
		t.Fatalf("migration pair: %+v", r)
	}
	found := false
	for _, f := range r.Pairs[0].Findings {
		if f.Code == "migration_multiple_heads" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing migration finding: %+v", r.Pairs[0])
	}
	git(t, root, "checkout", "-qB", "preexisting", "baseline")
	put(t, root, "db/versions/a.py", "revision = 'a'\ndown_revision = 'r0'\n")
	put(t, root, "db/versions/b.py", "revision = 'b'\ndown_revision = 'r0'\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "existing split")
	branch(t, root, "docs1", "preexisting", "one.md", "one")
	branch(t, root, "docs2", "preexisting", "two.md", "two")
	r, e = Plan(context.Background(), root, Options{Base: "preexisting", Branches: []string{"docs1", "docs2"}})
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range r.Pairs[0].Findings {
		if f.Code == "migration_multiple_heads" && f.Severity == model.SeverityError {
			t.Fatalf("blamed unrelated branches for base migration: %+v", f)
		}
	}
}

func TestDriverFailureDoesNotResolveAndLocalExecutableRejected(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("POSIX fixture")
	}
	root := fixture(t)
	branch(t, root, "a", "baseline", "service.py", "def charge():\n return 2\n")
	branch(t, root, "b", "baseline", "service.py", "def charge():\n return 3\n")
	dir := t.TempDir()
	if e := os.WriteFile(filepath.Join(dir, "weave-driver"), []byte("#!/bin/sh\nexit 1\n"), 0700); e != nil {
		t.Fatal(e)
	}
	original := os.Getenv("PATH")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+original)
	r, e := Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"a", "b"}, Driver: "weave", AllowExecution: true})
	if e != nil {
		t.Fatal(e)
	}
	if r.Pairs[0].Status != model.StatusFailed || r.Pairs[0].ResolvedBy != "" {
		t.Fatalf("driver failure passed: %+v", r)
	}
	put(t, root, "mergiraf", "#!/bin/sh\nexit 0\n")
	if e = os.Chmod(filepath.Join(root, "mergiraf"), 0700); e != nil {
		t.Fatal(e)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+original)
	if _, e = Plan(context.Background(), root, Options{Base: "baseline", Branches: []string{"a"}, Driver: "mergiraf", AllowExecution: true}); e == nil {
		t.Fatal("executed repository binary")
	}
}

func TestRestackFirstScopesRemainingCandidateAndRefusesApply(t *testing.T) {
	root := fixture(t)
	branch(t, root, "stale", "baseline", "service.py", "def charge():\n return 2\n")
	branch(t, root, "new-base", "baseline", "service.py", "def charge():\n return 3\n")
	for _, name := range []string{"a", "b", "c"} {
		branch(t, root, name, "new-base", name+".md", name+"\n")
	}
	git(t, root, "checkout", "-q", "new-base")
	before := git(t, root, "show-ref")
	state := git(t, root, "status", "--porcelain=v1")
	p, err := Plan(context.Background(), root, Options{Base: "new-base", Branches: []string{"stale", "c", "b", "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Branches) != 4 || len(p.RestackFirst) != 1 || p.RestackFirst[0].Name != "stale" || len(p.Pairs) != 3 || len(p.Order) != 3 || p.Complete {
		t.Fatalf("plan: %+v", p)
	}
	policy := gate.Policy{Version: 1, Require: []string{"textual_merge"}}
	r, err := PreviewPlan(context.Background(), root, p, Options{Analysis: integration.Options{Policy: &policy}})
	if err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict != gate.Blocked || len(r.Inputs) != 3 || r.CandidateCommit == "" {
		t.Fatalf("report: %+v", r)
	}
	policy.OnMissing = "fail"
	strict, err := PreviewPlan(context.Background(), root, p, Options{Analysis: integration.Options{Policy: &policy}})
	if err != nil || strict.Gate.Verdict != gate.Blocked {
		t.Fatalf("strict missing policy bypassed scope: %+v %v", strict, err)
	}
	for _, sha := range r.Inputs {
		if sha == p.RestackFirst[0].Commit {
			t.Fatal("excluded input in candidate")
		}
	}
	applied, err := Apply(context.Background(), root, p)
	if err == nil || applied.Outcome.ObjectsImported || applied.Outcome.Published {
		t.Fatalf("apply: %+v %v", applied, err)
	}
	if before != git(t, root, "show-ref") || state != git(t, root, "status", "--porcelain=v1") {
		t.Fatal("apply mutated source")
	}
	// An ancestor that conflicts alone must not exclude its repaired descendant.
	git(t, root, "checkout", "-qB", "fixed", "stale")
	git(t, root, "merge", "--no-ff", "-s", "ours", "-m", "resolve base", "new-base")
	p, err = Plan(context.Background(), root, Options{Base: "new-base", Branches: []string{"stale", "fixed"}})
	if err != nil || len(p.RestackFirst) != 1 || len(p.Order) != 1 || p.Order[0] != "fixed" {
		t.Fatalf("descendant plan: %+v %v", p, err)
	}
}

func TestAllRestackHasNoBaseOnlyPassingCandidate(t *testing.T) {
	root := fixture(t)
	branch(t, root, "stale", "baseline", "service.py", "def charge():\n return 2\n")
	branch(t, root, "new-base", "baseline", "service.py", "def charge():\n return 3\n")
	p, err := Plan(context.Background(), root, Options{Base: "new-base", Branches: []string{"stale"}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := PreviewPlan(context.Background(), root, p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict != gate.Blocked || r.CandidateCommit != "" || r.CandidateTree != "" || len(r.Inputs) != 0 {
		t.Fatalf("report: %+v", r)
	}
}

func TestRestackScopePreservesRemainingFailure(t *testing.T) {
	root := fixture(t)
	branch(t, root, "stale", "baseline", "service.py", "def charge():\n return 2\n")
	branch(t, root, "new-base", "baseline", "service.py", "def charge():\n return 3\n")
	branch(t, root, "a", "new-base", "notes.md", "left\n")
	branch(t, root, "b", "new-base", "notes.md", "right\n")
	p, err := Plan(context.Background(), root, Options{Base: "new-base", Branches: []string{"stale", "a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	r, err := PreviewPlan(context.Background(), root, p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict != gate.Fail {
		t.Fatalf("hidden remaining failure: %+v", r)
	}
}
