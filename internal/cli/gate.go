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
	"github.com/Jake-Network/radar/internal/workspace"
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
	return command{name: "gate", usage: "radar gate [[REPO:]BRANCH ...] [--base REF] [--with PATH] [--again] [--run [--suite targeted|balanced|full] [--timeout 10m]] [--policy PATH] [--plan PATH]",
		summary:       "Combine branches (default: every worktree branch) and tell whether they integrate; --run also tests the combined tree.",
		advancedFlags: []string{"only", "replay"},
		flags: func(fs *flag.FlagSet, o *options) {
			fs.Var(baseFlag{o}, "base", "integration `REF` (default: origin/HEAD's branch, main, master or trunk)")
			fs.Var(listFlag{&o.with}, "with", "also check the repository at `PATH` in this run (repeatable; relative to the repository root)")
			fs.BoolVar(&o.again, "again", false, "repeat the previous workspace run's selection on the latest commits")
			fs.Var(listFlag{&o.only}, "only", "check only workspace repository `REPO` in this run (repeatable)")
			fs.StringVar(&o.replay, "replay", "", "rebuild the exact commits of workspace `RUN` (run ID or last)")
			fs.BoolVar(&o.verify, "run", false, "execute the selected tests on the combined tree with your host permissions (not an OS sandbox)")
			fs.StringVar(&o.suite, "suite", "", "test selection `MODE` with --run: targeted, balanced (default) or full")
			fs.IntVar(&o.maxCommands, "max-commands", testselection.DefaultMaxCommands, "maximum grouped test commands (`N`) --run executes")
			fs.DurationVar(&o.timeout, "timeout", 10*time.Minute, "total --run time budget (`DURATION`), maximum 30m")
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
	progress := a.startProgress("combining branches in private Git state")
	options.Progress = progress.callback()
	r, err := integration.Preview(a.ctx, a.root, options)
	progress.done()
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
	case r.Gate.Verdict == gate.Error && missingRunner(r.Executions) != "":
		g.Next = "install " + missingRunner(r.Executions) + " where you run radar, then rerun: " + strings.TrimSpace("radar gate --run "+rerun)
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
		if f.Code == "integration_textual_conflict" {
			paths = append(paths, r.Conflicts...)
		}
		if strings.HasPrefix(f.Code, "integration_execution_") {
			environment := false
			for _, ev := range r.Executions {
				// integration names the command and directory exactly this way.
				if strings.Contains(f.Explanation, fmt.Sprintf("%q in %q", ev.Command, ev.CWD)) {
					paths = append(paths, testFiles[ev.SelectionID]...)
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
func shellArg(s string) string { return workspace.ShellPath(s) }

var verdictMark = map[gate.Verdict]string{gate.Pass: "PASS", gate.Fail: "FAIL", gate.Blocked: "NOT VERIFIED", gate.Error: "ERROR"}

func renderGate(w io.Writer, g gateReport, ran bool) {
	p := paletteOf(w)
	r := g.Report
	fmt.Fprintf(w, "%s%s — %s\n", brand(p, "Radar gate: "), p.verdict(r.Gate.Verdict, verdictText(r.Gate.Verdict, ran)), gateHeadline(g, ran))
	fmt.Fprintln(w, p.dim(fmt.Sprintf("%s onto %s @ %s", plural(len(g.Branches), "branch", "branches"), g.BaseRef, short(r.Base))))
	fmt.Fprintln(w)
	width := 0
	for _, b := range g.Branches {
		width = max(width, len(b.Ref))
	}
	for _, b := range g.Branches {
		fmt.Fprintf(w, "  %s%s  %s  %s changed\n", p.cyan(b.Ref), strings.Repeat(" ", width-len(b.Ref)), p.dim(short(b.Commit)), plural(len(b.Changed), "file", "files"))
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
	for _, x := range rows {
		fmt.Fprintf(w, "  %s %s%s  %s\n", p.mark(x.mark), p.bold(x.name), strings.Repeat(" ", nameWidth-len(x.name)), x.text)
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
			label := "look at"
			if strings.HasPrefix(f.Code, "integration_execution_") {
				label = "look at (import-based lead)"
			}
			fmt.Fprintf(w, "    %s: %s\n", label, renderLeads(a.Leads, p))
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
			fmt.Fprintf(w, "  %s %s  %s\n", p.mark(gateMark(ev.Status)), p.cyan(displayCommand(ev.Command)), p.dim(fmt.Sprintf("(in %s; %s)", ev.CWD, plural(ev.Observation.TestsRun, "recognized test", "recognized tests"))))
		}
		for _, b := range r.Selection.Blocking {
			fmt.Fprintf(w, "  %s not run: %s\n", p.mark("?"), b)
		}
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
	fmt.Fprintln(w, p.dim("Details: add --json. Radar never touches your branches; the combination is built in private Git state."))
}

// brand prefixes the report's first line with Radar's mark on a terminal.
func brand(p palette, title string) string {
	if !p.on {
		return title
	}
	return p.paint("1;32", "◉ ") + p.bold(title)
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
		if d := ev.Observation.Diagnosis; d.Environment() && !slices.Contains(names, d.Name) {
			names = append(names, d.Name)
		}
	}
	return strings.Join(names, ", ")
}

func renderLeads(leads []gateLead, p palette) string {
	out := []string{}
	for _, l := range leads {
		out = append(out, fmt.Sprintf("%s (%s)", p.cyan(l.Branch), strings.Join(shorten(l.Files, 3), ", ")))
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
	}
	l, ok := labels[c.ID]
	switch {
	case ok && c.Status == model.StatusPassed:
		return l[0]
	case ok && c.Status == model.StatusFailed:
		if c.ID == "textual_merge" && len(r.Conflicts) > 0 {
			return l[1] + " in " + strings.Join(shorten(r.Conflicts, 4), ", ")
		}
		if c.ID == "integration_execution" && len(r.Executions) > 0 {
			failed := 0
			for _, ev := range r.Executions {
				if gateMark(ev.Status) == "✗" {
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
		if n := len(r.Selection.Blocking); n > 0 {
			gaps = append(gaps, plural(n, "blocking gap", "blocking gaps")+" (see not run below)")
		}
		if len(gaps) > 0 {
			return "incomplete — " + strings.Join(gaps, "; ")
		}
	case c.ID == "integration_execution" && r.Selection != nil && c.Status == model.StatusIncomplete:
		return fmt.Sprintf("incomplete — ran %d of %s; required verification has gaps", len(r.Executions), plural(len(r.Selection.Commands), "selected command", "selected commands"))
	}
	if !ok {
		return string(c.Status) + ": " + c.Explanation
	}
	return "not verified (" + string(c.Status) + ") — " + c.Explanation
}
