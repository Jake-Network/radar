// Package watch schedules continuous verification without choosing a verification
// engine. Callers supply observation, analysis, and rendering functions.
package watch

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// State identifies all inputs relevant to a check. Value belongs to the caller;
// it must remain immutable while analysis is running. Key must be nonempty.
type State struct {
	Key   string
	Value any
}

type Result struct {
	Value any
}

// Event is delivered synchronously, in sequence order. A completed event always
// refers to inputs reobserved after analysis; a changed event invalidates the
// previous result immediately. Heartbeats deliberately contain no old result.
type Event struct {
	Type     string
	Sequence uint64
	Time     time.Time
	State    State
	Result   *Result
	Error    string
}

const (
	Changed   = "changed"
	Checking  = "checking"
	Completed = "completed"
	Error     = "error"
	Heartbeat = "heartbeat"
)

type Options struct {
	Interval  time.Duration
	Debounce  time.Duration
	Heartbeat time.Duration
}

type Observe func(context.Context) (State, error)
type Analyze func(context.Context, State) (Result, error)
type Emit func(Event)

type completion struct {
	state  State
	result Result
	err    error
	ctx    context.Context
}

// Run uses one analysis worker and coalesces changes until the debounce period
// expires. Analyze must honor cancellation and release its temporary resources
// before returning. Run waits for that cleanup on shutdown and returns ctx.Err().
// Observation and emission also must return promptly or honor cancellation.
func Run(ctx context.Context, options Options, observe Observe, analyze Analyze, emit Emit) error {
	if observe == nil || analyze == nil || emit == nil {
		return errors.New("watch requires observe, analyze, and emit callbacks")
	}
	if options.Interval < 0 || options.Debounce < 0 || options.Heartbeat < 0 {
		return errors.New("watch durations must not be negative")
	}
	if options.Interval == 0 {
		options.Interval = time.Second
	}
	if options.Heartbeat == 0 {
		options.Heartbeat = 30 * time.Second
	}
	poll := time.NewTicker(options.Interval)
	defer poll.Stop()
	heartbeat := time.NewTicker(options.Heartbeat)
	defer heartbeat.Stop()
	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	defer debounce.Stop()
	done := make(chan completion, 1)
	var current State
	var valid, pending, busy bool
	var readyAt time.Time
	var cancel context.CancelFunc
	var sequence uint64
	var observationError string
	publish := func(kind string, state State, result *Result, err error) {
		sequence++
		event := Event{Type: kind, Sequence: sequence, Time: time.Now().UTC(), State: state, Result: result}
		if err != nil {
			event.Error = err.Error()
		}
		emit(event)
	}
	refresh := func() {
		state, err := observe(ctx)
		if err == nil && state.Key == "" {
			err = fmt.Errorf("watch observation returned an empty key")
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			valid, pending = false, false
			if cancel != nil {
				cancel()
			}
			if observationError != err.Error() {
				publish(Error, State{}, nil, err)
			}
			observationError = err.Error()
			return
		}
		observationError = ""
		if valid && state.Key == current.Key {
			return
		}
		current, valid, pending = state, true, true
		readyAt = time.Now().Add(options.Debounce)
		if cancel != nil {
			cancel()
		}
		if !debounce.Stop() {
			select {
			case <-debounce.C:
			default:
			}
		}
		debounce.Reset(options.Debounce)
		publish(Changed, current, nil, nil)
	}
	start := func() {
		if !pending || busy || !valid || ctx.Err() != nil || time.Now().Before(readyAt) {
			return
		}
		pending, busy = false, true
		jobCtx, jobCancel := context.WithCancel(ctx)
		cancel = jobCancel
		state := current
		publish(Checking, state, nil, nil)
		go func() {
			result, err := analyze(jobCtx, state)
			done <- completion{state: state, result: result, err: err, ctx: jobCtx}
		}()
	}
	refresh()
	for {
		select {
		case <-ctx.Done():
			if cancel != nil {
				cancel()
			}
			if busy {
				<-done
			}
			return ctx.Err()
		case <-poll.C:
			refresh()
			start()
		case <-debounce.C:
			start()
		case completed := <-done:
			busy = false
			// Re-observe even between polling ticks before publishing a verdict.
			refresh()
			if completed.ctx.Err() == nil && valid && completed.state.Key == current.Key && ctx.Err() == nil {
				if completed.err != nil {
					publish(Error, completed.state, nil, completed.err)
				} else {
					publish(Completed, completed.state, &completed.result, nil)
				}
			}
			if cancel != nil {
				cancel()
				cancel = nil
			}
			start()
		case <-heartbeat.C:
			state := State{}
			if valid {
				state = current
			}
			publish(Heartbeat, state, nil, nil)
		}
	}
}
