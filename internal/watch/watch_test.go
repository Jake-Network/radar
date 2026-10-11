package watch

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func waitEvent(t *testing.T, events <-chan Event, kind, key string) Event {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Type == kind && event.State.Key == key {
				return event
			}
		case <-timer.C:
			t.Fatalf("did not receive %s for %s", kind, key)
		}
	}
}

func launch(t *testing.T, options Options, observe Observe, analyze Analyze) (chan Event, context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan Event, 1000)
	finished := make(chan error, 1)
	go func() { finished <- Run(ctx, options, observe, analyze, func(e Event) { events <- e }) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("watch did not release its worker")
		}
	})
	return events, cancel, finished
}

func TestUnchangedInputsAnalyzeOnceAndEventsAreConsistent(t *testing.T) {
	var calls atomic.Int32
	events, _, _ := launch(t, Options{Interval: time.Millisecond, Debounce: time.Millisecond, Heartbeat: 5 * time.Millisecond},
		func(context.Context) (State, error) { return State{Key: "revision"}, nil },
		func(context.Context, State) (Result, error) { calls.Add(1); return Result{Value: "pass"}, nil })
	var sequence uint64
	for i := 0; i < 8; i++ {
		select {
		case event := <-events:
			if event.Sequence != sequence+1 || event.Time.IsZero() {
				t.Fatalf("invalid event sequence: %#v", event)
			}
			sequence = event.Sequence
			if event.Type == Completed && (event.Result == nil || event.Result.Value != "pass") {
				t.Fatalf("missing result: %#v", event)
			}
			if event.Type != Completed && event.Result != nil {
				t.Fatalf("old result attached to %s", event.Type)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("missing heartbeat")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("unchanged state analyzed %d times", calls.Load())
	}
}

func TestRapidChangesAreDebounced(t *testing.T) {
	var key atomic.Value
	key.Store("initial")
	var calls atomic.Int32
	events, _, _ := launch(t, Options{Interval: time.Millisecond, Debounce: 40 * time.Millisecond},
		func(context.Context) (State, error) { return State{Key: key.Load().(string)}, nil },
		func(context.Context, State) (Result, error) { calls.Add(1); return Result{}, nil })
	waitEvent(t, events, Changed, "initial")
	for _, revision := range []string{"save1", "save2", "save3"} {
		key.Store(revision)
		waitEvent(t, events, Changed, revision)
	}
	waitEvent(t, events, Completed, "save3")
	if calls.Load() != 1 {
		t.Fatalf("expected one coalesced analysis, got %d", calls.Load())
	}
}

func TestChangeDuringAnalysisCancelsAndSuppressesObsoleteResult(t *testing.T) {
	var key atomic.Value
	key.Store("old")
	var active, maximum atomic.Int32
	cleaned := make(chan struct{}, 1)
	events, _, _ := launch(t, Options{Interval: time.Millisecond, Debounce: time.Millisecond},
		func(context.Context) (State, error) { return State{Key: key.Load().(string)}, nil },
		func(ctx context.Context, state State) (Result, error) {
			n := active.Add(1)
			if n > maximum.Load() {
				maximum.Store(n)
			}
			defer active.Add(-1)
			if state.Key == "old" {
				<-ctx.Done()
				cleaned <- struct{}{}
			}
			return Result{Value: "pass"}, nil
		})
	waitEvent(t, events, Checking, "old")
	key.Store("new")
	for {
		select {
		case event := <-events:
			if event.Type == Completed && event.State.Key == "old" {
				t.Fatal("obsolete PASS published")
			}
			if event.Type == Completed && event.State.Key == "new" {
				if maximum.Load() != 1 {
					t.Fatal("overlapping workers")
				}
				<-cleaned
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("new analysis did not complete")
		}
	}
}

func TestCompletionReobservesBeforePublishing(t *testing.T) {
	var key atomic.Value
	key.Store("old")
	events, _, _ := launch(t, Options{Interval: time.Hour, Debounce: time.Millisecond},
		func(context.Context) (State, error) { return State{Key: key.Load().(string)}, nil },
		func(_ context.Context, state State) (Result, error) {
			if state.Key == "old" {
				key.Store("new")
			}
			return Result{Value: "pass"}, nil
		})
	for {
		select {
		case event := <-events:
			if event.Type == Completed {
				if event.State.Key != "new" {
					t.Fatal("result published without validating current inputs")
				}
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("missing completion")
		}
	}
}

func TestCancellationWaitsForCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started, release, cleaned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		finished <- Run(ctx, Options{Debounce: time.Millisecond},
			func(context.Context) (State, error) { return State{Key: "revision"}, nil },
			func(ctx context.Context, _ State) (Result, error) {
				close(started)
				<-ctx.Done()
				<-release
				close(cleaned)
				return Result{}, ctx.Err()
			}, func(Event) {})
	}()
	<-started
	cancel()
	select {
	case <-finished:
		t.Fatal("returned before cleanup")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	<-cleaned
}

func TestObservationErrorDoesNotVerifyStaleInputs(t *testing.T) {
	var observations atomic.Int32
	events, _, _ := launch(t, Options{Interval: time.Hour, Debounce: time.Millisecond, Heartbeat: time.Hour},
		func(context.Context) (State, error) {
			if observations.Add(1) > 1 {
				return State{}, errors.New("worktree removed")
			}
			return State{Key: "revision"}, nil
		}, func(context.Context, State) (Result, error) { return Result{}, nil })
	event := waitEvent(t, events, Error, "")
	if event.Error != "worktree removed" {
		t.Fatalf("wrong error: %#v", event)
	}
	select {
	case event := <-events:
		if event.Type == Completed {
			t.Fatal("incomplete observation became a completion")
		}
	case <-time.After(10 * time.Millisecond):
	}
}
