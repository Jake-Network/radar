package integration

import (
	"context"
	"fmt"
	"time"

	"github.com/Jake-Network/radar/internal/generated"
	"github.com/Jake-Network/radar/internal/model"
)

// analyzeGenerated adds an honest static source/output warning and, only
// with explicit --run authorization, regenerates in another private copy.
// Regeneration can never alter the candidate used by later verification.
func analyzeGenerated(ctx context.Context, temp string, r *Report, o Options) error {
	g, e := generated.Analyze(ctx, temp, r.Base, r.CandidateCommit, r.Changed)
	if e != nil || !g.Applicable {
		return e
	}
	r.Findings = append(r.Findings, g.Findings...)
	r.Diagnostics = append(r.Diagnostics, g.Diagnostics...)
	check := Check{ID: generated.CheckID, Status: g.Status, Evidence: g.Evidence, Explanation: g.Explanation}
	if !o.Verify || !o.AllowExecution || len(g.Rules) == 0 {
		r.Checks = append(r.Checks, check)
		return nil
	}
	check.Status = model.StatusPassed
	check.Evidence = model.VerifiedTool
	check.Explanation = "All declared generators completed with identical output bytes in isolated candidates."
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	max := o.MaxCommands
	if max <= 0 {
		max = 16
	}
	for i, rule := range g.Rules {
		if i >= max || o.budget != nil && !o.budget.take(1) {
			check.Evidence = model.Unknown
			if check.Status != model.StatusFailed {
				check.Status = model.StatusIncomplete
			}
			check.Explanation = "Declared regeneration commands exceeded the execution budget."
			break
		}
		runCtx := ctx
		if o.budget != nil {
			runCtx = o.budget.context()
		}
		c, e := BuildCandidate(runCtx, temp, r.Base, []string{r.CandidateCommit})
		if e != nil {
			return e
		}
		if len(c.Conflicts) > 0 {
			c.Close()
			return fmt.Errorf("cannot construct isolated regeneration candidate")
		}
		run, e := generated.Run(runCtx, c.Dir, rule, timeout)
		run.CandidateCommit = r.CandidateCommit
		run.CandidateTree = r.CandidateTree
		r.GeneratedExecutions = append(r.GeneratedExecutions, run)
		c.Close()
		if e != nil {
			check.Evidence = model.Unknown
			if check.Status != model.StatusFailed {
				check.Status = model.StatusIncomplete
			}
			check.Explanation = "Generator " + rule.Name + " could not be observed: " + e.Error()
			continue
		}
		observedEvidence := model.VerifiedTool
		if run.Observation.OutputDigest == "" {
			observedEvidence = model.Unknown
			check.Evidence = model.Unknown
		}
		r.Checks = append(r.Checks, Check{ID: "generated:" + rule.Name, Status: run.Status, Evidence: observedEvidence, Explanation: run.Explanation})
		if run.Status == model.StatusFailed {
			check.Status = model.StatusFailed
			check.Explanation = "A declared generator failed or changed its committed outputs."
			r.Findings = append(r.Findings, model.Finding{ID: model.StableID("generated-drift", r.CandidateTree, rule.Name), Code: "generated_output_drift", Severity: model.SeverityError, Evidence: model.VerifiedTool, Explanation: run.Explanation, Remediation: "Run the declared generator on the owning branch and commit the regenerated outputs."})
		} else if run.Status != model.StatusPassed && check.Status != model.StatusFailed {
			check.Status = model.StatusIncomplete
			check.Explanation = run.Explanation
		}
	}
	if check.Status == model.StatusFailed {
		check.Evidence = model.VerifiedTool
	}
	r.Checks = append(r.Checks, check)
	return nil
}
