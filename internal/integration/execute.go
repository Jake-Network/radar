package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/testselection"
)

// runVerification executes the explicit command or the selected suite
// against the candidate and records the execution and selection checks.
func runVerification(ctx context.Context, temp string, r *Report, o Options) error {
	explicitCWD := o.CWD
	if explicitCWD == "" {
		explicitCWD = "."
	}
	selections := []testselection.Command{{ID: model.StableID("verification-command", explicitCWD, strings.Join(o.Command, "\x00")), Command: o.Command, CWD: explicitCWD, Tier: testselection.TierRequired}}
	if o.Suite != "" {
		selection, err := testselection.Plan(*r.VerificationProposal, r.Changed, o.Suite, o.MaxCommands)
		if err != nil {
			return err
		}
		r.Selection = &selection
		selections = selection.Commands
	}
	executionCtx, cancelExecution := context.WithTimeout(ctx, o.Timeout)
	defer cancelExecution()
	executionStatus := model.StatusPassed
	if len(selections) == 0 {
		executionStatus = model.StatusUnknown
	}
	for _, selection := range selections {
		r.Selected = append(r.Selected, selection.ID)
	}
	// skipped records a selected command that did not run. A required one
	// blocks the gate; it is a coverage gap, never a test failure or pass.
	requiredSkipped := 0
	skipped := func(selection testselection.Command, reason, explanation string) {
		if r.Selection != nil {
			r.Selection.Omitted = append(r.Selection.Omitted, testselection.Omission{ID: selection.ID, Command: selection.Command, CWD: selection.CWD, TestFiles: selection.TestFiles, Tier: selection.Tier, Reason: reason, Explanation: explanation})
		}
		status := model.StatusBlocked
		if selection.Tier == testselection.TierRequired || o.Suite == "" {
			requiredSkipped++
			if r.Selection != nil {
				r.Selection.Blocking = append(r.Selection.Blocking, reason+": "+strings.Join(selection.Command, " "))
			}
		} else {
			status = model.StatusWarning
		}
		r.Checks = append(r.Checks, Check{ID: "test:" + selection.ID, Status: status, Evidence: model.Unknown, Explanation: "Not executed (" + reason + "): " + explanation})
	}
	for i, selection := range selections {
		if executionCtx.Err() != nil {
			skipped(selection, "time_budget_exhausted", fmt.Sprintf("the total verification timeout of %s elapsed before this command started", o.Timeout))
			continue
		}
		// Explicit commands keep their observed execution-error semantics.
		if why := unavailable(temp, selection); o.Suite != "" && why != "" {
			skipped(selection, "environment_unavailable", why+"; Radar does not install dependencies. Prepare the environment or declare a reviewed plan test_run rule (with link for untracked dependency directories).")
			continue
		}
		o.step(Step{Stage: StageTest, Index: i + 1, Total: len(selections), Command: selection.Command})
		executionOptions := o
		executionOptions.Command = selection.Command
		executionOptions.CWD = selection.CWD
		if deadline, ok := executionCtx.Deadline(); ok && time.Until(deadline) < executionOptions.Timeout {
			executionOptions.Timeout = time.Until(deadline)
		}
		ev := execute(executionCtx, temp, *r, executionOptions)
		ev.SelectionID = selection.ID
		// Selection identity is also bound into the serialized artifact digest.
		ev.ID = ""
		data, _ := json.Marshal(ev)
		ev.ID = model.StableID("integration-evidence-v1", string(data))
		r.Executions = append(r.Executions, ev)
		if o.Suite == "" {
			r.Execution = &r.Executions[len(r.Executions)-1]
		}
		if selection.ID != "" {
			r.Checks = append(r.Checks, Check{ID: "test:" + selection.ID, Status: ev.Status, Evidence: model.ObservedTest, Explanation: "Selected command observed against the candidate; inspect bound execution metadata."})
		}
		executionStatus = aggregateExecution(executionStatus, ev.Status)
		if ev.Status != model.StatusPassed {
			f := model.NewFinding("integration_execution_"+string(ev.Status), fmt.Sprintf("Combined verification command %q in %q returned %s (exit %d).", executionOptions.Command, executionOptions.CWD, ev.Status, ev.ExitCode), model.ObservedTest)
			f.Severity = model.SeverityError
			if ev.Status == model.StatusUnknown || ev.Status == model.StatusIncomplete {
				f.Severity = model.SeverityWarning
			}
			f.Locations = ev.Observation.Locations
			for i := range f.Locations {
				f.Locations[i].Revision = r.CandidateCommit
			}
			if len(ev.Observation.FailedCases) > 0 {
				f.Explanation += " Failed cases: " + strings.Join(ev.Observation.FailedCases, ", ")
			}
			if d := ev.Observation.Diagnosis; d != nil {
				f.Explanation += " Cause: " + d.Message + "."
			}
			f.ID = model.StableID(r.CandidateTree, executionOptions.CWD, strings.Join(executionOptions.Command, "\x00"), strings.Join(ev.Observation.FailedCases, "\x00"), f.Code)
			f.Remediation = executionRemediation(ev)
			f.Verification = "Repeat merge-check --verify with the same command after repair; individual branch results are insufficient."
			r.Findings = append(r.Findings, f)
		}
		if ev.SourceAfterExecution != "unchanged" {
			break
		}
	}
	if len(r.Executions) < len(selections) && executionStatus == model.StatusPassed {
		executionStatus = model.StatusIncomplete
	}
	if r.Selection != nil {
		for _, omitted := range r.Selection.Omitted {
			if omitted.Reason == "budget_exceeded" && omitted.Tier == testselection.TierRequired {
				requiredSkipped++
			}
		}
		if len(r.Selection.Blocking) > 0 && executionStatus == model.StatusPassed {
			executionStatus = model.StatusIncomplete
		}
	}
	if requiredSkipped > 0 && executionStatus == model.StatusPassed {
		executionStatus = model.StatusIncomplete
	}
	explanation := fmt.Sprintf("Executed %d of %d selected commands against the combined candidate; output is hashed, not exposed.", len(r.Executions), len(selections))
	if requiredSkipped > 0 {
		explanation += fmt.Sprintf(" %d required command(s) were omitted or not executed; required verification is incomplete.", requiredSkipped)
	}
	r.Checks = append(r.Checks, Check{ID: "integration_execution", Status: executionStatus, Evidence: model.ObservedTest, Explanation: explanation})
	if r.Selection != nil {
		status, message := model.StatusPassed, r.Selection.Explanation
		if len(r.Selection.Blocking) > 0 || len(r.Selection.Uncovered) > 0 {
			status = model.StatusIncomplete
		}
		r.Checks = append(r.Checks, Check{ID: "test_selection", Status: status, Evidence: model.Inferred, Explanation: fmt.Sprintf("Mode %s selected %d of %d candidate commands (%d omitted, %d uncovered changed files). %s", r.Selection.Mode, len(r.Selection.Commands), r.Selection.Candidates, len(r.Selection.Omitted), len(r.Selection.Uncovered), message)})
	}
	return nil
}

func execute(ctx context.Context, root string, r Report, o Options) ExecutionEvidence {
	started := time.Now()
	ev := ExecutionEvidence{CandidateCommit: r.CandidateCommit, CandidateTree: r.CandidateTree, Base: r.Base, Inputs: r.Inputs, PlanDigest: o.PlanDigest, CWD: o.CWD, Command: append([]string(nil), o.Command...), Status: model.StatusPassed, ExitCode: 0, StartedAt: started.UTC().Format(time.RFC3339Nano)}

	identity, identityErr := evidence.RepositoryIdentity(ctx, root)
	cp := evidence.CandidateCheckpoint{Repository: identity, Base: r.Base, Revision: r.CandidateCommit, Tree: r.CandidateTree, Inputs: r.Inputs}
	observation := evidence.CandidateObservation{Status: model.StatusError, ExitCode: -1}
	var observeErr error
	match := false
	var declarationErr error
	if o.Plan != nil {
		match, declarationErr = evidence.CandidateDeclaration(*o.Plan, o.Command, o.CWD)
	}
	if identityErr != nil {
		observeErr = identityErr
	} else if declarationErr != nil {
		observeErr = declarationErr
	} else if match {
		runResult, runErr := evidence.RunCandidate(ctx, root, cp, *o.Plan, o.Command, o.CWD, o.Timeout)
		observeErr = runErr
		if runErr == nil {
			observation = runResult.Observation
		}
		if runErr == nil {
			ev.PlanRecord = &runResult.Record
		}
	} else {
		observation, observeErr = evidence.ObserveCandidateAt(ctx, root, o.CWD, o.Command, o.Timeout)
	}
	ev.Observation = observation
	ev.Status = observation.Status
	ev.ExitCode = observation.ExitCode
	ev.OutputDigest = observation.OutputDigest
	if observeErr != nil {
		ev.Status = model.StatusError
		ev.ExitCode = -1
		ev.ExecutionError = observeErr.Error()
		ev.Observation.Status = model.StatusError
		ev.Observation.ExitCode = -1
	}
	ev.DurationMS = time.Since(started).Milliseconds()
	sourceCtx, sourceCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer sourceCancel()
	after, sourceErr := evidence.InspectCandidateSource(sourceCtx, root, cp, evidence.CandidateArtifacts(o.Plan))
	if sourceErr != nil {
		ev.SourceAfterExecution = "unknown"
		if ev.Status != model.StatusFailed {
			ev.Status = model.StatusError
		}
		ev.ExecutionError = sourceErr.Error()
	} else if !after.Matches {
		ev.SourceAfterExecution = "modified"
		if ev.Status == model.StatusPassed {
			ev.Status = model.StatusIncomplete
		}
	} else {
		ev.SourceAfterExecution = "unchanged"
	}

	data, _ := json.Marshal(ev)
	ev.ID = model.StableID("integration-evidence-v1", string(data))
	return ev
}

// executionRecords returns the plan records of the executions and whether
// every execution left the candidate source intact.
func executionRecords(executions []ExecutionEvidence) ([]evidence.Record, bool) {
	sourceUnchanged := true
	var records []evidence.Record
	for _, ev := range executions {
		if ev.SourceAfterExecution != "unchanged" {
			sourceUnchanged = false
		}
		if ev.PlanRecord != nil {
			records = append(records, *ev.PlanRecord)
		}
	}
	return records, sourceUnchanged
}

// invalidateExecutions downgrades every execution and check once any command
// modified the candidate source: later observations are no longer trusted.
func invalidateExecutions(r *Report, o Options) {
	for i := range r.Executions {
		ev := &r.Executions[i]
		if ev.PlanRecord != nil {
			invalid := evidence.InvalidateCandidateRecord(*ev.PlanRecord)
			ev.PlanRecord = &invalid
		}
		if ev.Status == model.StatusPassed {
			ev.Status = model.StatusIncomplete
		}
		if ev.SourceAfterExecution == "unchanged" {
			ev.SourceAfterExecution = "invalidated_by_suite"
		}
		if ev.Observation.Status == model.StatusPassed {
			ev.Observation.Status = model.StatusIncomplete
		}
		ev.ID = ""
		data, _ := json.Marshal(ev)
		ev.ID = model.StableID("integration-evidence-v1", string(data))
	}
	if o.Suite == "" && len(r.Executions) > 0 {
		r.Execution = &r.Executions[0]
	}
	for i := range r.Checks {
		if (strings.HasPrefix(r.Checks[i].ID, "test:") || r.Checks[i].ID == "integration_execution") && r.Checks[i].Status == model.StatusPassed {
			r.Checks[i].Status = model.StatusIncomplete
		}
	}
}

// unavailable reports why a selected runner cannot start in the candidate,
// using only static checks: no repository code or package script runs.
func unavailable(root string, c testselection.Command) string {
	if len(c.Command) == 0 {
		return "empty command"
	}
	dir, err := pathutil.ResolveInside(root, c.CWD)
	if c.CWD == "" || c.CWD == "." {
		dir, err = root, nil
	}
	if err != nil {
		return "working directory " + c.CWD + " is outside the candidate"
	}
	if info, e := os.Stat(dir); e != nil || !info.IsDir() {
		return "working directory " + c.CWD + " does not exist in the candidate"
	}
	tool := c.Command[0]
	if strings.Contains(tool, "/") {
		path, e := pathutil.ResolveInside(dir, tool)
		if e != nil {
			return "runner " + tool + " is outside the candidate"
		}
		if info, e := os.Stat(path); e != nil || info.IsDir() || info.Mode()&0111 == 0 {
			return "runner " + tool + " is absent from the private candidate (untracked dependency directories such as node_modules are not copied)"
		}
		return ""
	}
	if _, e := exec.LookPath(tool); e != nil {
		return "runner " + tool + " is not on PATH"
	}
	return ""
}

// executionRemediation fits the repair advice to what was observed: only a
// failed test or a failed build points at the combined source; a missing runner, a timeout or
// an unrecognized result points at the environment or the command.
func executionRemediation(ev ExecutionEvidence) string {
	d := ev.Observation.Diagnosis
	switch {
	case ev.Status == model.StatusFailed && d != nil && d.Kind == "build_failed":
		return "The branches do not compile together. Open the reported location in a checkout of the combined branches, then either update the branch that still uses the old code or restore what another branch renamed or removed. Commit and rerun the same gate."
	case ev.Status == model.StatusFailed:
		return "Reproduce the supplied command on the combined changes, reconcile producer/consumer assumptions, and repair the failing invariant."
	case d.Environment():
		return fmt.Sprintf("Install or activate %s where you run radar (Radar copies only committed files and never installs dependencies), then rerun the same gate. The combined source was not tested.", d.Name)
	case d != nil:
		return fmt.Sprintf("If %s is a third-party dependency, install it where you run radar and rerun the same gate; if it is part of this repository, check whether a branch renamed or removed it.", d.Name)
	case ev.Status == model.StatusTimeout:
		return "The command did not finish in time. Check it for a hang, or raise --timeout if the suite is slow, then rerun the same gate."
	case ev.ExecutionError != "":
		return "Radar could not run or check the command (" + ev.ExecutionError + "). Fix that, then rerun the same gate."
	case ev.Status == model.StatusError:
		return "The command stopped before reporting any test result. Run it yourself in a checkout of the combined branches to see its output (Radar keeps only a digest), fix the environment or the command, then rerun the same gate."
	}
	return "The command finished without a test result Radar recognizes. Make sure it runs tests with a supported runner or writes a declared JUnit report, then rerun the same gate."
}

func aggregateExecution(current, next model.Status) model.Status {
	rank := map[model.Status]int{model.StatusPassed: 0, model.StatusUnknown: 1, model.StatusIncomplete: 1, model.StatusBlocked: 1, model.StatusWarning: 1, model.StatusFailed: 4, model.StatusTimeout: 3, model.StatusError: 3}
	if rank[next] > rank[current] {
		return next
	}
	return current
}
