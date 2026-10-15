package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Jake-Network/radar/internal/gate"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/landing"
)

func landCommand() command {
	return command{name: "land", usage: "radar land [BRANCH ...] [--base REF] [--apply] [--conflict-brief] [--driver mergiraf|weave --allow-execution]", summary: "Plan branch landing order and publish an opt-in private integration ref.", flags: func(fs *flag.FlagSet, o *options) {
		fs.Var(baseFlag{o}, "base", "integration base (REF, or REPO=REF in a workspace)")
		fs.Var(listFlag{&o.with}, "with", "also inspect a repository path (repeatable)")
		fs.Var(listFlag{&o.only}, "only", "limit workspace repositories (repeatable)")
		fs.BoolVar(&o.apply, "apply", false, "publish only refs/radar/integration/<id>; never update a branch")
		fs.BoolVar(&o.conflictBrief, "conflict-brief", false, "return repair material for exactly two branches")
		fs.StringVar(&o.driver, "driver", "", "explicit installed structural merge driver")
		fs.BoolVar(&o.allow, "allow-execution", false, "authorize the named installed merge driver with host permissions")
	}, run: (*app).land}
}

func (a *app) land(o options) int {
	if o.verify || o.trailer || o.observeCoverage || o.plan != "" || o.policy != "" || o.suite != "" || o.again || o.replay != "" {
		return a.fail(errors.New("landing is static; run a separate gate for test execution, policy or plan verification"))
	}
	if err := a.repository(); err != nil {
		return a.fail(err)
	}
	localAction := o.apply || o.driver != "" || o.allow || o.conflictBrief
	if localAction && (len(o.with) > 0 || len(o.only) > 0 || len(o.bases) > 1) {
		return a.fail(errors.New("apply, merge drivers and conflict briefs target the current repository; omit workspace scope flags"))
	}
	if !localAction {
		inv, err := a.workspaceInvocation(o)
		if err != nil {
			return a.fail(err)
		}
		if inv != nil {
			return a.workspaceLanding(o, inv)
		}
	}
	base := o.base
	if base == "" {
		base = gitrepo.DefaultBranch(a.ctx, a.root)
	}
	if base == "" {
		return a.fail(errors.New("no default base; use --base REF"))
	}
	sha, err := gitrepo.Resolve(a.ctx, a.root, base)
	if err != nil {
		return a.fail(err)
	}
	refs, _, err := a.gateBranches(o.args, base, sha)
	if err != nil {
		return a.fail(err)
	}
	if o.conflictBrief && (len(refs) != 2 || o.apply || o.driver != "" || o.allow) {
		return a.fail(errors.New("--conflict-brief requires exactly two branches and no apply or driver"))
	}
	lopts := landing.Options{Base: sha, Branches: refs, Driver: o.driver, AllowExecution: o.allow}
	var plan *landing.Report
	var brief *landing.ConflictBrief
	order := append([]string{}, refs...)
	pinned := append([]string{}, refs...)
	if o.conflictBrief {
		b, e := landing.Brief(a.ctx, a.root, sha, refs[0], refs[1])
		if e != nil {
			return a.fail(e)
		}
		brief = &b
		pinned = []string{b.Left, b.Right}
	} else {
		p, e := landing.Plan(a.ctx, a.root, lopts)
		if e != nil {
			return a.fail(e)
		}
		plan = &p
		order = p.Order
		byName := map[string]string{}
		for _, b := range p.Branches {
			byName[b.Name] = b.Commit
		}
		pinned = nil
		for _, ref := range order {
			pinned = append(pinned, byName[ref])
		}
	}
	var applied *landing.Applied
	var report integration.Report
	if o.apply {
		v, e := landing.ApplyWithOptions(a.ctx, a.root, *plan, lopts)
		if e != nil {
			return a.fail(e)
		}
		applied = &v
		report = v.Report
		report.Limitations = append(report.Limitations, "Integration ref covers this repository only; cross-repository contracts require a separate workspace gate.")
	} else {
		previewOptions := lopts
		previewOptions.Branches = pinned
		report, err = landing.PreviewWithOptions(a.ctx, a.root, previewOptions)
		if err != nil {
			return a.fail(err)
		}
	}
	g := gateReport{BaseRef: base, Report: report, Landing: plan, ConflictBrief: brief, IntegrationRef: applied, Attribution: map[string]gateAttribution{}}
	for i, ref := range order {
		changed, e := a.branchChanges(sha, report.Inputs[i])
		if e != nil {
			return a.fail(e)
		}
		g.Branches = append(g.Branches, gateBranch{Ref: ref, Commit: report.Inputs[i], Changed: changed})
	}
	g.attribute()
	a.report(g, func(w io.Writer) {
		if plan != nil {
			fmt.Fprintln(w, "Land order: "+strings.Join(plan.Order, " → "))
			if len(plan.Independent) > 0 {
				fmt.Fprintln(w, "Any time: "+strings.Join(plan.Independent, ", "))
			}
			fmt.Fprintf(w, "Estimated later branches needing rebase: %d (%s; not measured rebase executions)\n", plan.EstimatedRebases, plan.OrderMethod)
			for _, p := range plan.Pairs {
				if len(p.Findings) > 0 || len(p.Conflicts) > 0 {
					fmt.Fprintf(w, "! %s + %s: %s\n", p.Left, p.Right, p.Explanation)
					for _, path := range p.Conflicts {
						fmt.Fprintln(w, "  textual conflict:", path)
					}
					for _, f := range p.Findings {
						if f.Severity == "error" {
							fmt.Fprintf(w, "  %s: %s\n", f.Code, f.Explanation)
						}
					}
				}
			}
			if !plan.Complete {
				fmt.Fprintln(w, "? Some pairs were not checked; independent ordering remains unverified.")
			}
		}
		if brief != nil {
			for _, f := range brief.Files {
				fmt.Fprintf(w, "Conflict: %s; symbols: %s; related tests: %s\n", f.Path, strings.Join(f.Symbols, ", "), strings.Join(f.Tests, ", "))
			}
			fmt.Fprintln(w, "Use --json for bounded base/branch excerpts and conflict hunks.")
		}
		renderGate(w, g, false)
		if applied != nil {
			fmt.Fprintf(w, "Integration ref: %s\n", applied.Ref)
			if len(applied.FastForward) > 0 {
				fmt.Fprintf(w, "Fast-forward after selecting the intended base branch: %s\n", displayCommand(applied.FastForward))
			} else {
				fmt.Fprintln(w, "? Current HEAD cannot fast-forward to this candidate; select the intended base and verify ancestry first.")
			}
		}
	})
	return gate.Exit(report.Gate)
}
