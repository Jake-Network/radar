package overlap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
)

func TestPairBatchBoundsConcurrencyAndPreservesInputOrder(t *testing.T) {
	var active, maximum atomic.Int32
	laterStarted := make(chan struct{})
	results, err := inspectPairBatch(context.Background(), 5, 2, func(_ context.Context, index int) (integration.Report, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		if index == 0 {
			// The other worker must finish index 1 before starting index 2.
			<-laterStarted
		} else if index == 2 {
			close(laterStarted)
		}
		return integration.Report{Findings: []model.Finding{{Code: fmt.Sprint(index), Branches: []string{"branch"}}}}, nil
	})
	if err != nil || maximum.Load() != 2 || active.Load() != 0 {
		t.Fatal("pair concurrency unbounded, serial, or unjoined", err, maximum.Load(), active.Load())
	}
	for index, result := range results {
		if !result.complete || len(result.report.Findings) != 1 || result.report.Findings[0].Code != fmt.Sprint(index) {
			t.Fatal("completion order changed input order", index, result)
		}
	}
}

func TestPairBatchPreservesPrimaryFailureAndJoinsCanceledSibling(t *testing.T) {
	started := make(chan struct{})
	joined := make(chan struct{})
	failure := errors.New("private pair inspection failed")
	results, err := inspectPairBatch(context.Background(), 4, 2, func(ctx context.Context, index int) (integration.Report, error) {
		if index == 0 {
			close(started)
			<-ctx.Done()
			close(joined)
			return integration.Report{}, ctx.Err()
		}
		<-started
		return integration.Report{}, failure
	})
	if !errors.Is(err, failure) {
		t.Fatal("primary pair error hidden by cancellation", err)
	}
	select {
	case <-joined:
	default:
		t.Fatal("pair inspection returned before sibling cleanup")
	}
	for _, result := range results {
		if result.complete {
			t.Fatal("failed or unscheduled pair became complete", result)
		}
	}
}

func TestPairBatchCanceledParentCannotCreateCompletedEmptyPairs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	results, err := inspectPairBatch(ctx, 3, 2, func(context.Context, int) (integration.Report, error) {
		calls.Add(1)
		return integration.Report{}, nil
	})
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatal("canceled parent launched pair work or lost error", calls.Load(), err)
	}
	for _, result := range results {
		if result.complete {
			t.Fatal("canceled pair became an empty completed inspection")
		}
	}
}

func TestPairBatchClonesSharedReportBeforeAttribution(t *testing.T) {
	shared := integration.Report{Findings: []model.Finding{{Code: "finding", Branches: []string{"original"}, Locations: []model.Provenance{{Path: "source.py"}}}}}
	var readers sync.WaitGroup
	readers.Add(2)
	results, err := inspectPairBatch(context.Background(), 2, 2, func(context.Context, int) (integration.Report, error) {
		readers.Done()
		readers.Wait()
		return shared, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	results[0].report.Findings[0].Branches[0] = "changed"
	results[0].report.Findings[0].Locations[0].Path = "changed.py"
	if results[1].report.Findings[0].Branches[0] != "original" || shared.Findings[0].Locations[0].Path != "source.py" {
		t.Fatal("worker report aliases shared cache or another pair")
	}
}

func TestParallelPairFindingsRetainOriginalOrderWithSharedCache(t *testing.T) {
	ctx := WithAnalysisCache(context.Background())
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := gitrepo.RunPrivate(ctx, root, args...)
		if err != nil {
			t.Fatal(args, err, out)
		}
		return out
	}
	put := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, "shared.txt"), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	git("config", "core.autocrlf", "false")
	put("base\n")
	git("add", ".")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	inputs := []Input{}
	for _, name := range []string{"a", "b", "c"} {
		git("checkout", "-qb", name, base)
		put(name + "\n")
		git("add", ".")
		git("commit", "-qm", name)
		inputs = append(inputs, Input{Branch: name, Head: git("rev-parse", "HEAD")})
	}
	var prior []byte
	for attempt := 0; attempt < 2; attempt++ {
		report, err := Analyze(ctx, root, base, inputs)
		if err != nil || report.Pairs != 3 || len(report.Findings) != 3 {
			t.Fatal("parallel pair inspection lost completed evidence", report, err)
		}
		for index, branches := range [][]string{{"a", "b"}, {"a", "c"}, {"b", "c"}} {
			finding := report.Findings[index]
			if finding.Code != "worktree_textual_conflict" || len(finding.Branches) != 2 || finding.Branches[0] != branches[0] || finding.Branches[1] != branches[1] {
				t.Fatal("pair finding order or ownership changed", index, finding)
			}
		}
		data, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if prior != nil && string(prior) != string(data) {
			t.Fatal("shared cache changed deterministic pair findings")
		}
		prior = data
	}
}

func TestPairBatchChecksEveryRequestedPair(t *testing.T) {
	var checked atomic.Int32
	results, err := inspectPairBatch(context.Background(), 66, 2, func(context.Context, int) (integration.Report, error) {
		checked.Add(1)
		return integration.Report{}, nil
	})
	if err != nil || checked.Load() != 66 || len(results) != 66 {
		t.Fatal("pair budget was silently truncated", checked.Load(), len(results), err)
	}
	for index, result := range results {
		if !result.complete {
			t.Fatal("requested pair never completed", index)
		}
	}
}

func TestPairBatchKeepsCompletedPrefixWhenLaterPairFails(t *testing.T) {
	laterStarted := make(chan struct{})
	failure := errors.New("later pair failed")
	results, err := inspectPairBatch(context.Background(), 4, 2, func(ctx context.Context, index int) (integration.Report, error) {
		switch index {
		case 0:
			return integration.Report{Findings: []model.Finding{{Code: "completed"}}}, nil
		case 1:
			<-laterStarted
			return integration.Report{}, failure
		default:
			close(laterStarted)
			<-ctx.Done()
			return integration.Report{}, ctx.Err()
		}
	})
	if !errors.Is(err, failure) || !results[0].complete || results[0].report.Findings[0].Code != "completed" {
		t.Fatal("completed prefix or original error was lost", results, err)
	}
	for index := 1; index < len(results); index++ {
		if results[index].complete {
			t.Fatal("failed or uninspected pair became complete", index)
		}
	}
}
