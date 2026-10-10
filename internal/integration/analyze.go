package integration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/contracts"
	"github.com/Jake-Network/radar/internal/discovery"
	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/graph"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
	"github.com/Jake-Network/radar/internal/verification"
)

// reportConflicts records textual merge conflicts. No candidate exists, so no
// contract obligation was analyzed.
func reportConflicts(r *Report, conflicts []string) {
	r.Conflicts = append(r.Conflicts, conflicts...)
	r.Status = model.StatusFailed
	r.Checks = append(r.Checks, Check{ID: "textual_merge", Status: model.StatusFailed, Evidence: model.VerifiedTool, Explanation: "Git reported unmerged paths in the private candidate."})
	f := model.NewFinding("integration_textual_conflict", "Resolve overlapping edits before integration.", model.VerifiedTool)
	f.Severity = model.SeverityError
	f.Remediation = "Reconcile the reported paths on the feature branches and repeat merge-check."
	r.Findings = append(r.Findings, f)
	r.contractUnverified = []string{"combined candidate could not be built because of textual conflicts"}
}

// analyzeContracts checks declared contract configuration and obligations
// between the base and the candidate.
func analyzeContracts(ctx context.Context, temp string, r *Report, o Options) error {
	configurationCheck := Check{ID: "declared_contract_configuration", Status: model.StatusPassed, Evidence: model.VerifiedStatic, Explanation: "Declared contract configuration is absent or readable at both checkpoints."}
	for _, ref := range []string{r.Base, r.CandidateCommit} {
		manifest, loadErr := contracts.LoadManifest(ctx, temp, ref)
		if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
			configurationCheck.Status = model.StatusError
			configurationCheck.Evidence = model.Unknown
			configurationCheck.Explanation = "Declared contract configuration cannot be read or parsed at " + ref + ": " + loadErr.Error()
			continue
		}
		for _, binding := range manifest.Bindings {
			schema, schemaErr := contracts.ReadSchema(ctx, temp, ref, binding.Schema)
			if schemaErr == nil {
				_, schemaErr = contracts.Analyzable(schema, binding.Pointer)
			}
			if schemaErr != nil && configurationCheck.Status != model.StatusError {
				configurationCheck.Status = model.StatusIncomplete
				configurationCheck.Evidence = model.Unknown
				configurationCheck.Explanation = "Declared schema coverage is unavailable for " + binding.ID + " at " + ref + ": " + schemaErr.Error()
			}
		}
	}

	impact, e := contracts.Impact(ctx, temp, r.Base, r.CandidateCommit, verification.ApprovedRetirements(o.Plan)...)
	if e != nil {
		return e
	}
	if unestablished := impact.Unestablished(); configurationCheck.Status == model.StatusPassed && len(unestablished) > 0 {
		configurationCheck.Status = model.StatusIncomplete
		configurationCheck.Evidence = model.Unknown
		configurationCheck.Explanation = "Declared contract obligations are unverified: " + strings.Join(unestablished, "; ")
	}
	r.contractUnverified = impact.Unestablished()
	r.ContractObligations = impact.Obligations
	r.DeclaredBindings = impact.Bindings
	r.Checks = append(r.Checks, configurationCheck)
	r.Findings = append(r.Findings, impact.Findings...)
	r.Diagnostics = append(r.Diagnostics, impact.Diagnostics...)
	r.Checks = append(r.Checks, Check{ID: "declared_contracts", Status: impact.Status, Evidence: model.VerifiedStatic, Explanation: fmt.Sprintf("Analyzed %d of %d declared bindings.", impact.Analyzed, impact.Bindings)})
	return nil
}

// analyzeDiscovered compares discovered producer and consumer relationships.
func analyzeDiscovered(ctx context.Context, temp string, r *Report) error {
	discovered, e := discovery.Compare(ctx, temp, r.Base, r.CandidateCommit)
	if e != nil {
		return e
	}
	r.Findings = append(r.Findings, discovered.Findings...)
	r.Diagnostics = append(r.Diagnostics, discovered.Diagnostics...)
	r.Limitations = append(r.Limitations, discovered.Limitations...)
	r.Checks = append(r.Checks, Check{ID: "discovered_contracts", Status: discovered.Status, Evidence: model.Proposed, Explanation: "Compared supported discovered producer and consumer relationships; candidates remain proposed."})
	return nil
}

// analyzeImpact maps changed files to the files that reach them through
// resolved static import dependencies.
func analyzeImpact(r *Report, snapshot model.Snapshot) error {
	g, e := graph.New(snapshot)
	if e != nil {
		return e
	}
	r.AffectedBy = map[string][]string{}
	for _, changed := range r.Changed {
		if _, ok := g.Nodes[model.FileID(changed)]; !ok {
			continue
		}
		for _, hop := range g.Distances(model.FileID(changed), "DEPENDS_ON", true, 0) {
			if p := model.PathFromID(hop.ID); p != "" && p != changed {
				r.AffectedBy[p] = append(r.AffectedBy[p], changed)
			}
		}
	}
	for p := range r.AffectedBy {
		r.Affected = append(r.Affected, p)
	}
	sort.Strings(r.Affected)
	r.Checks = append(r.Checks, Check{ID: "dependency_compatibility", Status: model.StatusUnknown, Evidence: model.Inferred, Explanation: "Resolved static dependency neighborhood; runtime and build compatibility requires execution."})
	return nil
}

// verifyPlan checks the reviewed plan using only source-intact records from
// this combined candidate.
func verifyPlan(ctx context.Context, temp string, r *Report, p planning.Plan, snapshot model.Snapshot, records []evidence.Record, sourceUnchanged bool) error {
	identity, identityErr := evidence.RepositoryIdentity(ctx, temp)
	if identityErr != nil {
		return identityErr
	}
	cp := evidence.CandidateCheckpoint{Repository: identity, Base: r.Base, Revision: r.CandidateCommit, Tree: r.CandidateTree, Inputs: r.Inputs}
	planReport := verification.VerifyCandidateWithEvidence(ctx, p, snapshot, temp, cp, records, sourceUnchanged)
	r.Plan = &planReport
	r.Findings = append(r.Findings, planReport.Findings...)
	r.Checks = append(r.Checks, Check{ID: "plan_verification", Status: planReport.Status, Evidence: model.VerifiedStatic, Explanation: "Reviewed exact-command criteria use only source-intact records from this combined candidate; unmatched observations remain informational."})
	for _, criterion := range planReport.Checks {
		r.Checks = append(r.Checks, Check{ID: "criterion:" + criterion.ID, Status: criterion.Status, Evidence: criterion.Evidence, Explanation: criterion.Explanation})
	}
	return nil
}
