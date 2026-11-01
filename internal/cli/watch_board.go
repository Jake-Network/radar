package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/composition"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/runproof"
	"github.com/Jake-Network/radar/internal/termui"
	"github.com/Jake-Network/radar/internal/watch"
	"path"
)

type boardBranch struct {
	Ref     string   `json:"ref"`
	Commit  string   `json:"commit"`
	Ahead   int      `json:"ahead"`
	Behind  int      `json:"behind"`
	Changed []string `json:"changed"`
	Error   string   `json:"error,omitempty"`
}
type boardOverlap struct {
	Left  string   `json:"left"`
	Right string   `json:"right"`
	Files []string `json:"files"`
}
type boardDetail struct {
	Branches  []string            `json:"branches"`
	Status    string              `json:"status"`
	Conflicts []string            `json:"conflicts,omitempty"`
	Report    *integration.Report `json:"report,omitempty"`
	Error     string              `json:"error,omitempty"`
}
type boardReport struct {
	Recent              []runproof.RunSummary `json:"recent_runs,omitempty"`
	HistoryError        string                `json:"history_error,omitempty"`
	MigrationCandidates []boardOverlap        `json:"migration_candidates,omitempty"`
	VerificationState   string                `json:"verification_state"`
	Branches            []boardBranch         `json:"branches"`
	Overlaps            []boardOverlap        `json:"overlaps"`
	Details             []boardDetail         `json:"details"`
	MigrationStatus     string                `json:"migration_status"`
}

func parseBoardPairs(raw []string, in watchInput) ([][2]int, error) {
	var pairs [][2]int
	seen := map[[2]int]bool{}
	for _, value := range raw {
		names := strings.Split(value, ",")
		if len(names) != 2 || names[0] == names[1] {
			return nil, errors.New("--pair requires two distinct selected branches A,B")
		}
		pair := [2]int{-1, -1}
		for i, ref := range in.Refs {
			for j, name := range names {
				if name == ref {
					pair[j] = i
				}
			}
		}
		if pair[0] < 0 || pair[1] < 0 {
			return nil, fmt.Errorf("--pair %q must name selected branches", value)
		}
		if pair[0] > pair[1] {
			pair[0], pair[1] = pair[1], pair[0]
		}
		if !seen[pair] {
			pairs = append(pairs, pair)
			seen[pair] = true
		}
	}
	return pairs, nil
}
func (a *app) boardSnapshot(ctx context.Context, in watchInput) boardReport {
	r := boardReport{VerificationState: "NOT VERIFIED", Branches: []boardBranch{}, Overlaps: []boardOverlap{}, Details: []boardDetail{}, MigrationStatus: "not_checked"}
	if state, err := composition.StateDir(); err == nil {
		r.Recent, err = runproof.Recent(state, gitrepo.Identity(ctx, a.root), 5)
		if err != nil {
			r.HistoryError = "historical reports unavailable"
		}
	} else {
		r.HistoryError = "historical reports unavailable"
	}
	for i, ref := range in.Refs {
		b := boardBranch{Ref: ref, Commit: in.SHAs[i], Changed: []string{}}
		counts, err := gitrepo.RunPrivate(ctx, a.root, "rev-list", "--left-right", "--count", in.Base+"..."+in.SHAs[i])
		fields := strings.Fields(counts)
		if err != nil || len(fields) != 2 {
			b.Error = "ahead/behind unavailable"
		} else {
			var e1, e2 error
			b.Behind, e1 = strconv.Atoi(fields[0])
			b.Ahead, e2 = strconv.Atoi(fields[1])
			if e1 != nil || e2 != nil {
				b.Error = "ahead/behind unavailable"
			}
		}
		paths, err := a.watchBranchChanges(in, i)
		if err != nil {
			b.Error = "changed paths unavailable"
		} else {
			b.Changed = paths
		}
		r.Branches = append(r.Branches, b)
	}
	for i, left := range r.Branches {
		for j := i + 1; j < len(r.Branches); j++ {
			right := r.Branches[j]
			set := map[string]bool{}
			for _, p := range left.Changed {
				set[p] = true
			}
			var common []string
			for _, p := range right.Changed {
				if set[p] {
					common = append(common, p)
				}
			}
			sort.Strings(common)
			slots := map[string][]string{}
			for _, p := range left.Changed {
				if key := boardMigrationSlot(p); key != "" {
					slots[key] = append(slots[key], p)
				}
			}
			var candidates []string
			for _, p := range right.Changed {
				if key := boardMigrationSlot(p); key != "" && len(slots[key]) > 0 {
					candidates = append(candidates, slots[key]...)
					candidates = append(candidates, p)
				}
			}
			if len(candidates) > 0 {
				sort.Strings(candidates)
				r.MigrationCandidates = append(r.MigrationCandidates, boardOverlap{left.Ref, right.Ref, candidates})
			}
			if len(common) > 0 {
				r.Overlaps = append(r.Overlaps, boardOverlap{left.Ref, right.Ref, common})
			}
		}
	}
	for _, b := range r.Branches {
		r.Details = append(r.Details, boardDetail{Branches: []string{b.Ref}, Status: "not_checked"})
	}
	return r
}

func (a *app) boardDetails(ctx context.Context, in watchInput, o options, r boardReport, pairs [][2]int) (watch.Result, error) {
	// Standalone probes and requested analyses operate only in private Git state.
	r.Details = nil
	for _, b := range r.Branches {
		if ctx.Err() != nil {
			return watch.Result{}, ctx.Err()
		}
		var c *integration.Candidate
		var err error
		var snapshots []integration.DirtySnapshot
		for _, s := range in.Snapshots {
			if s.InputIndex < len(in.Refs) && s.InputIndex >= 0 && in.Refs[s.InputIndex] == b.Ref {
				copy := s
				copy.InputIndex = 0
				snapshots = append(snapshots, copy)
			}
		}
		if o.includeDirty {
			c, err = integration.BuildDirtyCandidate(ctx, a.root, in.Base, []string{b.Commit}, snapshots)
		} else {
			c, err = integration.BuildCandidate(ctx, a.root, in.Base, []string{b.Commit})
		}
		d := boardDetail{Branches: []string{b.Ref}, Status: "not_checked"}
		if err != nil {
			d.Error = "standalone conflict probe unavailable"
		} else {
			d.Status = "textual_clean"
			d.Conflicts = append([]string(nil), c.Conflicts...)
			if len(d.Conflicts) > 0 {
				d.Status = "textual_conflict"
			}
		}
		if c != nil {
			c.Close()
		}
		r.Details = append(r.Details, d)
	}
	for _, pair := range pairs {
		refs := []string{in.SHAs[pair[0]], in.SHAs[pair[1]]}
		d := boardDetail{Branches: []string{in.Refs[pair[0]], in.Refs[pair[1]]}, Status: "not_checked"}
		var snapshots []integration.DirtySnapshot
		for _, s := range in.Snapshots {
			for j, idx := range pair {
				if s.InputIndex == idx {
					copy := s
					copy.InputIndex = j
					snapshots = append(snapshots, copy)
				}
			}
		}
		var c *integration.Candidate
		var err error
		if o.includeDirty {
			c, err = integration.BuildDirtyCandidate(ctx, a.root, in.Base, refs, snapshots)
		} else {
			c, err = integration.BuildCandidate(ctx, a.root, in.Base, refs)
		}
		if err == nil {
			report, e := integration.Analyze(ctx, c, integration.Options{Base: in.Base, Branches: refs, Policy: in.config.Policy, SuggestTests: true, Verify: false, AllowExecution: false, Timeout: o.timeout, MaxCommands: o.maxCommands})
			if e != nil {
				err = e
			} else {
				in.config.applyIntegration(&report)
				d.Report = &report
				d.Status = string(report.Gate.Verdict)
			}
		}
		if c != nil {
			c.Close()
		}
		if err != nil {
			d.Error = "requested pair analysis unavailable"
		}
		r.Details = append(r.Details, d)
	}
	if o.verify {
		var c *integration.Candidate
		var err error
		if o.includeDirty {
			c, err = integration.BuildDirtyCandidate(ctx, a.root, in.Base, in.SHAs, in.Snapshots)
		} else {
			c, err = integration.BuildCandidate(ctx, a.root, in.Base, in.SHAs)
		}
		d := boardDetail{Branches: append([]string(nil), in.Refs...), Status: "not_checked"}
		if err == nil {
			report, e := integration.Analyze(ctx, c, integration.Options{Base: in.Base, Branches: in.SHAs, Policy: in.config.Policy, SuggestTests: true, Verify: true, AllowExecution: true, Typecheck: o.typecheck, Suite: o.suite, Timeout: o.timeout, MaxCommands: o.maxCommands, ObserveCoverage: o.observeCoverage})
			if e != nil {
				err = e
			} else {
				in.config.applyIntegration(&report)
				d.Report = &report
				d.Status = string(report.Gate.Verdict)
			}
		}
		if c != nil {
			c.Close()
		}
		if err != nil {
			d.Error = "combined execution unavailable"
		}
		r.Details = append(r.Details, d)
	}
	return watch.Result{Value: r}, nil
}

func (a *app) watchBoard(o options) int {
	initial, err := a.observeWatch(a.ctx, o)
	if err != nil {
		return a.fail(err)
	}
	if _, err = parseBoardPairs(o.pairs, initial.Value.(watchInput)); err != nil {
		return a.fail(err)
	}
	if o.verify {
		fmt.Fprintln(a.errout, "Board execution authorized for this session: repeated selected repository commands with your permissions. Private candidates are not OS sandboxes.")
	}
	ctx, cancel := context.WithCancel(a.ctx)
	defer cancel()
	var outputErr error
	lastNotification := ""
	display := watchDisplay{VerificationState: "NOT VERIFIED"}
	err = watch.Run(ctx, watch.Options{Interval: o.interval, Debounce: o.debounce, Heartbeat: 30 * time.Second}, func(ctx context.Context) (watch.State, error) { return a.observeWatch(ctx, o) }, func(ctx context.Context, s watch.State) (watch.Result, error) {
		in := s.Value.(watchInput)
		pairs, e := parseBoardPairs(o.pairs, in)
		if e != nil {
			return watch.Result{}, e
		}
		r := a.boardSnapshot(ctx, in)
		result := watch.Result{Value: r}
		if len(in.SHAs) > 0 {
			result.Followup = func(ctx context.Context) (watch.Result, error) {
				bounded, stop := context.WithTimeout(ctx, o.timeout)
				defer stop()
				return a.boardDetails(bounded, in, o, r, pairs)
			}
		}
		return result, nil
	}, func(e watch.Event) {
		switch e.Type {
		case watch.Changed:
			display.LastChange = e.Time
			display.VerificationState = "NOT VERIFIED"
		case watch.Checking:
			display.VerificationState = "CHECKING"
		case watch.Error:
			display.VerificationState = "ERROR"
		case watch.Completed, watch.Attributed:
			if e.Error != "" {
				display.VerificationState = "ERROR"
			} else {
				display.LastCompleted = e.Time
				display.VerificationState = "NOT VERIFIED"
			}
		}
		if o.jsonl || a.machine {
			outputErr = json.NewEncoder(a.out).Encode(struct {
				watch.Event
				watchDisplay
			}{e, display})
		} else {
			a.renderBoard(e)
		}
		if outputErr != nil {
			cancel()
		}
		if o.notify && e.Result != nil {
			if r, ok := e.Result.Value.(boardReport); ok {
				for _, d := range r.Details {
					if d.Status == "textual_conflict" || d.Status == "fail" || d.Status == "blocked" {
						key := e.State.Key + ":" + d.Status
						if key != lastNotification {
							lastNotification = key
							if err := notifyBoard(ctx, "Radar: "+d.Status); err != nil {
								fmt.Fprintln(a.errout, "Board notification unavailable; analysis is unchanged.")
							}
						}
						break
					}
				}
			}
		}
	})
	if outputErr != nil {
		return a.fail(outputErr)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		return a.fail(err)
	}
	return 0
}
func (a *app) renderBoard(e watch.Event) {
	if e.Type == watch.Heartbeat {
		fmt.Fprintln(a.out, "Radar board: still watching; no new result.")
		return
	}
	output := a.out
	if styled, ok := output.(styledWriter); ok {
		output = styled.Writer
	}
	if termui.IsTerminal(output) {
		fmt.Fprint(a.out, "\x1b[2J\x1b[H")
	}
	fmt.Fprintf(a.out, "Radar board: %s — NOT VERIFIED\n", e.Type)
	if e.Error != "" {
		fmt.Fprintln(a.out, e.Error)
		return
	}
	if e.Type == watch.Changed {
		fmt.Fprintln(a.out, "Inputs changed; previous results are stale.")
	}
	if e.Result == nil {
		return
	}
	r, ok := e.Result.Value.(boardReport)
	if !ok {
		return
	}
	for _, b := range r.Branches {
		fmt.Fprintf(a.out, "  %s @ %s  +%d -%d  %d changed files", b.Ref, short(b.Commit), b.Ahead, b.Behind, len(b.Changed))
		if b.Error != "" {
			fmt.Fprintf(a.out, " — %s", b.Error)
		}
		fmt.Fprintln(a.out)
	}
	for _, overlap := range r.Overlaps {
		fmt.Fprintf(a.out, "  ! overlap %s + %s: %s (not compatibility evidence)\n", overlap.Left, overlap.Right, strings.Join(overlap.Files, ", "))
	}
	for _, d := range r.Details {
		fmt.Fprintf(a.out, "  %s: %s", strings.Join(d.Branches, " + "), d.Status)
		if d.Error != "" {
			fmt.Fprintf(a.out, " — %s", d.Error)
		}
		fmt.Fprintln(a.out)
		if d.Report != nil {
			renderGate(a.out, gateReport{Report: *d.Report, Attribution: map[string]gateAttribution{}, Next: "repair and commit; the board rechecks automatically"}, len(d.Report.Executions) > 0)
		}
	}
	for _, c := range r.MigrationCandidates {
		fmt.Fprintf(a.out, "  ! migration slot candidate %s + %s: %s (inferred; not checked)\n", c.Left, c.Right, strings.Join(c.Files, ", "))
	}
	for _, h := range r.Recent {
		fmt.Fprintf(a.out, "  historical gate %s: %s @ %s (not current evidence)\n", h.ID, h.Verdict, h.CreatedAt)
	}
	if r.HistoryError != "" {
		fmt.Fprintln(a.out, "  "+r.HistoryError)
	}
	fmt.Fprintln(a.out, "  migration slots: "+r.MigrationStatus)
}
func notifyBoard(ctx context.Context, message string) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.CommandContext(ctx, "notify-send", "Radar", message)
	case "darwin":
		cmd = exec.CommandContext(ctx, "osascript", "-e", "on run argv\n display notification (item 1 of argv) with title \"Radar\"\nend run", message)
	default:
		return errors.New("native notifications unsupported on this platform")
	}
	return cmd.Run()
}

func boardMigrationSlot(p string) string {
	dir := path.Dir(p)
	if !strings.Contains("/"+dir+"/", "/migrations/") {
		return ""
	}
	name := path.Base(p)
	i := 0
	for i < len(name) && name[i] >= '0' && name[i] <= '9' {
		i++
	}
	if i == 0 || i == len(name) || (name[i] != '_' && name[i] != '.') {
		return ""
	}
	return dir + "/" + strings.TrimLeft(name[:i], "0")
}
