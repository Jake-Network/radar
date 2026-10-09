package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Jake-Network/radar/internal/contracts"
	"github.com/Jake-Network/radar/internal/discovery"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
	"github.com/Jake-Network/radar/internal/verification"
)

type coverageCheck struct {
	ID          string         `json:"id"`
	Status      model.Status   `json:"status"`
	Evidence    model.Evidence `json:"evidence"`
	Explanation string         `json:"explanation"`
}
type checkReport struct {
	Status         model.Status         `json:"status"`
	Base           string               `json:"base"`
	Head           string               `json:"head"`
	Impact         affectedReport       `json:"impact"`
	Declared       contracts.Report     `json:"declared_contracts"`
	Discovered     discovery.Comparison `json:"discovered_contracts"`
	Plan           *planning.Report     `json:"plan,omitempty"`
	Checks         []coverageCheck      `json:"checks"`
	Findings       []model.Finding      `json:"findings"`
	Diagnostics    []model.Diagnostic   `json:"diagnostics"`
	Limitations    []string             `json:"limitations"`
	FeedbackDigest string               `json:"feedback_digest"`
	RepairBudget   int                  `json:"suggested_repair_attempts"`
}

func checkCommand() command {
	return command{name: "check", usage: "radar check --base REF [--head REF|WORKTREE] [--plan PATH] [--require-complete]", summary: "Analyze changed files, dependency impact, declared and discovered contracts, and optional plan verification.", flags: func(fs *flag.FlagSet, o *options) {
		fs.StringVar(&o.base, "base", "", "base Git revision")
		fs.StringVar(&o.head, "head", "WORKTREE", "head revision (default current working tree)")
		planFlag(fs, o)
		fs.BoolVar(&o.strict, "require-complete", false, "exit 1 for unknown or incomplete analysis coverage")
	}, run: (*app).check}
}

// check never runs repository code and needs no initialized state. Coverage
// remains explicit even when all supported comparisons produce no findings.
func (a *app) check(o options) int {
	if o.base == "" {
		return a.fail(errors.New("--base is required"))
	}
	// Pin branch names once so concurrent ref movement cannot mix revisions.
	baseSHA, err := gitrepo.Resolve(a.ctx, a.root, o.base)
	if err != nil {
		return a.fail(err)
	}
	o.base = baseSHA
	if o.head != "WORKTREE" {
		headSHA, e := gitrepo.Resolve(a.ctx, a.root, o.head)
		if e != nil {
			return a.fail(e)
		}
		o.head = headSHA
	}
	initial, err := a.snapshot(o.head)
	if err != nil {
		return a.fail(err)
	}
	impact, err := a.affectedReport(o)
	if err != nil {
		return a.fail(err)
	}
	declared, err := contracts.Impact(a.ctx, a.root, impact.Base, o.head)
	if err != nil {
		return a.fail(err)
	}
	discovered, err := discovery.Compare(a.ctx, a.root, impact.Base, o.head)
	if err != nil {
		return a.fail(err)
	}
	r := checkReport{Status: model.StatusPassed, Base: impact.Base, Head: impact.Head, Impact: impact, Declared: declared, Discovered: discovered, Findings: []model.Finding{}, Checks: []coverageCheck{}, Diagnostics: []model.Diagnostic{}, RepairBudget: 2,
		Limitations: []string{"File dependencies are inferred from imports; compiler-resolved and runtime relationships are not established.", "Discovered producer-consumer candidates are static proposals, not authoritative runtime bindings.", "No build or tests execute in check; working-tree findings are informational. Use merge-check with explicit execution authorization for combined branches."}}
	add := func(id string, status model.Status, evidence model.Evidence, message string) {
		r.Checks = append(r.Checks, coverageCheck{id, status, evidence, message})
	}
	add("dependency_impact", model.StatusPassed, model.Inferred, "Changed files and reverse import dependencies analyzed in both source checkpoints.")
	add("declared_contracts", declared.Status, model.VerifiedStatic, fmt.Sprintf("%d/%d declared bindings analyzed; absent or unreadable manifest leaves coverage incomplete.", declared.Analyzed, declared.Bindings))
	add("discovered_contracts", discovered.Status, model.Inferred, "Supported static contract candidates compared; discovery does not verify runtime transport.")
	r.Findings = append(r.Findings, declared.Findings...)
	r.Findings = append(r.Findings, discovered.Findings...)
	r.Diagnostics = append(r.Diagnostics, impact.Diagnostics...)
	r.Diagnostics = append(r.Diagnostics, declared.Diagnostics...)
	r.Diagnostics = append(r.Diagnostics, discovered.Diagnostics...)
	r.Limitations = append(r.Limitations, discovered.Limitations...)
	// Lint distinguishes a malformed manifest from mere absence and checks stale
	// consumer declarations even when the baseline manifest was absent.
	lint := contracts.Lint(a.ctx, a.root, o.head)
	if _, e := contracts.LoadManifest(a.ctx, a.root, o.head); e == nil || !errors.Is(e, os.ErrNotExist) {
		add("manifest_lint", lint.Status, model.VerifiedStatic, "Current declared manifest lint.")
		if lint.Error != "" {
			r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: contracts.ManifestPath, Severity: model.SeverityError, Message: lint.Error})
		}
		for _, binding := range lint.Bindings {
			for _, issue := range binding.Issues {
				f := model.NewFinding("manifest_lint", issue, model.VerifiedStatic)
				f.Contract = binding.ID
				f.Severity = model.SeverityWarning
				f.Remediation = "Repair the declared schema or binding; do not discard dependency evidence merely to silence the finding."
				f.Verification = "Re-run radar contracts and radar check."
				r.Findings = append(r.Findings, f)
			}
		}
	}
	if o.plan != "" {
		p, e := a.loadPlan(o.plan)
		if e != nil {
			return a.fail(e)
		}
		s, e := a.snapshot(o.head)
		if e != nil {
			return a.fail(e)
		}
		pr := verification.VerifyWithEvidence(a.ctx, p, s, a.root, nil)
		r.Plan = &pr
		r.Findings = append(r.Findings, pr.Findings...)
		add("plan_verification", pr.Status, model.VerifiedStatic, "Plan criteria evaluated; this read-only command supplies no observed test evidence.")
	} else {
		add("plan_verification", model.StatusUnknown, model.Unknown, "No plan selected; architecture intent and acceptance criteria were not verified.")
	}
	add("integration_tests", model.StatusUnknown, model.Unknown, "No tests executed or evidence claimed by this read-only analysis.")
	for _, c := range r.Checks {
		switch c.Status {
		case model.StatusFailed:
			r.Status = model.StatusFailed
		case model.StatusError, model.StatusTimeout:
			if r.Status != model.StatusFailed {
				r.Status = model.StatusError
			}
		case model.StatusUnknown, model.StatusIncomplete, model.StatusBlocked:
			if r.Status == model.StatusPassed || r.Status == model.StatusWarning {
				r.Status = model.StatusIncomplete
			}
		case model.StatusWarning:
			if r.Status == model.StatusPassed {
				r.Status = model.StatusWarning
			}
		}
	}
	for _, d := range r.Diagnostics {
		if d.Severity == model.SeverityError && r.Status != model.StatusFailed && r.Status != model.StatusError {
			r.Status = model.StatusIncomplete
		}
	}
	snapshot, e := a.snapshot(o.head)
	if e != nil {
		return a.fail(e)
	}
	r.Head = snapshot.Revision
	if initial.Revision != snapshot.Revision {
		r.Checks = append(r.Checks, coverageCheck{"source_stability", model.StatusIncomplete, model.Unknown, "Working-tree source changed during analysis; rerun before using these findings."})
		if r.Status != model.StatusFailed && r.Status != model.StatusError {
			r.Status = model.StatusIncomplete
		}
	} else {
		r.Checks = append(r.Checks, coverageCheck{"source_stability", model.StatusPassed, model.VerifiedStatic, "Indexed source fingerprint was stable at the analysis boundaries; transient runtime changes are not observed."})
	}
	ids := []string{r.Base, r.Head}
	for _, f := range r.Findings {
		ids = append(ids, f.ID)
	}
	r.FeedbackDigest = model.StableID(ids...)
	a.report(r, func(w io.Writer) {
		fmt.Fprintf(w, "Check: %s (%d changed files, %d dependent files)\n", r.Status, len(impact.Changed), len(impact.Affected))
		renderAffected(w, impact)
		for _, c := range r.Checks {
			fmt.Fprintf(w, "%s: %s — %s\n", c.ID, c.Status, c.Explanation)
		}
		for _, f := range r.Findings {
			renderFinding(w, f)
		}
		for _, d := range r.Diagnostics {
			fmt.Fprintf(w, "%s: %s %s\n", d.Severity, d.Path, d.Message)
		}
		fmt.Fprintln(w, "For agent repair feedback: rerun with --json; investigate each finding, repair, and verify again (suggested maximum: 2 attempts).")
	})
	if r.Status == model.StatusError {
		return 2
	}
	if r.Status == model.StatusFailed || o.strict && r.Status == model.StatusIncomplete {
		return 1
	}
	return 0
}
