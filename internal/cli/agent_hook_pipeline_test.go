package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/integration"
)

func TestAgentHookPreservesSelfFailureWhenPeerUnavailable(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"db/env.py": "# alembic\n", "db/versions/r0.py": "revision = 'r0'\ndown_revision = None\n", "db/versions/r1.py": "revision = 'r1'\ndown_revision = 'r0'\n"})
	current := agent("current", nil)
	peer := agent("peer", nil)
	put(t, current, "db/versions/r2.py", "revision = 'r2'\ndown_revision = 'r0'\n")
	// An unsupported peer configuration must not prevent the current branch's
	// independent saved-change failure from being analyzed, cached and returned.
	put(t, peer, "db/versions/r2.py", "revision = 'peer'\ndown_revision = 'r0'\n")
	gitTest(t, peer, "update-index", "--assume-unchanged", "db/env.py")
	result := hookInvoke(t, root, hookEvent(t, current), "--base", "main", "--json")
	if result.Decision != "block" || !strings.Contains(result.Reason, "migration_multiple_heads") || !strings.Contains(result.Message, "peer inspection incomplete") {
		t.Fatalf("peer hid completed self failure: %+v", result)
	}
	if result.Radar == nil || result.Radar.Self != "complete" || result.Radar.Omitted == 0 {
		t.Fatalf("missing partial diagnostics: %+v", result.Radar)
	}
	again := hookInvoke(t, root, hookEvent(t, current), "--base", "main", "--json")
	if again.Decision != "block" || again.Radar == nil || again.Radar.Self != "reused" {
		t.Fatalf("completed self result was not retained: %+v", again)
	}
}

func TestAgentHookPeerDeadlineRetainsSelfResult(t *testing.T) {
	root, _ := checkoutRepo(t)
	a := &app{ctx: context.Background(), root: root}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	diag := &hookDiagnostics{}
	own := agentHookResponse{Decision: "block", Reason: "known self failure"}
	result := a.hookPeers(ctx, options{}, watchInput{}, own, indexer.NewParseCache(), diag)
	if result.Decision != "block" || result.Reason != "known self failure" || !strings.Contains(result.Message, "NOT VERIFIED") {
		t.Fatalf("deadline erased self result: %+v", result)
	}
}

func TestAgentHookNativeProtocolOmitsDiagnostics(t *testing.T) {
	root, _ := checkoutRepo(t)
	result := hookInvoke(t, root, hookEvent(t, root), "--base", "main")
	if result.Radar != nil {
		t.Fatalf("native response gained Radar diagnostics: %+v", result)
	}
	detailed := hookInvoke(t, root, hookEvent(t, root), "--base", "main", "--json")
	if detailed.Radar == nil || detailed.Radar.Self != "reused" || detailed.Radar.StagesMS == nil {
		t.Fatalf("explicit JSON lacks diagnostics: %+v", detailed)
	}
}

func TestAgentHookUnselectedCurrentSnapshotStillRequiresRevalidation(t *testing.T) {
	_, agent := checkoutRepo(t)
	current := agent("current", nil)
	agent("selected", map[string]string{"selected.md": "selected\n"})
	a := &app{ctx: context.Background(), root: current}
	o := options{base: "main", args: []string{"selected"}, includeDirty: true}
	before, err := a.observeHookSelf(a.ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	w, err := gitrepo.InspectWorktree(a.ctx, current)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := integration.ObserveDirty(a.ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	put(t, current, "client.py", "VALUE=42\n")
	after, err := a.observeHookSelf(a.ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	if before.Key != after.Key {
		t.Fatal("fixture did not exclude the current branch from selected preview")
	}
	if hookCurrentPinned(a.ctx, snapshot) {
		t.Fatal("unselected current saved change escaped independent pair revalidation")
	}
}

func TestAgentHookTwelveWorktreesColdAndWarmSelection(t *testing.T) {
	files := map[string]string{"shared.py": "VALUE=1\n"}
	for i := 0; i < 1600; i++ {
		files[fmt.Sprintf("pkg/file_%04d.py", i)] = "VALUE=1\n"
	}
	root, agent := agentRepo(t, files)
	current := agent("current", nil)
	put(t, current, "shared.py", "VALUE=2\n")
	other := agent("related", nil)
	put(t, other, "shared.py", "VALUE=3\n")
	for i := 0; i < 9; i++ {
		dir := agent(fmt.Sprintf("docs-%02d", i), nil)
		put(t, dir, fmt.Sprintf("note-%02d.md", i), "documentation\n")
	}
	completed := false
	for i := 0; i < 3; i++ {
		started := time.Now()
		r := hookInvoke(t, root, hookEvent(t, current), "--base", "main", "--json")
		t.Logf("cold=%v duration=%s diagnostics=%+v", i == 0, time.Since(started), r.Radar)
		if r.Radar == nil || r.Radar.PeersTotal != 11 || r.Radar.Candidates != 1 {
			t.Fatalf("selection scope lost: %+v", r)
		}
		if i == 0 && r.Radar.Self != "complete" || i > 0 && r.Radar.Self != "reused" {
			t.Fatalf("completed self preview was lost or not persisted: %+v", r)
		}
		if !strings.Contains(r.Message, "NOT VERIFIED") {
			t.Fatal("unresolved peers were presented as verified")
		}
		if r.Radar.Checked+r.Radar.Reused == 0 {
			// Cold peer work may exhaust the bounded budget after self has
			// completed. The next invocation must resume using that self cache.
			if i != 0 || r.Decision != "" || r.Radar.Omitted != 11 {
				t.Fatalf("partial peer work failed to make bounded progress: %+v", r)
			}
			continue
		}
		if r.Decision != "block" || r.Radar.Omitted != 10 {
			t.Fatalf("completed pair lost conflict or unresolved scope: %+v", r)
		}
		if completed && r.Radar.Reused != 1 {
			t.Fatalf("completed pair was not reused: %+v", r.Radar)
		}
		completed = true
	}
	if !completed {
		t.Fatal("pair never completed after the cached self preview")
	}
}

func TestAgentHookSelectsPeersNewSavedImportAndRenamedSource(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"backend.py": "def charge(): return 1\n", "client.py": "VALUE=1\n"})
	current := agent("api", nil)
	peer := agent("consumer", nil)
	gitTest(t, peer, "mv", "client.py", "renamed.py")
	put(t, peer, "renamed.py", "from backend import charge\nVALUE=charge()\n")
	put(t, current, "backend.py", "def bill(): return 1\n")
	completed := false
	for attempt := 0; attempt < 3; attempt++ {
		started := time.Now()
		result := hookInvoke(t, root, hookEvent(t, current), "--base", "main", "--json")
		t.Logf("attempt=%d duration=%s diagnostics=%+v", attempt, time.Since(started), result.Radar)
		if result.Radar == nil || result.Radar.PeersTotal != 2 || result.Radar.Candidates != 1 {
			t.Fatalf("new saved import/rename selection scope lost: %+v", result)
		}
		if attempt == 0 && result.Radar.Self != "complete" || attempt > 0 && result.Radar.Self != "reused" {
			t.Fatalf("saved self preview was lost or not cached: %+v", result)
		}
		if !strings.Contains(result.Message, "NOT VERIFIED") {
			t.Fatal("unresolved peer scope was presented as verified")
		}
		if result.Radar.Checked+result.Radar.Reused == 0 {
			// The default budget may end during a cold semantic pair. Preserve
			// completed self work, then require the next call to complete the pair.
			if attempt != 0 || result.Decision != "" || result.Radar.Omitted != 2 || !strings.Contains(result.Message, "peer inspection incomplete:") {
				t.Fatalf("import/rename pair failed to resume bounded work: %+v", result)
			}
			continue
		}
		if result.Decision != "block" || !strings.Contains(result.Reason, "symbol_dangling_reference") || result.Radar.Omitted != 1 {
			t.Fatalf("completed new import/rename pair lost semantic drift: %+v", result)
		}
		if completed && result.Radar.Reused != 1 || !completed && result.Radar.Checked != 1 {
			t.Fatalf("completed semantic pair was not checked then reused: %+v", result.Radar)
		}
		completed = true
	}
	if !completed {
		t.Fatal("new saved import/rename pair never completed")
	}
}

func TestAgentHookDoesNotAttributeSymbolDriftWhenDirtyBaselineConflicts(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"note.md": "base\n", "backend.py": "def charge(): return 1\n", "client.py": "VALUE=1\n"})
	current := agent("api", map[string]string{"note.md": "api\n"})
	agent("consumer", map[string]string{"note.md": "consumer\n", "client.py": "from backend import charge\nVALUE=charge()\n"})
	// Resolve the inherited unrelated textual conflict and introduce a symbol
	// change. The old pair never completed symbol analysis, so it cannot justify
	// attributing the newly observed symbol finding to the current saved edit.
	put(t, current, "note.md", "consumer\n")
	put(t, current, "backend.py", "def bill(): return 1\n")
	result := hookInvoke(t, root, hookEvent(t, current), "--base", "main", "--json")
	if result.Decision != "" || !strings.Contains(result.Message, "baseline comparison unavailable") || result.Radar == nil || result.Radar.Checked != 0 {
		t.Fatalf("incomplete baseline became a new drift decision: %+v", result)
	}
	again := hookInvoke(t, root, hookEvent(t, current), "--base", "main", "--json")
	if again.Decision != "" || again.Radar == nil || again.Radar.Reused != 0 || again.Radar.Self != "reused" {
		t.Fatalf("incomplete pair result was cached: %+v", again)
	}
}

func TestAgentHookPeerDeadlineReservesOriginalBudget(t *testing.T) {
	started := time.Unix(100, 0)
	tests := []struct {
		name                     string
		budget, elapsed, reserve time.Duration
	}{
		{"default late self", 20 * time.Second, 19 * time.Second, 2 * time.Second},
		{"short explicit", time.Second, 950 * time.Millisecond, 100 * time.Millisecond},
		{"helper default", 0, 19 * time.Second, 2 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			total := tt.budget
			if total == 0 {
				total = 20 * time.Second
			}
			deadline := started.Add(total)
			got := hookPeerDeadline(deadline, tt.budget)
			if !got.Equal(deadline.Add(-tt.reserve)) {
				t.Fatalf("peer deadline %s, wanted reserve %s", got, tt.reserve)
			}
			if !got.Before(started.Add(tt.elapsed)) {
				t.Fatal("late self stage reopened the reserved response budget for peer work")
			}
		})
	}
}

func TestAgentHookPeerTextualBaselineKeepsInheritedAndNewHunksDistinct(t *testing.T) {
	source := func(first, second string) string {
		return "def first():\n    return " + first + "\n" + strings.Repeat("\n# spacing\n", 12) + "def second():\n    return " + second + "\n"
	}
	root, agent := agentRepo(t, map[string]string{"shared.py": source("1", "1")})
	current := agent("current", map[string]string{"shared.py": source("2", "1")})
	agent("peer", map[string]string{"shared.py": source("3", "3")})
	invoke := func() agentHookResponse {
		return hookInvoke(t, root, hookEvent(t, current), "--base", "main", "--json")
	}
	if response := invoke(); response.Decision != "" || response.Radar == nil || response.Radar.Checked != 1 {
		t.Fatalf("inherited peer conflict became actionable: %+v", response)
	}
	put(t, current, "shared.py", source("2", "2"))
	if response := invoke(); response.Decision != "block" || !strings.Contains(response.Reason, "shared.py") {
		t.Fatalf("new saved hunk hidden by inherited peer conflict: %+v", response)
	}
	put(t, current, "shared.py", source("3", "1"))
	if response := invoke(); response.Decision != "" || response.Radar == nil || response.Radar.Checked != 1 {
		t.Fatalf("saved conflict resolution became new drift: %+v", response)
	}
}
