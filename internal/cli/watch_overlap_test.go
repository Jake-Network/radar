package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/overlap"
	"github.com/Jake-Network/radar/internal/project"
	"github.com/Jake-Network/radar/internal/watch"
)

func TestWorktreeDirtySymbolDriftAndHookOwnership(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"backend.py": "def charge():\n    return 1\n", "client.py": "VALUE = 1\n"})
	a := agent("agent-api", nil)
	b := agent("agent-client", nil)
	put(t, a, "backend.py", "def bill():\n    return 1\n")
	put(t, b, "client.py", "from backend import charge\nVALUE = charge()\n")
	beforeA, beforeB := watchSourceFingerprint(t, a), watchSourceFingerprint(t, b)
	cli := &app{ctx: context.Background(), root: root}
	start := time.Now()
	state, err := cli.observeWatch(cli.ctx, options{base: "main", includeDirty: true})
	if err != nil {
		t.Fatal(err)
	}
	in := state.Value.(watchInput)
	report, err := overlap.Analyze(cli.ctx, root, in.Base, overlapInputs(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 1 || report.Findings[0].Code != "symbol_dangling_reference" || len(report.Findings[0].Locations) != 2 {
		t.Fatalf("missing symbol drift: %+v", report)
	}
	// The five-second incremental watch target belongs to Phase 2. This
	// regression verifies static drift and ownership; retain the actual cold
	// measurement without treating a functional pass as that performance claim.
	t.Logf("cold dirty observation and overlap elapsed=%s (Phase 2 target: 5s)", time.Since(start))
	result := hookPairWithContinuation(t, root, b, "symbol_dangling_reference")
	if result.Decision != "block" || !strings.Contains(result.Reason, "cross-worktree drift") || !strings.Contains(result.Reason, "backend.py:1") || !strings.Contains(result.Reason, "agent-api") {
		t.Fatalf("dirty consumer not blocked: %+v", result)
	}
	// Peer bytes change with identical status paths: invalidate the pair result
	// while retaining the independent unchanged self preview.
	put(t, a, "backend.py", "def charge():\n    return 2\n")
	repaired := hookInvoke(t, root, hookEvent(t, b), "--base", "main", "--json")
	if repaired.Decision != "" || repaired.Radar == nil || repaired.Radar.Self != "reused" || repaired.Radar.Checked == 0 {
		t.Fatalf("peer edit did not invalidate cached block: %+v", repaired)
	}
	put(t, a, "backend.py", "def bill():\n    return 1\n")
	if watchSourceFingerprint(t, a) != beforeA || watchSourceFingerprint(t, b) != beforeB {
		t.Fatal("drift inspection mutated sources")
	}
	// Once the consumer is committed, this same pre-existing peer drift is
	// informational; repeated Stop events must not ask it to repair peers.
	gitTest(t, b, "add", "client.py")
	gitTest(t, b, "commit", "-qm", "consume charge")
	inherited := hookInvoke(t, root, hookEvent(t, b), "--base", "main")
	if inherited.Decision != "" || !strings.Contains(inherited.Message, "informational") {
		t.Fatalf("inherited drift blocked clean branch: %+v", inherited)
	}
}

func TestOverlapExcludesStandaloneFailuresAndUnrelatedChanges(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"backend.py": "def charge():\n    return 1\n", "client.py": "VALUE = 1\n"})
	a := agent("api", nil)
	b := agent("docs", nil)
	put(t, a, "backend.py", "def charge():\n    return 2\n")
	put(t, b, "readme.md", "documentation\n")
	cli := &app{ctx: context.Background(), root: root}
	state, err := cli.observeWatch(cli.ctx, options{base: "main", includeDirty: true})
	if err != nil {
		t.Fatal(err)
	}
	in := state.Value.(watchInput)
	report, err := overlap.Analyze(cli.ctx, root, in.Base, overlapInputs(in))
	if err != nil || len(report.Findings) != 0 {
		t.Fatalf("unrelated drift fabricated: %+v %v", report, err)
	}
	put(t, a, "backend.py", "def bill():\n    return 2\n")
	put(t, a, "bad.py", "from backend import charge\n")
	state, err = cli.observeWatch(cli.ctx, options{base: "main", includeDirty: true})
	if err != nil {
		t.Fatal(err)
	}
	in = state.Value.(watchInput)
	report, err = overlap.Analyze(cli.ctx, root, in.Base, overlapInputs(in))
	if err != nil || len(report.Findings) != 0 {
		t.Fatalf("standalone failure attributed to pair: %+v %v", report, err)
	}
}

func TestWatchEmitsDirtyDriftJSONL(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"backend.py": "def charge():\n    return 1\n", "client.py": "VALUE = 1\n"})
	a := agent("api", nil)
	b := agent("client", nil)
	put(t, a, "backend.py", "def bill():\n    return 1\n")
	put(t, b, "client.py", "from backend import charge\n")
	beforeA, beforeB := watchSourceFingerprint(t, a), watchSourceFingerprint(t, b)
	// Stop on the completed event, rather than racing native Git startup with
	// the not-yet-implemented five-second incremental indexing target. The Go
	// test deadline is only a watchdog for a missing event or failed shutdown.
	ctx, cancel := watchFunctionalContext(t)
	defer cancel()
	writer := &driftEventWriter{cancel: cancel}
	var errs bytes.Buffer
	cli := &app{ctx: ctx, root: root, out: writer, errout: &errs}
	started := time.Now()
	code := cli.watch(options{base: "main", includeDirty: true, jsonl: true, interval: 100 * time.Millisecond, timeout: time.Minute, maxCommands: 16})
	t.Logf("cold dirty watch completed event elapsed=%s (Phase 2 target: 5s)", time.Since(started))
	if code != 0 || ctx.Err() != context.Canceled {
		t.Fatalf("watch did not stop on completion, exit=%d context=%v stderr=%s: %s", code, ctx.Err(), errs.String(), writer.out.String())
	}
	completed := 0
	for _, line := range strings.Split(strings.TrimSpace(writer.out.String()), "\n") {
		var event struct {
			Type   string `json:"type"`
			Error  string `json:"error"`
			Result *struct {
				Value integration.Report `json:"value"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal("invalid watch JSONL", err, line)
		}
		if event.Type == watch.Error || event.Error != "" {
			t.Fatal("watch emitted an analysis error", line)
		}
		if event.Type != watch.Completed {
			continue
		}
		completed++
		if event.Result == nil {
			t.Fatal("completed watch event omitted its report")
		}
		report := event.Result.Value
		if report.CandidateCommit == "" || report.CandidateTree == "" || len(report.Inputs) != 2 || report.Gate.Verdict != gate.Fail {
			t.Fatalf("completed watch event lost candidate-bound failure: %+v", report)
		}
		if len(report.Drift) != 1 || report.Drift[0].Code != "symbol_dangling_reference" || len(report.Drift[0].Branches) != 2 || len(report.Drift[0].Locations) != 2 {
			t.Fatalf("completed watch event lost concrete cross-worktree drift: %+v", report.Drift)
		}
	}
	if completed != 1 {
		t.Fatalf("expected one completed event, got %d: %s", completed, writer.out.String())
	}
	if watchSourceFingerprint(t, a) != beforeA || watchSourceFingerprint(t, b) != beforeB {
		t.Fatal("watch drift event mutated source worktrees")
	}
}

type driftEventWriter struct {
	out    bytes.Buffer
	cancel context.CancelFunc
}

func (w *driftEventWriter) Write(data []byte) (int, error) {
	n, err := w.out.Write(data)
	var e struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(bytes.TrimSpace(data), &e) == nil && (e.Type == watch.Completed || e.Type == watch.Error) {
		w.cancel()
	}
	return n, err
}

func TestHookCrossWorktreeDirtyTextualConflict(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"backend.py": "def charge():\n    return 1\n"})
	a := agent("api", nil)
	b := agent("client", nil)
	put(t, a, "backend.py", "def charge():\n    return 2\n")
	put(t, b, "backend.py", "def charge():\n    return 3\n")
	r := hookInvoke(t, root, hookEvent(t, b), "--base", "main")
	if r.Decision != "block" || !strings.Contains(r.Reason, "textual conflict") {
		t.Fatalf("peer dirty conflict did not block: %+v", r)
	}
	gitTest(t, b, "add", "backend.py")
	gitTest(t, b, "commit", "-qm", "client saved")
	r = hookInvoke(t, root, hookEvent(t, b), "--base", "main")
	if r.Decision != "" || !strings.Contains(r.Message, "informational") {
		t.Fatalf("committed peer conflict blocked: %+v", r)
	}
}

func TestHookPersistsSyntaxOnlyCache(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"backend.py": "def charge():\n    return 1\n"})
	a := agent("api", nil)
	put(t, a, "backend.py", "def bill():\n    return 1\n")
	for i, cwd := range []string{a, a + string(os.PathSeparator) + "."} {
		result := hookInvoke(t, root, hookEvent(t, cwd), "--base", "main", "--json")
		if result.Decision != "" || result.Radar == nil || result.Radar.Self != "complete" && result.Radar.Self != "reused" {
			t.Fatalf("static preview did not complete before checking syntax persistence: %+v", result)
		}
		if i > 0 && result.Radar.Self != "reused" {
			t.Fatalf("equivalent public CWD did not reuse the completed self preview: %+v", result.Radar)
		}
		// Production callers resolve the public CWD before choosing a cache namespace.
		canonical, err := project.Root(cwd)
		if err != nil {
			t.Fatal(err)
		}
		cache, _ := savedSyntaxCache(canonical)
		if cache.Stats().Entries == 0 {
			t.Fatalf("hook did not persist reusable syntax for equivalent root %q; response=%+v diagnostics=%+v", cwd, result, result.Radar)
		}
	}
	other := agent("other", nil)
	canonicalOther, err := project.Root(other)
	if err != nil {
		t.Fatal(err)
	}
	cache, _ := savedSyntaxCache(canonicalOther)
	if cache.Stats().Entries != 0 {
		t.Fatal("syntax cache crossed into a different worktree")
	}
	otherRoot, _ := agentRepo(t, map[string]string{"backend.py": "def bill():\n    return 1\n"})
	canonicalRoot, err := project.Root(otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	cache, _ = savedSyntaxCache(canonicalRoot)
	if cache.Stats().Entries != 0 {
		t.Fatal("syntax cache crossed into a different repository")
	}
}

func TestOverlapCLIKeepsGateContractAndFilters(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"backend.py": "def charge():\n    return 1\n", "client.py": "VALUE = 1\n"})
	a := agent("api", nil)
	b := agent("client", nil)
	put(t, a, "backend.py", "def bill():\n    return 1\n")
	put(t, b, "client.py", "from backend import charge\n")
	r := invoke(t, root, 1, "gate", "--overlap", "--base", "main", "--symbol", "charge", "--file", "backend.py")
	for _, key := range []string{"gate", "coverage", "checks", "status", "base", "inputs", "findings", "diagnostics", "limitations", "overlap", "drift"} {
		if _, ok := r[key]; !ok {
			t.Fatalf("missing core %s in %+v", key, r)
		}
	}
	if r["gate"].(map[string]any)["verdict"] != "fail" || len(r["drift"].([]any)) != 1 {
		t.Fatalf("wrong overlap verdict: %+v", r)
	}
	r = invoke(t, root, 1, "gate", "--overlap", "--base", "main", "--symbol", "unrelated")
	if r["gate"].(map[string]any)["verdict"] != "fail" {
		t.Fatal("display filter hid combined failure", r)
	}
	if d, ok := r["drift"]; ok && len(d.([]any)) != 0 {
		t.Fatal("symbol filter ignored", d)
	}
}

func TestHookInheritedPeerConflictIgnoresCurrentDirtyContext(t *testing.T) {
	source := func(value string) string {
		return "def first():\n    return " + value + "\n" + strings.Repeat("\n# separate functions\n", 12) + "def second():\n    return 1\n"
	}
	root, agent := agentRepo(t, map[string]string{"backend.py": source("1")})
	agent("api", map[string]string{"backend.py": source("2")})
	b := agent("client", map[string]string{"backend.py": source("3")})
	put(t, b, "backend.py", source("3")+"\n# unrelated saved comment\n")
	r := hookInvoke(t, root, hookEvent(t, b), "--base", "main")
	if r.Decision != "" || !strings.Contains(r.Message, "informational") {
		t.Fatalf("unrelated same-file dirty context caused peer block: %+v", r)
	}
	put(t, b, "backend.py", source("4")+"\n# unrelated saved comment\n")
	r = hookInvoke(t, root, hookEvent(t, b), "--base", "main")
	if r.Decision != "block" || !strings.Contains(r.Reason, "cross-worktree drift") {
		t.Fatalf("dirty conflict content did not block: %+v", r)
	}
}

func TestHookDirtyPairUniqueKeyAndRouteDrift(t *testing.T) {
	for _, fixture := range []struct {
		name, code         string
		files, left, right map[string]string
	}{
		{name: "key", code: "duplicate_unique_key", files: map[string]string{".radar/unique.json": `{"version":1,"rules":[{"name":"error code","files":["src/**/*.txt"],"pattern":"E[0-9]+"}]}`, "src/seed.txt": "E0"}, left: map[string]string{"src/a.txt": "E100"}, right: map[string]string{"src/b.txt": "E100"}},
		{name: "route", code: "duplicate_route", files: map[string]string{"main.py": "from fastapi import FastAPI\nfrom a import router as a\nfrom b import router as b\napp=FastAPI()\napp.include_router(a)\napp.include_router(b)\n", "a.py": "from fastapi import APIRouter\nrouter=APIRouter()\n", "b.py": "from fastapi import APIRouter\nrouter=APIRouter()\n"}, left: map[string]string{"a.py": "from fastapi import APIRouter\nrouter=APIRouter()\n@router.get(\"/items\")\ndef first(): pass\n"}, right: map[string]string{"b.py": "from fastapi import APIRouter\nrouter=APIRouter()\n@router.get(\"/items\")\ndef second(): pass\n"}},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			root, agent := agentRepo(t, fixture.files)
			a, b := agent("api", nil), agent("client", nil)
			for p, s := range fixture.left {
				put(t, a, p, s)
			}
			for p, s := range fixture.right {
				put(t, b, p, s)
			}
			r := hookPairWithContinuation(t, root, b, fixture.code)
			if r.Decision != "block" || !strings.Contains(r.Reason, fixture.code) {
				t.Fatalf("missing %s dirty pair block: %+v", fixture.code, r)
			}
		})
	}
}

func TestWatchBranchRenameInvalidatesDriftLabels(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"backend.py": "def charge():\n    return 1\n", "client.py": "VALUE=1\n"})
	agent("api", map[string]string{"backend.py": "def bill():\n    return 1\n"})
	b := agent("client", map[string]string{"client.py": "from backend import charge\n"})
	completed := 0
	watchOverlapSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
		if e.Type != watch.Completed {
			return
		}
		completed++
		report := watchReport(t, e)
		drift := report["drift"].([]any)
		if len(drift) != 1 {
			t.Fatalf("missing drift: %+v", report)
		}
		branches := drift[0].(map[string]any)["branches"].([]any)
		if completed == 1 {
			gitTest(t, b, "branch", "-m", "consumer-renamed")
			return
		}
		for _, branch := range branches {
			if branch == "client" {
				t.Fatalf("cached previous branch label after rename: %+v", drift)
			}
		}
		found := false
		for _, branch := range branches {
			if branch == "consumer-renamed" {
				found = true
			}
		}
		if !found {
			t.Fatalf("new branch label missing: %+v", drift)
		}
		cancel()
	})
	if completed != 2 {
		t.Fatalf("wanted two checks, got %d", completed)
	}
}

func TestHookEarlyMergeFailureLeavesLaterDirtyInputUnchecked(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"backend.py": "VALUE = 1\n"})
	agent("early", map[string]string{"backend.py": "VALUE = 2\n"})
	put(t, root, "backend.py", "VALUE = 3\n")
	gitTest(t, root, "add", "backend.py")
	gitTest(t, root, "commit", "-qm", "base advanced")
	current := agent("current", nil)
	put(t, current, "new.py", "VALUE = 4\n")
	result := hookInvoke(t, root, hookEvent(t, current), "--base", "main", "--branches", "early,current")
	if result.Decision != "" || !strings.Contains(result.Message, "later inputs remain unchecked") || strings.Contains(result.Message, "introduced no new") {
		t.Fatalf("skipped dirty input fabricated an unchanged-conflict claim: %+v", result)
	}
}

func TestOverlapCurrentScopeUsesBoundedPeerPairs(t *testing.T) {
	root, _ := agentRepo(t, map[string]string{"backend.py": "VALUE = 1\n"})
	head := gitTest(t, root, "rev-parse", "HEAD")
	inputs := []overlap.Input{}
	for i := 0; i < 13; i++ {
		inputs = append(inputs, overlap.Input{Branch: fmt.Sprintf("branch-%d", i), Head: head})
	}
	focused, err := overlap.AnalyzeCurrent(context.Background(), root, head, "branch-12", inputs)
	if err != nil || focused.Pairs != 11 || !strings.Contains(strings.Join(focused.Limitations, " "), "1 of 12 eligible pairs omitted") {
		t.Fatalf("current scope was not bounded and incomplete: %+v %v", focused, err)
	}
}

func TestOverlapSymbolFilterMatchesNamesOnly(t *testing.T) {
	tests := []struct{ code, text, want string }{
		{"symbol_dangling_reference", "Cross-worktree drift: client.py newly references removed name charge from backend.py.", "charge"},
		{"symbol_argument_count", "client.py newly calls charge with 2 arguments; the combined declaration now accepts 1.", "charge"},
		{"symbol_duplicate_definition", "New duplicate declaration go:api:server:charge in the combined tree.", "charge"},
		{"worktree_textual_conflict", "Saved branch snapshots have a textual conflict in charge.py.", ""},
	}
	for _, tt := range tests {
		got := overlapFindingSymbol(model.Finding{Code: tt.code, Explanation: tt.text})
		if got != tt.want {
			t.Fatalf("%s: got %q want %q", tt.code, got, tt.want)
		}
	}
}

func TestWatchMoreThanTwelveInputsRetainsCandidatePreview(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"backend.py": "VALUE = 1\n"})
	refs := []string{}
	for i := 0; i < 13; i++ {
		name := fmt.Sprintf("agent-%02d", i)
		refs = append(refs, name)
		agent(name, map[string]string{fmt.Sprintf("docs/note-%02d.md", i): "documentation\n"})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	writer := &driftEventWriter{cancel: cancel}
	cli := &app{ctx: ctx, root: root, out: writer, errout: &bytes.Buffer{}}
	code := cli.watch(options{base: "main", args: refs, jsonl: true, interval: time.Second, timeout: time.Minute, maxCommands: 16})
	if code != 0 {
		t.Fatalf("watch exit %d: %s", code, writer.out.String())
	}
	var report *integration.Report
	for _, line := range strings.Split(strings.TrimSpace(writer.out.String()), "\n") {
		var event struct {
			Type   string `json:"type"`
			Error  string `json:"error"`
			Result *struct {
				Value integration.Report `json:"value"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == "error" {
			t.Fatalf("overlap limit killed candidate preview: %s", event.Error)
		}
		if event.Type == "completed" && event.Result != nil {
			report = &event.Result.Value
		}
	}
	if report == nil || report.CandidateTree == "" || len(report.Changed) != 13 || report.Gate.Verdict != gate.Pass {
		t.Fatalf("complete static candidate preview missing: %+v", report)
	}
	foundCheck, foundCoverage := false, false
	for _, check := range report.Checks {
		if check.ID == "worktree_overlap" {
			foundCheck = check.Status == model.StatusUnknown
		}
	}
	for _, coverage := range report.Coverage {
		if coverage.Analyzer == "worktree_overlap" {
			foundCoverage = coverage.Status == model.StatusUnknown && len(coverage.Unavailable) > 0
		}
	}
	for _, required := range report.Gate.Required {
		if required.ID == "worktree_overlap" {
			t.Fatal("supplemental pair gap changed candidate requirements")
		}
	}
	if !strings.Contains(strings.Join(report.Limitations, " "), "12 of 78 eligible pairs omitted") {
		t.Fatalf("all-pair limit was not reported precisely: %+v", report.Limitations)
	}
	if !foundCheck || !foundCoverage {
		t.Fatalf("pair omissions missing machine-readable unknown check/coverage: %+v", report)
	}
	// No omission means no additional public check or coverage.
	beforeChecks, beforeCoverage := len(report.Checks), len(report.Coverage)
	watchOverlapCoverage(report, overlap.Report{}, refs)
	if len(report.Checks) != beforeChecks || len(report.Coverage) != beforeCoverage {
		t.Fatal("complete pair reports gained unknown coverage")
	}
}

// These sessions verify events and shutdown, not the Phase 2 latency target.
// Their lifecycle ends on a semantic completion; the test runner owns the
// watchdog so native Git startup does not silently cancel the fixture early.
func watchFunctionalContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	if deadline, ok := t.Deadline(); ok {
		return context.WithDeadline(context.Background(), deadline.Add(-time.Second))
	}
	return context.WithCancel(context.Background())
}

func watchOverlapSession(t *testing.T, root string, callback func(watch.Event, context.CancelFunc)) {
	t.Helper()
	ctx, cancel := watchFunctionalContext(t)
	defer cancel()
	out := &watchEventWriter{callback: func(event watch.Event) {
		if event.Type == watch.Error {
			cancel()
			return
		}
		callback(event, cancel)
	}}
	var errs bytes.Buffer
	started := time.Now()
	code := Run(ctx, []string{"watch", "--root", root, "--jsonl", "--interval", "100ms", "--debounce", "1ms"}, out, &errs)
	t.Logf("watch event session elapsed=%s", time.Since(started))
	if code != 0 || ctx.Err() != context.Canceled || out.err != nil || len(out.pending) != 0 {
		t.Fatalf("watch event session failed: exit=%d context=%v JSONL=%v stderr=%s", code, ctx.Err(), out.err, errs.String())
	}
	for i, event := range out.events {
		if event.Type == watch.Error || event.Error != "" || event.Sequence != uint64(i+1) || event.Time.IsZero() {
			t.Fatal("invalid watch event", event)
		}
	}
}

func hookPairWithContinuation(t *testing.T, root, current, code string) agentHookResponse {
	t.Helper()
	completed := false
	for attempt := 0; attempt < 3; attempt++ {
		started := time.Now()
		response := hookInvoke(t, root, hookEvent(t, current), "--base", "main", "--json")
		t.Logf("hook %s attempt=%d elapsed=%s diagnostics=%+v", code, attempt, time.Since(started), response.Radar)
		if response.Radar == nil || response.Radar.PeersTotal != 2 || response.Radar.Candidates != 1 {
			t.Fatalf("hook pair selection scope lost: %+v", response)
		}
		if attempt == 0 && response.Radar.Self != "complete" || attempt > 0 && response.Radar.Self != "reused" || !strings.Contains(response.Message, "NOT VERIFIED") {
			t.Fatalf("completed self or unresolved scope lost: %+v", response)
		}
		if response.Radar.Checked+response.Radar.Reused == 0 {
			if attempt != 0 || response.Decision != "" || response.Radar.Omitted != 2 || !strings.Contains(response.Message, "peer inspection incomplete:") {
				t.Fatalf("pair inspection failed to make bounded progress: %+v", response)
			}
			continue
		}
		if response.Decision != "block" || !strings.Contains(response.Reason, code) || response.Radar.Omitted != 1 {
			t.Fatalf("completed hook pair lost %s drift: %+v", code, response)
		}
		if completed {
			if response.Radar.Reused != 1 {
				t.Fatalf("completed pair was not reused: %+v", response.Radar)
			}
			return response
		}
		if response.Radar.Checked != 1 {
			t.Fatalf("pair was not checked before reuse: %+v", response.Radar)
		}
		completed = true
	}
	t.Fatal("hook pair never completed and reused within three invocations")
	return agentHookResponse{}
}

func TestDriftEventWriterStopsForCompletionOrError(t *testing.T) {
	for _, kind := range []string{watch.Changed, watch.Checking, watch.Completed, watch.Error, watch.Heartbeat} {
		t.Run(kind, func(t *testing.T) {
			canceled := false
			writer := driftEventWriter{cancel: func() { canceled = true }}
			data := []byte(fmt.Sprintf("{\"type\":%q}\n", kind))
			if n, err := writer.Write(data); err != nil || n != len(data) || !bytes.Equal(writer.out.Bytes(), data) {
				t.Fatal("JSONL event was altered", n, err)
			}
			if canceled != (kind == watch.Completed || kind == watch.Error) {
				t.Fatal("wrong event ended watch session", kind, canceled)
			}
		})
	}
}
