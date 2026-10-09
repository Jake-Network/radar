package storage

import (
	"context"
	"github.com/radar-engine/radar/internal/model"
	"path/filepath"
	"sync"
	"testing"
)

func TestVersionedAtomicSnapshots(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	snap := model.Snapshot{Repository: "repo", Revision: "one", Nodes: []model.Node{{ID: "a", Name: "before", Kind: "file"}}}
	if e = s.SaveSnapshot(ctx, snap); e != nil {
		t.Fatal(e)
	}
	bad := snap
	bad.Edges = []model.Edge{{ID: "bad", From: "a", To: "absent"}}
	if s.SaveSnapshot(ctx, bad) == nil {
		t.Fatal("invalid graph accepted")
	}
	got, e := s.Snapshot(ctx, "repo", "one")
	if e != nil || got.Nodes[0].Name != "before" {
		t.Fatal("invalid update damaged snapshot", e)
	}
	snap.Revision = "two"
	snap.Nodes[0].Name = "after"
	if e = s.SaveSnapshot(ctx, snap); e != nil {
		t.Fatal(e)
	}
	got, e = s.Snapshot(ctx, "repo", "one")
	if e != nil || got.Nodes[0].Name != "before" {
		t.Fatal("historical revision lost")
	}
	f := model.NewFinding("x", "evidence", model.VerifiedStatic)
	if e = s.SaveFindings(ctx, []model.Finding{f}); e != nil {
		t.Fatal(e)
	}
	if got, e := s.Finding(ctx, f.ID); e != nil || got.Explanation != f.Explanation {
		t.Fatal("finding lost", e)
	}
}
func TestConcurrentWriters(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	a, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	b, e := Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer b.Close()
	var wg sync.WaitGroup
	for _, s := range []*Store{a, b} {
		wg.Add(1)
		go func(s *Store) {
			defer wg.Done()
			for i := 0; i < 3; i++ {
				e := s.SaveSnapshot(ctx, model.Snapshot{Repository: "repo", Revision: "r", Nodes: []model.Node{{ID: "a", Kind: "file"}}})
				if e != nil {
					t.Error(e)
				}
			}
		}(s)
	}
	wg.Wait()
	snap, e := a.Snapshot(ctx, "repo", "r")
	if e != nil || len(snap.Nodes) != 1 {
		t.Fatal("partial update", e)
	}
}
func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s, e := Open(ctx, filepath.Join(t.TempDir(), "state.db")); e == nil {
		s.Close()
		t.Fatal("canceled open succeeded")
	}
}

func TestEvidenceIsInsertOnly(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.SaveEvidence(ctx, "proof", map[string]string{"result": "passed"}); e != nil {
		t.Fatal(e)
	}
	if e = s.SaveEvidence(ctx, "proof", map[string]string{"result": "failed"}); e == nil {
		t.Fatal("evidence overwritten")
	}
	kind, raw, e := s.Artifact(ctx, "proof")
	if e != nil || kind != "test_evidence" || string(raw) != "{\"result\":\"passed\"}" {
		t.Fatal("evidence mutated", e, string(raw))
	}
}
