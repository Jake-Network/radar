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

	"github.com/Jake-Network/radar/internal/watch"
)

// watchEventWriter decodes complete JSONL events synchronously. Callbacks can
// cancel a session or commit a fixture update without timing-dependent sleeps.
type watchEventWriter struct {
	pending  []byte
	events   []watch.Event
	callback func(watch.Event)
	err      error
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
func watchSession(t *testing.T, root string, callback func(watch.Event, context.CancelFunc), args ...string) ([]watch.Event, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out := &watchEventWriter{callback: func(e watch.Event) { callback(e, cancel) }}
	var errs bytes.Buffer
	argv := append([]string{"watch", "--root", root, "--jsonl", "--interval", "100ms", "--debounce", "1ms"}, args...)
	if code := Run(ctx, argv, out, &errs); code != 0 {
		t.Fatalf("watch exit %d: %s", code, errs.String())
	}
	if ctx.Err() == context.DeadlineExceeded {
		t.Fatal("watch did not complete before deadline", errs.String())
	}
	if out.err != nil || len(out.pending) > 0 {
		t.Fatalf("invalid JSONL: %v %q", out.err, out.pending)
	}
	for i, e := range out.events {
		if e.Sequence != uint64(i+1) || e.Time.IsZero() {
			t.Fatalf("inconsistent event %d: %+v", i, e)
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
	events, _ := watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
	events, _ := watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
	_, scope := watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
	events, _ := watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
	if r["executions"] != nil {
		t.Fatal("dirty static verification executed tests", r)
	}
	inputs := r["inputs"].([]any)
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
	events, _ := watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
