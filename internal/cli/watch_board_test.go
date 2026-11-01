package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/watch"
)

func TestBoardPairsValidateSelectedDistinctNames(t *testing.T) {
	in := watchInput{Refs: []string{"a", "b"}}
	got, err := parseBoardPairs([]string{"a,b", "b,a"}, in)
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"a,a", "a,unknown", "a,b,c"} {
		if _, err := parseBoardPairs([]string{bad}, in); err == nil {
			t.Fatal(bad)
		}
	}
}
func TestBoardSnapshotIsNotVerification(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("one", map[string]string{"backend.py": "def price():\n return 2\n", "migrations/0007_one.sql": "select 1;\n"})
	agent("two", map[string]string{"backend.py": "def price():\n return 3\n", "migrations/7_two.sql": "select 2;\n"})
	before := watchSourceFingerprint(t, root)
	var out bytes.Buffer
	a := &app{ctx: context.Background(), root: root, out: &out}
	state, err := a.observeWatch(a.ctx, options{base: "main"})
	if err != nil {
		t.Fatal(err)
	}
	r := a.boardSnapshot(a.ctx, state.Value.(watchInput))
	if r.VerificationState != "NOT VERIFIED" || len(r.Branches) != 2 || len(r.Overlaps) != 1 || len(r.MigrationCandidates) != 1 {
		t.Fatalf("%+v", r)
	}
	for _, d := range r.Details {
		if d.Status != "not_checked" || d.Report != nil {
			t.Fatal(d)
		}
	}
	a.renderBoard(watch.Event{Type: watch.Completed, Result: &watch.Result{Value: r}})
	if !strings.Contains(out.String(), "not compatibility evidence") || !strings.Contains(out.String(), "inferred; not checked") {
		t.Fatal(out.String())
	}
	if watchSourceFingerprint(t, root) != before {
		t.Fatal("board changed source")
	}
}
func TestBoardStreamsQuickResultThenPrivateConflictProbes(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("one", map[string]string{"backend.py": "def price():\n return 2\n"})
	agent("two", map[string]string{"backend.py": "def price():\n return 3\n"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	writer := &watchEventWriter{}
	writer.callback = func(e watch.Event) {
		if e.Type == watch.Attributed {
			cancel()
		}
	}
	var errs bytes.Buffer
	a := &app{ctx: ctx, root: root, out: writer, errout: &errs}
	code := a.watch(options{base: "main", board: true, jsonl: true, interval: 100 * time.Millisecond, timeout: 10 * time.Second, maxCommands: 16})
	if code != 0 || writer.err != nil {
		t.Fatalf("%d %v %s", code, writer.err, errs.String())
	}
	var fast, detail bool
	for _, e := range writer.events {
		if e.Result == nil {
			continue
		}
		b, _ := json.Marshal(e.Result.Value)
		var r boardReport
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		if e.Type == watch.Completed {
			fast = true
			for _, d := range r.Details {
				if d.Status != "not_checked" {
					t.Fatal(d)
				}
			}
		}
		if e.Type == watch.Attributed {
			detail = true
			if r.VerificationState != "NOT VERIFIED" {
				t.Fatal(r)
			}
		}
	}
	if !fast || !detail {
		t.Fatalf("events %+v", writer.events)
	}
}
func TestBoardOptionsDoNotAuthorizeExecution(t *testing.T) {
	root, _ := checkoutRepo(t)
	for _, args := range [][]string{{"watch", "--typecheck"}, {"watch", "--notify"}, {"watch", "--pair", "a,b"}} {
		code, _, errs := run(t, root, args...)
		if code != 2 {
			t.Fatalf("%v exit %d %s", args, code, errs)
		}
	}
}

func TestBoardRejectsUnimplementedAttributionInsteadOfIgnoringIt(t *testing.T) {
	root, _ := checkoutRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var out, errs bytes.Buffer
	a := &app{ctx: ctx, root: root, out: &out, errout: &errs}
	code := a.watch(options{base: "main", board: true, verify: true, attributionRuns: 1, interval: 100 * time.Millisecond, timeout: time.Second, maxCommands: 16})
	if code != 2 || !strings.Contains(errs.String(), "--attribution-runs is not supported with --board") {
		t.Fatalf("exit %d errors %s", code, errs.String())
	}
}

func TestBoardJSONRetainsObservationTimesAcrossEvents(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("one", map[string]string{"backend.py": "def price():\n return 2\n"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var out, errs bytes.Buffer
	events := &watchEventWriter{callback: func(e watch.Event) {
		if e.Type == watch.Attributed {
			cancel()
		}
	}}
	a := &app{ctx: ctx, root: root, out: io.MultiWriter(&out, events), errout: &errs}
	if code := a.watch(options{base: "main", board: true, jsonl: true, interval: 100 * time.Millisecond, timeout: time.Second, maxCommands: 16}); code != 0 {
		t.Fatalf("%d %s", code, errs.String())
	}
	var lastChange, lastCompleted time.Time
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var event struct {
			watch.Event
			watchDisplay
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == watch.Changed {
			lastChange = event.Time
		}
		if event.Type == watch.Completed || event.Type == watch.Attributed {
			lastCompleted = event.Time
		}
		if event.LastChange.IsZero() || !event.LastChange.Equal(lastChange) {
			t.Fatalf("lost last change: %s", line)
		}
		if !lastCompleted.IsZero() && !event.LastCompleted.Equal(lastCompleted) {
			t.Fatalf("lost completion time: %s", line)
		}
	}
	if lastCompleted.IsZero() {
		t.Fatal("no completed result")
	}
}
func TestBoardCoverageObservationRequiresExecutionOptIn(t *testing.T) {
	root, _ := checkoutRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var out, errs bytes.Buffer
	a := &app{ctx: ctx, root: root, out: &out, errout: &errs}
	if code := a.watch(options{base: "main", board: true, observeCoverage: true, interval: 100 * time.Millisecond, timeout: time.Second, maxCommands: 16}); code != 2 || !strings.Contains(errs.String(), "--observe-coverage requires --run") {
		t.Fatalf("exit %d errors %s", code, errs.String())
	}
}

// This fixture measures wall time rather than enforcing a machine-dependent
// benchmark threshold in CI. The session deadline bounds hangs; the measured
// result is reported separately against the five-second product target.
func TestBoardTwelveWorktreeLatency(t *testing.T) {
	root, agent := checkoutRepo(t)
	for i := 0; i < 11; i++ {
		agent(fmt.Sprintf("board-agent-%02d", i), map[string]string{fmt.Sprintf("agent_%02d.py", i): fmt.Sprintf("VALUE = %d\n", i)})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var errs bytes.Buffer
	events := &watchEventWriter{}
	started := time.Now()
	var latency time.Duration
	var branches int
	events.callback = func(e watch.Event) {
		if e.Type != watch.Completed || e.Result == nil {
			return
		}
		data, err := json.Marshal(e.Result.Value)
		if err != nil {
			t.Error(err)
			cancel()
			return
		}
		var report boardReport
		if err = json.Unmarshal(data, &report); err != nil {
			t.Error(err)
			cancel()
			return
		}
		if report.VerificationState != "NOT VERIFIED" {
			t.Errorf("lightweight board fabricated verification: %+v", report)
		}
		for _, d := range report.Details {
			if d.Status != "not_checked" || d.Report != nil {
				t.Errorf("lightweight board performed detailed verification: %+v", d)
			}
		}
		if len(report.Details) != len(report.Branches) {
			t.Errorf("missing unverified standalone scopes: %+v", report)
		}
		branches = len(report.Branches)
		latency = time.Since(started)
		cancel()
	}
	a := &app{ctx: ctx, root: root, out: events, errout: &errs}
	code := a.watch(options{base: "main", board: true, jsonl: true, interval: 100 * time.Millisecond, timeout: 10 * time.Second, maxCommands: 16})
	if code != 0 || events.err != nil || latency == 0 || branches != 11 {
		t.Fatalf("exit=%d error=%v branches=%d latency=%s stderr=%s", code, events.err, branches, latency, errs.String())
	}
	t.Logf("12-worktree CLI board: first lightweight snapshot=%s, five-second target met=%t (11 agent branches plus base worktree; no index or repository execution)", latency, latency < 5*time.Second)
}
