package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/landing"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/testselection"
	"github.com/Jake-Network/radar/internal/workspace"
)

// gateBranch records what one input branch changed relative to its merge base
// with the integration base, so findings can name the branches involved.
type gateBranch struct {
	Ref            string   `json:"ref"`
	Commit         string   `json:"commit"`
	Changed        []string `json:"changed"`
	changedUnknown bool
}

// gateLead names a branch whose changed files a finding points at.
type gateLead struct {
	Branch string   `json:"branch"`
	Files  []string `json:"files"`
}

// gateAttribution is a lead for repair, not a verdict of blame: Basis says
// whether the files were named by the finding, reached through imports, or
// the branches were observed reproducing the failure on fewer branches.
type gateAttribution struct {
	Basis    string                   `json:"basis"`
	Leads    []gateLead               `json:"leads"`
	Observed *integration.Attribution `json:"observed,omitempty"`
}

// gateReport is merge-check's report plus branch attribution and the single
// next command a person or agent should run.
type gateReport struct {
	Landing                 *landing.Report            `json:"landing,omitempty"`
	ConflictBrief           *landing.ConflictBrief     `json:"conflict_brief,omitempty"`
	IntegrationRef          *landing.Applied           `json:"integration_ref,omitempty"`
	RunID                   string                     `json:"run_id,omitempty"`
	WorktreeInspectionError string                     `json:"worktree_inspection_error,omitempty"`
	Worktrees               []gitrepo.WorktreeStatus   `json:"worktrees"`
	Configuration           []ConfigurationInput       `json:"configuration_inputs,omitempty"`
	BaseRef                 string                     `json:"base_ref"`
	Branches                []gateBranch               `json:"branches"`
	Skipped                 []string                   `json:"skipped_branches,omitempty"`
	Attribution             map[string]gateAttribution `json:"attribution"`
	Next                    string                     `json:"next,omitempty"`
	integration.Report
	// title and details let watch reuse the renderer with its own words.
	title, details string
	// snapshot marks inputs that include private snapshot commits (watch
	// --include-dirty), which do not exist in the user's repository.
	snapshot bool
}

func gateCommand() command {
	return command{name: "gate", usage: "radar gate [[REPO:]BRANCH ...] [--base REF] [--with PATH] [--again] [--run [--suite targeted|balanced|full] [--timeout 10m]] [--policy PATH] [--plan PATH]",
		summary:       "Combine branches (default: every worktree branch) and tell whether they integrate; --run also tests the combined tree.",
		advancedFlags: []string{"only", "replay", "attribution-runs"},
		flags: func(fs *flag.FlagSet, o *options) {
			fs.BoolVar(&o.landing, "landing", false, "compute the static pair matrix and landing order")
			fs.BoolVar(&o.conflictBrief, "conflict-brief", false, "return a static conflict brief for exactly two branches")
			fs.BoolVar(&o.overlap, "overlap", false, "inspect saved changes across worktrees for supported drift")
			fs.StringVar(&o.symbol, "symbol", "", "filter --overlap by symbol name")
			fs.StringVar(&o.file, "file", "", "filter --overlap by repository-relative file")
			fs.BoolVar(&o.trailer, "trailer", false, "persist a passing gate and emit its Radar-Verified commit trailer")
			fs.Var(baseFlag{o}, "base", "integration `REF` (default: origin/HEAD's branch, main, master or trunk)")
			fs.Var(listFlag{&o.with}, "with", "also check the repository at `PATH` in this run (repeatable; relative to the repository root)")
			fs.BoolVar(&o.again, "again", false, "repeat the previous workspace run's selection on the latest commits")
			fs.Var(listFlag{&o.only}, "only", "check only workspace repository `REPO` in this run (repeatable)")
			fs.StringVar(&o.replay, "replay", "", "rebuild the exact commits of workspace `RUN` (run ID or last)")
			fs.BoolVar(&o.observeCoverage, "observe-coverage", false, "with execution opt-in, observe covered files for later test selection")
			fs.BoolVar(&o.verify, "run", false, "execute the selected tests on the combined tree with your host permissions (not an OS sandbox)")
			fs.StringVar(&o.suite, "suite", "", "test selection `MODE` with --run: targeted, balanced (default) or full")
			fs.IntVar(&o.maxCommands, "max-commands", testselection.DefaultMaxCommands, "maximum grouped test commands (`N`) --run executes")
			fs.DurationVar(&o.timeout, "timeout", 10*time.Minute, "total --run time budget (`DURATION`), maximum 30m")
			fs.IntVar(&o.attributionRuns, "attribution-runs", 0, "with --run, opt in to at most `N` diagnostic reruns on smaller branch combinations (default 0; for example 8)")
			policyFlag(fs, o)
			planFlag(fs, o)
		}, run: (*app).gate}
}

func (a *app) gate(o options) int {
	if o.overlap {
		if o.verify || o.trailer || o.landing || o.conflictBrief || o.plan != "" || o.policy != "" || o.suite != "" || o.observeCoverage {
			return a.fail(errors.New("--overlap is a static query and cannot be combined with execution, plan, policy, trailer or landing options"))
		}
		inv, err := a.workspaceInvocation(o)
		if err != nil {
			return a.fail(err)
		}
		if inv != nil {
			return a.workspaceOverlap(o, inv)
		}
		return a.overlap(o)
	}
	if o.landing || o.conflictBrief {
		return a.land(o)
	}
	if o.symbol != "" || o.file != "" {
		return a.fail(errors.New("--symbol and --file require --overlap"))
	}
	if o.trailer && a.machine {
		return a.fail(errors.New("--trailer emits commit text; omit --json"))
	}
	if o.observeCoverage && !o.verify {
		return a.fail(errors.New("--observe-coverage requires --run"))
	}
	if o.suite != "" && !o.verify {
		return a.fail(errors.New("--suite selects what --run executes; add --run or use --suite only with it"))
	}
	if e := a.repository(); e != nil {
		return a.fail(e)
	}
	// A repository in a workspace, or a run with --with, --again or --replay,
	// checks several repositories. Anything else is the single-repository gate.
	invocation, err := a.workspaceInvocation(o)
	if err != nil {
		return a.fail(err)
	}
	if invocation != nil {
		return a.workspaceGate(o, invocation)
	}
	configuration, err := a.captureConfiguration(o)
	if err != nil {
		return a.fail(err)
	}
	policy := configuration.Policy
	baseRef := o.base
	if baseRef == "" {
		if baseRef = gitrepo.DefaultBranch(a.ctx, a.root); baseRef == "" {
			return a.fail(errors.New("no main, master or trunk branch found; pass --base REF"))
		}
	}
	baseSHA, err := gitrepo.Resolve(a.ctx, a.root, baseRef)
	if err != nil {
		return a.fail(fmt.Errorf("base %q: %w", baseRef, err))
	}
	refs, skipped, err := a.gateBranches(o.args, baseRef, baseSHA)
	if err != nil && len(o.args) == 0 && strings.HasPrefix(err.Error(), "no branches to combine:") {
		// First-run gate can inspect HEAD even without parallel worktrees.
		// Watch and doctor keep their independent empty-selection behavior.
		refs, err = []string{"HEAD"}, nil
	}
	if err != nil {
		return a.fail(err)
	}
	plan := configuration.Plan
	options := integration.Options{Base: baseSHA, Branches: refs, Policy: policy, Plan: plan, SuggestTests: true, MaxCommands: o.maxCommands, Timeout: o.timeout, ObserveCoverage: o.observeCoverage}
	if o.verify {
		options.Verify, options.AllowExecution = true, true
		options.AttributionRuns = o.attributionRuns
		options.Suite = o.suite
		if options.Suite == "" {
			options.Suite = testselection.ModeBalanced
		}
	}
	progress := a.startProgress("combining branches in private Git state")
	options.Progress = progress.callback()
	r, err := integration.Preview(a.ctx, a.root, options)
	progress.done()
	if err != nil {
		return a.fail(a.explainGateError(err, baseSHA))
	}
	configuration.applyIntegration(&r)
	g := gateReport{Configuration: configuration.Inputs, BaseRef: baseRef, Skipped: skipped, Attribution: map[string]gateAttribution{}, Report: r}
	g.Worktrees, err = gitrepo.InspectWorktrees(a.ctx, a.root)
	if err != nil {
		// Worktree warnings are independent of committed candidate verification.
		g.WorktreeInspectionError = err.Error()
		g.Worktrees = []gitrepo.WorktreeStatus{}
	}
	for i := range g.Worktrees {
		w := &g.Worktrees[i]
		w.CommitIncluded = w.Commit == r.Base || slices.Contains(r.Inputs, w.Commit)
	}
	for i, ref := range refs {
		changed, e := a.branchChanges(baseSHA, r.Inputs[i])
		if e != nil {
			return a.fail(e)
		}
		g.Branches = append(g.Branches, gateBranch{Ref: ref, Commit: r.Inputs[i], Changed: changed})
	}
	g.attribute()
	rerun := gateArgs(o, baseRef, refs)
	switch {
	case r.Gate.Verdict == gate.Fail && o.verify:
		g.Next = "repair on the branches above, commit, then rerun: " + strings.TrimSpace("radar gate --run "+rerun)
	case r.Gate.Verdict == gate.Fail:
		g.Next = "repair on the branches above, commit, then rerun: " + strings.TrimSpace("radar gate "+rerun)
	case r.Gate.Verdict == gate.Blocked && staticGap(r) != "":
		// Running tests cannot settle a static check Radar could not read.
		g.Next = staticGap(r) + " could not be checked; resolve what its explanation names, then rerun: " + strings.TrimSpace("radar gate "+rerun)
	case r.Gate.Verdict == gate.Blocked && o.verify:
		g.Next = blockedNext(r, gateGaps(r), rerunWith(o, baseRef, refs), o)
	case configurationUnstable(configuration.Inputs):
		g.Next = "restore stable configuration, then rerun: " + strings.TrimSpace("radar gate "+rerun)
	case r.Gate.Verdict == gate.Error && missingRunner(r.Executions) != "":
		g.Next = "install " + missingRunner(r.Executions) + " where you run radar, then rerun: " + strings.TrimSpace("radar gate --run "+rerun)
	case r.Gate.Verdict == gate.Error:
		g.Next = "resolve the reported analysis or execution error and rerun the same gate."
	case !o.verify:
		g.Next = strings.TrimSpace("radar gate --run " + rerun)
	}
	recordVerdict(a.ctx, r.Gate.Verdict)
	if o.trailer {
		return a.emitGateTrailer(g, o.verify)
	}
	a.report(g, func(w io.Writer) { renderGate(w, g, o.verify) })
	if policy != nil || o.verify || configurationUnstable(configuration.Inputs) {
		return gate.Exit(r.Gate)
	}
	// Without --run no test evidence exists by construction; only a supported
	// failure (conflict, breaking contract) fails the static gate.
	if r.Gate.Verdict == gate.Fail {
		return 1
	}
	if r.Gate.Verdict == gate.Error {
		return 2
	}
	return 0
}

// staticChecks are required checks a person settles by editing files, not by
// running tests.
var staticChecks = map[string]bool{"no_breaking_contracts": true, "declared_contracts": true, "declared_contract_configuration": true, "migration_order": true, "no_symbol_conflicts": true, "lockfile_consistency": true, "unique_keys": true, "generated_drift": true}

// staticGap names the first required static check that neither passed nor
// failed, or "" when only other evidence (such as tests) is missing.
func staticGap(r integration.Report) string {
	for _, c := range r.Gate.Required {
		if staticChecks[c.ID] && c.Status != model.StatusPassed && c.Status != model.StatusFailed {
			return gateCheckName(c.ID)
		}
	}
	return ""
}

// explainGateError adds the next step to candidate errors a person can act
// on. The original message is kept so scripts matching it still work.
func (a *app) explainGateError(err error, baseSHA string) error {
	var entry *integration.UnsupportedEntryError
	if !errors.As(err, &entry) || entry.Unsafe != nil {
		return err
	}
	message := err.Error() + " (unsupported layout: Radar does not reproduce symlinks or submodules in its private copy, so nothing was checked)"
	entries, _ := gitrepo.Entries(a.ctx, a.root, baseSHA)
	// The same path as a regular file on the base means a branch made it a
	// symlink or submodule; only the same unsupported entry blames the base.
	if slices.ContainsFunc(entries, func(e gitrepo.TreeEntry) bool {
		return e.Path == entry.Path && (e.Mode == "120000" || e.Mode == "160000")
	}) {
		// On the base it is in every combination; no branch choice helps.
		return &workspace.Error{Message: message, Next: shellArg(entry.Path) + " is on the base, so Radar cannot check this repository until it is replaced there with a regular file"}
	}
	return &workspace.Error{Message: message, Next: "remove or replace " + shellArg(entry.Path) + " on the branch that adds it, or name only the branches that do not add it"}
}

// baseFlag records every --base value for workspace runs; the
// single-repository gate keeps using the last one.
type baseFlag struct{ o *options }

func (f baseFlag) String() string {
	if f.o == nil {
		return ""
	}
	return f.o.base
}
func (f baseFlag) Set(v string) error {
	f.o.bases = append(f.o.bases, v)
	f.o.base = v
	return nil
}

// gateBranches returns the explicit branches, or every worktree branch with
// commits beyond the base when none are named.
func (a *app) gateBranches(args []string, baseRef, baseSHA string) ([]string, []string, error) {
	refs := []string{}
	for _, arg := range args {
		// Commas are legal in Git branch names. Prefer the exact reference;
		// retain the historical comma-list shorthand only when it is not a ref.
		if _, err := gitrepo.Resolve(a.ctx, a.root, arg); err == nil {
			refs = append(refs, arg)
			continue
		}
		for _, ref := range strings.Split(arg, ",") {
			if ref = strings.TrimSpace(ref); ref != "" {
				refs = append(refs, ref)
			}
		}
	}
	if len(refs) > 0 {
		for _, ref := range refs {
			if _, err := gitrepo.Resolve(a.ctx, a.root, ref); err != nil {
				if a.ctx.Err() != nil {
					return nil, nil, a.ctx.Err()
				}
				next := "list branches with git branch --list, or run radar gate without names to combine every worktree branch"
				if strings.Contains(ref, ":") {
					next = "REPO:BRANCH names need a workspace (radar workspace add PATH); in one repository, name the branch alone"
				}
				return nil, nil, &workspace.Error{Message: fmt.Sprintf("branch %q not found: no branch, tag or commit by that name in this repository", ref), Next: next}
			}
		}
		return refs, nil, nil
	}
	worktrees, err := gitrepo.WorktreeBranches(a.ctx, a.root)
	if err != nil {
		return nil, nil, err
	}
	skipped := []string{}
	for _, ref := range worktrees {
		sha, e := gitrepo.Resolve(a.ctx, a.root, ref)
		if e != nil || ref == baseRef || strings.TrimPrefix(baseRef, "origin/") == ref {
			continue
		}
		if mb, e := gitrepo.MergeBase(a.ctx, a.root, baseSHA, sha); e == nil && mb == sha {
			skipped = append(skipped, ref+" (no commits beyond "+baseRef+")")
			continue
		}
		refs = append(refs, ref)
	}
	if len(refs) == 0 {
		return nil, nil, &workspace.Error{Message: fmt.Sprintf("no branches to combine: no worktree branch has commits beyond %s", baseRef), Next: "commit work on an agent branch, or name branches: radar gate feature/a feature/b (radar doctor shows what Radar sees)"}
	}
	return refs, skipped, nil
}

func (a *app) branchChanges(baseSHA, sha string) ([]string, error) {
	mb, err := gitrepo.MergeBase(a.ctx, a.root, baseSHA, sha)
	if err != nil {
		return nil, gitrepo.ExplainShallow(a.ctx, a.root, err)
	}
	return gitrepo.ChangedFiles(a.ctx, a.root, mb, sha)
}

// attribute links each finding to the branches whose changes it points at:
// paths the finding names, or, for failing tests, the changed files those
// tests are or import (inferred, so a lead rather than proof).
func (g *gateReport) attribute() {
	r := g.Report
	testFiles := map[string][]string{}
	if r.Selection != nil {
		for _, c := range r.Selection.Commands {
			testFiles[c.ID] = c.TestFiles
		}
	}
	for _, f := range r.Findings {
		paths, basis := []string{}, "files named by the finding"
		for _, l := range f.Locations {
			paths = append(paths, l.Path)
		}
		for _, p := range []string{f.Producer, f.Consumer} {
			if p != "" {
				paths = append(paths, p, model.PathFromID(p))
			}
		}
		if f.Code == "integration_base_failure" {
			continue
		}
		if f.Code == "integration_textual_conflict" {
			paths = append(paths, r.Conflicts...)
		}
		if strings.HasPrefix(f.Code, "integration_execution_") {
			environment := false
			for _, ev := range r.Executions {
				// integration names the command and directory exactly this way.
				if strings.Contains(f.Explanation, fmt.Sprintf("%q in %q", ev.Command, ev.CWD)) {
					paths = append(paths, ev.FailurePaths(testFiles[ev.SelectionID])...)
					environment = environment || ev.Observation.Diagnosis.Environment()
				}
			}
			// A missing runner says nothing about which branch is at fault.
			if environment {
				continue
			}
			reached := []string{}
			for _, p := range paths {
				reached = append(reached, r.AffectedBy[p]...)
			}
			paths = append(paths, reached...)
			basis = "changed files the failing tests are or import (inferred from static imports; runtime reads are not traced)"
			if observed := r.ObservedAttribution(f.ID); observed != nil {
				if a, ok := g.observedAttribution(*observed, paths); ok {
					g.Attribution[f.ID] = a
					continue
				}
				// Inconclusive: keep the inferred lead and show what was observed.
				if leads := g.leads(paths); len(leads) > 0 {
					g.Attribution[f.ID] = gateAttribution{Basis: basis, Leads: leads, Observed: observed}
				} else {
					g.Attribution[f.ID] = gateAttribution{Basis: basis, Leads: []gateLead{}, Observed: observed}
				}
				continue
			}
		}
		if leads := g.leads(paths); len(leads) > 0 {
			g.Attribution[f.ID] = gateAttribution{Basis: basis, Leads: leads}
		}
	}
}

// observedAttribution turns a conclusive observation into a lead: the
// branches whose combination reproduced the failure, with the changed files
// the failing tests reach as a hint. A failure already on the base names no
// branch. An inconclusive observation returns false.
func (g gateReport) observedAttribution(o integration.Attribution, paths []string) (gateAttribution, bool) {
	switch o.Outcome {
	case integration.AttributionBase:
		return gateAttribution{Basis: "observed: the same command fails the same way on the base without any branch (each run observed once)", Leads: []gateLead{}, Observed: &o}, true
	case integration.AttributionIsolated:
	default:
		return gateAttribution{}, false
	}
	leads := []gateLead{}
	for _, set := range o.Reproducing {
		for _, i := range set {
			if i < 0 || i >= len(g.Branches) || slices.ContainsFunc(leads, func(l gateLead) bool { return l.Branch == g.Branches[i].Ref }) {
				continue
			}
			files := []string{}
			for _, p := range paths {
				if p != "" && contains(g.Branches[i].Changed, p) && !slices.Contains(files, p) {
					files = append(files, p)
				}
			}
			sort.Strings(files)
			leads = append(leads, gateLead{Branch: g.Branches[i].Ref, Files: files})
		}
	}
	return gateAttribution{Basis: "observed: the failure reproduces with these branches combined and every smaller combination ran and passed (each run observed once)", Leads: leads, Observed: &o}, true
}

// leads groups the paths each branch changed.
func (g gateReport) leads(paths []string) []gateLead {
	out := []gateLead{}
	for _, b := range g.Branches {
		files := []string{}
		for _, p := range paths {
			if p != "" && contains(b.Changed, p) && !contains(files, p) {
				files = append(files, p)
				sort.Strings(files)
			}
		}
		if len(files) > 0 {
			out = append(out, gateLead{Branch: b.Ref, Files: files})
		}
	}
	return out
}

func contains(sorted []string, p string) bool {
	i := sort.SearchStrings(sorted, p)
	return i < len(sorted) && sorted[i] == p
}

// gateArgs reproduces the selection; auto-detected branches stay implicit.
func gateArgs(o options, baseRef string, refs []string) string {
	args := []string{}
	if o.base != "" {
		args = append(args, "--base "+shellArg(baseRef))
	}
	if o.policy != "" {
		args = append(args, "--policy "+shellArg(o.policy))
	}
	if o.plan != "" {
		args = append(args, "--plan "+shellArg(o.plan))
	}
	if o.suite != "" {
		args = append(args, "--suite "+shellArg(o.suite))
	}
	if o.maxCommands != 0 && o.maxCommands != testselection.DefaultMaxCommands {
		args = append(args, fmt.Sprintf("--max-commands %d", o.maxCommands))
	}
	if o.timeout != 10*time.Minute {
		args = append(args, "--timeout "+o.timeout.String())
	}
	if o.observeCoverage {
		args = append(args, "--observe-coverage")
	}
	if o.verify && o.attributionRuns != 0 {
		args = append(args, fmt.Sprintf("--attribution-runs %d", o.attributionRuns))
	}
	if len(o.args) > 0 {
		for _, ref := range refs {
			args = append(args, shellArg(ref))
		}
	}
	return strings.Join(args, " ")
}

// rerunWith renders `radar gate --run` for this selection after adjusting a
// copy of the invocation's options.
func rerunWith(o options, baseRef string, refs []string) func(func(*options)) string {
	return func(change func(*options)) string {
		c := o
		change(&c)
		return strings.TrimSpace("radar gate --run " + gateArgs(c, baseRef, refs))
	}
}

// Suggested commands are presentation only, but remain safe to copy into a
// POSIX shell even when a valid Git ref contains shell metacharacters.
func shellArg(s string) string { return workspace.ShellPath(s) }

var verdictMark = map[gate.Verdict]string{gate.Pass: "PASS", gate.Fail: "FAIL", gate.Blocked: "NOT VERIFIED", gate.Error: "ERROR"}

func renderGate(w io.Writer, g gateReport, ran bool) {
	p := paletteOf(w)
	r := g.Report
	title := g.title
	if title == "" {
		title = "Radar gate: "
	}
	fmt.Fprintf(w, "%s%s — %s\n", brand(p, title), p.verdict(r.Gate.Verdict, verdictText(r.Gate.Verdict, ran)), gateHeadline(g, ran))
	fmt.Fprintln(w, p.dim(fmt.Sprintf("%s onto %s @ %s", plural(len(g.Branches), "branch", "branches"), g.BaseRef, short(r.Base))))
	fmt.Fprintln(w)
	width := 0
	for _, b := range g.Branches {
		width = max(width, len(b.Ref))
	}
	for _, b := range g.Branches {
		changed := plural(len(b.Changed), "file", "files") + " changed"
		if b.changedUnknown {
			changed = "changed files unavailable"
		}
		fmt.Fprintf(w, "  %s%s  %s  %s\n", p.cyan(b.Ref), strings.Repeat(" ", width-len(b.Ref)), p.dim(short(b.Commit)), changed)
	}
	for _, s := range g.Skipped {
		fmt.Fprintf(w, "  %s skipped: %s\n", p.mark("·"), s)
	}
	if g.WorktreeInspectionError != "" {
		fmt.Fprintf(w, "  %s worktree inspection unavailable: %s. Candidate verification is unchanged; inspect worktrees manually for excluded uncommitted changes.\n", p.mark("!"), g.WorktreeInspectionError)
	}
	for _, wt := range g.Worktrees {
		if wt.InspectionError != "" {
			fmt.Fprintf(w, "  %s worktree %q: status unavailable: %s\n", p.mark("!"), wt.Path, wt.InspectionError)
		} else if len(wt.Staged)+len(wt.Unstaged)+len(wt.Untracked) > 0 {
			fmt.Fprintf(w, "  %s worktree %q (%s): %d staged, %d unstaged, %d untracked; uncommitted changes EXCLUDED. Commit intended changes and rerun.\n", p.mark("!"), wt.Path, wt.Branch, len(wt.Staged), len(wt.Unstaged), len(wt.Untracked))
		}
		if wt.Detached {
			fmt.Fprintf(w, "  %s detached worktree %q @ %s: commit included=%t; name its commit explicitly to include it.\n", p.mark("!"), wt.Path, short(wt.Commit), wt.CommitIncluded)
		}
	}

	// Required checks, phrased for people; check IDs stay in --json.
	type row struct{ mark, name, text string }
	rows := []row{}
	for _, c := range r.Gate.Required {
		if contractsUndeclared(c, r) {
			// Nothing was declared, so nothing was checked: not a green check.
			rows = append(rows, row{"·", gateCheckName(c.ID), "none declared, so no contract was checked (radar discover proposes candidates)"})
			continue
		}
		rows = append(rows, row{gateMark(c.Status), gateCheckName(c.ID), gateLabel(c, r)})
	}
	if !ran && len(r.Conflicts) == 0 {
		rows = append(rows, row{"?", "Tests", "combined tree not tested yet (add --run)"})
	}
	nameWidth := 0
	for _, x := range rows {
		nameWidth = max(nameWidth, len(x.name))
	}
	fmt.Fprintln(w)
	checked, unchecked := []row{}, []row{}
	for _, x := range rows {
		if x.mark == "✓" || x.mark == "✗" {
			checked = append(checked, x)
		} else {
			unchecked = append(unchecked, x)
		}
	}
	fmt.Fprintln(w, p.bold(fmt.Sprintf("Checked %d", len(checked))))
	for _, x := range checked {
		fmt.Fprintf(w, "  %s %s%s  %s\n", p.mark(x.mark), p.bold(x.name), strings.Repeat(" ", nameWidth-len(x.name)), x.text)
	}
	if len(unchecked) > 0 {
		fmt.Fprintln(w, p.bold(fmt.Sprintf("Couldn't check %d", len(unchecked))))
		for _, x := range unchecked {
			fmt.Fprintf(w, "  %s %s%s  %s\n", p.mark(x.mark), p.bold(x.name), strings.Repeat(" ", nameWidth-len(x.name)), x.text)
		}
	}
	fmt.Fprintf(w, "  %s\n", p.dim(fmt.Sprintf("policy %s requires %s", r.Gate.Policy.Name, strings.Join(r.Gate.Policy.Require, ", "))))

	problems := 0
	for _, f := range r.Findings {
		if f.Severity != model.SeverityError {
			continue
		}
		if problems++; problems == 1 {
			fmt.Fprintln(w, "\n"+p.bold("Problems"))
		}
		explanation, cases, _ := strings.Cut(readableExplanation(f.Explanation, r.Executions, p), " Failed cases: ")
		explanation, cause, _ := strings.Cut(explanation, " Cause: ")
		fmt.Fprintf(w, "  %s %s\n", p.mark("✗"), p.paint("1;31", f.Code))
		fmt.Fprintf(w, "    %s\n", explanation)
		if cause != "" {
			fmt.Fprintf(w, "    cause: %s\n", p.yellow(strings.TrimSuffix(cause, ".")))
		}
		if cases != "" {
			fmt.Fprintf(w, "    failed cases: %s\n", p.red(cases))
		}
		if a, ok := g.Attribution[f.ID]; ok {
			renderAttribution(w, g, a, f.Code, p)
		}
		if repro := g.reproduce(f); repro != "" {
			fmt.Fprintf(w, "    reproduce: %s\n", p.cyan(repro))
		}
		if len(r.Conflicts) > 0 && f.Code == "integration_textual_conflict" {
			fmt.Fprintf(w, "    paths: %s\n", strings.Join(r.Conflicts, ", "))
		}
		if f.Remediation != "" {
			fmt.Fprintf(w, "    fix: %s\n", f.Remediation)
		}
	}
	warnings := 0
	for _, f := range r.Findings {
		if f.Severity != model.SeverityWarning {
			continue
		}
		if warnings++; warnings == 1 {
			fmt.Fprintln(w, "\n"+p.bold("Warnings"))
		}
		if warnings <= 5 {
			fmt.Fprintf(w, "  %s %s: %s\n", p.mark("!"), p.yellow(f.Code), readableExplanation(f.Explanation, r.Executions, p))
			if a, ok := g.Attribution[f.ID]; ok {
				fmt.Fprintf(w, "    look at: %s\n", renderLeads(a.Leads, p))
			}
		}
	}
	if warnings > 5 {
		fmt.Fprintf(w, "  … %s; see --json\n", plural(warnings-5, "more warning", "more warnings"))
	}
	if !ran && r.VerificationProposal != nil && len(r.VerificationProposal.Commands) > 0 {
		fmt.Fprintf(w, "\n%s %s\n", p.bold("Suggested tests"), p.dim("(static relationships; not run)"))
		for i, c := range r.VerificationProposal.Commands {
			if i == 5 {
				fmt.Fprintf(w, "  … %d more\n", len(r.VerificationProposal.Commands)-5)
				break
			}
			fmt.Fprintf(w, "  %s  %s\n", p.cyan(displayCommand(c.Command)), p.dim("(in "+c.CWD+")"))
		}
	}
	if ran && r.Selection != nil {
		fmt.Fprintf(w, "\n%s ran %d of %s\n", p.bold("Tests ("+r.Selection.Mode+"):"), len(r.Executions), plural(len(r.Selection.Commands), "selected command", "selected commands"))
		for _, ev := range r.Executions {
			fmt.Fprintf(w, "  %s %s  %s\n", p.mark(gateMark(ev.IntegrationStatus())), p.cyan(displayCommand(ev.Command)), p.dim(executionDetail(ev)))
		}
		renderGaps(w, gateGaps(r), p)
		fmt.Fprintf(w, "  %s\n", p.dim(fmt.Sprintf("Test files: %d inventoried, %d selected (relationships are not behavioral coverage)", r.Selection.Inventory, r.Selection.TestFiles)))
		if len(r.Selection.Uncovered) > 0 {
			fmt.Fprintf(w, "  %s Uncovered changes (no established test relationship): %s\n", p.mark("?"), strings.Join(r.Selection.Uncovered, ", "))
			fmt.Fprintln(w, "    Resolve with supported tests, review --suite full, or explicitly select a limited --policy.")
		}
		if !slices.Contains(r.Gate.Policy.Require, "test_selection") || !slices.Contains(r.Gate.Policy.Require, "integration_execution") {
			fmt.Fprintf(w, "  %s Limited policy: test selection or combined execution is not required; PASS applies only to the named requirements.\n", p.mark("!"))
		}
	}
	if g.Next != "" {
		fmt.Fprintf(w, "\n%s %s\n", p.bold("Next:"), p.next(g.Next))
	}
	details := g.details
	if details == "" {
		details = "Details: add --json. Radar never touches your branches; the combination is built in private Git state."
	}
	fmt.Fprintln(w, p.dim(details))
}

// reproduce is a copyable command that rebuilds the combination in a
// throwaway detached worktree, merging the branches in Radar's order, and
// runs the failing command there. It leaves the user's checkout and branches
// alone. Only failed commands get one: an environment problem would not
// reproduce from source.
func (g gateReport) reproduce(f model.Finding) string {
	if f.Code != "integration_execution_failed" || g.snapshot || len(g.Branches) == 0 || len(g.Branches) != len(g.Inputs) || g.Base == "" {
		return ""
	}
	for i, b := range g.Branches {
		if b.Commit != g.Inputs[i] {
			return ""
		}
	}
	for _, ev := range g.Executions {
		if ev.Status != model.StatusFailed || !strings.Contains(f.Explanation, fmt.Sprintf("%q in %q", ev.Command, ev.CWD)) {
			continue
		}
		// Plan-declared commands carry setup, env and report steps that one
		// shell line would leave out, so it could reproduce a different run.
		if g.Selection != nil && slices.ContainsFunc(g.Selection.Commands, func(c testselection.Command) bool {
			return c.ID == ev.SelectionID && c.Framework == "plan-declared"
		}) {
			return ""
		}
		// A fresh directory outside the repository, so repeated use and runs
		// from a subdirectory never collide with or dirty the checkout.
		parts := []string{`d=$(mktemp -d)`, `git worktree add --detach "$d" ` + short(g.Base), `cd "$d"`}
		for _, b := range g.Branches {
			// Exact commits (a branch may have moved since this check), and
			// no commit hooks, as in Radar's private merge. post-checkout and
			// post-merge hooks and the user's Git configuration still apply.
			parts = append(parts, "git merge --no-ff --no-edit --no-verify "+short(b.Commit))
		}
		if ev.CWD != "" && ev.CWD != "." {
			parts = append(parts, "cd "+shellArg(ev.CWD))
		}
		// The full argv: displayCommand elides long commands, which would not run.
		argv := make([]string, len(ev.Command))
		for i, a := range ev.Command {
			argv[i] = shellArg(a)
		}
		return strings.Join(append(parts, strings.Join(argv, " ")), " && ")
	}
	return ""
}

// brand prefixes the report's first line with Radar's mark on a terminal.
func brand(p palette, title string) string {
	if !p.on {
		return title
	}
	return p.paint("1;32", "◉ ") + p.bold(title)
}

// documentationOnly reports a run in which only documentation changed, so
// selection proposed no test and none ran. It explains a NOT VERIFIED result
// to people; it is not evidence that no test reads the documents.
// docsOnlyBlock reports a default-policy block whose only unmet requirement
// is test execution on a documentation-only change, so the static gate is
// a complete answer for what Radar can verify.
func docsOnlyBlock(r integration.Report) bool {
	if !documentationOnly(r) || r.Gate.Policy.Name != "supported-integration" {
		return false
	}
	for _, c := range r.Gate.Required {
		if c.Status != model.StatusPassed && c.ID != "integration_execution" && c.ID != "test_selection" {
			return false
		}
	}
	return true
}

func documentationOnly(r integration.Report) bool {
	if r.Selection == nil || len(r.Selection.Commands) > 0 || len(r.Selection.Blocking) > 0 || len(r.Selection.Uncovered) > 0 || len(r.Changed) == 0 {
		return false
	}
	for _, f := range r.Changed {
		if !testselection.Documentation(f) {
			return false
		}
	}
	return true
}

func verdictText(v gate.Verdict, ran bool) string {
	switch {
	case v == gate.Pass && !ran:
		return "PASS (static)"
	case v == gate.Pass:
		return "PASS (selected policy; bounded verification)"
	}
	return verdictMark[v]
}

// gateHeadline says in a few words why the verdict is what it is.
func gateHeadline(g gateReport, ran bool) string {
	r := g.Report
	reasons := []string{}
	for _, c := range r.Gate.Required {
		switch {
		case r.Gate.Verdict == gate.Fail && c.Status == model.StatusFailed:
			reasons = append(reasons, gateLabel(c, r))
		case r.Gate.Verdict == gate.Error && (c.Status == model.StatusError || c.Status == model.StatusTimeout):
			reasons = append(reasons, strings.ToLower(gateCheckName(c.ID)))
		case r.Gate.Verdict == gate.Blocked && c.Status != model.StatusPassed:
			state := "not verified"
			if c.Status == model.StatusIncomplete {
				state = "incomplete"
			}
			reasons = append(reasons, strings.ToLower(gateCheckName(c.ID))+" "+state)
		}
	}
	switch r.Gate.Verdict {
	case gate.Fail:
		if len(reasons) > 0 {
			return strings.Join(reasons, "; ")
		}
	case gate.Blocked:
		if ran && docsOnlyBlock(r) {
			return "only documentation changed, so no test was selected and none ran"
		}
		if len(reasons) > 0 {
			return "required evidence is missing: " + strings.Join(reasons, ", ")
		}
	case gate.Error:
		if names := missingRunner(r.Executions); names != "" {
			return "the tests could not run; not installed: " + names
		}
		if len(reasons) > 0 {
			return "could not complete: " + strings.Join(reasons, ", ") + " (see Problems)"
		}
	case gate.Pass:
		if !ran {
			return "static checks passed; tests not run yet"
		}
		return "every required check passed"
	}
	return firstSentence(r.Gate.Explanation)
}

// displayCommand renders argv as a copyable POSIX shell line, eliding
// trailing arguments only when the line would be very long.
func displayCommand(argv []string) string {
	const limit = 96
	parts, length := []string{}, 0
	for i, a := range argv {
		q := shellArg(a)
		if length+len(q) > limit && i > 0 {
			return strings.Join(parts, " ") + fmt.Sprintf(" … (+%d args)", len(argv)-i)
		}
		parts = append(parts, q)
		length += len(q) + 1
	}
	return strings.Join(parts, " ")
}

// readableExplanation replaces the Go-quoted argv that integration findings
// carry ("[\"python3\" \"-m\" …] in \".\"") with a copyable shell line.
func readableExplanation(text string, executions []integration.ExecutionEvidence, p palette) string {
	for _, ev := range executions {
		quoted := fmt.Sprintf("%q in %q", ev.Command, ev.CWD)
		if strings.Contains(text, quoted) {
			text = strings.Replace(text, quoted, p.cyan(displayCommand(ev.Command))+" (in "+ev.CWD+")", 1)
		}
	}
	return text
}

// missingRunner names the runners or programs the selected commands could
// not find, when that is why they produced no test result.
func missingRunner(executions []integration.ExecutionEvidence) string {
	names := []string{}
	for _, ev := range executions {
		if d := ev.Observation.Diagnosis; d != nil && (d.Kind == "runner_missing" || d.Kind == "command_missing") && !slices.Contains(names, d.Name) {
			names = append(names, d.Name)
		}
	}
	return strings.Join(names, ", ")
}

func renderLeads(leads []gateLead, p palette) string {
	out := []string{}
	for _, l := range leads {
		if len(l.Files) == 0 {
			out = append(out, p.cyan(l.Branch))
			continue
		}
		out = append(out, fmt.Sprintf("%s (%s)", p.cyan(l.Branch), strings.Join(shorten(l.Files, 3), ", ")))
	}
	return strings.Join(out, "; ")
}

// renderAttribution prints a problem's lead: observed reruns when they were
// conclusive, otherwise the static lead and what the reruns did show.
func renderAttribution(w io.Writer, g gateReport, a gateAttribution, code string, p palette) {
	o := a.Observed
	runs := ""
	if o != nil {
		runs = plural(len(o.Runs), "rerun", "reruns")
	}
	switch {
	case o != nil && o.Outcome == integration.AttributionBase:
		fmt.Fprintf(w, "    observed: already fails on %s without any branch, so these branches did not cause it (%s)\n", g.BaseRef, runs)
		return
	case o != nil && o.Outcome == integration.AttributionIsolated:
		alone := ""
		if len(g.Branches) > 1 && !slices.ContainsFunc(o.Reproducing, func(set []int) bool { return len(set) > 1 }) {
			alone = " alone"
		}
		fmt.Fprintf(w, "    observed: fails with %s%s; %s (%s)\n", g.subsetNames(o.Reproducing, p), alone, smallerPassed(o), runs)
		if hint := renderLeads(withFiles(a.Leads), p); hint != "" {
			fmt.Fprintf(w, "    look at: %s\n", hint)
		}
		return
	}
	if len(a.Leads) > 0 {
		label := "look at"
		if strings.HasPrefix(code, "integration_execution_") {
			label = "look at (import-based lead)"
		}
		fmt.Fprintf(w, "    %s: %s\n", label, renderLeads(a.Leads, p))
	}
	if o != nil {
		if len(o.Reproducing) > 0 {
			fmt.Fprintf(w, "    observed: reproduced with %s, but not shown minimal: %s (%s)\n", g.subsetNames(o.Reproducing, p), notComparable(o), runs)
		} else {
			fmt.Fprintf(w, "    observed: no smaller combination tried reproduced it before the budget ran out (%s; --attribution-runs)\n", runs)
		}
	}
}

// subsetNames renders branch subsets as "a + b; c".
func (g gateReport) subsetNames(sets [][]int, p palette) string {
	out := []string{}
	for _, set := range sets {
		names := []string{}
		for _, i := range set {
			if i >= 0 && i < len(g.Branches) {
				names = append(names, p.cyan(g.Branches[i].Ref))
			}
		}
		out = append(out, strings.Join(names, " + "))
	}
	return strings.Join(out, "; ")
}

func smallerPassed(o *integration.Attribution) string {
	for _, set := range o.Reproducing {
		if len(set) > 1 {
			return "each smaller combination passes"
		}
	}
	if len(o.Runs) > 0 && len(o.Runs[0].Branches) == 0 {
		return "passes on the base alone"
	}
	return "every smaller combination passes"
}

// notComparable names why some subset runs could not be compared.
func notComparable(o *integration.Attribution) string {
	n := 0
	for _, run := range o.Runs {
		if run.Result != integration.SubsetPassed && run.Result != integration.SubsetReproduced {
			n++
		}
	}
	return plural(n, "smaller combination", "smaller combinations") + " failed differently or could not run"
}

func withFiles(leads []gateLead) []gateLead {
	out := []gateLead{}
	for _, l := range leads {
		if len(l.Files) > 0 {
			out = append(out, l)
		}
	}
	return out
}

func gateMark(s model.Status) string {
	switch s {
	case model.StatusPassed:
		return "✓"
	case model.StatusFailed, model.StatusError, model.StatusTimeout:
		return "✗"
	}
	return "?"
}

func executionDetail(ev integration.ExecutionEvidence) string {
	detail := fmt.Sprintf("in %s; %s", ev.CWD, plural(ev.Observation.TestsRun, "recognized test", "recognized tests"))
	if ev.BaseComparison != nil {
		if ev.BaseComparison.AllExisting {
			detail += "; already fails on base; integration not verified"
		} else if n := len(ev.BaseComparison.ExistingCases); n > 0 {
			detail += fmt.Sprintf("; %d failed cases already fail on base; %d remaining candidate failures", n, len(ev.NewFailedCases()))
		}
	}
	return "(" + detail + ")"
}

// gateCheckName names a required check for people; IDs stay in --json.
func gateCheckName(id string) string {
	switch id {
	case "textual_merge":
		return "Merge"
	case "no_breaking_contracts":
		return "Contracts"
	case "integration_execution":
		return "Tests"
	case "test_selection":
		return "Test selection"
	case "migration_order":
		return "Migrations"
	case "no_symbol_conflicts", "symbol_conflicts":
		return "Symbols"
	case "lockfile_consistency":
		return "Lockfiles"
	case "unique_keys":
		return "Unique keys"
	case "generated_drift":
		return "Generated files"
	}
	return id
}

// contractsUndeclared reports a contract requirement that passed only
// because the repository declares no contract and the base had no obligation.
func contractsUndeclared(c gate.Check, r integration.Report) bool {
	return c.ID == "no_breaking_contracts" && c.Status == model.StatusPassed && r.DeclaredBindings == 0 && len(r.ContractObligations) == 0
}

// gateLabel phrases a required check's result.
func gateLabel(c gate.Check, r integration.Report) string {
	labels := map[string][2]string{
		"textual_merge":         {"branches merge without conflicts", "branches conflict"},
		"no_breaking_contracts": {"no breaking contract change found", "breaking contract change"},
		"integration_execution": {"selected tests pass on the combined tree", "tests on the combined tree did not pass"},
		"test_selection":        {"complete (no uncovered changes)", "test selection failed"},
		"migration_order":       {"migration history stays in order on the combined tree", "migration order conflict on the combined tree"},
		"no_symbol_conflicts":   {"supported declarations and named references stay consistent", "symbol conflict on the combined tree"},
		"lockfile_consistency":  {"direct dependencies are recorded in supported lockfiles", "manifest and lockfile disagree"},
		"unique_keys":           {"supported route and declared keys stay unique", "duplicate unique key"},
		"generated_drift":       {"declared generated outputs match", "declared generated outputs drift"},
	}
	l, ok := labels[c.ID]
	switch {
	case c.ID == "integration_execution" && c.Status == model.StatusUnknown && documentationOnly(r):
		return "not verified — only documentation changed, so no test was selected and none ran"
	case ok && c.Status == model.StatusPassed:
		return l[0]
	case ok && c.Status == model.StatusFailed:
		if c.ID == "textual_merge" && len(r.Conflicts) > 0 {
			return l[1] + " in " + strings.Join(shorten(r.Conflicts, 4), ", ")
		}
		if c.ID == "integration_execution" && len(r.Executions) > 0 {
			failed := 0
			for _, ev := range r.Executions {
				if gateMark(ev.IntegrationStatus()) == "✗" {
					failed++
				}
			}
			return fmt.Sprintf("%d of %s failed on the combined tree", failed, plural(len(r.Executions), "test command", "test commands"))
		}
		return l[1]
	case c.ID == "test_selection" && r.Selection != nil && c.Status == model.StatusIncomplete:
		gaps := []string{}
		if n := len(r.Selection.Uncovered); n > 0 {
			gaps = append(gaps, plural(n, "changed file has", "changed files have")+" no related test")
		}
		if n := len(gateGaps(r)); n > 0 && len(r.Selection.Blocking) > 0 {
			gaps = append(gaps, plural(n, "blocking gap", "blocking gaps")+" (see Tests below)")
		}
		if len(gaps) > 0 {
			return "incomplete — " + strings.Join(gaps, "; ")
		}
	case c.ID == "integration_execution" && r.Selection != nil && c.Status == model.StatusIncomplete:
		existing := 0
		for _, ev := range r.Executions {
			if ev.Status == model.StatusFailed && ev.IntegrationStatus() == model.StatusIncomplete {
				existing++
			}
		}
		if existing > 0 {
			return fmt.Sprintf("incomplete — %d of %s already fail on base; integration not verified", existing, plural(len(r.Executions), "test command", "test commands"))
		}
		return fmt.Sprintf("incomplete — ran %d of %s; required verification has gaps", len(r.Executions), plural(len(r.Selection.Commands), "selected command", "selected commands"))
	}
	if !ok {
		return string(c.Status) + ": " + c.Explanation
	}
	return "not verified (" + string(c.Status) + ") — " + c.Explanation
}
