package watch

import (
	"context"
	"errors"
	"os"
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

func TestFailedAnalysisIsReportedOnceUntilInputsChange(t *testing.T) {
	var calls atomic.Int32
	events, _, _ := launch(t, Options{Interval: time.Millisecond, Heartbeat: 5 * time.Millisecond},
		func(context.Context) (State, error) { return State{Key: "revision"}, nil },
		func(context.Context, State) (Result, error) {
			calls.Add(1)
			return Result{}, errors.New("verification failed")
		})
	event := waitEvent(t, events, Error, "revision")
	if event.Error != "verification failed" {
		t.Fatalf("missing analysis error: %#v", event)
	}
	waitEvent(t, events, Heartbeat, "revision")
	if calls.Load() != 1 {
		t.Fatalf("failed unchanged inputs repeatedly analyzed: %d", calls.Load())
	}
}

func TestInvalidOptionsAndEmptyObservation(t *testing.T) {
	observe := func(context.Context) (State, error) { return State{}, nil }
	analyze := func(context.Context, State) (Result, error) { t.Fatal("invalid state analyzed"); return Result{}, nil }
	if err := Run(context.Background(), Options{Interval: -1}, observe, analyze, func(Event) {}); err == nil {
		t.Fatal("negative interval accepted")
	}
	if err := Run(context.Background(), Options{}, nil, analyze, func(Event) {}); err == nil {
		t.Fatal("nil callback accepted")
	}
	events, _, _ := launch(t, Options{Interval: time.Millisecond}, observe, analyze)
	event := waitEvent(t, events, Error, "")
	if event.Error != "watch observation returned an empty key" {
		t.Fatalf("empty key accepted: %#v", event)
	}
}

func TestFastResultPrecedesFollowupAndCleanup(t *testing.T) {
	var closed atomic.Int32
	release := make(chan struct{})
	events, _, _ := launch(t, Options{Interval: time.Millisecond},
		func(context.Context) (State, error) { return State{Key: "revision"}, nil },
		func(context.Context, State) (Result, error) {
			return Result{
				Value: "fast",
				Close: func() { closed.Add(1) },
				Followup: func(ctx context.Context) (Result, error) {
					select {
					case <-release:
						return Result{Value: "observed"}, nil
					case <-ctx.Done():
						return Result{}, ctx.Err()
					}
				},
			}, nil
		})
	fast := waitEvent(t, events, Completed, "revision")
	if fast.Result.Value != "fast" || closed.Load() != 0 {
		t.Fatal("fast result waited for attribution or closed its candidate early")
	}
	close(release)
	observed := waitEvent(t, events, Attributed, "revision")
	if observed.Result.Value != "observed" || closed.Load() != 1 {
		t.Fatal("attribution or retained candidate cleanup missing")
	}
}

func TestFollowupCanceledWhenInputsChange(t *testing.T) {
	var key atomic.Value
	key.Store("old")
	var closed atomic.Int32
	started := make(chan struct{})
	events, _, _ := launch(t, Options{Interval: time.Millisecond},
		func(context.Context) (State, error) { return State{Key: key.Load().(string)}, nil },
		func(_ context.Context, state State) (Result, error) {
			if state.Key == "new" {
				return Result{}, nil
			}
			return Result{
				Close: func() { closed.Add(1) },
				Followup: func(ctx context.Context) (Result, error) {
					close(started)
					<-ctx.Done()
					return Result{Value: "obsolete observed"}, nil
				},
			}, nil
		})
	<-started
	key.Store("new")
	for {
		select {
		case event := <-events:
			if event.Type == Attributed {
				t.Fatal("obsolete attribution published")
			}
			if event.Type == Completed && event.State.Key == "new" {
				if closed.Load() != 1 {
					t.Fatalf("candidate closed %d times", closed.Load())
				}
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("new inputs not analyzed")
		}
	}
}

func TestStaleResultStillClosesResources(t *testing.T) {
	var key atomic.Value
	key.Store("old")
	var closed atomic.Int32
	events, _, _ := launch(t, Options{Interval: time.Hour},
		func(context.Context) (State, error) { return State{Key: key.Load().(string)}, nil },
		func(_ context.Context, state State) (Result, error) {
			if state.Key == "old" {
				key.Store("new")
				return Result{Close: func() { closed.Add(1) }}, nil
			}
			return Result{}, nil
		})
	waitEvent(t, events, Completed, "new")
	if closed.Load() != 1 {
		t.Fatalf("stale candidate closed %d times", closed.Load())
	}
}

func TestFollowupErrorPreservesFastResultEvent(t *testing.T) {
	events, _, _ := launch(t, Options{},
		func(context.Context) (State, error) { return State{Key: "revision"}, nil },
		func(context.Context, State) (Result, error) {
			return Result{Value: "fast pass", Followup: func(context.Context) (Result, error) {
				return Result{}, errors.New("attribution unavailable")
			}}, nil
		})
	fast := waitEvent(t, events, Completed, "revision")
	if fast.Result.Value != "fast pass" {
		t.Fatal("missing initial fast result")
	}
	observed := waitEvent(t, events, Attributed, "revision")
	if observed.Error != "attribution unavailable" || observed.Result != nil {
		t.Fatalf("attribution error replaced fast gate: %#v", observed)
	}
}

func TestSlowObservationDoesNotImmediatelyConsumeExpiredPoll(t *testing.T) {
	for _, mode := range []string{"0", "1"} {
		t.Run("asynctimerchan="+mode, func(t *testing.T) {
			t.Setenv("GODEBUG", os.Getenv("GODEBUG")+",asynctimerchan="+mode)
			const interval = 40 * time.Millisecond
			started, release := make(chan struct{}), make(chan struct{})
			gaps := make(chan time.Duration, 1)
			var observed atomic.Int32
			var finishedFirst time.Time
			_, cancel, _ := launch(t, Options{Interval: interval, Heartbeat: time.Hour},
				func(ctx context.Context) (State, error) {
					switch observed.Add(1) {
					case 1:
						close(started)
						select {
						case <-release:
						case <-ctx.Done():
							return State{}, ctx.Err()
						}
						finishedFirst = time.Now()
					case 2:
						gaps <- time.Since(finishedFirst)
					}
					return State{Key: "unchanged"}, nil
				},
				func(ctx context.Context, _ State) (Result, error) {
					<-ctx.Done() // No completion may trigger a forced observation.
					return Result{}, ctx.Err()
				})
			<-started
			// Force the original ticker to expire while observation is blocked.
			time.Sleep(2 * interval)
			close(release)
			select {
			case gap := <-gaps:
				if gap < interval/2 {
					t.Fatalf("slow observation immediately consumed an expired poll after %s", gap)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("next periodic observation never ran")
			}
			cancel()
		})
	}
}

func TestCancellationInterruptsSlowObservationWithoutPublishing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	finished := make(chan error, 1)
	events := make(chan Event, 10)
	var analyzed atomic.Int32
	go func() {
		finished <- Run(ctx, Options{Interval: time.Millisecond},
			func(ctx context.Context) (State, error) {
				close(started)
				<-ctx.Done()
				return State{}, ctx.Err()
			},
			func(context.Context, State) (Result, error) {
				analyzed.Add(1)
				return Result{}, nil
			}, func(event Event) { events <- event })
	}()
	<-started
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("slow observation lost cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled slow observation did not stop")
	}
	if analyzed.Load() != 0 {
		t.Fatal("canceled observation launched analysis")
	}
	select {
	case event := <-events:
		t.Fatal("canceled observation published an event", event)
	default:
	}
}
