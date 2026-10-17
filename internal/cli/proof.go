package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/composition"
	"github.com/Jake-Network/radar/internal/gate"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/runproof"
)

func showCommand() command {
	return command{name: "show", usage: "radar show RUN-ID [--json]", summary: "Read an immutable gate report named by a Radar-Verified trailer.", advanced: true, run: (*app).showProof}
}

type gateProof struct {
	Report            gateReport       `json:"gate_report"`
	DeclaredBindings  int              `json:"declared_bindings"`
	Workspace         *workspaceReport `json:"workspace_report,omitempty"`
	WorkspaceBindings map[string]int   `json:"workspace_bindings,omitempty"`
}

func (a *app) emitWorkspaceTrailer(g workspaceReport) int {
	if g.Verdict != gate.Pass {
		renderWorkspaceGate(a.out, g)
		return gate.Exit(gate.Result{Verdict: g.Verdict})
	}
	state, err := composition.StateDir()
	if err != nil {
		return a.fail(err)
	}
	identity := gitrepo.Identity(a.ctx, a.root)
	if identity == "" {
		return a.fail(errors.New("cannot bind workspace proof to repository history"))
	}
	bindings := map[string]int{}
	observed, passed, total := 0, 0, 0
	for _, repo := range g.Repos {
		if repo.Report != nil {
			bindings[repo.ID] = repo.Report.DeclaredBindings
			for _, ev := range repo.Report.Executions {
				observed += ev.Observation.TestsRun
				total++
				if ev.IntegrationStatus() == model.StatusPassed {
					passed++
				}
			}
		}
	}
	r, err := runproof.Save(state, identity, gateProof{Workspace: &g, WorkspaceBindings: bindings})
	if err != nil {
		return a.fail(err)
	}
	tests := "not_run"
	if g.ran {
		tests = fmt.Sprintf("%d observed; %d/%d selected commands", observed, passed, total)
	}
	fmt.Fprintf(a.out, "Radar-Verified: static(%d repositories; cross_repo=%s) tests(%s) run=%s\n", g.RepoCount, g.CrossRepo.Status, tests, r.ID)
	return 0
}

func (a *app) emitGateTrailer(g gateReport, ran bool) int {
	if g.Gate.Verdict != gate.Pass {
		renderGate(a.out, g, ran)
		return gate.Exit(g.Gate)
	}
	state, err := composition.StateDir()
	if err != nil {
		return a.fail(err)
	}
	identity := gitrepo.Identity(a.ctx, a.root)
	if identity == "" {
		return a.fail(errors.New("cannot bind gate proof to repository history"))
	}
	r, err := runproof.Save(state, identity, gateProof{Report: g, DeclaredBindings: g.DeclaredBindings})
	if err != nil {
		return a.fail(err)
	}
	fmt.Fprintln(a.out, gateTrailer(g.Report, ran, r.ID))
	return 0
}

func gateTrailer(r integration.Report, ran bool, id string) string {
	names := map[string]string{"textual_merge": "merge", "no_symbol_conflicts": "symbols", "migration_order": "migrations", "lockfile_consistency": "lockfile", "unique_keys": "unique_keys", "no_breaking_contracts": "contracts", "generated_drift": "generated"}
	static := []string{}
	for _, c := range r.Checks {
		name := names[c.ID]
		if name == "" || c.Status != model.StatusPassed || (c.Evidence != model.VerifiedStatic && c.Evidence != model.VerifiedTool) {
			continue
		}
		if c.ID == "no_breaking_contracts" && r.DeclaredBindings == 0 {
			continue
		}
		static = append(static, name)
	}
	sort.Strings(static)
	tests := "not_run"
	if ran {
		observed, passed := 0, 0
		for _, ev := range r.Executions {
			observed += ev.Observation.TestsRun
			if ev.IntegrationStatus() == model.StatusPassed {
				passed++
			}
		}
		tests = fmt.Sprintf("%d observed; %d/%d selected commands", observed, passed, len(r.Executions))
		if r.Selection != nil && len(r.Selection.Omitted) > 0 {
			tests += fmt.Sprintf("; %d omitted", len(r.Selection.Omitted))
		}
	}
	return fmt.Sprintf("Radar-Verified: static(%s) tests(%s) run=%s", strings.Join(static, ","), tests, id)
}

func (a *app) showProof(o options) int {
	if len(o.args) != 1 {
		return a.fail(errors.New("usage: radar show RUN-ID"))
	}
	if err := a.repository(); err != nil {
		return a.fail(err)
	}
	state, err := composition.StateDir()
	if err != nil {
		return a.fail(err)
	}
	record, err := runproof.Load(state, gitrepo.Identity(a.ctx, a.root), o.args[0])
	if err != nil {
		return a.fail(err)
	}
	var saved gateProof
	if err = json.Unmarshal(record.Report, &saved); err != nil {
		return a.fail(err)
	}
	if saved.Workspace != nil {
		g := *saved.Workspace
		for i := range g.Repos {
			if g.Repos[i].Report != nil {
				g.Repos[i].Report.DeclaredBindings = saved.WorkspaceBindings[g.Repos[i].ID]
				g.ran = g.ran || len(g.Repos[i].Report.Executions) > 0
			}
		}
		a.report(g, func(w io.Writer) { renderWorkspaceGate(w, g); fmt.Fprintln(w, "Historical workspace run:", record.ID) })
		return 0
	}
	report := saved.Report
	report.DeclaredBindings = saved.DeclaredBindings
	// Report provenance is historical. Never recalculate it against today's HEAD.
	a.report(report, func(w io.Writer) {
		renderGate(w, report, len(report.Executions) > 0)
		fmt.Fprintf(w, "Recorded run: %s (historical candidate %s)\n", record.ID, short(report.CandidateTree))
	})
	return 0
}
