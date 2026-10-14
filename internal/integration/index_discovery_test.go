package integration

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/checkpoint"
	"github.com/Jake-Network/radar/internal/indexer"
)

func TestParallelIndexDiscoveryPreservesSerialEvidence(t *testing.T) {
	root := fixture(t)
	c, err := BuildCandidate(context.Background(), root, "baseline", []string{"backend", "frontend"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	serial := Report{Base: c.Base, CandidateCommit: c.Commit}
	if err := analyzeDiscovered(context.Background(), c.Dir, &serial); err != nil {
		t.Fatal(err)
	}
	snapshot, err := checkpoint.Index(context.Background(), c.Dir, c.Commit)
	if err != nil {
		t.Fatal(err)
	}
	parallel := Report{Base: c.Base, CandidateCommit: c.Commit}
	got, err := indexAndDiscover(context.Background(), c.Dir, &parallel, indexer.NewParseCache())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(serial, parallel) || !reflect.DeepEqual(snapshot, got) {
		t.Fatal("parallel reads changed the evidence or its deterministic order")
	}
}

func TestParallelIndexDiscoveryJoinsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() {
		r := Report{Base: "HEAD", CandidateCommit: "HEAD"}
		_, err := indexAndDiscover(ctx, t.TempDir(), &r, indexer.NewParseCache())
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled read returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled index/discovery did not join its worker")
	}
}
