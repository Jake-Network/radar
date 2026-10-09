package composition

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/workspace"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}

func write(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

type fixture struct {
	t    *testing.T
	base string
}

func newFixture(t *testing.T) *fixture {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, base: base}
}

// repo creates a repository with files committed on main.
func (f *fixture) repo(name string, files map[string]string) string {
	root := filepath.Join(f.base, name)
	os.MkdirAll(root, 0o700)
	git(f.t, root, "init", "-q")
	git(f.t, root, "checkout", "-q", "-b", "main")
	for p, c := range files {
		write(f.t, root, p, c)
	}
	git(f.t, root, "add", ".")
	git(f.t, root, "commit", "-qm", "baseline")
	return root
}

// agent adds a worktree branch of root with changes committed.
func (f *fixture) agent(root, branch string, changes map[string]string) string {
	dir := filepath.Join(f.base, "wt", filepath.Base(root), strings.ReplaceAll(branch, "/", "-"))
	git(f.t, root, "worktree", "add", "-q", "-b", branch, dir, "main")
	if len(changes) > 0 {
		for p, c := range changes {
			write(f.t, dir, p, c)
		}
		git(f.t, dir, "add", ".")
		git(f.t, dir, "commit", "-qm", branch)
	}
	return dir
}

func scopeOf(t *testing.T, roots ...string) workspace.Scope {
	t.Helper()
	s := workspace.Scope{Workspace: "shop", Key: "shop", Source: workspace.SourceRegistry}
	for _, root := range roots {
		loc, err := workspace.Locate(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		s.Repos = append(s.Repos, workspace.ScopeRepo{ID: loc.DefaultRepoID(), Path: loc.Root, CommonDir: loc.CommonDir, Origin: workspace.SourceRegistry})
		s.All = append(s.All, loc.DefaultRepoID())
	}
	s.Current = s.Repos[0].ID
	return s
}

func run(t *testing.T, req Request) []Result {
	t.Helper()
	repos, err := Collect(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return Build(context.Background(), repos, integration.Options{SuggestTests: true})
}

func shop(t *testing.T) (*fixture, string, string) {
	f := newFixture(t)
	orders := f.repo("orders", map[string]string{"api.py": "def total():\n    return 1\n", "util.py": "X = 1\n"})
	payments := f.repo("payments", map[string]string{"client.ts": "export const total = 1;\n"})
	f.agent(orders, "agent/rounding", map[string]string{"util.py": "X = 2\n"})
	f.agent(orders, "agent/api", map[string]string{"api.py": "def total():\n    return 2\n"})
	f.agent(orders, "agent/idle", nil)
	f.agent(payments, "agent/client", map[string]string{"client.ts": "export const total = 2;\n"})
	return f, orders, payments
}

func TestCollectAutoSelectsBranchesPerRepo(t *testing.T) {
	_, orders, payments := shop(t)
	results := run(t, Request{Scope: scopeOf(t, orders, payments)})
	if len(results) != 2 {
		t.Fatal(results)
	}
	o, p := results[0], results[1]
	if o.ID != "orders" || o.Selection != SelectionAuto || o.BaseRef != "main" || o.BaseSource != BaseAuto {
		t.Fatalf("orders %+v", o.Repo)
	}
	if len(o.Branches) != 2 || o.Branches[0].Ref != "agent/api" || o.Branches[1].Ref != "agent/rounding" || len(o.Branches[0].Changed) != 1 {
		t.Fatalf("auto selection must be ref-name ordered: %+v", o.Branches)
	}
	if len(o.Skipped) != 1 || !strings.HasPrefix(o.Skipped[0], "agent/idle") {
		t.Fatal("idle branch", o.Skipped)
	}
	if len(p.Branches) != 1 || p.Branches[0].Ref != "agent/client" {
		t.Fatal("payments", p.Branches)
	}
	for _, r := range results {
		if r.Report == nil || r.Report.Gate.Verdict != gate.Pass || r.Report.CandidateTree == "" {
			t.Fatalf("%s did not pass: %+v %s", r.ID, r.Report, r.Error)
		}
	}
}

func TestNamedSelectionLeavesOtherReposAtBase(t *testing.T) {
	_, orders, payments := shop(t)
	results := run(t, Request{Scope: scopeOf(t, orders, payments), Named: map[string][]string{"orders": {"agent/rounding", "agent/api"}}})
	o, p := results[0], results[1]
	if o.Selection != SelectionNamed || o.Branches[0].Ref != "agent/rounding" || o.Branches[1].Ref != "agent/api" {
		t.Fatal("named refs keep argument order", o.Branches)
	}
	if p.Selection != SelectionBase || len(p.Branches) != 0 || p.Report == nil || p.Report.CandidateCommit != p.Base {
		t.Fatalf("unnamed repo must take part at its base: %+v", p.Repo)
	}
}

func TestConflictInOneRepoDoesNotHideAnother(t *testing.T) {
	f := newFixture(t)
	orders := f.repo("orders", map[string]string{"api.py": "A = 1\n"})
	payments := f.repo("payments", map[string]string{"client.ts": "export const a = 1;\n"})
	f.agent(orders, "agent/a", map[string]string{"api.py": "A = 2\n"})
	f.agent(orders, "agent/b", map[string]string{"api.py": "A = 3\n"})
	f.agent(payments, "agent/client", map[string]string{"client.ts": "export const a = 2;\n"})
	results := run(t, Request{Scope: scopeOf(t, orders, payments)})
	if results[0].Report.Gate.Verdict != gate.Fail || len(results[0].Report.Conflicts) != 1 {
		t.Fatal("orders conflict", results[0].Report)
	}
	if results[1].Report.Gate.Verdict != gate.Pass {
		t.Fatal("payments must still be checked", results[1].Report)
	}
}

func TestBaseFailureIsolatedToItsRepo(t *testing.T) {
	_, orders, payments := shop(t)
	git(t, payments, "branch", "-m", "main", "develop")
	results := run(t, Request{Scope: scopeOf(t, orders, payments)})
	if results[1].Error == "" || results[1].Next != "radar gate --base payments:<REF>" {
		t.Fatalf("payments base failure: %+v", results[1].Repo)
	}
	if results[0].Report == nil || results[0].Report.Gate.Verdict != gate.Pass {
		t.Fatal("orders must still pass", results[0].Error)
	}
	results = run(t, Request{Scope: scopeOf(t, orders, payments), Bases: map[string]string{"payments": "develop"}})
	if results[1].Error != "" || results[1].BaseRef != "develop" || results[1].BaseSource != BaseNamed {
		t.Fatal("--base payments:develop", results[1].Repo)
	}
}

func TestDigestIsStableAndLocationIndependent(t *testing.T) {
	f, orders, payments := shop(t)
	first := Digest(run(t, Request{Scope: scopeOf(t, orders, payments)}))
	if again := Digest(run(t, Request{Scope: scopeOf(t, orders, payments)})); again != first {
		t.Fatal("same selection, different digest", first, again)
	}
	// The registry keeps the repo ID when a repository moves; so does this
	// scope, which derives IDs from folder names.
	moved := filepath.Join(f.base, "elsewhere", "payments")
	os.MkdirAll(filepath.Dir(moved), 0o700)
	if err := os.Rename(payments, moved); err != nil {
		t.Fatal(err)
	}
	// Worktrees of a moved repository need repair; the digest must not care.
	git(t, moved, "worktree", "repair")
	if after := Digest(run(t, Request{Scope: scopeOf(t, orders, moved)})); after != first {
		t.Fatal("moving a repository changed the digest", first, after)
	}
	other := Digest(run(t, Request{Scope: scopeOf(t, orders, moved), Named: map[string][]string{"orders": {"agent/api"}}}))
	if other == first {
		t.Fatal("a different selection must have a different digest")
	}
}

func TestCollectRepinsWhenRefMoves(t *testing.T) {
	f, orders, payments := shop(t)
	wt := filepath.Join(f.base, "wt", "orders", "agent-api")
	moves := 0
	collected = func(round int) {
		if round == 0 {
			write(t, wt, "api.py", "def total():\n    return 3\n")
			git(t, wt, "commit", "-qam", "moved during collection")
			moves++
		}
	}
	defer func() { collected = func(int) {} }()
	repos, err := Collect(context.Background(), Request{Scope: scopeOf(t, orders, payments)})
	if err != nil || moves != 1 {
		t.Fatal(err, moves)
	}
	if head := git(t, wt, "rev-parse", "HEAD"); repos[0].Branches[0].Commit != head {
		t.Fatal("collection must pin the moved ref's new commit", repos[0].Branches[0].Commit, head)
	}
	collected = func(int) {
		write(t, wt, "api.py", "def total():\n    return "+time.Now().Format("150405.000000000")+"\n")
		git(t, wt, "commit", "-qam", "keeps moving")
	}
	if _, err = Collect(context.Background(), Request{Scope: scopeOf(t, orders, payments)}); !errors.Is(err, ErrMoving) {
		t.Fatal("refs moving twice must be an error", err)
	}
}

func TestReplayReproducesRecordedCommits(t *testing.T) {
	f, orders, payments := shop(t)
	scope := scopeOf(t, orders, payments)
	results := run(t, Request{Scope: scope})
	rec := NewRecord(scope, Selection{Mode: "auto"}, results, Digest(results), "pass")
	wt := filepath.Join(f.base, "wt", "orders", "agent-api")
	write(t, wt, "api.py", "def total():\n    return 9\n")
	git(t, wt, "commit", "-qam", "after the run")
	replay := run(t, Request{Scope: scope, Replay: &rec})
	if Digest(replay) != rec.Digest || replay[0].Error != "" || replay[0].Report.CandidateTree != rec.Repos[0].CandidateTree {
		t.Fatalf("replay must rebuild the recorded commits: %+v", replay[0].Repo)
	}

	// A recorded commit that is gone cannot be reproduced.
	lost := rec
	lost.Repos = append([]RecordRepo(nil), rec.Repos...)
	lost.Repos[1].Branches = []Branch{{Ref: "agent/client", Commit: strings.Repeat("ab", 20), Source: SelectionAuto}}
	replay = run(t, Request{Scope: scope, Replay: &lost})
	if !strings.HasPrefix(replay[1].Error, "not reproducible:") || replay[0].Error != "" {
		t.Fatal("missing object", replay[1].Error)
	}
	// A different tree is not a reproduction.
	tampered := rec
	tampered.Repos = append([]RecordRepo(nil), rec.Repos...)
	tampered.Repos[0].CandidateTree = strings.Repeat("0", 40)
	if replay = run(t, Request{Scope: scope, Replay: &tampered}); !strings.Contains(replay[0].Error, "differs from the recorded") {
		t.Fatal("tree mismatch", replay[0].Error)
	}
}

func TestSymlinkRepoIsAnErrorWithGuidance(t *testing.T) {
	f := newFixture(t)
	orders := f.repo("orders", map[string]string{"a.py": "A = 1\n"})
	links := f.repo("links", map[string]string{"a.py": "A = 1\n"})
	if err := os.Symlink("a.py", filepath.Join(links, "b.py")); err != nil {
		t.Skip(err)
	}
	git(t, links, "add", ".")
	git(t, links, "commit", "-qm", "symlink")
	f.agent(orders, "agent/a", map[string]string{"a.py": "A = 2\n"})
	results := run(t, Request{Scope: scopeOf(t, links, orders)})
	if !strings.Contains(results[0].Error, UnsupportedTree) || results[1].Report == nil {
		t.Fatal("symlink repo", results[0].Error, results[1].Error)
	}
	if paths, err := UnsupportedEntries(context.Background(), links, "HEAD", 5); err != nil || len(paths) != 1 || paths[0] != "b.py" {
		t.Fatal(paths, err)
	}
}

func TestDirtyWorktreesAndDetached(t *testing.T) {
	f, orders, payments := shop(t)
	write(t, filepath.Join(f.base, "wt", "orders", "agent-rounding"), "scratch.txt", "wip\n")
	write(t, filepath.Join(f.base, "wt", "orders", "agent-idle"), "idle.txt", "wip\n")
	detached := filepath.Join(f.base, "wt", "payments", "detached")
	git(t, payments, "worktree", "add", "-q", "--detach", detached, "main")
	write(t, detached, "client.ts", "export const total = 7;\n")
	git(t, detached, "commit", "-qam", "detached work")
	results := run(t, Request{Scope: scopeOf(t, orders, payments)})
	selected, other := 0, 0
	for _, d := range results[0].Dirty {
		if d.Selected && d.Branch == "agent/rounding" && d.Count() == 1 {
			selected++
		} else if !d.Selected {
			other++
		}
	}
	if selected != 1 || other != 1 {
		t.Fatalf("dirty worktrees %+v", results[0].Dirty)
	}
	if len(results[1].Detached) != 1 || len(results[1].Branches) != 1 {
		t.Fatalf("detached worktree must be reported, not selected: %+v", results[1].Repo)
	}
}

func TestSourceRepositoriesAreNotModified(t *testing.T) {
	f, orders, payments := shop(t)
	write(t, filepath.Join(f.base, "wt", "orders", "agent-rounding"), "scratch.txt", "wip\n")
	state := func() string {
		out := ""
		for _, root := range []string{orders, payments, filepath.Join(f.base, "wt", "orders", "agent-rounding")} {
			out += git(t, root, "for-each-ref", "--format=%(refname) %(objectname)") + "\n"
			out += git(t, root, "rev-parse", "HEAD") + git(t, root, "symbolic-ref", "-q", "HEAD") + "\n"
			out += git(t, root, "status", "--porcelain=v1", "--untracked-files=all") + "\n"
			out += git(t, root, "ls-files", "-s") + "\n"
		}
		return out
	}
	before := state()
	run(t, Request{Scope: scopeOf(t, orders, payments)})
	if after := state(); after != before {
		t.Fatalf("source repositories changed:\n%s\n---\n%s", before, after)
	}
}

func TestStoreKeepsRecentRunsAndConcurrentSaves(t *testing.T) {
	t.Setenv("RADAR_STATE_DIR", t.TempDir())
	s, err := OpenStore("shop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Load("last"); !errors.Is(err, ErrNoRun) {
		t.Fatal("empty store", err)
	}
	var wg sync.WaitGroup
	ids := make(chan string, KeepRuns+5)
	for i := range KeepRuns + 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := Record{Verdict: "pass", Digest: "d" + string(rune('a'+i%26))}
			if err := s.Save(&rec, func() any { return map[string]string{"run_id": rec.RunID} }); err != nil {
				t.Error(err)
			}
			ids <- rec.RunID
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatal("two runs share an ID", id)
		}
		seen[id] = true
	}
	if n := len(s.ids()); n != KeepRuns {
		t.Fatal("kept runs", n)
	}
	last, err := s.Load("last")
	if err != nil || !seen[last.RunID] {
		t.Fatal("last", last.RunID, err)
	}
	report, err := os.ReadFile(filepath.Join(s.Dir, last.RunID, "report.json"))
	if err != nil || !strings.Contains(string(report), last.RunID) {
		t.Fatal("report must carry its run ID", string(report), err)
	}
	if recent := s.Recent(5); len(recent) != 5 || recent[0].RunID < recent[4].RunID {
		t.Fatal("recent runs newest first", len(recent))
	}
	if _, err = s.Load("../../etc"); err == nil {
		t.Fatal("run ID path traversal")
	}
	if _, err = OpenStore("../x"); err == nil {
		t.Fatal("key path traversal")
	}
}
