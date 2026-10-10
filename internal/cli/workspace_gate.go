package cli

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Jake-Network/radar/internal/composition"
	"github.com/Jake-Network/radar/internal/gate"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/testselection"
	"github.com/Jake-Network/radar/internal/workspace"
)

// WorkspaceReportVersion is the version of schemas/workspace-report.schema.json.
const WorkspaceReportVersion = 1

const perRepoOnly = "this version checks each repo separately"

// workspaceInvocation is a resolved multi-repository gate: the scope, how
// refs and bases were chosen, and the run it repeats or replays.
type workspaceInvocation struct {
	scope     workspace.Scope
	selection composition.Selection
	named     map[string][]string
	bases     map[string]string
	previous  *composition.Record
	replay    *composition.Record
	team      *composition.Team
	// noTeam explains why a workspace with a home repo has no declared links.
	noTeam string
}

func wsError(next, format string, args ...any) error {
	return &workspace.Error{Message: fmt.Sprintf(format, args...), Next: next}
}

// registry reads the local workspace registry; without a configuration
// directory there is no registry, so nothing is registered.
func (a *app) registry() (workspace.Registry, error) {
	dir, err := workspace.ConfigDir()
	if err != nil {
		return workspace.Registry{Version: workspace.RegistryVersion}, nil
	}
	return workspace.Load(dir)
}

func (a *app) usable(r workspace.Repo) string { return workspace.Usable(a.ctx, r) }

// workspaceInvocation resolves the scope rules. It returns nil, and the
// unchanged single-repository gate runs, for a repository that is not in a
// workspace when no --with, --again or --replay is given.
func (a *app) workspaceInvocation(o options) (*workspaceInvocation, error) {
	selecting := len(o.args) > 0 || len(o.bases) > 0 || len(o.with) > 0 || len(o.only) > 0
	if o.replay != "" && (selecting || o.again) {
		return nil, wsError("radar gate --replay "+shellArg(o.replay)+", or radar gate --again", "--replay rebuilds a recorded run exactly; it cannot be combined with branches, --base, --with, --only or --again")
	}
	if o.again && selecting {
		return nil, wsError("radar gate --again, or radar gate "+strings.Join(o.args, " "), "--again repeats the previous run's selection; it cannot be combined with branches, --base, --with or --only")
	}
	registry, err := a.registry()
	if err != nil {
		if len(o.with) == 0 && len(o.only) == 0 && !o.again && o.replay == "" {
			// A broken machine-local registry must not stop the gate of every
			// repository; it may hide this repository's workspace, so say so.
			fmt.Fprintf(a.errout, "radar: warning: workspace registry not read (%v); checking this repository alone, without its workspace\nNext: fix or remove the registry file, then rerun\n", err)
			return nil, nil
		}
		return nil, wsError("fix or remove the registry file, then rerun", "%v", err)
	}
	current, err := workspace.Locate(a.ctx, a.root)
	if err != nil {
		return nil, err
	}
	key := workspace.AdhocKey(current)
	if wi, _, ok := registry.Find(current.CommonDir); ok {
		key = registry.Workspaces[wi].Name
	}
	inv := &workspaceInvocation{}
	if o.replay != "" || o.again {
		store, err := composition.OpenStore(key)
		if err != nil {
			return nil, err
		}
		id := o.replay
		if o.again {
			id = "last"
		}
		rec, err := store.Load(id)
		if err != nil && o.again {
			return nil, wsError("radar gate", "--again repeats the previous workspace run, and there is none here")
		}
		if err != nil {
			return nil, wsError("radar workspace show", "%v", err)
		}
		if o.replay != "" {
			inv.replay = &rec
			inv.selection = rec.Selection
			inv.scope = a.replayScope(registry, current, key, rec)
			inv.team, err = composition.LoadTeam(a.ctx, inv.scope, "", nil, &rec)
			return inv, err
		}
		inv.previous = &rec
		o.args, o.bases, o.with, o.only = rec.Selection.Targets, rec.Selection.Bases, rec.Selection.With, rec.Selection.Only
	}
	with, withArgs := []workspace.Location{}, []string{}
	for _, path := range o.with {
		if !filepath.IsAbs(path) {
			path = filepath.Join(a.root, path)
		}
		loc, err := workspace.Locate(a.ctx, path)
		if err != nil {
			return nil, wsError("radar gate --with <PATH of a Git checkout>", "--with %s: %v", path, err)
		}
		with, withArgs = append(with, loc), append(withArgs, path)
	}
	// --only applies after team membership, so it is not given here.
	scope, err := workspace.ResolveScope(workspace.ScopeInput{Registry: registry, Current: current, With: with, WithArgs: withArgs, Usable: a.usable})
	if err != nil {
		return nil, err
	}
	if scope == nil {
		if len(o.only) > 0 {
			return nil, wsError("radar workspace add <PATH>", "--only limits a workspace run, but this repository is not in a workspace")
		}
		return nil, nil
	}
	// Resolve committed team membership before assigning CLI refs or bases.
	home := ""
	if i := registry.Named(scope.Workspace); i >= 0 {
		home = registry.Workspaces[i].Home
	}
	loadBases, err := scope.AssignBases(o.bases)
	if err != nil {
		return nil, err
	}
	inv.team, err = composition.LoadTeam(a.ctx, *scope, home, loadBases, nil)
	if err != nil {
		return nil, err
	}
	if inv.team == nil && home != "" {
		inv.noTeam = fmt.Sprintf("home repo %s has no committed %s on its base; no declared links to check", home, workspace.TeamPath)
	}
	scope.Only = o.only
	var teamFile *workspace.TeamFile
	if inv.team != nil {
		teamFile = inv.team.File
	}
	resolved, err := workspace.ResolveTeamScope(*scope, teamFile, composition.Member(a.ctx))
	if err != nil {
		return nil, err
	}
	scope = &resolved
	if inv.team != nil && loadBases[inv.team.Home] == "" {
		for i := range scope.Repos {
			if scope.Repos[i].ID == inv.team.Home {
				scope.Repos[i].TeamBase = inv.team.BaseRef
			}
		}
	}
	inv.scope = *scope
	valid := func(t workspace.Target) bool {
		id := scope.Current
		if t.Qualified {
			id = t.Repo
		}
		for _, r := range scope.Repos {
			if r.ID == id && r.Missing == "" {
				_, err := gitrepo.Resolve(a.ctx, r.Path, t.Ref)
				return err == nil
			}
		}
		return false
	}
	targets := workspace.ParseTargets(o.args, valid)
	if inv.named, err = scope.AssignTargets(targets); err != nil {
		return nil, err
	}
	mode := "named"
	if len(targets) == 0 {
		inv.named, mode = nil, "auto"
	}
	if inv.bases, err = scope.AssignBases(o.bases); err != nil {
		return nil, err
	}
	absWith := []string{}
	for _, loc := range with {
		absWith = append(absWith, loc.Root)
	}
	only := append([]string{}, scope.Only...)
	inv.selection = composition.Selection{Mode: mode, Targets: scope.Qualify(targets), Bases: scope.QualifyBases(o.bases), With: absWith, Only: only, All: scope.All}
	return inv, nil
}

// replayScope locates the recorded repositories: the first of the registered
// path of the same repo ID, the recorded path and this checkout that holds
// every recorded commit, else the first that is a checkout at all, so the
// rebuild reports which commit is missing.
func (a *app) replayScope(registry workspace.Registry, current workspace.Location, key string, rec composition.Record) workspace.Scope {
	s := workspace.Scope{Workspace: rec.Workspace, Key: key, Source: workspace.SourceReplay, OneOff: len(rec.Selection.With) > 0, Only: rec.Selection.Only, All: rec.Selection.All}
	registered := workspace.Workspace{}
	if i := registry.Named(rec.Workspace); i >= 0 {
		registered = registry.Workspaces[i]
	}
	for _, r := range rec.Repos {
		sr := workspace.ScopeRepo{ID: r.ID, Path: r.Path, CommonDir: r.CommonDir, Origin: workspace.SourceReplay}
		candidates := []string{}
		if repo, ok := registered.Repo(r.ID); ok {
			candidates = append(candidates, repo.Path)
		}
		candidates = append(candidates, r.Path)
		if r.CommonDir == current.CommonDir {
			s.Current = r.ID
			candidates = append(candidates, current.Root)
		}
		sr.Missing = "path " + r.Path + " is not available"
		for _, path := range candidates {
			loc, err := workspace.Locate(a.ctx, path)
			if err != nil {
				continue
			}
			if sr.Missing != "" {
				sr.Path, sr.CommonDir, sr.Missing = loc.Root, loc.CommonDir, ""
			}
			if a.hasCommits(loc.Root, r) {
				sr.Path, sr.CommonDir = loc.Root, loc.CommonDir
				break
			}
		}
		s.Repos = append(s.Repos, sr)
	}
	if len(s.All) == 0 {
		for _, r := range s.Repos {
			s.All = append(s.All, r.ID)
		}
	}
	return s
}

// hasCommits reports whether root holds the recorded base and inputs of r.
func (a *app) hasCommits(root string, r composition.RecordRepo) bool {
	commits := []string{r.Base}
	for _, b := range r.Branches {
		commits = append(commits, b.Commit)
	}
	for _, c := range commits {
		if c == "" {
			continue
		}
		if _, err := gitrepo.Resolve(a.ctx, root, c); err != nil {
			return false
		}
	}
	return true
}

type workspaceRepo struct {
	ID                      string                     `json:"id"`
	Path                    string                     `json:"path"`
	CommonDir               string                     `json:"common_dir"`
	Origin                  string                     `json:"origin"`
	BaseRef                 string                     `json:"base_ref"`
	Base                    string                     `json:"base"`
	BaseSource              string                     `json:"base_source"`
	SelectionSource         string                     `json:"selection_source"`
	Branches                []composition.Branch       `json:"branches"`
	Skipped                 []string                   `json:"skipped_branches,omitempty"`
	Detached                []composition.Detached     `json:"detached,omitempty"`
	Dirty                   []composition.Dirty        `json:"dirty"`
	WorktreeInspectionError string                     `json:"worktree_inspection_error,omitempty"`
	Verdict                 gate.Verdict               `json:"verdict"`
	Error                   string                     `json:"error,omitempty"`
	Next                    string                     `json:"next,omitempty"`
	Attribution             map[string]gateAttribution `json:"attribution,omitempty"`
	Report                  *integration.Report        `json:"report,omitempty"`
}

type crossRepo struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// againChange lists how a repeated selection differs from the previous run.
type againChange struct {
	PreviousRun string   `json:"previous_run"`
	Mode        string   `json:"mode"`
	Added       []string `json:"added"`
	Removed     []string `json:"removed"`
	Moved       []string `json:"moved"`
}

// workspaceReport is the version 1 JSON report of a multi-repository gate;
// see schemas/workspace-report.schema.json.
type workspaceReport struct {
	Version            int                   `json:"version"`
	Workspace          string                `json:"workspace"`
	ScopeSource        string                `json:"scope_source"`
	ScopeSummary       string                `json:"scope_summary"`
	OneOff             bool                  `json:"one_off"`
	Only               []string              `json:"only"`
	RepoCount          int                   `json:"repo_count"`
	BranchCount        int                   `json:"branch_count"`
	Repos              []workspaceRepo       `json:"repos"`
	SuggestedLinks     []workspaceSuggestion `json:"suggested_links,omitempty"`
	Links              []composition.Link    `json:"links,omitempty"`
	TeamFile           *composition.Team     `json:"team_file,omitempty"`
	ExcludedRepos      []workspace.ScopeRepo `json:"excluded_repos,omitempty"`
	CrossRepo          crossRepo             `json:"cross_repo"`
	CrossRepoExecution *crossRepo            `json:"cross_repo_execution,omitempty"`
	Selection          composition.Selection `json:"selection"`
	Again              *againChange          `json:"again,omitempty"`
	ReplayOf           string                `json:"replay_of,omitempty"`
	Digest             string                `json:"digest"`
	RunID              string                `json:"run_id"`
	RecordError        string                `json:"record_error,omitempty"`
	Configuration      []ConfigurationInput  `json:"configuration_inputs,omitempty"`
	Verdict            gate.Verdict          `json:"verdict"`
	Next               string                `json:"next"`
	ran                bool
}

// rank orders repository verdicts for the workspace verdict. A supported
// failure in one repository decides the workspace even when another could
// not be checked: error (exit 2) means no verdict could be made at all.
func rank(v gate.Verdict) int {
	return map[gate.Verdict]int{gate.Pass: 0, gate.Blocked: 1, gate.Error: 2, gate.Fail: 3}[v]
}

func (a *app) workspaceGate(o options, inv *workspaceInvocation) int {
	if o.plan != "" && len(inv.scope.Repos) != 1 {
		return a.fail(wsError("radar gate --only "+inv.scope.Current+" --plan "+shellArg(o.plan), "--plan applies to one repository, and this workspace run checks %d", len(inv.scope.Repos)))
	}
	configuration, err := a.captureConfiguration(o)
	if err != nil {
		return a.fail(err)
	}
	repos, err := composition.Collect(a.ctx, composition.Request{Scope: inv.scope, Named: inv.named, Bases: inv.bases, Replay: inv.replay})
	if err != nil {
		return a.fail(wsError("rerun the same radar gate command once agents stop committing", "%v", err))
	}
	if inv.team != nil {
		for _, r := range repos {
			if r.ID == inv.team.Home && r.Error == "" && r.Base != inv.team.Base {
				return a.fail(wsError("radar gate --again", "team configuration base moved during collection; rerun to read a consistent declaration"))
			}
		}
	}
	if inv.replay == nil && inv.named == nil {
		branches, failures := 0, 0
		for _, r := range repos {
			branches += len(r.Branches)
			if r.Error != "" {
				failures++
			}
		}
		if branches == 0 && failures == 0 {
			return a.fail(wsError("radar gate "+inv.scope.Repos[0].ID+":<BRANCH>", "no branches to combine: no worktree branch of %s has commits beyond its base", strings.Join(inv.scope.All, ", ")))
		}
	}
	options := integration.Options{Policy: configuration.Policy, Plan: configuration.Plan, SuggestTests: true, MaxCommands: o.maxCommands, Timeout: o.timeout}
	if o.verify {
		options.Verify, options.AllowExecution = true, true
		options.Suite = o.suite
		if options.Suite == "" {
			options.Suite = testselection.ModeBalanced
		}
	}
	results := composition.Build(a.ctx, repos, options, inv.team)
	g := workspaceReport{Version: WorkspaceReportVersion, Workspace: inv.scope.Workspace, ScopeSource: inv.scope.Source, OneOff: inv.scope.OneOff, Only: inv.scope.Only, Configuration: configuration.Inputs, Selection: inv.selection, Repos: []workspaceRepo{}, Verdict: gate.Pass, ran: o.verify}
	if g.Only == nil {
		g.Only = []string{}
	}
	g.CrossRepo = crossRepo{Status: "not_checked", Reason: perRepoOnly}
	if inv.noTeam != "" {
		g.CrossRepo.Reason = inv.noTeam
	}
	if o.verify {
		g.CrossRepoExecution = &crossRepo{Status: "not_checked", Reason: perRepoOnly}
	}
	for i := range results {
		r := &results[i]
		if r.Report != nil {
			configuration.applyIntegration(r.Report)
		}
		g.Repos = append(g.Repos, workspaceEntry(*r))
		g.BranchCount += len(r.Branches)
		if v := g.Repos[i].Verdict; rank(v) > rank(g.Verdict) {
			g.Verdict = v
		}
	}
	g.RepoCount = len(g.Repos)
	g.TeamFile, g.ExcludedRepos = inv.team, inv.scope.Excluded
	g.Links = composition.CheckLinks(results, inv.team)
	applyWorkspaceLinks(&g)
	if inv.team == nil {
		for _, proposal := range composition.Suggestions(results) {
			g.SuggestedLinks = append(g.SuggestedLinks, workspaceSuggestion{SuggestedLink: proposal, Command: suggestionCommand(proposal)})
		}
	}
	g.Digest = composition.Digest(results, inv.team)
	if inv.previous != nil {
		g.Again = againDiff(*inv.previous, results)
	}
	if inv.replay != nil {
		g.ReplayOf = inv.replay.RunID
		g.RunID = inv.replay.RunID
	}
	g.ScopeSummary = scopeSummary(inv.scope, g.ReplayOf, g.Links)
	unstable := configurationUnstable(configuration.Inputs)
	errored := slices.ContainsFunc(g.Repos, func(r workspaceRepo) bool { return r.Verdict == gate.Error })
	g.Next = workspaceNext(g.Verdict, o.verify, unstable, errored)
	if inv.replay == nil {
		rec := composition.NewRecord(inv.scope, inv.selection, results, g.Digest, string(g.Verdict), inv.team)
		if inv.team != nil {
			rec.Links = g.Links
		}
		if store, err := composition.OpenStore(inv.scope.Key); err != nil {
			g.RecordError = err.Error()
		} else if err = store.Save(&rec, func() any { g.RunID = rec.RunID; return g }); err != nil {
			g.RecordError = err.Error()
			g.RunID = ""
		}
	}
	a.report(g, func(w io.Writer) { renderWorkspaceGate(w, g) })
	if configuration.Policy != nil || o.verify || unstable {
		return gate.Exit(gate.Result{Verdict: g.Verdict})
	}
	// Without --run no test evidence exists by construction; only a supported
	// failure fails the static gate.
	switch g.Verdict {
	case gate.Fail:
		return 1
	case gate.Error:
		return 2
	}
	return 0
}

func workspaceEntry(r composition.Result) workspaceRepo {
	e := workspaceRepo{ID: r.ID, Path: r.Path, CommonDir: r.CommonDir, Origin: r.Origin, BaseRef: r.BaseRef, Base: r.Base, BaseSource: r.BaseSource, SelectionSource: r.Selection, Branches: append([]composition.Branch{}, r.Branches...), Skipped: r.Skipped, Detached: r.Detached, Dirty: r.Dirty, WorktreeInspectionError: r.WorktreeInspectionError, Error: r.Error, Next: r.Next, Report: r.Report, Verdict: gate.Error}
	branches := []gateBranch{}
	for _, b := range r.Branches {
		branches = append(branches, gateBranch{Ref: b.Ref, Commit: b.Commit, Changed: b.Changed})
	}
	if r.Report != nil {
		e.Verdict = r.Report.Gate.Verdict
		g := gateReport{Branches: branches, Attribution: map[string]gateAttribution{}, Report: *r.Report}
		g.attribute()
		if len(g.Attribution) > 0 {
			e.Attribution = g.Attribution
		}
	}
	return e
}

func againDiff(previous composition.Record, results []composition.Result) *againChange {
	c := &againChange{PreviousRun: previous.RunID, Mode: previous.Selection.Mode, Added: []string{}, Removed: []string{}, Moved: []string{}}
	before := map[string]string{}
	for _, r := range previous.Repos {
		for _, b := range r.Branches {
			before[r.ID+":"+b.Ref] = b.Commit
		}
	}
	now := map[string]bool{}
	for _, r := range results {
		for _, b := range r.Branches {
			key := r.ID + ":" + b.Ref
			now[key] = true
			commit, ok := before[key]
			switch {
			case !ok:
				c.Added = append(c.Added, key)
			case commit != b.Commit:
				c.Moved = append(c.Moved, key)
			}
		}
	}
	for _, r := range previous.Repos {
		for _, b := range r.Branches {
			if key := r.ID + ":" + b.Ref; !now[key] {
				c.Removed = append(c.Removed, key)
			}
		}
	}
	return c
}

// scopeSummary counts a link as checked when its candidate+candidate cell
// reached a result: passed, failed, or passed with warnings.
func scopeSummary(s workspace.Scope, replayOf string, links []composition.Link) string {
	parts := []string{"per-repo checks only", "0 cross-repo links checked"}
	if len(links) > 0 {
		checked := 0
		for _, l := range links {
			if linkChecked(l.Status) {
				checked++
			}
		}
		parts = []string{fmt.Sprintf("%d/%d cross-repo links checked", checked, len(links))}
	}
	if s.Partial() {
		parts = append(parts, fmt.Sprintf("partial workspace (%d/%d repos)", len(s.Repos), len(s.All)))
	}
	if s.OneOff {
		parts = append(parts, "one-off scope (--with)")
	}
	if replayOf != "" {
		parts = append(parts, "replay of "+replayOf)
	}
	return strings.Join(parts, " · ")
}

// workspaceNext always ends with radar gate --again, with --run when the
// combined trees should be (re)tested. errored reports a repository that
// could not be checked, which a failure elsewhere does not hide.
func workspaceNext(v gate.Verdict, ran, unstable, errored bool) string {
	again := "radar gate --again"
	if ran {
		again += " --run"
	}
	switch {
	case v == gate.Fail && errored:
		return "repair and commit on the branches above and resolve the errors above, then: " + again
	case v == gate.Fail:
		return "repair and commit on the branches above, then: " + again
	case unstable:
		return "restore stable configuration, then: " + again
	case v == gate.Error:
		return "resolve the errors above, then: " + again
	case v == gate.Blocked:
		return "resolve the items marked ! above, then: " + again
	case !ran:
		return "radar gate --again --run"
	}
	return "after new commits: " + again
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

var workspaceLabels = map[string][2]string{
	"no_breaking_contracts": {"no breaking contract change", "breaking contract change"},
	"integration_execution": {"selected tests pass", "tests on the combined tree did not pass"},
	"test_selection":        {"test selection complete", "test selection incomplete"},
}

// repoLine is the ✓/✗/! summary of one repository.
func repoLine(r workspaceRepo) (string, string) {
	if r.Error != "" {
		return "✗", "error — " + r.Error
	}
	rep := r.Report
	combined := plural(len(r.Branches), "branch combined", "branches combined")
	if len(r.Branches) == 0 {
		combined = "base only"
	}
	switch r.Verdict {
	case gate.Pass:
		parts := []string{combined}
		for _, c := range rep.Gate.Required {
			if l, ok := workspaceLabels[c.ID]; ok {
				parts = append(parts, l[0])
			} else if c.ID != "textual_merge" {
				parts = append(parts, c.ID+" passed")
			}
		}
		return "✓", strings.Join(parts, " · ")
	case gate.Fail:
		for _, c := range rep.Gate.Required {
			if c.Status != model.StatusFailed {
				continue
			}
			if c.ID == "textual_merge" {
				return "✗", "branches conflict: " + strings.Join(shorten(rep.Conflicts, 4), ", ")
			}
			if l, ok := workspaceLabels[c.ID]; ok {
				return "✗", l[1]
			}
			return "✗", c.ID + " failed"
		}
		return "✗", "failed"
	}
	for _, c := range rep.Gate.Required {
		if c.Status == model.StatusPassed {
			continue
		}
		if r.Verdict == gate.Error {
			return "✗", "error — " + c.ID + ": " + firstSentence(c.Explanation)
		}
		return "!", "not verified — " + c.ID + ": " + firstSentence(c.Explanation)
	}
	return "!", "not verified — " + rep.Gate.Explanation
}

func chain(r workspaceRepo) string {
	if r.Base == "" {
		if r.BaseRef != "" {
			return r.BaseRef
		}
		return "—"
	}
	parts := []string{r.BaseRef + "@" + short(r.Base)}
	for _, b := range r.Branches {
		parts = append(parts, b.Ref+"@"+short(b.Commit))
	}
	return strings.Join(parts, " + ")
}

func renderWorkspaceGate(w io.Writer, g workspaceReport) {
	verdict := verdictMark[g.Verdict]
	if !g.ran && g.Verdict == gate.Pass {
		verdict = "PASS (static)"
	}
	if g.ran && g.Verdict == gate.Pass {
		verdict = "PASS (selected policy; bounded verification)"
	}
	head := []string{}
	if g.Workspace != "" {
		head = append(head, fmt.Sprintf("workspace %q", g.Workspace))
	}
	head = append(head, plural(g.RepoCount, "repo", "repos"), plural(g.BranchCount, "branch", "branches"), g.ScopeSummary)
	fmt.Fprintf(w, "Radar gate: %s — %s\n\n", verdict, strings.Join(head, " · "))

	idWidth, chainWidth := 0, 0
	for _, r := range g.Repos {
		idWidth = max(idWidth, len(r.ID))
		chainWidth = max(chainWidth, len(chain(r)))
	}
	for _, r := range g.Repos {
		source := r.SelectionSource
		if r.Error != "" && r.Base == "" {
			source = "error"
		}
		fmt.Fprintf(w, "  %-*s  %-*s   (%s)\n", idWidth, r.ID, chainWidth, chain(r), source)
	}
	fmt.Fprintln(w)
	for _, r := range g.Repos {
		mark, text := repoLine(r)
		fmt.Fprintf(w, "  %s %-*s  %s\n", mark, idWidth, r.ID, text)
		if r.Next != "" {
			fmt.Fprintf(w, "      next: %s\n", r.Next)
		}
		if r.Report == nil {
			continue
		}
		warnings := 0
		for _, f := range r.Report.Findings {
			switch f.Severity {
			case model.SeverityError:
				fmt.Fprintf(w, "      ✗ %s: %s\n", f.Code, f.Explanation)
				if f.Remediation != "" {
					fmt.Fprintf(w, "        fix: %s\n", f.Remediation)
				}
			case model.SeverityWarning:
				warnings++
			}
		}
		if warnings > 0 {
			fmt.Fprintf(w, "      · %s; see --json\n", plural(warnings, "warning", "warnings"))
		}
		if s := r.Report.Selection; g.ran && s != nil {
			fmt.Fprintf(w, "      tests (%s): ran %d of %d selected command(s)\n", s.Mode, len(r.Report.Executions), len(s.Commands))
			for _, ev := range r.Report.Executions {
				fmt.Fprintf(w, "        %s  %s  (in %s; %d recognized test(s))\n", gateMark(ev.Status), strings.Join(shorten(ev.Command, 6), " "), ev.CWD, ev.Observation.TestsRun)
			}
			for _, b := range s.Blocking {
				fmt.Fprintf(w, "        not run: %s\n", b)
			}
			if len(s.Uncovered) > 0 {
				fmt.Fprintf(w, "        uncovered changes: %s\n", strings.Join(shorten(s.Uncovered, 5), ", "))
			}
		}
	}
	if len(g.Links) == 0 {
		fmt.Fprintf(w, "  ! cross-repo links: not checked — %s\n", g.CrossRepo.Reason)
	} else {
		renderWorkspaceLinks(w, g)
	}
	for _, suggestion := range g.SuggestedLinks[:min(3, len(g.SuggestedLinks))] {
		fmt.Fprintf(w, "  · proposed link  %s → %s: %s (not checked)\n    %s\n", suggestion.Producer, suggestion.Consumer, suggestion.Candidate.Endpoint, suggestion.Command)
	}
	for _, r := range g.ExcludedRepos {
		fmt.Fprintf(w, "  · %s  registered but not in the team file\n", r.ID)
	}
	if g.TeamFile != nil && g.TeamFile.Uncommitted {
		fmt.Fprintf(w, "  ! %s:%s  uncommitted team file excluded; using the committed base declaration\n", g.TeamFile.Home, g.TeamFile.Path)
	}
	if g.CrossRepoExecution != nil {
		fmt.Fprintf(w, "  ! cross-repo execution: not checked — %s\n", g.CrossRepoExecution.Reason)
	}
	for _, r := range g.Repos {
		for _, d := range r.Dirty {
			if d.Selected {
				fmt.Fprintf(w, "  · %s:%s  committed changes only (%d uncommitted excluded)\n", r.ID, d.Branch, d.Count())
			}
		}
		for _, s := range r.Skipped {
			fmt.Fprintf(w, "  · %s:%s\n", r.ID, strings.Replace(s, " (", "  skipped (", 1))
		}
		for _, d := range r.Detached {
			fmt.Fprintf(w, "  ! %s  detached worktree %s @ %s not selected — name it: radar gate %s:%s\n", r.ID, d.Path, short(d.Commit), r.ID, d.Commit)
		}
		if r.WorktreeInspectionError != "" {
			fmt.Fprintf(w, "  ! %s  worktree inspection unavailable: %s\n", r.ID, r.WorktreeInspectionError)
		}
	}
	if a := g.Again; a != nil {
		changes := []string{}
		for _, k := range a.Added {
			changes = append(changes, "+ "+k)
		}
		for _, k := range a.Removed {
			changes = append(changes, "− "+k)
		}
		for _, k := range a.Moved {
			changes = append(changes, k+" has new commits")
		}
		if len(a.Added)+len(a.Removed) > 0 {
			fmt.Fprintf(w, "  ! again: branches changed since run %s: %s\n", a.PreviousRun, strings.Join(changes, " · "))
		} else if len(a.Moved) > 0 {
			fmt.Fprintf(w, "  · again: same branches as run %s; %s\n", a.PreviousRun, strings.Join(changes, " · "))
		} else {
			fmt.Fprintf(w, "  · again: same branches and commits as run %s\n", a.PreviousRun)
		}
	}
	leads := []string{}
	for _, r := range g.Repos {
		for _, attribution := range sortedAttributions(r.Attribution) {
			for _, l := range attribution.Leads {
				line := fmt.Sprintf("  %s:%s  %s", r.ID, l.Branch, strings.Join(shorten(l.Files, 3), ", "))
				if !slices.Contains(leads, line) {
					leads = append(leads, line)
				}
			}
		}
	}
	leads = append(leads, workspaceLinkLeads(g)...)
	if len(leads) > 0 {
		fmt.Fprintln(w, "\nRepair leads (where to look, not proof of cause)")
		for _, l := range leads {
			fmt.Fprintln(w, l)
		}
	}
	switch {
	case g.ReplayOf != "":
		fmt.Fprintf(w, "\n  · replayed run %s · digest %s\n", g.ReplayOf, shortDigest(g.Digest))
	case g.RecordError != "":
		fmt.Fprintf(w, "\n  ! run not recorded: %s (--again and --replay cannot use this run)\n", g.RecordError)
	default:
		fmt.Fprintf(w, "\n  · run %s · digest %s\n", g.RunID, shortDigest(g.Digest))
	}
	fmt.Fprintf(w, "\nNext: %s\n", g.Next)
}

func sortedAttributions(m map[string]gateAttribution) []gateAttribution {
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := []gateAttribution{}
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

// firstSentence keeps summary lines short; --json has the full explanation.
func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}

func shortDigest(d string) string {
	if len(d) > len("sha256:")+16 {
		return d[:len("sha256:")+16]
	}
	return d
}

func linkChecked(s model.Status) bool {
	return s == model.StatusPassed || s == model.StatusWarning || s == model.StatusFailed
}

// Only candidate+candidate contributes to the development verdict. A link
// with only inferred risks (warning) is checked and does not block, like
// warning findings of a single-repository gate.
func applyWorkspaceLinks(g *workspaceReport) {
	if len(g.Links) == 0 {
		return
	}
	g.CrossRepo = crossRepo{Status: "passed", Reason: "declared cross-repo links checked statically"}
	for _, l := range g.Links {
		v := gate.Pass
		switch l.Status {
		case model.StatusFailed:
			v = gate.Fail
			g.CrossRepo.Status = "failed"
		case model.StatusPassed, model.StatusWarning:
		default:
			v = gate.Blocked
			if g.CrossRepo.Status != "failed" {
				g.CrossRepo.Status = "incomplete"
			}
		}
		if rank(v) > rank(g.Verdict) {
			g.Verdict = v
		}
	}
}

func linkDetail(l composition.Link) string {
	c := l.Cells[3]
	if c.Reason != "" {
		return c.Reason
	}
	for _, f := range c.Findings {
		if f.Severity == model.SeverityError {
			return f.Explanation
		}
	}
	if len(c.Findings) > 0 {
		return c.Findings[0].Explanation
	}
	return "declared fields are compatible"
}

func renderWorkspaceLinks(w io.Writer, g workspaceReport) {
	for _, l := range g.Links {
		switch l.Status {
		case model.StatusPassed:
			fmt.Fprintf(w, "  ✓ link %s  %s → %s\n", l.ID, l.Producer, l.Consumer)
		case model.StatusWarning:
			fmt.Fprintf(w, "  ✓ link %s  %s → %s (warning: %s)\n", l.ID, l.Producer, l.Consumer, linkDetail(l))
		case model.StatusFailed:
			fmt.Fprintf(w, "  ✗ link %s  %s → %s: %s (candidate+candidate)\n", l.ID, l.Producer, l.Consumer, linkDetail(l))
		default:
			fmt.Fprintf(w, "  ! link %s  not checked — %s\n", l.ID, linkDetail(l))
		}
		for _, warning := range l.Warnings {
			fmt.Fprintf(w, "  · link %s  %s\n", l.ID, warning)
		}
		for _, i := range []int{1, 2} {
			c := l.Cells[i]
			if c.Status != model.StatusFailed {
				continue
			}
			order := "producer first fails"
			if i == 2 {
				order = "consumer first fails"
			}
			reason := c.Reason
			if reason == "" && len(c.Findings) > 0 {
				reason = c.Findings[0].Explanation
			}
			fmt.Fprintf(w, "  ! link %s  %s → %s: %s — %s (%s producer + %s consumer; excluded from verdict)\n", l.ID, l.Producer, l.Consumer, order, reason, c.Producer, c.Consumer)
		}
	}
}

func workspaceLinkLeads(g workspaceReport) []string {
	leads := []string{}
	for _, l := range g.Links {
		if l.Status != model.StatusFailed {
			continue
		}
		for _, r := range g.Repos {
			file, detail := "", ""
			if r.ID == l.Producer {
				file = l.ProducerPath
				detail = linkDetail(l)
			}
			if r.ID == l.Consumer {
				file = workspace.ConsumesPath
				detail = "declares consumed fields"
			}
			if file == "" {
				continue
			}
			matched := false
			for _, b := range r.Branches {
				if slices.Contains(b.Changed, file) {
					leads = append(leads, fmt.Sprintf("  %s:%s  %s  %s", r.ID, b.Ref, file, detail))
					matched = true
				}
			}
			if !matched {
				leads = append(leads, fmt.Sprintf("  %s:%s  %s  %s", r.ID, r.BaseRef, file, detail))
			}
		}
	}
	return leads
}

type workspaceSuggestion struct {
	composition.SuggestedLink
	Command string `json:"command"`
}

func suggestionCommand(p composition.SuggestedLink) string {
	v := p.Candidate
	command := "radar workspace connect " + shellArg(p.Producer+":"+v.SchemaPath+"#"+v.Pointer) + " " + shellArg(p.Consumer) + " --direction response"
	if len(v.Fields) > 0 {
		command += " --fields " + shellArg(strings.Join(v.Fields, ","))
	}
	if v.Consumer != "" {
		command += " --source " + shellArg(v.Consumer)
	}
	return command
}
