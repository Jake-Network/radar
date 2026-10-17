package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/overlap"
	"github.com/Jake-Network/radar/internal/testselection"
	"github.com/Jake-Network/radar/internal/watch"
)

func watchCommand() command {
	return command{name: "watch", usage: "radar watch [BRANCH ...] [--base REF] [--interval 1s] [--debounce 500ms] [--include-dirty] [--jsonl] [--run] [--policy PATH]", summary: "Continuously check worktree branches in private Git state.", flags: func(fs *flag.FlagSet, o *options) {
		fs.StringVar(&o.base, "base", "", "integration REF (same defaults as gate)")
		fs.DurationVar(&o.interval, "interval", time.Second, "poll interval, minimum 100ms")
		fs.DurationVar(&o.debounce, "debounce", 500*time.Millisecond, "wait for changes to settle")
		fs.BoolVar(&o.includeDirty, "include-dirty", false, "preview saved staged, unstaged and nonignored untracked changes")
		fs.BoolVar(&o.jsonl, "jsonl", false, "emit one structured event per line")
		fs.IntVar(&o.attributionRuns, "attribution-runs", 0, "with --run, separately observe up to N diagnostic subset executions after Fast Gate")
		fs.BoolVar(&o.observeCoverage, "observe-coverage", false, "with execution opt-in, observe covered files for later test selection")
		fs.BoolVar(&o.verify, "run", false, "authorize repeated repository test execution for this session with host permissions")
		fs.DurationVar(&o.timeout, "timeout", 10*time.Minute, "execution time budget per check")
		fs.IntVar(&o.maxCommands, "max-commands", testselection.DefaultMaxCommands, "maximum commands per check")
		fs.StringVar(&o.suite, "suite", "", "test selection mode with --run")
		policyFlag(fs, o)
	}, run: (*app).watch}
}

type watchInput struct {
	OverlapInputs []overlap.Input             `json:"overlap_inputs,omitempty"`
	Base          string                      `json:"base"`
	Refs          []string                    `json:"branches"`
	SHAs          []string                    `json:"commits"`
	Worktrees     []gitrepo.WorktreeStatus    `json:"worktrees"`
	Configuration []ConfigurationInput        `json:"configuration_inputs,omitempty"`
	Snapshots     []integration.DirtySnapshot `json:"dirty_snapshots,omitempty"`
	config        selectedConfiguration
}

func (a *app) observeWatch(ctx context.Context, o options) (watch.State, error) {
	b := *a
	b.ctx = ctx
	base := o.base
	if base == "" {
		base = gitrepo.DefaultBranch(ctx, a.root)
	}
	if base == "" {
		return watch.State{}, errors.New("no default base; pass --base REF")
	}
	sha, err := gitrepo.Resolve(ctx, a.root, base)
	if err != nil {
		return watch.State{}, err
	}
	refs, _, err := b.gateBranches(o.args, base, sha)
	if err != nil && (len(o.args) > 0 || !strings.HasPrefix(err.Error(), "no branches to combine:")) {
		return watch.State{}, err
	}
	if err != nil {
		refs = nil
	} // No worktree branches is a waiting state, never a pass.
	input := watchInput{Base: sha, Refs: refs}
	for _, ref := range refs {
		s, e := gitrepo.Resolve(ctx, a.root, ref)
		if e != nil {
			return watch.State{}, e
		}
		input.SHAs = append(input.SHAs, s)
	}
	input.Worktrees, err = gitrepo.InspectWorktrees(ctx, a.root)
	if err != nil {
		return watch.State{}, err
	}
	if len(input.Worktrees) > 32 || len(input.SHAs) > 32 {
		return watch.State{}, errors.New("watch supports at most 32 worktrees and selected branches")
	}
	for _, w := range input.Worktrees {
		if w.InspectionError != "" {
			return watch.State{}, fmt.Errorf("worktree %q: %s", w.Path, w.InspectionError)
		}
	}
	if o.includeDirty {
		localBranches, e := gitrepo.LocalBranches(ctx, a.root)
		if e != nil {
			return watch.State{}, e
		}
		for wi, w := range input.Worktrees {
			idx := -1
			for i, ref := range input.Refs {
				named := strings.TrimPrefix(ref, "refs/heads/")
				if named == w.Branch && !w.Detached || ref == "HEAD" && w.Path == a.root {
					idx = i
					break
				}
				if ref == "HEAD" {
					continue
				}
				if input.SHAs[i] != w.Commit {
					continue
				}
				knownBranch := slices.Contains(localBranches, named)
				if knownBranch {
					continue
				}
				matches := 0
				for _, t := range input.Worktrees {
					if t.Commit == w.Commit {
						matches++
					}
				}
				if matches > 1 {
					return watch.State{}, fmt.Errorf("dirty selection %q matches multiple worktrees; select branch names explicitly", ref)
				}
				idx = i
				break
			}
			baseBranch := strings.TrimPrefix(strings.TrimPrefix(base, "refs/heads/"), "origin/")
			autoEligible := len(o.args) == 0 && !w.Detached && w.Branch != baseBranch
			if idx < 0 && !autoEligible {
				continue
			}
			// Inspect eligible worktrees even when Git status is clean: index flags,
			// file modes and normalization can hide unsupported working state.
			snap, e := integration.ObserveDirty(ctx, w)
			if e != nil {
				return watch.State{}, fmt.Errorf("dirty preview NOT VERIFIED for %q: %w", w.Path, e)
			}
			if len(snap.Staged)+len(snap.Unstaged)+len(snap.Untracked) == 0 {
				continue
			}
			if idx < 0 {
				idx = len(input.Refs)
				input.Refs = append(input.Refs, w.Branch)
				input.SHAs = append(input.SHAs, w.Commit)
			}
			if len(input.Snapshots) >= 8 {
				return watch.State{}, errors.New("dirty watch supports at most 8 selected worktree snapshots")
			}
			snap.InputIndex = idx
			input.Snapshots = append(input.Snapshots, snap)
			input.Worktrees[wi].UncommittedIncluded = true
		}
	}
	for i := range input.Worktrees {
		w := &input.Worktrees[i]
		w.CommitIncluded = w.Commit == input.Base || slices.Contains(input.SHAs, w.Commit)
	}
	if o.policy != "" {
		path := o.policy
		if !filepath.IsAbs(path) {
			path = filepath.Join(a.root, path)
		}
		info, e := os.Stat(path)
		if e != nil {
			return watch.State{}, e
		}
		if !info.Mode().IsRegular() {
			return watch.State{}, errors.New("watch policy must be a regular file")
		}
	}
	input.config, err = b.captureConfiguration(o)
	if err != nil {
		return watch.State{}, err
	}
	input.Configuration = input.config.Inputs
	data, _ := json.Marshal(input)
	return watch.State{Key: configurationDigest(data), Value: input}, nil
}

func (a *app) watch(o options) int {
	if e := a.repository(); e != nil {
		return a.fail(e)
	}
	if o.attributionRuns < 0 || o.attributionRuns > 64 || o.attributionRuns > 0 && !o.verify {
		return a.fail(errors.New("attribution-runs must be 0..64 and requires --run"))
	}
	if o.suite != "" && !o.verify {
		return a.fail(errors.New("--suite requires --run"))
	}
	if o.interval < 100*time.Millisecond || o.interval > time.Minute || o.debounce < 0 || o.debounce > time.Minute {
		return a.fail(errors.New("interval must be 100ms..1m and debounce 0..1m"))
	}
	if o.timeout <= 0 || o.timeout > 30*time.Minute || o.maxCommands < 1 || o.maxCommands > 256 {
		return a.fail(errors.New("invalid timeout or command budget"))
	}
	if o.verify && o.suite == "" {
		o.suite = testselection.ModeBalanced
	}
	if o.suite != "" {
		if _, e := testselection.NormalizeMode(o.suite); e != nil {
			return a.fail(e)
		}
	}
	// Initial invocation errors are errors; later failures are live events.
	if _, e := a.observeWatch(a.ctx, o); e != nil {
		return a.fail(e)
	}
	if o.verify {
		fmt.Fprintln(a.errout, "Watch execution authorized for this session: repeated selected tests on the monitored branches, including newly discovered worktrees, with your permissions. Private checkouts are not OS sandboxes. No dependencies are installed or fetched.")
	}
	sessionCtx, stop := context.WithCancel(a.ctx)
	defer stop()
	var outputErr error
	display := watchDisplay{VerificationState: "NOT VERIFIED"}
	var cachedKey string
	var cachedReport *integration.Report
	parseCache, saveParseCache := savedSyntaxCache(a.root)
	defer saveParseCache()
	err := watch.Run(sessionCtx, watch.Options{Interval: o.interval, Debounce: o.debounce, Heartbeat: 30 * time.Second}, func(ctx context.Context) (watch.State, error) { return a.observeWatch(ctx, o) }, func(ctx context.Context, s watch.State) (watch.Result, error) {
		in := s.Value.(watchInput)
		in.config.Inputs = append([]ConfigurationInput(nil), in.config.Inputs...)
		if len(in.SHAs) == 0 {
			return watch.Result{Value: map[string]string{"state": "NOT VERIFIED", "reason": "No selected branches with commits beyond the base; waiting for changes."}}, nil
		}
		semantic, _ := json.Marshal(struct {
			Base      string
			Refs      []string
			Inputs    []string
			Config    []ConfigurationInput
			Snapshots []integration.DirtySnapshot
		}{in.Base, in.Refs, in.SHAs, in.Configuration, in.Snapshots})
		semanticKey := configurationDigest(semantic)
		if o.attributionRuns == 0 && cachedReport != nil && cachedKey == semanticKey {
			reused := *cachedReport
			reused.Limitations = append(append([]string(nil), reused.Limitations...), "Reused analysis and evidence for unchanged pinned verification inputs; worktree monitoring metadata changed.")
			return watch.Result{Value: reused}, nil
		}
		var c *integration.Candidate
		var e error
		if o.includeDirty {
			c, e = integration.BuildDirtyCandidate(ctx, a.root, in.Base, in.SHAs, in.Snapshots)
		} else {
			c, e = integration.BuildCandidate(ctx, a.root, in.Base, in.SHAs)
		}
		if e != nil {
			c.Close()
			return watch.Result{}, e
		}
		opts := integration.Options{Base: in.Base, Branches: in.SHAs, SuggestTests: true, Policy: in.config.Policy, Verify: o.verify, AllowExecution: o.verify, Suite: o.suite, Timeout: o.timeout, MaxCommands: o.maxCommands, ObserveCoverage: o.observeCoverage, ParseCache: parseCache}
		r, e := integration.Analyze(ctx, c, opts)
		if e != nil {
			c.Close()
			return watch.Result{}, e
		}
		in.config.applyIntegration(&r)
		if len(in.SHAs) > 1 {
			drift, err := overlap.Analyze(ctx, a.root, in.Base, overlapInputs(in), parseCache)
			if err != nil {
				c.Close()
				return watch.Result{}, err
			}
			r.Drift = drift.Findings
			r.Limitations = append(r.Limitations, drift.Limitations...)
			watchOverlapCoverage(&r, drift, in.Refs)
		}
		if !configurationUnstable(in.config.Inputs) {
			cachedKey = semanticKey
			cachedReport = &r
		}
		result := watch.Result{Value: r, Close: func() { c.Close() }}
		if o.attributionRuns > 0 && slices.ContainsFunc(r.Executions, func(ev integration.ExecutionEvidence) bool { return ev.Status == model.StatusFailed }) {
			result.Followup = func(ctx context.Context) (watch.Result, error) {
				opts.AttributionRuns = o.attributionRuns
				observed, e := integration.RunObservedAttribution(ctx, c, r, opts)
				return watch.Result{Value: watchAttribution{CandidateTree: r.CandidateTree, Observed: observed}}, e
			}
		}
		return result, nil
	}, func(e watch.Event) {
		switch e.Type {
		case watch.Changed:
			display.VerificationState = "NOT VERIFIED"
			display.LastChange = e.Time
		case watch.Checking:
			display.VerificationState = "CHECKING"
		case watch.Error:
			display.VerificationState = "ERROR"
		case watch.Completed:
			display.LastCompleted = e.Time
			display.VerificationState = "NOT VERIFIED"
			if e.Result != nil {
				if r, ok := e.Result.Value.(integration.Report); ok {
					display.VerificationState = verdictText(r.Gate.Verdict, o.verify)
				}
			}
		}
		if o.jsonl || a.machine {
			outputErr = json.NewEncoder(a.out).Encode(struct {
				watch.Event
				watchDisplay
			}{e, display})
			if outputErr != nil {
				stop()
			}
		} else {
			if e.Type == watch.Heartbeat {
				// One quiet line: the state is unchanged since the last event.
				p := paletteOf(a.out)
				fmt.Fprintln(a.out, p.dim(fmt.Sprintf("%s · still watching: %s; last check %s, last change %s", e.Time.Format("15:04:05"), display.VerificationState, watchTime(display.LastCompleted), watchTime(display.LastChange))))
			} else {
				a.renderWatch(e, o)
			}
		}
	})
	if outputErr != nil {
		return a.fail(fmt.Errorf("watch output: %w", outputErr))
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		return a.fail(err)
	}
	return 0
}
func (a *app) renderWatch(e watch.Event, o options) {
	if o.jsonl || a.machine {
		json.NewEncoder(a.out).Encode(e)
		return
	}
	p := paletteOf(a.out)
	fmt.Fprintf(a.out, "%s %s %s\n", p.cyan(e.Time.Format("15:04:05")), p.bold(string(e.Type)), p.dim("@ "+short(e.State.Key)))
	if e.Error != "" {
		label := "ERROR (NOT VERIFIED): "
		if e.Type == watch.Attributed {
			label = "Observed Attribution unavailable (Fast Gate unchanged): "
		}
		fmt.Fprintln(a.out, p.yellow(label+e.Error))
		return
	}
	if e.Type == "checking" {
		fmt.Fprintln(a.out, "  CHECKING — the latest change; the previous result is superseded")
	}
	if e.Type == "changed" {
		fmt.Fprintln(a.out, "  "+p.yellow("NOT VERIFIED")+" — inputs changed; the previous result is no longer current")
		if in, ok := e.State.Value.(watchInput); ok {
			for _, w := range in.Worktrees {
				if len(w.Staged)+len(w.Unstaged)+len(w.Untracked) == 0 {
					continue
				}
				fmt.Fprintf(a.out, "  %s @ %s %q: %d staged, %d unstaged, %d untracked (%s)\n", w.Branch, short(w.Commit), w.Path, len(w.Staged), len(w.Unstaged), len(w.Untracked), dirtyWatchLabel(o.includeDirty))
			}
		}
	}
	if e.Result != nil {
		if r, ok := e.Result.Value.(integration.Report); ok {
			fmt.Fprintln(a.out, p.dim(fmt.Sprintf("  combined tree %s", short(r.CandidateTree))))
			g := gateReport{Report: r, Attribution: map[string]gateAttribution{}, Next: watchNext(r.Gate.Verdict, o.verify), title: "Radar watch: ", details: "Details: add --jsonl for machine-readable events. Your branches are never touched.", snapshot: o.includeDirty}
			if in, ok := e.State.Value.(watchInput); ok {
				g.BaseRef = o.base
				if g.BaseRef == "" {
					g.BaseRef = gitrepo.DefaultBranch(a.ctx, a.root)
				}
				if g.BaseRef == "" {
					g.BaseRef = "base"
				}
				for i, ref := range in.Refs {
					changed, err := a.watchBranchChanges(in, i)
					if err != nil {
						fmt.Fprintf(a.out, "  %s branch %q: changed-file inspection unavailable: %s. Candidate verification is unchanged; inspect this branch manually for repair leads.\n", p.mark("!"), ref, err)
					}
					g.Branches = append(g.Branches, gateBranch{Ref: ref, Commit: r.Inputs[i], Changed: changed, changedUnknown: err != nil})
				}
			}
			g.attribute()
			renderGate(a.out, g, o.verify)
			for _, f := range r.Drift {
				fmt.Fprintf(a.out, "  ! drift [%s]: %s\n", strings.Join(f.Branches, ", "), f.Explanation)
				for _, p := range f.Locations {
					fmt.Fprintf(a.out, "    %s:%d\n", p.Path, p.Line)
				}
			}
		} else if observed, ok := e.Result.Value.(watchAttribution); ok {
			fmt.Fprintln(a.out, "Observed Attribution — diagnostic observations; Fast Gate unchanged")
			for _, attr := range observed.Observed {
				fmt.Fprintf(a.out, "  %s: %s (%d subset runs)\n", attr.SelectionID, attr.Outcome, len(attr.Runs))
			}
		} else if idle, ok := e.Result.Value.(map[string]string); ok {
			fmt.Fprintf(a.out, "  %s — %s\n", p.yellow(idle["state"]), idle["reason"])
		} else {
			fmt.Fprintln(a.out, e.Result.Value)
		}
	}
}

// Snapshot paths belong to the pinned input, even when its committed head has
// no changes beyond the base. Keep the union sorted for gate's path lookup.
func (a *app) watchBranchChanges(in watchInput, i int) ([]string, error) {
	changed, err := a.branchChanges(in.Base, in.SHAs[i])
	for _, snapshot := range in.Snapshots {
		if snapshot.InputIndex != i {
			continue
		}
		changed = append(changed, snapshot.Staged...)
		changed = append(changed, snapshot.Unstaged...)
		changed = append(changed, snapshot.Untracked...)
	}
	slices.Sort(changed)
	return slices.Compact(changed), err
}

// watchNext is the next step after a completed watch check. Watch reruns by
// itself, so the step is what to change, not a command to repeat.
func watchNext(v gate.Verdict, ran bool) string {
	switch {
	case v == gate.Fail:
		return "repair on the branches above and commit; watch rechecks automatically."
	case v == gate.Blocked:
		return "resolve the items marked ? above; watch rechecks after the next change."
	case v == gate.Error:
		return "resolve the reported error; watch rechecks after the next change."
	case !ran:
		return "static checks only; restart with radar watch --run to also run the related tests."
	}
	return ""
}

func dirtyWatchLabel(included bool) string {
	if included {
		return "selected worktree snapshots included; others excluded"
	}
	return "excluded"
}

type watchAttribution struct {
	CandidateTree string                    `json:"candidate_tree"`
	Observed      []integration.Attribution `json:"observed_attributions"`
}

type watchDisplay struct {
	VerificationState string    `json:"verification_state"`
	LastCompleted     time.Time `json:"last_completed"`
	LastChange        time.Time `json:"last_change"`
}

func watchTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.Format("15:04:05")
}

// Pairwise drift is supplemental to the complete candidate check. Preserve its
// verdict, while making omitted pair analysis visible to machine consumers.
func watchOverlapCoverage(report *integration.Report, drift overlap.Report, refs []string) {
	gaps := []string{}
	for _, limitation := range drift.Limitations {
		if strings.HasPrefix(limitation, "Overlap incomplete:") {
			gaps = append(gaps, limitation)
		}
	}
	if len(gaps) == 0 {
		return
	}
	check := gate.Check{ID: "worktree_overlap", Status: model.StatusUnknown, Evidence: model.Unknown, Explanation: strings.Join(gaps, " ")}
	report.Checks = append(report.Checks, check)
	report.Coverage = append(report.Coverage, gate.Coverage{Analyzer: check.ID, Status: check.Status, Scope: append([]string{}, refs...), Excluded: []string{}, Unavailable: append([]string{}, gaps...), Uncertainty: []string{"Unchecked pairs are not evidence of compatibility."}})
}
