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
	Key   string `json:"key"`
	Value any    `json:"value,omitempty"`
}

type Result struct {
	Value any `json:"value,omitempty"`
	// Followup performs slower observations after the initial result is emitted.
	// It uses the same bounded worker slot and cancellation scope as Analyze.
	Followup func(context.Context) (Result, error) `json:"-"`
	// Close releases retained resources after followup or when superseded.
	Close func() `json:"-"`
}

// Event is delivered synchronously, in sequence order. A completed event always
// refers to inputs reobserved after analysis; a changed event invalidates the
// previous result immediately. Heartbeats deliberately contain no old result.
type Event struct {
	Type     string    `json:"type"`
	Sequence uint64    `json:"sequence"`
	Time     time.Time `json:"time"`
	State    State     `json:"state"`
	Result   *Result   `json:"result,omitempty"`
	Error    string    `json:"error,omitempty"`
}

const (
	Changed    = "changed"
	Checking   = "checking"
	Completed  = "completed"
	Attributed = "attributed"
	Error      = "error"
	Heartbeat  = "heartbeat"
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
	state    State
	result   Result
	err      error
	ctx      context.Context
	followup bool
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
	resetPoll := func() {
		poll.Stop()
		// Legacy timer channels can retain an expired tick after Stop/Reset.
		select {
		case <-poll.C:
		default:
		}
		poll.Reset(options.Interval)
	}
	refresh := func() {
		// Pace polling after observation so slow reads cannot queue continuous work.
		defer resetPoll()
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
				completed := <-done
				if completed.result.Close != nil {
					completed.result.Close()
				}
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
			accepted := completed.ctx.Err() == nil && valid && completed.state.Key == current.Key && ctx.Err() == nil
			if accepted {
				if completed.err != nil {
					kind := Error
					if completed.followup {
						kind = Attributed
					}
					publish(kind, completed.state, nil, completed.err)
				} else {
					kind := Completed
					if completed.followup {
						kind = Attributed
					}
					publish(kind, completed.state, &completed.result, nil)
				}
			}
			if accepted && ctx.Err() == nil && completed.err == nil && !completed.followup && completed.result.Followup != nil {
				busy = true
				go func() {
					result, err := completed.result.Followup(completed.ctx)
					if completed.result.Close != nil {
						completed.result.Close()
					}
					done <- completion{state: completed.state, result: result, err: err, ctx: completed.ctx, followup: true}
				}()
				continue
			}
			if completed.result.Close != nil {
				completed.result.Close()
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
