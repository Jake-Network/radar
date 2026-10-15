package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/overlap"
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
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("dirty fixture inspection exceeded 5s: %v", elapsed)
	}
	result := hookInvoke(t, root, hookEvent(t, b), "--base", "main")
	if result.Decision != "block" || !strings.Contains(result.Reason, "cross-worktree drift") || !strings.Contains(result.Reason, "backend.py:1") || !strings.Contains(result.Reason, "agent-api") {
		t.Fatalf("dirty consumer not blocked: %+v", result)
	}
	cached := hookInvoke(t, root, hookEvent(t, b), "--base", "main")
	if !strings.Contains(cached.Message, "cache") {
		t.Fatalf("unchanged drift not cached: %+v", cached)
	}
	// Peer bytes change with identical status paths: digest must invalidate cache.
	put(t, a, "backend.py", "def charge():\n    return 2\n")
	repaired := hookInvoke(t, root, hookEvent(t, b), "--base", "main")
	if repaired.Decision != "" || strings.Contains(repaired.Message, "cache") {
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	writer := &driftEventWriter{cancel: cancel}
	cli := &app{ctx: ctx, root: root, out: writer, errout: &bytes.Buffer{}}
	code := cli.watch(options{base: "main", includeDirty: true, jsonl: true, interval: 100 * time.Millisecond, timeout: time.Minute, maxCommands: 16})
	if code != 0 || !strings.Contains(writer.out.String(), `"drift":[`) || !strings.Contains(writer.out.String(), "symbol_dangling_reference") {
		t.Fatalf("missing dirty drift event, exit=%d: %s", code, writer.out.String())
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
	if json.Unmarshal(bytes.TrimSpace(data), &e) == nil && e.Type == "completed" {
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
	result := hookInvoke(t, root, hookEvent(t, a), "--base", "main")
	if result.Decision != "" {
		t.Fatalf("unexpected block: %+v", result)
	}
	cache, save := savedSyntaxCache(a)
	defer save()
	if cache.Stats().Entries == 0 {
		t.Fatal("hook did not persist reusable syntax")
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
			r := hookInvoke(t, root, hookEvent(t, b), "--base", "main")
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
	watchSession(t, root, func(e watch.Event, cancel context.CancelFunc) {
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
