package runproof

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHistoryIsRepositoryBoundAndTamperChecked(t *testing.T) {
	state := t.TempDir()
	a, err := Save(state, "repo", map[string]string{"verdict": "fail"})
	if err != nil {
		t.Fatal(err)
	}
	s := RunSummary{ID: a.ID, Verdict: "fail", InputKey: "sources", CreatedAt: "2026-11-01T00:00:00Z"}
	if err = Remember(state, "repo", s); err != nil {
		t.Fatal(err)
	}
	got, err := Recent(state, "repo", 5)
	if err != nil || len(got) != 1 || got[0] != s {
		t.Fatalf("%+v %v", got, err)
	}
	if other, err := Recent(state, "other", 5); err != nil || len(other) != 0 {
		t.Fatalf("%v %v", other, err)
	}
	if err = Remember(state, "other", s); err == nil {
		t.Fatal("cross repo history accepted")
	}
	dir, _ := recentDir(state, "repo")
	if err = os.WriteFile(filepath.Join(dir, summaryName(s)), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Recent(state, "repo", 5); err == nil {
		t.Fatal("tampered summary accepted")
	}
}
func TestRecentOrdersTimestampsWithAndWithoutFractionalSeconds(t *testing.T) {
	state := t.TempDir()
	for i, stamp := range []string{"2026-11-01T00:00:00Z", "2026-11-01T00:00:00.1Z"} {
		r, err := Save(state, "repo", map[string]int{"observation": i})
		if err != nil {
			t.Fatal(err)
		}
		if err = Remember(state, "repo", RunSummary{ID: r.ID, Verdict: "blocked", InputKey: r.ID, CreatedAt: stamp}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Recent(state, "repo", 5)
	if err != nil || len(got) != 2 || got[0].CreatedAt != "2026-11-01T00:00:00.1Z" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestHistoryIsPrunedAndOnlyShownReportsAreLoaded(t *testing.T) {
	state := t.TempDir()
	var first Record
	for i := 0; i < maxHistory+5; i++ {
		r, err := Save(state, "repo", map[string]int{"run": i})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = r
		}
		stamp := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano)
		if err = Remember(state, "repo", RunSummary{ID: r.ID, Verdict: "pass", InputKey: r.ID, CreatedAt: stamp}); err != nil {
			t.Fatal(err)
		}
	}
	dir, _ := recentDir(state, "repo")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != maxHistory {
		t.Fatalf("kept %d summaries, err %v", len(entries), err)
	}
	got, err := Recent(state, "repo", 5)
	if err != nil || len(got) != 5 || got[0].CreatedAt != "2026-11-01T00:08:24Z" {
		t.Fatalf("%+v %v", got, err)
	}
	// The oldest report is no longer listed, and its record is kept.
	if _, err := Load(state, "repo", first.ID); err != nil {
		t.Fatalf("pruning deleted a run record: %v", err)
	}
	// A damaged report outside the shown window is not read.
	old, _ := Recent(state, "repo", 50)
	p, _ := recordPath(state, "repo", old[49].ID)
	if err = os.WriteFile(p, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Recent(state, "repo", 5); err != nil {
		t.Fatalf("unshown report was loaded: %v", err)
	}
	if _, err := Recent(state, "repo", 50); err == nil {
		t.Fatal("damaged shown report was accepted")
	}
}

func TestHistoryOverInspectionLimitRecoversOnNextRun(t *testing.T) {
	state := t.TempDir()
	r, err := Save(state, "repo", map[string]string{"verdict": "pass"})
	if err != nil {
		t.Fatal(err)
	}
	if err = Remember(state, "repo", RunSummary{ID: r.ID, Verdict: "pass", InputKey: "seed", CreatedAt: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	dir, _ := recentDir(state, "repo")
	for i := 0; i < maxHistoryInspection; i++ {
		s := RunSummary{ID: r.ID, Verdict: "pass", InputKey: fmt.Sprint("legacy-", i), CreatedAt: "2026-01-01T00:00:01Z"}
		b, _ := json.Marshal(s)
		if err = os.WriteFile(filepath.Join(dir, summaryName(s)), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = Recent(state, "repo", 5); err == nil {
		t.Fatal("over-limit history was scanned")
	}
	if err = Remember(state, "repo", RunSummary{ID: r.ID, Verdict: "pass", InputKey: "next", CreatedAt: "2026-11-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	got, err := Recent(state, "repo", 5)
	if err != nil || len(got) != 5 || got[0].InputKey != "next" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestPruneKeepsTheRunJustRememberedWhenTheClockMovedBack(t *testing.T) {
	state := t.TempDir()
	for i := 0; i < maxHistory; i++ {
		r, err := Save(state, "repo", map[string]int{"run": i})
		if err != nil {
			t.Fatal(err)
		}
		if err = Remember(state, "repo", RunSummary{ID: r.ID, Verdict: "pass", InputKey: r.ID, CreatedAt: "2027-01-01T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Save(state, "repo", map[string]string{"run": "late"})
	if err != nil {
		t.Fatal(err)
	}
	late := RunSummary{ID: r.ID, Verdict: "fail", InputKey: r.ID, CreatedAt: "2026-11-01T00:00:00Z"}
	if err = Remember(state, "repo", late); err != nil {
		t.Fatal(err)
	}
	dir, _ := recentDir(state, "repo")
	if _, err := os.Stat(filepath.Join(dir, summaryName(late))); err != nil {
		t.Fatalf("just remembered run was pruned: %v", err)
	}
}
