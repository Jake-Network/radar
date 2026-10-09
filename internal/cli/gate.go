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
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/testselection"
)

// gateBranch records what one input branch changed relative to its merge base
// with the integration base, so findings can name the branches involved.
type gateBranch struct {
	Ref     string   `json:"ref"`
	Commit  string   `json:"commit"`
	Changed []string `json:"changed"`
}

// gateLead names a branch whose changed files a finding points at.
type gateLead struct {
	Branch string   `json:"branch"`
	Files  []string `json:"files"`
}

// gateAttribution is a lead for repair, not a verdict of blame: Basis says
// whether the files were named by the finding or reached through imports.
type gateAttribution struct {
	Basis string     `json:"basis"`
	Leads []gateLead `json:"leads"`
}

// gateReport is merge-check's report plus branch attribution and the single
// next command a person or agent should run.
type gateReport struct {
	WorktreeInspectionError string                     `json:"worktree_inspection_error,omitempty"`
	Worktrees               []gitrepo.WorktreeStatus   `json:"worktrees"`
	Configuration           []ConfigurationInput       `json:"configuration_inputs,omitempty"`
	BaseRef                 string                     `json:"base_ref"`
	Branches                []gateBranch               `json:"branches"`
	Skipped                 []string                   `json:"skipped_branches,omitempty"`
	Attribution             map[string]gateAttribution `json:"attribution"`
	Next                    string                     `json:"next,omitempty"`
	integration.Report
}

func gateCommand() command {
	return command{name: "gate", usage: "radar gate [BRANCH ...] [--base REF] [--run [--suite targeted|balanced|full] [--timeout 10m]] [--policy PATH] [--plan PATH]",
		summary: "Combine branches (default: every worktree branch) and tell whether they integrate; --run also tests the combined tree.",
		flags: func(fs *flag.FlagSet, o *options) {
			fs.StringVar(&o.base, "base", "", "integration base (default: origin/HEAD's branch, main, master or trunk)")
			fs.BoolVar(&o.verify, "run", false, "execute the selected tests on the combined tree with your host permissions (not an OS sandbox)")
			fs.StringVar(&o.suite, "suite", "", "test selection with --run: targeted, balanced (default) or full")
			fs.IntVar(&o.maxCommands, "max-commands", testselection.DefaultMaxCommands, "maximum grouped test commands --run executes")
			fs.DurationVar(&o.timeout, "timeout", 10*time.Minute, "total --run time budget, maximum 30m")
			policyFlag(fs, o)
			planFlag(fs, o)
		}, run: (*app).gate}
}

func (a *app) gate(o options) int {
	if o.suite != "" && !o.verify {
		return a.fail(errors.New("--suite selects what --run executes; add --run or use --suite only with it"))
	}
	if e := a.repository(); e != nil {
		return a.fail(e)
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
	if err != nil {
		return a.fail(err)
	}
	plan := configuration.Plan
	options := integration.Options{Base: baseSHA, Branches: refs, Policy: policy, Plan: plan, SuggestTests: true, MaxCommands: o.maxCommands, Timeout: o.timeout}
	if o.verify {
		options.Verify, options.AllowExecution = true, true
		options.Suite = o.suite
		if options.Suite == "" {
			options.Suite = testselection.ModeBalanced
		}
	}
	r, err := integration.Preview(a.ctx, a.root, options)
	if err != nil {
		return a.fail(err)
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
	case r.Gate.Verdict == gate.Blocked && o.verify:
		g.Next = "resolve the missing evidence above, then rerun: " + strings.TrimSpace("radar gate --run "+rerun)
	case configurationUnstable(configuration.Inputs):
		g.Next = "restore stable configuration, then rerun: " + strings.TrimSpace("radar gate "+rerun)
	case r.Gate.Verdict == gate.Error:
		g.Next = "resolve the reported analysis or execution error and rerun the same gate."
	case !o.verify:
		g.Next = strings.TrimSpace("radar gate --run " + rerun)
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
		return nil, nil, fmt.Errorf("no branches to combine: no worktree branch has commits beyond %s; name them, e.g. radar gate feature/a feature/b", baseRef)
	}
	return refs, skipped, nil
}

func (a *app) branchChanges(baseSHA, sha string) ([]string, error) {
	mb, err := gitrepo.MergeBase(a.ctx, a.root, baseSHA, sha)
	if err != nil {
		return nil, err
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
		if f.Code == "integration_textual_conflict" {
			paths = append(paths, r.Conflicts...)
		}
		if strings.HasPrefix(f.Code, "integration_execution_") {
			for _, ev := range r.Executions {
				// integration names the command and directory exactly this way.
				if strings.Contains(f.Explanation, fmt.Sprintf("%q in %q", ev.Command, ev.CWD)) {
					paths = append(paths, testFiles[ev.SelectionID]...)
				}
			}
			reached := []string{}
			for _, p := range paths {
				reached = append(reached, r.AffectedBy[p]...)
			}
			paths = append(paths, reached...)
			basis = "changed files the failing tests are or import (inferred from static imports; runtime reads are not traced)"
		}
		if leads := g.leads(paths); len(leads) > 0 {
			g.Attribution[f.ID] = gateAttribution{Basis: basis, Leads: leads}
		}
	}
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
	if len(o.args) > 0 {
		for _, ref := range refs {
			args = append(args, shellArg(ref))
		}
	}
	return strings.Join(args, " ")
}

// Suggested commands are presentation only, but remain safe to copy into a
// POSIX shell even when a valid Git ref contains shell metacharacters.
func shellArg(s string) string {
	safe := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_./:@%+=,-", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

var verdictMark = map[gate.Verdict]string{gate.Pass: "PASS", gate.Fail: "FAIL", gate.Blocked: "NOT VERIFIED", gate.Error: "ERROR"}

func renderGate(w io.Writer, g gateReport, ran bool) {
	r := g.Report
	verdict := verdictMark[r.Gate.Verdict]
	if !ran && r.Gate.Verdict == gate.Pass {
		verdict = "PASS (static)"
	}
	if ran && r.Gate.Verdict == gate.Pass {
		verdict = "PASS (selected policy; bounded verification)"
	}
	fmt.Fprintf(w, "Radar gate: %s — %d branch(es) onto %s @ %s\n\n", verdict, len(g.Branches), g.BaseRef, short(r.Base))
	width := 0
	for _, b := range g.Branches {
		width = max(width, len(b.Ref))
	}
	for _, b := range g.Branches {
		fmt.Fprintf(w, "  %-*s  %s  %d file(s) changed\n", width, b.Ref, short(b.Commit), len(b.Changed))
	}
	for _, s := range g.Skipped {
		fmt.Fprintf(w, "  skipped: %s\n", s)
	}
	if g.WorktreeInspectionError != "" {
		fmt.Fprintf(w, "  ! worktree inspection unavailable: %s. Candidate verification is unchanged; inspect worktrees manually for excluded uncommitted changes.\n", g.WorktreeInspectionError)
	}
	for _, wt := range g.Worktrees {
		if wt.InspectionError != "" {
			fmt.Fprintf(w, "  ! worktree %q: status unavailable: %s\n", wt.Path, wt.InspectionError)
		} else if len(wt.Staged)+len(wt.Unstaged)+len(wt.Untracked) > 0 {
			fmt.Fprintf(w, "  ! worktree %q (%s): %d staged, %d unstaged, %d untracked; uncommitted changes EXCLUDED. Commit intended changes and rerun.\n", wt.Path, wt.Branch, len(wt.Staged), len(wt.Unstaged), len(wt.Untracked))
		}
		if wt.Detached {
			fmt.Fprintf(w, "  ! detached worktree %q @ %s: commit included=%t; name its commit explicitly to include it.\n", wt.Path, short(wt.Commit), wt.CommitIncluded)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintf(w, "  Policy: %s; requires %s\n", r.Gate.Policy.Name, strings.Join(r.Gate.Policy.Require, ", "))
	for _, c := range r.Gate.Required {
		fmt.Fprintf(w, "  %s  %s\n", gateMark(c.Status), gateLabel(c))
	}
	if !ran && len(r.Conflicts) == 0 {
		fmt.Fprintln(w, "  ?  combined tree not tested yet (add --run)")
	}
	for _, f := range r.Findings {
		if f.Severity != model.SeverityError {
			continue
		}
		fmt.Fprintf(w, "\n  ✗ %s: %s\n", f.Code, f.Explanation)
		if a, ok := g.Attribution[f.ID]; ok {
			label := "look at"
			if strings.HasPrefix(f.Code, "integration_execution_") {
				label = "look at (import-based lead)"
			}
			fmt.Fprintf(w, "    %s: %s\n", label, renderLeads(a.Leads))
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
		if warnings++; warnings <= 5 {
			fmt.Fprintf(w, "\n  ! %s: %s\n", f.Code, f.Explanation)
			if a, ok := g.Attribution[f.ID]; ok {
				fmt.Fprintf(w, "    look at: %s\n", renderLeads(a.Leads))
			}
		}
	}
	if warnings > 5 {
		fmt.Fprintf(w, "\n  … %d more warning(s); see --json\n", warnings-5)
	}
	if !ran && r.VerificationProposal != nil && len(r.VerificationProposal.Commands) > 0 {
		fmt.Fprintf(w, "\n  Suggested tests (static relationships; not run):\n")
		for i, c := range r.VerificationProposal.Commands {
			if i == 5 {
				fmt.Fprintf(w, "    … %d more\n", len(r.VerificationProposal.Commands)-5)
				break
			}
			fmt.Fprintf(w, "    %s  (in %s)\n", strings.Join(shorten(c.Command, 6), " "), c.CWD)
		}
	}
	if ran && r.Selection != nil {
		fmt.Fprintf(w, "\n  Test files: %d inventoried, %d selected (relationships are not behavioral coverage)\n", r.Selection.Inventory, r.Selection.TestFiles)
		fmt.Fprintf(w, "\n  Tests (%s): ran %d of %d selected command(s)\n", r.Selection.Mode, len(r.Executions), len(r.Selection.Commands))
		for _, ev := range r.Executions {
			fmt.Fprintf(w, "    %s  %s  (in %s; %d recognized test(s))\n", gateMark(ev.Status), strings.Join(shorten(ev.Command, 6), " "), ev.CWD, ev.Observation.TestsRun)
		}
		if len(r.Selection.Uncovered) > 0 {
			fmt.Fprintf(w, "  Uncovered changes (no established test relationship): %s\n", strings.Join(r.Selection.Uncovered, ", "))
			fmt.Fprintln(w, "  Resolve with supported tests, review --suite full, or explicitly select a limited --policy.")
		}
		if !slices.Contains(r.Gate.Policy.Require, "test_selection") || !slices.Contains(r.Gate.Policy.Require, "integration_execution") {
			fmt.Fprintln(w, "  Limited policy: test selection or combined execution is not required; PASS applies only to the named requirements.")
		}
		for _, b := range r.Selection.Blocking {
			fmt.Fprintf(w, "    not run: %s\n", b)
		}
	}
	if g.Next != "" {
		fmt.Fprintf(w, "\nNext: %s\n", g.Next)
	}
	fmt.Fprintln(w, "Details: add --json. Radar never touches your branches; the combination is built in private Git state.")
}

func renderLeads(leads []gateLead) string {
	out := []string{}
	for _, l := range leads {
		out = append(out, fmt.Sprintf("%s (%s)", l.Branch, strings.Join(shorten(l.Files, 3), ", ")))
	}
	return strings.Join(out, "; ")
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

// gateLabel phrases required checks for people; IDs stay in --json.
func gateLabel(c gate.Check) string {
	labels := map[string][2]string{
		"textual_merge":         {"branches merge without conflicts", "branches conflict"},
		"no_breaking_contracts": {"no breaking contract change found", "breaking contract change"},
		"integration_execution": {"selected tests pass on the combined tree", "tests on the combined tree did not pass"},
	}
	l, ok := labels[c.ID]
	if !ok {
		return c.ID + ": " + string(c.Status)
	}
	switch c.Status {
	case model.StatusPassed:
		return l[0]
	case model.StatusFailed:
		return l[1]
	}
	return c.ID + " " + string(c.Status) + ": " + c.Explanation
}
