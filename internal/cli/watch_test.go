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
	events, _ := watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
	events, _ := watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
	watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
	watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
	watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
	events, _ := watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
	events, _ := watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
	watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
	events, _ := watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
