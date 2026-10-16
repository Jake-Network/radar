package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/testselection"
	"github.com/Jake-Network/radar/internal/watch"
)

func TestWatchDirtyFailureHumanRepairLead(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip(err)
	}
	root, agent := checkoutRepo(t)
	branch := agent("dirty-only", nil)
	put(t, branch, "backend.py", "def price():\n    return 2\n")
	// The same path appears in staged and unstaged inventories; count it once.
	gitTest(t, branch, "add", "backend.py")
	put(t, branch, "backend.py", "def price():\n    return 3\n")
	before := watchSourceFingerprint(t, branch)
	var out bytes.Buffer
	a := &app{ctx: context.Background(), root: root, out: &out}
	o := options{base: "main", includeDirty: true, verify: true, suite: testselection.ModeFull, maxCommands: 16, timeout: time.Minute}
	state, err := a.observeWatch(a.ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	in := state.Value.(watchInput)
	c, err := integration.BuildDirtyCandidate(a.ctx, root, in.Base, in.SHAs, in.Snapshots)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	r, err := integration.Analyze(a.ctx, c, integration.Options{Base: in.Base, Branches: in.SHAs, SuggestTests: true, Verify: true, AllowExecution: true, Suite: o.suite, MaxCommands: o.maxCommands, Timeout: o.timeout})
	if err != nil {
		t.Fatal(err)
	}
	if r.Gate.Verdict != gate.Fail {
		t.Fatalf("dirty-only candidate did not fail: %+v", r.Gate)
	}
	a.renderWatch(watch.Event{Type: watch.Completed, State: state, Result: &watch.Result{Value: r}}, o)
	text := out.String()
	for _, want := range []string{"dirty-only", "1 file changed", "look at (import-based lead): dirty-only (backend.py)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in human dirty failure output:\n%s", want, text)
		}
	}
	if got := watchSourceFingerprint(t, branch); got != before {
		t.Fatal("dirty preview or human rendering changed the source worktree")
	}
}

func TestWatchBranchChangesPinnedSnapshotUnion(t *testing.T) {
	root, agent := checkoutRepo(t)
	branch := agent("dirty-files", map[string]string{"backend.py": "def price():\n    return 2\n"})
	put(t, branch, "backend.py", "def price():\n    return 3\n")
	gitTest(t, branch, "add", "backend.py")
	put(t, branch, "backend.py", "def price():\n    return 4\n")
	gitTest(t, branch, "mv", "frontend.ts", "renamed.ts")
	if err := os.Remove(filepath.Join(branch, "test_checkout.py")); err != nil {
		t.Fatal(err)
	}
	put(t, branch, "untracked.py", "VALUE = 1\n")
	a := &app{ctx: context.Background(), root: root}
	state, err := a.observeWatch(a.ctx, options{base: "main", includeDirty: true})
	if err != nil {
		t.Fatal(err)
	}
	in := state.Value.(watchInput)
	// An advanced base's unrelated files must not become branch repair leads.
	put(t, root, "base_only.py", "VALUE = 1\n")
	gitTest(t, root, "add", "base_only.py")
	gitTest(t, root, "commit", "-m", "advance base")
	in.Base = gitTest(t, root, "rev-parse", "HEAD")
	// Inventories remain pinned even if the live worktree changes afterwards.
	put(t, branch, "later.py", "VALUE = 1\n")
	in.Snapshots = append(in.Snapshots, integration.DirtySnapshot{InputIndex: 99, Untracked: []string{"other-branch.py"}})
	changed, err := a.watchBranchChanges(in, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"backend.py", "frontend.ts", "renamed.ts", "test_checkout.py", "untracked.py"}
	if fmt.Sprint(changed) != fmt.Sprint(want) {
		t.Fatalf("changed files = %v, want %v", changed, want)
	}
}

func TestWatchRenderBranchInspectionErrorIsIncomplete(t *testing.T) {
	root, _ := checkoutRepo(t)
	base := gitTest(t, root, "rev-parse", "HEAD")
	in := watchInput{Base: base, Refs: []string{"unavailable", "available"}, SHAs: []string{"missing-commit", base}, Snapshots: []integration.DirtySnapshot{
		{InputIndex: 0, Untracked: []string{"known.py"}},
		{InputIndex: 1, Untracked: []string{"available.py"}},
	}}
	var out bytes.Buffer
	a := &app{ctx: context.Background(), root: root, out: &out}
	changed, err := a.watchBranchChanges(in, 0)
	if err == nil || fmt.Sprint(changed) != "[known.py]" {
		t.Fatalf("inspection failure lost known snapshot paths: %v, %v", changed, err)
	}
	a.renderWatch(watch.Event{Type: watch.Completed, State: watch.State{Value: in}, Result: &watch.Result{Value: integration.Report{Base: base, Inputs: in.SHAs}}}, options{base: "main", includeDirty: true})
	text := out.String()
	for _, want := range []string{"branch \"unavailable\": changed-file inspection unavailable", "changed files unavailable", "available", "1 file changed"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in incomplete inventory output:\n%s", want, text)
		}
	}
}

// watchEventWriter decodes complete JSONL events synchronously. Callbacks can
// cancel a session or commit a fixture update without timing-dependent sleeps.
type watchEventWriter struct {
	pending  []byte
	events   []watch.Event
	callback func(watch.Event)
	err      error
	onError  func(error)
}

func (w *watchEventWriter) Write(p []byte) (int, error) {
	w.pending = append(w.pending, p...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			break
		}
		var event watch.Event
		if err := json.Unmarshal(w.pending[:i], &event); err != nil {
			w.err = err
			if w.onError != nil {
				w.onError(err)
			}
			return 0, err
		}
		w.pending = w.pending[i+1:]
		w.events = append(w.events, event)
		if w.callback != nil {
			w.callback(event)
		}
	}
	return len(p), nil
}

func TestWatchEventWriterKeepsPartialJSONLAndRejectsMalformedEvent(t *testing.T) {
	var callbacks, errors int
	w := watchEventWriter{callback: func(watch.Event) { callbacks++ }, onError: func(error) { errors++ }}
	first := []byte(`{"type":"completed","sequence":1`)
	if n, err := w.Write(first); err != nil || n != len(first) || callbacks != 0 || !bytes.Equal(w.pending, first) {
		t.Fatal("partial JSONL was lost or published", n, err, w)
	}
	if _, err := w.Write([]byte("}\n{invalid}\n")); err == nil || w.err == nil || errors != 1 {
		t.Fatal("malformed JSONL did not stop the session", err, w)
	}
	if callbacks != 1 || len(w.events) != 1 || w.events[0].Type != watch.Completed {
		t.Fatal("malformed event was accepted or earlier event lost", w)
	}
}

type watchSessionEvents struct {
	completed, errors, attributed int
}

func watchSession(t *testing.T, root string, want watchSessionEvents, callback func(watch.Event, context.CancelFunc), args ...string) ([]watch.Event, string) {
	t.Helper()
	// Functional fixtures stop on their expected semantic event. The Go test
	// deadline is a watchdog, not a product latency budget for native Git.
	ctx, cancel := watchFunctionalContext(t)
	defer cancel()
	out := &watchEventWriter{onError: func(error) { cancel() }}
	var got watchSessionEvents
	out.callback = func(e watch.Event) {
		switch e.Type {
		case watch.Completed:
			got.completed++
		case watch.Attributed:
			got.attributed++
		}
		if e.Type == watch.Error || e.Error != "" {
			got.errors++
		}
		if got.completed > want.completed || got.attributed > want.attributed || got.errors > want.errors {
			cancel()
			return
		}
		callback(e, cancel)
		if got == want {
			cancel()
		}
	}
	var errs bytes.Buffer
	argv := append([]string{"watch", "--root", root, "--jsonl", "--interval", "100ms", "--debounce", "1ms"}, args...)
	started := time.Now()
	code := Run(ctx, argv, out, &errs)
	t.Logf("watch functional session elapsed=%s completed=%d attributed=%d errors=%d", time.Since(started), got.completed, got.attributed, got.errors)
	if code != 0 || ctx.Err() != context.Canceled || got != want {
		t.Fatalf("watch did not stop on expected events: exit=%d context=%v got=%+v want=%+v stderr=%s events=%+v", code, ctx.Err(), got, want, errs.String(), out.events)
	}
	if out.err != nil || len(out.pending) > 0 {
		t.Fatalf("invalid JSONL: %v %q", out.err, out.pending)
	}
	for i, e := range out.events {
		if e.Sequence != uint64(i+1) || e.Time.IsZero() {
			t.Fatalf("inconsistent event %d: %+v", i, e)
		}
		if e.Type == watch.Completed && e.Result == nil {
			t.Fatal("completed event missing result", e)
		}
	}
	return out.events, errs.String()
}
func watchReport(t *testing.T, e watch.Event) map[string]any {
	t.Helper()
	if e.Result == nil {
		t.Fatal("completed event missing result")
	}
	r, ok := e.Result.Value.(map[string]any)
	if !ok {
		t.Fatalf("result %T", e.Result.Value)
	}
	return r
}
func watchVerdict(t *testing.T, e watch.Event) string {
	t.Helper()
	g, ok := watchReport(t, e)["gate"].(map[string]any)
	if !ok {
		t.Fatal("missing gate", e)
	}
	return g["verdict"].(string)
}

func TestWatchInitialInvalidReferences(t *testing.T) {
	root, _ := checkoutRepo(t)
	for _, args := range [][]string{{"--base", "missing-base"}, {"missing-branch"}, {"--suite", "full"}} {
		code, _, errs := run(t, root, append([]string{"watch"}, args...)...)
		if code != 2 || errs == "" {
			t.Fatalf("%v: exit %d, %q", args, code, errs)
		}
	}
}
func TestWatchNoSelectedBranchesWaitsWithoutPass(t *testing.T) {
	root, _ := checkoutRepo(t)
	events, _ := watchSession(t, root, watchSessionEvents{completed: 1}, func(e watch.Event, cancel context.CancelFunc) {
		if e.Type == watch.Completed {
			cancel()
		}
	})
	if len(events) != 3 || events[0].Type != watch.Changed || events[1].Type != watch.Checking || events[2].Type != watch.Completed {
		t.Fatalf("events: %+v", events)
	}
	result := watchReport(t, events[2])
	if result["state"] != "NOT VERIFIED" || result["gate"] != nil {
		t.Fatal("empty selection claimed verification", result)
	}
}
func TestWatchAutomaticallyRechecksCommittedChanges(t *testing.T) {
	root, agent := checkoutRepo(t)
	wt := agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	var trees []string
	events, _ := watchSession(t, root, watchSessionEvents{completed: 2}, func(e watch.Event, cancel context.CancelFunc) {
		if e.Type != watch.Completed {
			return
		}
		if watchVerdict(t, e) != "pass" {
			t.Fatal("static check", watchReport(t, e))
		}
		r := watchReport(t, e)
		trees = append(trees, r["candidate_tree"].(string))
		if r["executions"] != nil {
			t.Fatal("default watch executed code", r)
		}
		if len(trees) == 1 {
			put(t, wt, "backend.py", "def price():\n    return 3\n")
			gitTest(t, wt, "add", "backend.py")
			gitTest(t, wt, "commit", "-qm", "new agent change")
		} else {
			cancel()
		}
	})
	if len(trees) != 2 || trees[0] == trees[1] {
		t.Fatalf("candidate identities: %v", trees)
	}
	var completedKeys []string
	for _, e := range events {
		if e.Type == watch.Completed {
			completedKeys = append(completedKeys, e.State.Key)
		}
	}
	if len(completedKeys) != 2 || completedKeys[0] == completedKeys[1] {
		t.Fatal("committed change was not detected", completedKeys)
	}
}

func TestWatchCompletedWorktreeInclusionMetadata(t *testing.T) {
	for _, includeDirty := range []bool{false, true} {
		t.Run(fmt.Sprintf("include-dirty=%t", includeDirty), func(t *testing.T) {
			root, agent := checkoutRepo(t)
			selected := agent("selected", map[string]string{"backend.py": "def price():\n    return 2\n"})
			put(t, selected, "private-note.txt", "saved change\n")
			args := []string{"selected"}
			want := map[string][2]bool{"main": {true, false}, "selected": {true, false}}
			original := map[string]string{"main": gitTest(t, root, "rev-parse", "HEAD"), "selected": gitTest(t, selected, "rev-parse", "HEAD")}
			if includeDirty {
				// This branch has no commits beyond the base, so only dirty
				// auto-selection adds it to the pinned inputs.
				dirty := agent("dirty-at-base", nil)
				put(t, dirty, "new_module.py", "VALUE = 3\n")
				args = []string{"--include-dirty"}
				want["selected"] = [2]bool{true, true}
				want["dirty-at-base"] = [2]bool{true, true}
				original["dirty-at-base"] = gitTest(t, dirty, "rev-parse", "HEAD")
			} else {
				excluded := agent("excluded", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})
				want["excluded"] = [2]bool{false, false}
				original["excluded"] = gitTest(t, excluded, "rev-parse", "HEAD")
			}
			var completed watch.Event
			watchSession(t, root, watchSessionEvents{completed: 1}, func(e watch.Event, cancel context.CancelFunc) {
				if e.Type == watch.Completed {
					completed = e
					cancel()
				}
			}, args...)
			if watchVerdict(t, completed) != "pass" {
				t.Fatal("static check", watchReport(t, completed))
			}
			report := watchReport(t, completed)
			wantInputs := 1
			if includeDirty {
				wantInputs = 2
			}
			commit, _ := report["candidate_commit"].(string)
			tree, _ := report["candidate_tree"].(string)
			if commit == "" || tree == "" || len(report["inputs"].([]any)) != wantInputs {
				t.Fatal("completed event lost pinned candidate scope", report)
			}
			state := completed.State.Value.(map[string]any)
			worktrees := state["worktrees"].([]any)
			if len(worktrees) != len(want) {
				t.Fatal("worktrees", worktrees)
			}
			for _, value := range worktrees {
				w := value.(map[string]any)
				branch := w["branch"].(string)
				flags, ok := want[branch]
				if !ok || w["commit_included"] != flags[0] || w["uncommitted_included"] != flags[1] {
					t.Fatalf("incorrect inclusion for %q: %+v", branch, w)
				}
				if w["commit"] != original[branch] {
					t.Fatalf("original commit provenance changed for %q: %+v", branch, w)
				}
			}
			if includeDirty {
				refs := state["branches"].([]any)
				if len(refs) != 2 || !strings.Contains(fmt.Sprint(refs), "dirty-at-base") {
					t.Fatal("dirty branch absent from pinned selection", state)
				}
			}
		})
	}
}

func watchSourceFingerprint(t *testing.T, root string) string {
	t.Helper()
	var state bytes.Buffer
	state.WriteString(gitTest(t, root, "show-ref"))
	state.WriteString(gitTest(t, root, "status", "--porcelain=v1", "-z", "--untracked-files=all"))
	index := gitTest(t, root, "rev-parse", "--git-path", "index")
	if !filepath.IsAbs(index) {
		index = filepath.Join(root, index)
	}
	b, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	state.Write(b)
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		fmt.Fprintln(&state, path)
		state.Write(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(state.Bytes()))
}
func TestWatchRunReproducesCombinedFailureAndPreservesSources(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip(err)
	}
	root, agent := checkoutRepo(t)
	a := agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	b := agent("agent-frontend", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})
	for _, branch := range []string{"agent-backend", "agent-frontend"} {
		r := invoke(t, root, 0, "gate", "--run", "--suite", "full", branch)
		if r["gate"].(map[string]any)["verdict"] != "pass" {
			t.Fatal("independent branch failed", branch, r)
		}
	}
	put(t, a, "backend.py", "def price():\n    return 9\n")
	gitTest(t, a, "add", "backend.py")
	put(t, a, "backend.py", "def price():\n    return 10\n")
	put(t, a, "private-note.txt", "uncommitted data\n")
	before := map[string]string{}
	for _, path := range []string{root, a, b} {
		before[path] = watchSourceFingerprint(t, path)
	}
	var completed watch.Event
	_, scope := watchSession(t, root, watchSessionEvents{completed: 1}, func(e watch.Event, cancel context.CancelFunc) {
		if e.Type == watch.Completed {
			completed = e
			cancel()
		}
	}, "--run", "--suite", "full")
	if watchVerdict(t, completed) != "fail" {
		t.Fatal("combined failure not found", watchReport(t, completed))
	}
	if !strings.Contains(scope, "repeated") || !strings.Contains(scope, "not OS sandboxes") {
		t.Fatal("execution scope absent", scope)
	}
	r := watchReport(t, completed)
	runs, ok := r["executions"].([]any)
	if !ok || len(runs) == 0 {
		t.Fatal("missing execution evidence", r)
	}
	for path, want := range before {
		if got := watchSourceFingerprint(t, path); got != want {
			t.Fatalf("watch changed source %s", path)
		}
	}
}

func TestWatchDirtyBranchesAtBaseConflictThenRepair(t *testing.T) {
	root, agent := checkoutRepo(t)
	a := agent("agent-a", nil)
	b := agent("agent-b", nil)
	put(t, a, "backend.py", "def price():\n    return 2\n")
	gitTest(t, a, "add", "backend.py")
	put(t, a, "backend.py", "def price():\n    return 3\n")
	put(t, a, "new_module.py", "VALUE = 3\n")
	put(t, b, "backend.py", "def price():\n    return 4\n")
	expected := map[string]string{}
	for _, path := range []string{root, a, b} {
		expected[path] = watchSourceFingerprint(t, path)
	}
	var checks []watch.Event
	events, _ := watchSession(t, root, watchSessionEvents{completed: 2}, func(e watch.Event, cancel context.CancelFunc) {
		if e.Type != watch.Completed {
			return
		}
		checks = append(checks, e)
		for path, want := range expected {
			if got := watchSourceFingerprint(t, path); got != want {
				t.Fatalf("dirty preview changed %s", path)
			}
		}
		if len(checks) == 1 {
			if watchVerdict(t, e) != "fail" {
				t.Fatal("dirty changes at branch base were omitted", watchReport(t, e))
			}
			if len(watchReport(t, e)["conflicts"].([]any)) != 1 {
				t.Fatal("missing dirty merge conflict", watchReport(t, e))
			}
			put(t, b, "backend.py", "def price():\n    return 1\n")
			expected[b] = watchSourceFingerprint(t, b)
		} else {
			if watchVerdict(t, e) != "pass" {
				t.Fatal("repair not reflected", watchReport(t, e))
			}
			cancel()
		}
	}, "--include-dirty")
	if len(checks) != 2 || checks[0].State.Key == checks[1].State.Key {
		t.Fatal("dirty repair did not change cache key", checks)
	}
	state := events[0].State.Value.(map[string]any)
	snapshots, ok := state["dirty_snapshots"].([]any)
	if !ok || len(snapshots) != 2 {
		t.Fatal("missing dirty snapshot identities", state)
	}
	var found bool
	for _, v := range snapshots {
		s := v.(map[string]any)
		if s["branch"] != "agent-a" {
			continue
		}
		found = true
		if s["digest"] == "" || !watchPathsContain(s["staged"], "backend.py") || !watchPathsContain(s["unstaged"], "backend.py") || !watchPathsContain(s["untracked"], "new_module.py") {
			t.Fatal("lost staged/unstaged/untracked provenance", s)
		}
	}
	if !found {
		t.Fatal("agent snapshot missing", snapshots)
	}
	r := watchReport(t, checks[1])
	commit, _ := r["candidate_commit"].(string)
	tree, _ := r["candidate_tree"].(string)
	if commit == "" || tree == "" {
		t.Fatal("repair omitted candidate identity", r)
	}
	if r["executions"] != nil {
		t.Fatal("dirty static verification executed tests", r)
	}
	inputs := r["inputs"].([]any)
	// Restoring agent-b to the base makes it clean, so automatic selection
	// must retain only agent-a's dirty input in the repaired candidate.
	repairedState := checks[1].State.Value.(map[string]any)
	refs := repairedState["branches"].([]any)
	if len(inputs) != 1 || len(refs) != 1 || refs[0] != "agent-a" {
		t.Fatal("repair did not remove the now-clean branch", inputs, refs)
	}
	base := gitTest(t, root, "rev-parse", "main")
	if inputs[0] == base {
		t.Fatal("dirty candidate reused committed identity", inputs)
	}
}

func watchPathsContain(value any, path string) bool {
	entries, ok := value.([]any)
	if !ok {
		return false
	}
	for _, v := range entries {
		if v == path {
			return true
		}
	}
	return false
}

func TestWatchDirtyUnsupportedEntryInvalidatesCurrentResult(t *testing.T) {
	root, agent := checkoutRepo(t)
	wt := agent("agent-a", map[string]string{"backend.py": "def price():\n    return 2\n"})
	// Creating a symlink may require additional privileges on Windows.
	probe := filepath.Join(t.TempDir(), "probe")
	if err := os.Symlink("backend.py", probe); err != nil {
		t.Skip(err)
	}
	var completed int
	var reportedError bool
	events, _ := watchSession(t, root, watchSessionEvents{completed: 1, errors: 1}, func(e watch.Event, cancel context.CancelFunc) {
		if e.Type == watch.Completed {
			completed++
			if completed > 1 {
				t.Fatal("unsupported dirty snapshot completed", e)
			}
			if watchVerdict(t, e) != "pass" {
				t.Fatal("initial static check", e)
			}
			if err := os.Symlink("backend.py", filepath.Join(wt, "unsupported.py")); err != nil {
				t.Fatal(err)
			}
		}
		if e.Type == watch.Error {
			reportedError = true
			if e.Result != nil {
				t.Fatal("error retains obsolete result", e)
			}
			cancel()
		}
	}, "--include-dirty")
	if completed != 1 || !reportedError {
		t.Fatalf("unsupported snapshot not invalidated: %+v", events)
	}
	if _, err := os.Lstat(filepath.Join(wt, "unsupported.py")); err != nil {
		t.Fatal("watch removed user entry", err)
	}
}

func TestWatchAttributionRequiresExecutionAuthorization(t *testing.T) {
	root, _ := checkoutRepo(t)
	for _, value := range []string{"1", "-1", "65"} {
		code, _, errs := run(t, root, "watch", "--attribution-runs", value)
		if code != 2 || errs == "" {
			t.Fatalf("attribution %s: exit %d %q", value, code, errs)
		}
	}
}

func TestWatchAttributionFollowsCompletedFailure(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip(err)
	}
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-frontend", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})
	var completed watch.Event
	events, _ := watchSession(t, root, watchSessionEvents{completed: 1, attributed: 1}, func(e watch.Event, cancel context.CancelFunc) {
		switch e.Type {
		case watch.Completed:
			if completed.Sequence != 0 {
				t.Fatal("gate completed twice", e)
			}
			completed = e
			if watchVerdict(t, e) != "fail" {
				t.Fatal("Fast Gate lost failure", watchReport(t, e))
			}
			if watchReport(t, e)["observed_attributions"] != nil {
				t.Fatal("Fast Gate waited for attribution", e)
			}
		case watch.Attributed:
			if completed.Sequence == 0 || e.Sequence <= completed.Sequence || e.State.Key != completed.State.Key {
				t.Fatal("attribution ordering or identity", e)
			}
			diagnostic := watchReport(t, e)
			if diagnostic["gate"] != nil || diagnostic["candidate_tree"] != watchReport(t, completed)["candidate_tree"] {
				t.Fatal("attribution replaced gate or candidate", diagnostic)
			}
			observations, ok := diagnostic["observed_attributions"].([]any)
			if !ok || len(observations) == 0 {
				t.Fatal("missing diagnostic observations", diagnostic)
			}
			if watchVerdict(t, completed) != "fail" {
				t.Fatal("attribution changed original verdict")
			}
			cancel()
		case watch.Error:
			t.Fatal("attribution error", e.Error)
		}
	}, "--run", "--suite", "full", "--attribution-runs", "8")
	if len(events) == 0 || events[len(events)-1].Type != watch.Attributed {
		t.Fatal("attribution not published", events)
	}
}

func TestWatchDirtyExplicitFullBranchReference(t *testing.T) {
	root, agent := checkoutRepo(t)
	wt := agent("feature", nil)
	put(t, wt, "backend.py", "def price():\n    return 8\n")
	events, _ := watchSession(t, root, watchSessionEvents{completed: 1}, func(e watch.Event, cancel context.CancelFunc) {
		if e.Type == watch.Completed {
			if watchVerdict(t, e) != "pass" {
				t.Fatal(e)
			}
			cancel()
		}
	}, "--include-dirty", "refs/heads/feature")
	state := events[0].State.Value.(map[string]any)
	snapshots, ok := state["dirty_snapshots"].([]any)
	if !ok || len(snapshots) != 1 || snapshots[0].(map[string]any)["branch"] != "feature" {
		t.Fatal("full branch ref omitted dirty worktree", state)
	}
	r := watchReport(t, events[len(events)-1])
	if r["inputs"].([]any)[0] == gitTest(t, root, "rev-parse", "main") {
		t.Fatal("dirty input not represented", r)
	}
}

func TestWatchDirtySharedSHARefusesAmbiguousWorktree(t *testing.T) {
	root, agent := checkoutRepo(t)
	a := agent("feature-a", nil)
	agent("feature-b", nil)
	put(t, a, "backend.py", "def price():\n    return 8\n")
	sha := gitTest(t, root, "rev-parse", "main")
	code, out, errs := run(t, root, "watch", "--jsonl", "--include-dirty", sha)
	if code != 2 || !strings.Contains(errs, "multiple worktrees") || strings.Contains(out, `"completed"`) {
		t.Fatalf("ambiguous SHA: exit %d stdout %q stderr %q", code, out, errs)
	}
}

func TestWatchUnsupportedTestConfigurationCannotPass(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"app.ts": "export const n = 1;\n", "app.test.ts": "import test from 'node:test';\ntest('n', () => {});\n"})
	agent("feature", map[string]string{"app.test.ts": "import test from 'node:test';\ntest('edited', () => {});\n"})
	var completed watch.Event
	watchSession(t, root, watchSessionEvents{completed: 1}, func(e watch.Event, cancel context.CancelFunc) {
		if e.Type == watch.Completed {
			completed = e
			cancel()
		}
	}, "--run", "--suite", "full")
	if watchVerdict(t, completed) == "pass" {
		t.Fatal("unsupported TypeScript runner passed", watchReport(t, completed))
	}
	r := watchReport(t, completed)
	selection, ok := r["selection"].(map[string]any)
	if !ok {
		t.Fatal("unsupported inventory missing selection", r)
	}
	omitted, ok := selection["omitted"].([]any)
	if !ok || len(omitted) == 0 {
		t.Fatal("unsupported test silently omitted", selection)
	}
	found := false
	for _, item := range omitted {
		if item.(map[string]any)["reason"] == "unsupported_configuration" {
			found = true
		}
	}
	if !found {
		t.Fatal("unsupported configuration not explained", omitted)
	}
}

func TestWatchDiscoversWorktreeCreationAndRemoval(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-a", map[string]string{"backend.py": "def price():\n    return 2\n"})
	var checks []watch.Event
	var added string
	watchSession(t, root, watchSessionEvents{completed: 3}, func(e watch.Event, cancel context.CancelFunc) {
		if e.Type != watch.Completed {
			return
		}
		checks = append(checks, e)
		switch len(checks) {
		case 1:
			if watchVerdict(t, e) != "pass" {
				t.Fatal(e)
			}
			added = agent("agent-b", map[string]string{"backend.py": "def price():\n    return 3\n"})
		case 2:
			if watchVerdict(t, e) != "fail" {
				t.Fatal("new worktree omitted", watchReport(t, e))
			}
			gitTest(t, root, "worktree", "remove", added)
		case 3:
			if watchVerdict(t, e) != "pass" {
				t.Fatal("removed worktree retained", watchReport(t, e))
			}
			cancel()
		}
	})
	if len(checks) != 3 || checks[0].State.Key == checks[1].State.Key || checks[1].State.Key == checks[2].State.Key {
		t.Fatal("worktree lifecycle not observed", checks)
	}
	if _, err := os.Stat(added); !os.IsNotExist(err) {
		t.Fatal("fixture worktree unexpectedly remains", err)
	}
}

func TestWatchReusesCommittedAnalysisForExcludedDirtyMetadata(t *testing.T) {
	root, agent := checkoutRepo(t)
	wt := agent("feature", map[string]string{"backend.py": "def price():\n    return 2\n"})
	var checks []watch.Event
	var savedFingerprint string
	watchSession(t, root, watchSessionEvents{completed: 2}, func(e watch.Event, cancel context.CancelFunc) {
		if e.Type != watch.Completed {
			return
		}
		checks = append(checks, e)
		if watchVerdict(t, e) != "pass" {
			t.Fatal("static gate", watchReport(t, e))
		}
		if len(checks) == 1 {
			put(t, wt, "backend.py", "def price():\n    return 999\n")
			savedFingerprint = watchSourceFingerprint(t, wt)
			return
		}
		first := watchReport(t, checks[0])
		reused := watchReport(t, e)
		if checks[0].State.Key == e.State.Key {
			t.Fatal("excluded dirty status was not observed")
		}
		for _, field := range []string{"base", "inputs", "candidate_commit", "candidate_tree", "gate", "checks", "executions"} {
			before, _ := json.Marshal(first[field])
			after, _ := json.Marshal(reused[field])
			if !bytes.Equal(before, after) {
				t.Fatalf("cache changed %s evidence: %s -> %s", field, before, after)
			}
		}
		found := false
		for _, v := range reused["limitations"].([]any) {
			if strings.Contains(v.(string), "Reused analysis") {
				found = true
			}
		}
		if !found {
			t.Fatal("unchanged committed analysis was redundantly rebuilt", reused)
		}
		if got := watchSourceFingerprint(t, wt); got != savedFingerprint {
			t.Fatal("cache check changed saved user edits")
		}
		cancel()
	})
	if len(checks) != 2 {
		t.Fatal("expected initial and reused report", checks)
	}
}

func TestWatchRefusesNonregularPolicy(t *testing.T) {
	root, _ := checkoutRepo(t)
	code, out, errs := run(t, root, "watch", "--jsonl", "--policy", t.TempDir())
	if code != 2 || !strings.Contains(errs, "regular file") || strings.Contains(out, `"completed"`) {
		t.Fatalf("nonregular policy: exit %d stdout %q stderr %q", code, out, errs)
	}
}

func TestWatchDirtyUnattachedLocalBranchDoesNotSelectSharedHeadWorktree(t *testing.T) {
	root, agent := checkoutRepo(t)
	wt := agent("attached", nil)
	gitTest(t, root, "branch", "unattached", "main")
	put(t, wt, "backend.py", "def price():\n    return 99\n")
	var completed watch.Event
	events, _ := watchSession(t, root, watchSessionEvents{completed: 1}, func(e watch.Event, cancel context.CancelFunc) {
		if e.Type == watch.Completed {
			completed = e
			cancel()
		}
	}, "--include-dirty", "unattached")
	state := events[0].State.Value.(map[string]any)
	if snapshots, ok := state["dirty_snapshots"].([]any); ok && len(snapshots) > 0 {
		t.Fatal("unattached branch acquired unrelated dirty worktree", snapshots)
	}
	if watchVerdict(t, completed) != "pass" {
		t.Fatal("explicit committed branch refused", watchReport(t, completed))
	}
	inputs := watchReport(t, completed)["inputs"].([]any)
	if len(inputs) != 1 || inputs[0] != gitTest(t, root, "rev-parse", "unattached") {
		t.Fatal("unattached branch preview identity changed", inputs)
	}
}

func TestWatchDirtyHEADSelectsOnlyInvocationWorktree(t *testing.T) {
	root, agent := checkoutRepo(t)
	sibling := agent("sibling", nil)
	put(t, root, "backend.py", "def price():\n    return 7\n")
	put(t, sibling, "backend.py", "def price():\n    return 99\n")
	var completed watch.Event
	events, _ := watchSession(t, root, watchSessionEvents{completed: 1}, func(e watch.Event, cancel context.CancelFunc) {
		if e.Type == watch.Completed {
			completed = e
			cancel()
		}
	}, "--include-dirty", "HEAD")
	state := events[0].State.Value.(map[string]any)
	snapshots, ok := state["dirty_snapshots"].([]any)
	if !ok || len(snapshots) != 1 || snapshots[0].(map[string]any)["worktree"] != root {
		t.Fatal("HEAD selected sibling snapshot or omitted root", state)
	}
	if watchVerdict(t, completed) != "pass" {
		t.Fatal("HEAD dirty preview not verified", watchReport(t, completed))
	}
	if watchReport(t, completed)["inputs"].([]any)[0] == gitTest(t, root, "rev-parse", "HEAD") {
		t.Fatal("HEAD dirty edits omitted")
	}
}

func TestWatchDirtyAutoSelectsStatusHiddenModeChange(t *testing.T) {
	root, agent := checkoutRepo(t)
	wt := agent("hidden-mode", nil)
	gitTest(t, wt, "config", "core.fileMode", "false")
	path := filepath.Join(wt, "backend.py")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, info.Mode()|0111); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode()&0111 == 0 {
		t.Skip("filesystem does not expose executable permissions")
	}
	if status := gitTest(t, wt, "status", "--porcelain"); status != "" {
		t.Fatalf("fixture mode was not hidden: %q", status)
	}
	var completed watch.Event
	watchSession(t, root, watchSessionEvents{completed: 1}, func(e watch.Event, cancel context.CancelFunc) {
		if e.Type == watch.Completed {
			completed = e
			cancel()
		}
	}, "--include-dirty")
	if watchVerdict(t, completed) != "pass" {
		t.Fatal("hidden mode preview not checked", watchReport(t, completed))
	}
	if watchReport(t, completed)["inputs"].([]any)[0] == gitTest(t, wt, "rev-parse", "HEAD") {
		t.Fatal("hidden mode preview fell back to committed state")
	}
}

func TestWatchDirtyRejectsNormalizationEvenWithCleanStatus(t *testing.T) {
	root, agent := checkoutRepo(t)
	wt := agent("normalized", nil)
	gitTest(t, wt, "config", "core.autocrlf", "true")
	if status := gitTest(t, wt, "status", "--porcelain"); status != "" {
		t.Fatalf("fixture unexpectedly dirty: %q", status)
	}
	code, out, errs := run(t, root, "watch", "--include-dirty", "--jsonl")
	if code != 2 || !strings.Contains(errs, "normalization") || strings.Contains(out, `"completed"`) {
		t.Fatalf("normalization was not refused: exit %d stdout %q stderr %q", code, out, errs)
	}
}

func TestWatchDirtyCanonicalBaseReferenceExcludesBaseWorktree(t *testing.T) {
	root, _ := checkoutRepo(t)
	put(t, root, "backend.py", "def price():\n    return 42\n")
	before := watchSourceFingerprint(t, root)
	var completed watch.Event
	events, _ := watchSession(t, root, watchSessionEvents{completed: 1}, func(e watch.Event, cancel context.CancelFunc) {
		if e.Type == watch.Completed {
			completed = e
			cancel()
		}
	}, "--include-dirty", "--base", "refs/heads/main")
	r := watchReport(t, completed)
	if r["state"] != "NOT VERIFIED" || r["gate"] != nil {
		t.Fatal("dirty base was incorrectly treated as agent input", r)
	}
	state := events[0].State.Value.(map[string]any)
	for _, field := range []string{"branches", "commits", "dirty_snapshots"} {
		if entries, ok := state[field].([]any); ok && len(entries) > 0 {
			t.Fatalf("base reference admitted %s: %v", field, entries)
		}
	}
	if got := watchSourceFingerprint(t, root); got != before {
		t.Fatal("base exclusion changed user source")
	}
}
