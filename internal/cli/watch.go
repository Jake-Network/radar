package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"
	"time"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
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
		fs.BoolVar(&o.verify, "run", false, "authorize repeated repository test execution for this session with host permissions")
		fs.DurationVar(&o.timeout, "timeout", 10*time.Minute, "execution time budget per check")
		fs.IntVar(&o.maxCommands, "max-commands", testselection.DefaultMaxCommands, "maximum commands per check")
		fs.StringVar(&o.suite, "suite", "", "test selection mode with --run")
		policyFlag(fs, o)
	}, run: (*app).watch}
}

type watchInput struct {
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
		for _, w := range input.Worktrees {
			idx := -1
			for i, ref := range input.Refs {
				named := strings.TrimPrefix(ref, "refs/heads/")
				if named == w.Branch && !w.Detached || ref == "HEAD" && w.Path == a.root {
					idx = i
					break
				}
				if input.SHAs[i] != w.Commit {
					continue
				}
				knownBranch := slices.ContainsFunc(input.Worktrees, func(t gitrepo.WorktreeStatus) bool { return t.Branch == named })
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
			if idx < 0 && len(o.args) == 0 && !w.Detached && w.Branch != base && w.Branch != strings.TrimPrefix(base, "origin/") && len(w.Staged)+len(w.Unstaged)+len(w.Untracked) > 0 {
				idx = len(input.Refs)
				input.Refs = append(input.Refs, w.Branch)
				input.SHAs = append(input.SHAs, w.Commit)
			}

			if idx < 0 {
				continue
			}
			if len(input.Snapshots) >= 8 {
				return watch.State{}, errors.New("dirty watch supports at most 8 selected worktree snapshots")
			}
			snap, e := integration.ObserveDirty(ctx, w)
			if e != nil {
				return watch.State{}, e
			}
			snap.InputIndex = idx
			input.Snapshots = append(input.Snapshots, snap)
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
	err := watch.Run(a.ctx, watch.Options{Interval: o.interval, Debounce: o.debounce, Heartbeat: 30 * time.Second}, func(ctx context.Context) (watch.State, error) { return a.observeWatch(ctx, o) }, func(ctx context.Context, s watch.State) (watch.Result, error) {
		in := s.Value.(watchInput)
		in.config.Inputs = append([]ConfigurationInput(nil), in.config.Inputs...)
		if len(in.SHAs) == 0 {
			return watch.Result{Value: map[string]string{"state": "NOT VERIFIED", "reason": "No selected branches with commits beyond the base; waiting for changes."}}, nil
		}
		var c *integration.Candidate
		var e error
		if o.includeDirty {
			c, e = integration.BuildDirtyCandidate(ctx, a.root, in.Base, in.SHAs, in.Snapshots)
		} else {
			c, e = integration.BuildCandidate(ctx, a.root, in.Base, in.SHAs)
		}
		defer c.Close()
		if e != nil {
			return watch.Result{}, e
		}
		r, e := integration.Analyze(ctx, c, integration.Options{Base: in.Base, Branches: in.SHAs, SuggestTests: true, Policy: in.config.Policy, Verify: o.verify, AllowExecution: o.verify, Suite: o.suite, Timeout: o.timeout, MaxCommands: o.maxCommands})
		if e != nil {
			return watch.Result{}, e
		}
		in.config.applyIntegration(&r)
		return watch.Result{Value: r}, nil
	}, func(e watch.Event) { a.renderWatch(e, o) })
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
	fmt.Fprintf(a.out, "%s Radar watch %s @ %s\n", p.cyan(e.Time.Format("15:04:05")), e.Type, short(e.State.Key))
	if e.Error != "" {
		fmt.Fprintln(a.out, p.yellow("NOT VERIFIED: "+e.Error))
		return
	}
	if e.Type == "checking" {
		fmt.Fprintln(a.out, "CHECKING — latest change; previous result superseded")
	}
	if e.Type == "changed" {
		if in, ok := e.State.Value.(watchInput); ok {
			for _, w := range in.Worktrees {
				fmt.Fprintf(a.out, "  %s @ %s %q: %d staged, %d unstaged, %d untracked (%s)\n", w.Branch, short(w.Commit), w.Path, len(w.Staged), len(w.Unstaged), len(w.Untracked), dirtyWatchLabel(o.includeDirty))
			}
		}
	}
	if e.Result != nil {
		if r, ok := e.Result.Value.(integration.Report); ok {
			fmt.Fprintln(a.out, p.verdict(r.Gate.Verdict, verdictText(r.Gate.Verdict, o.verify)))
			fmt.Fprintf(a.out, "  snapshot %s: %d merge conflicts; %d findings; %d selected tests\n", short(r.CandidateTree), len(r.Conflicts), len(r.Findings), len(r.Selected))
			g := gateReport{Report: r, Attribution: map[string]gateAttribution{}, Next: "Repair reported problems or missing evidence; watch rechecks after the next change."}
			renderGate(a.out, g, o.verify)
		} else {
			fmt.Fprintln(a.out, e.Result.Value)
		}
	}
}

func dirtyWatchLabel(included bool) string {
	if included {
		return "selected worktree snapshots included; others excluded"
	}
	return "excluded"
}
