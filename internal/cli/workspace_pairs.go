package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Jake-Network/radar/internal/composition"
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/landing"
	"github.com/Jake-Network/radar/internal/model"
)

func (a *app) workspaceLanding(o options, inv *workspaceInvocation) int {
	if o.apply || o.driver != "" || o.allow || o.conflictBrief {
		return a.fail(errors.New("workspace landing provides per-repository orders and cross-repository pairs; apply, merge drivers and conflict briefs require an explicitly selected single repository"))
	}
	return a.workspacePairInspection(o, inv, false)
}
func (a *app) workspaceOverlap(o options, inv *workspaceInvocation) int {
	if o.symbol != "" || o.file != "" {
		return a.fail(errors.New("workspace overlap checks declared cross-repository contracts; symbol/file filters require a single repository"))
	}
	return a.workspacePairInspection(o, inv, true)
}
func (a *app) workspacePairInspection(o options, inv *workspaceInvocation, dirty bool) int {
	if o.verify || o.observeCoverage || o.allow || o.policy != "" || o.plan != "" || o.trailer {
		return a.fail(errors.New("workspace pair inspection is static and does not authorize repository execution"))
	}
	repos, err := composition.Collect(a.ctx, composition.Request{Scope: inv.scope, Named: inv.named, Bases: inv.bases, Replay: inv.replay})
	if err != nil {
		return a.fail(err)
	}
	plans := map[string]landing.Report{}
	if !dirty {
		for i, r := range repos {
			if r.Error != "" {
				return a.fail(fmt.Errorf("%s: %s", r.ID, r.Error))
			}
			if len(r.Branches) == 0 {
				continue
			}
			refs := []string{}
			byRef := map[string]composition.Branch{}
			for _, b := range r.Branches {
				refs = append(refs, b.Ref)
				byRef[b.Ref] = b
			}
			plan, err := landing.Plan(a.ctx, r.Path, landing.Options{Base: r.Base, Branches: refs})
			if err != nil {
				return a.fail(err)
			}
			for _, branch := range plan.Branches {
				pin, ok := byRef[branch.Name]
				if !ok || pin.Commit != branch.Commit {
					return a.fail(composition.ErrMoving)
				}
			}
			plans[r.ID] = plan
			repos[i].Branches = nil
			for _, name := range plan.Order {
				repos[i].Branches = append(repos[i].Branches, byRef[name])
			}
		}
	}
	pairs, results, err := composition.Pairwise(a.ctx, repos, inv.team, 256, dirty)
	if err != nil {
		return a.fail(err)
	}
	g := workspaceReport{Version: WorkspaceReportVersion, Workspace: inv.scope.Workspace, ScopeSource: inv.scope.Source, ScopeSummary: scopeSummary(inv.scope, "", nil), OneOff: inv.scope.OneOff, Only: inv.scope.Only, Selection: inv.selection, Repos: []workspaceRepo{}, Verdict: gate.Pass, TeamFile: inv.team, ExcludedRepos: inv.scope.Excluded, CrossRepoPairs: &pairs, Landings: plans, Next: "Inspect declared static link cells and missing evidence before landing. Runtime interoperability remains unverified."}
	if g.Only == nil {
		g.Only = []string{}
	}
	for i, r := range results {
		if p, ok := plans[r.ID]; ok && r.Report != nil {
			if len(p.Order) == 0 && len(p.RestackFirst) > 0 {
				preview, err := landing.PreviewPlan(a.ctx, r.Path, p, landing.Options{})
				if err != nil {
					return a.fail(err)
				}
				r.Report = &preview
			} else {
				p.EnforceInputScope(r.Report)
			}
		}
		results[i] = r
		entry := workspaceEntry(r)
		g.Repos = append(g.Repos, entry)
		g.BranchCount += len(r.Branches)
		if rank(entry.Verdict) > rank(g.Verdict) {
			g.Verdict = entry.Verdict
		}
	}
	g.RepoCount = len(g.Repos)
	g.Links = composition.CheckLinks(results, inv.team)
	g.CrossRepo = crossRepo{Status: "not_checked", Reason: "no declared cross-repository links"}
	applyWorkspaceLinks(&g)
	if pairs.Status == model.StatusFailed {
		g.Verdict = gate.Fail
	} else if pairs.Status != model.StatusPassed && rank(g.Verdict) < rank(gate.Blocked) {
		g.Verdict = gate.Blocked
	}
	for _, p := range plans {
		if !p.Complete && rank(g.Verdict) < rank(gate.Blocked) {
			g.Verdict = gate.Blocked
		}
	}
	g.ScopeSummary = scopeSummary(inv.scope, "", g.Links)
	g.Digest = composition.Digest(results, inv.team)
	a.report(g, func(w io.Writer) {
		for _, r := range repos {
			if p, ok := plans[r.ID]; ok {
				renderLandingOrder(w, p, r.ID)
			}
		}
		if dirty {
			fmt.Fprintln(w, "Cross-repository saved-change drift (declared static contracts):")
		}
		for _, p := range pairs.Pairs {
			labels := []string{}
			for _, in := range p.Inputs {
				labels = append(labels, in.Repo+":"+in.Ref)
			}
			fmt.Fprintf(w, "  %s: %s — %s\n", strings.Join(labels, " + "), p.Status, p.Reason)
		}
		for _, p := range pairs.Omitted {
			fmt.Fprintf(w, "  ? Pair not checked: %s\n", p.Reason)
		}
		renderWorkspaceGate(w, g)
	})
	return gate.Exit(gate.Result{Verdict: g.Verdict})
}
