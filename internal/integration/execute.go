package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"

	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/coverage"
	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/runners"
	"github.com/Jake-Network/radar/internal/testselection"
)

// runVerification executes the explicit command or the selected suite
// against the candidate and records the execution and selection checks.
func runVerification(ctx context.Context, temp string, r *Report, o Options) error {
	explicitCWD := o.CWD
	if explicitCWD == "" {
		explicitCWD = "."
	}
	provider := indexer.NewCommit(temp, r.CandidateCommit)
	defer provider.Close()
	profiles, profileErr := verificationProfiles(ctx, provider)
	if errors.Is(profileErr, context.DeadlineExceeded) {
		explanation := "Verification budget expired while reading runner metadata; no verification command started."
		r.Checks = append(r.Checks, Check{ID: "integration_execution", Status: model.StatusTimeout, Evidence: model.Unknown, Explanation: explanation})
		if o.Suite != "" {
			r.Checks = append(r.Checks, Check{ID: "test_selection", Status: model.StatusIncomplete, Evidence: model.Unknown, Explanation: "Suite selection could not be completed before the verification budget expired."})
		}
		return nil
	}
	if profileErr != nil {
		return fmt.Errorf("runner profiles: %w", profileErr)
	}
	selections := []testselection.Command{{ID: model.StableID("verification-command", explicitCWD, strings.Join(o.Command, "\x00")), Command: o.Command, CWD: explicitCWD, Tier: testselection.TierRequired}}
	if o.Suite != "" {
		selection, err := testselection.Plan(*r.VerificationProposal, r.Changed, o.Suite, o.MaxCommands)
		if err != nil {
			return err
		}
		testselection.ApplyRunnerProfiles(&selection, profiles)
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
	prepared := map[string]evidence.RunnerPreparation{}
	artifacts := []string{}
	type executedCommand struct {
		selection testselection.Command
		options   Options
	}
	executed := []executedCommand{}
	for i, selection := range selections {
		if executionCtx.Err() != nil {
			skipped(selection, "time_budget_exhausted", fmt.Sprintf("the total verification timeout of %s elapsed before this command started", o.Timeout))
			continue
		}
		if profile := profiles.Match(selection.CWD); profile != nil && o.Suite != "" && !planSelected(selection) {
			preparation, seen := prepared[profile.Root]
			if !seen {
				identity, err := evidence.RepositoryIdentity(executionCtx, temp)
				if err != nil {
					return err
				}
				state, err := runtimeState()
				if err != nil {
					return err
				}
				cacheDir, err := pathutil.StatePath(state, "runner-cache")
				if err != nil {
					return err
				}
				preparation, err = evidence.PrepareRunnerBudget(executionCtx, temp, identity, *profile, runners.Cache{Dir: cacheDir, MaxBytes: profiles.MaxCacheBytes}, o.Timeout, func() bool { return o.budget == nil || o.budget.take(1) })
				if err != nil {
					preparation.Status = model.StatusIncomplete
					preparation.Error = evidence.PreparationDiagnostic(temp, err)
					coverageDiagnostic(r, "Declared runner preparation unavailable: "+preparation.Error)
				}
				prepared[profile.Root] = preparation
				artifacts = append(artifacts, preparation.Artifacts...)
				cp := evidence.CandidateCheckpoint{Repository: identity, Base: r.Base, Revision: r.CandidateCommit, Tree: r.CandidateTree, Inputs: r.Inputs}
				inspection, inspectErr := evidence.InspectCandidateSource(executionCtx, temp, cp, artifacts)
				if inspectErr != nil || !inspection.Matches {
					preparation.Status = model.StatusIncomplete
					if inspectErr != nil {
						preparation.Error = "Runner source inspection unavailable: " + evidence.PreparationDiagnostic(temp, inspectErr)
					} else {
						preparation.Error = "Runner setup changed candidate source"
					}
					prepared[profile.Root] = preparation
					coverageDiagnostic(r, preparation.Error+"; selected tests were not run.")
				}
				r.RunnerPreparations = append(r.RunnerPreparations, preparation)
			}
			if preparation.Status != model.StatusPassed {
				skipped(selection, "runner_setup_unavailable", "declared setup did not complete against unchanged candidate source")
				continue
			}
		}
		if o.budget != nil && !o.budget.take(1) {
			skipped(selection, "command_budget_exhausted", "generators, declared setup and selected tests share the command budget")
			continue
		}
		// Explicit commands keep their observed execution-error semantics.
		if why := unavailable(temp, selection); o.Suite != "" && why != "" {
			skipped(selection, "environment_unavailable", why+"; No usable declared setup supplied this runner. Prepare the environment, declare .radar/runners.json setup, or use reviewed plan test_run setup; combined candidates do not link host dependency directories.")
			continue
		}
		o.step(Step{Stage: StageTest, Index: i + 1, Total: len(selections), Command: selection.Command})
		executionOptions := o
		executionOptions.Command = selection.Command
		executionOptions.CWD = selection.CWD
		executionOptions.report = selection.JUnit
		executionOptions.artifacts = append([]string{}, artifacts...)
		if o.ObserveCoverage {
			if profile := profiles.Match(selection.CWD); profile != nil && profile.Coverage != nil {
				executionOptions.coverageReport = profile.Coverage
				executionOptions.artifacts = append(executionOptions.artifacts, path.Join(selection.CWD, profile.Coverage.Report))
			}
		}
		if deadline, ok := executionCtx.Deadline(); ok && time.Until(deadline) < executionOptions.Timeout {
			executionOptions.Timeout = time.Until(deadline)
		}
		// JVM commands can share a report directory across tiers or chunks,
		// and Maven -am also writes upstream reports. Each command needs fresh
		// artifacts from the same immutable candidate, not an earlier run.
		commandRoot := temp
		var isolated *Candidate
		if selection.Framework == "maven" || selection.Framework == "gradle" || executionOptions.coverageReport != nil {
			var err error
			isolated, err = BuildCandidate(executionCtx, temp, r.CandidateCommit, nil)
			if err != nil {
				return fmt.Errorf("isolate report-producing verification: %w", err)
			}
			if isolated.Commit != r.CandidateCommit || isolated.Tree != r.CandidateTree {
				_ = isolated.Close()
				return fmt.Errorf("isolated Java verification candidate does not match the selected commit and tree")
			}
			commandRoot = isolated.Dir
			// The fresh JVM copy needs the same declared dependencies; cache
			// reuse counts as preparation, never observed test evidence.
			if profile := profiles.Match(selection.CWD); profile != nil && !planSelected(selection) {
				identity, prepErr := evidence.RepositoryIdentity(executionCtx, commandRoot)
				state, stateErr := runtimeState()
				cacheDir, cacheErr := pathutil.StatePath(state, "runner-cache")
				if prepErr == nil && stateErr == nil && cacheErr == nil {
					prep, err := evidence.PrepareRunnerBudget(executionCtx, commandRoot, identity, *profile, runners.Cache{Dir: cacheDir, MaxBytes: profiles.MaxCacheBytes}, executionOptions.Timeout, func() bool { return o.budget == nil || o.budget.take(1) })
					prepErr = err
					r.RunnerPreparations = append(r.RunnerPreparations, prep)
					if prep.Status != model.StatusPassed {
						prepErr = fmt.Errorf("runner setup incomplete in isolated JVM candidate")
					}
				}
				if prepErr != nil || stateErr != nil || cacheErr != nil {
					_ = isolated.Close()
					skipped(selection, "runner_setup_unavailable", "declared setup was unavailable in the isolated JVM candidate")
					continue
				}
			}
		}
		ev := execute(executionCtx, commandRoot, *r, executionOptions)
		ev.SelectionID = selection.ID
		if isolated != nil {
			_ = isolated.Close()
		}
		r.Executions = append(r.Executions, ev)
		executed = append(executed, executedCommand{selection: selection, options: executionOptions})
		if ev.Status == model.StatusPassed && ev.SourceAfterExecution == "unchanged" {
			confirmExecutedRunner(r.Selection, selection)
		}
		if ev.SourceAfterExecution != "unchanged" {
			break
		}
	}

	// Complete candidate verification before spending anything on baseline
	// diagnostics. Baselines share only the remaining timeout and commands.
	baselines := baseVerifier{}
	defer baselines.close()
	for i, command := range executed {
		selection, executionOptions := command.selection, command.options
		ev := r.Executions[i]
		if ev.Status == model.StatusFailed {
			var profile *runners.Profile
			if o.Suite != "" && !planSelected(selection) {
				profile = profiles.Match(selection.CWD)
			}
			ev.BaseComparison = baselines.compare(executionCtx, temp, *r, ev, executionOptions, profile, profiles.MaxCacheBytes)
			r.Limitations = append(r.Limitations, ev.BaseComparison.Explanation)
		}
		ev.ID = ""
		data, _ := json.Marshal(ev)
		ev.ID = model.StableID("integration-evidence-v1", string(data))
		r.Executions[i] = ev
		if o.Suite == "" {
			r.Execution = &r.Executions[i]
		}
		if selection.ID != "" {
			r.Checks = append(r.Checks, Check{ID: "test:" + selection.ID, Status: ev.IntegrationStatus(), Evidence: model.ObservedTest, Explanation: "Selected command observed against the candidate; inspect bound execution metadata."})
		}
		executionStatus = aggregateExecution(executionStatus, ev.IntegrationStatus())
		if ev.Status != model.StatusPassed {
			f := model.NewFinding("integration_execution_"+string(ev.Status), fmt.Sprintf("Combined verification command %q in %q returned %s (exit %d).", executionOptions.Command, executionOptions.CWD, ev.Status, ev.ExitCode), model.ObservedTest)
			f.Severity = model.SeverityError
			if ev.BaseComparison != nil && ev.BaseComparison.AllExisting {
				f.Code = "integration_base_failure"
				f.Explanation = "The selected command already fails on base. " + ev.BaseComparison.Explanation
				f.Severity = model.SeverityWarning
			}
			if ev.Status == model.StatusUnknown || ev.Status == model.StatusIncomplete {
				f.Severity = model.SeverityWarning
			}
			// Finding revisions must not mutate locations already hashed into
			// immutable execution evidence.
			for _, l := range ev.Observation.Locations {
				if ev.BaseComparison != nil && ev.BaseComparison.AllExisting {
					continue
				}
				if len(ev.FailurePaths([]string{l.Path})) > 0 {
					f.Locations = append(f.Locations, l)
				}
			}
			for i := range f.Locations {
				f.Locations[i].Revision = r.CandidateCommit
			}
			if len(ev.NewFailedCases()) > 0 {
				f.Explanation += " Failed cases: " + strings.Join(ev.NewFailedCases(), ", ")
			}
			if d := ev.Observation.Diagnosis; d != nil {
				f.Explanation += " Cause: " + d.Message + "."
			}
			f.ID = model.StableID(r.CandidateTree, executionOptions.CWD, strings.Join(executionOptions.Command, "\x00"), strings.Join(ev.Observation.FailedCases, "\x00"), f.Code)
			f.Remediation = executionRemediation(ev)
			if ev.BaseComparison != nil && ev.BaseComparison.AllExisting {
				f.Remediation = "Repair the failures on base, then rerun verification; existing base failures cannot prove this integration."
			}
			f.Verification = "Repeat merge-check --verify with the same command after repair; individual branch results are insufficient."
			r.Findings = append(r.Findings, f)

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

// Preserve manifest validation failures as errors, even if the deadline arrives
// while parsing. Track metadata read deadlines separately from validation.
type verificationMetadata struct {
	indexer.Provider
	timedOut bool
}

func (p *verificationMetadata) Entries(ctx context.Context) ([]indexer.Entry, error) {
	entries, err := p.Provider.Entries(ctx)
	p.timedOut = p.timedOut || err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)
	return entries, err
}

func (p *verificationMetadata) Read(ctx context.Context, entry indexer.Entry, limit int64) ([]byte, error) {
	data, err := p.Provider.Read(ctx, entry, limit)
	p.timedOut = p.timedOut || err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)
	return data, err
}

func verificationProfiles(ctx context.Context, provider indexer.Provider) (runners.Config, error) {
	metadata := &verificationMetadata{Provider: provider}
	profiles, err := runners.Load(ctx, metadata)
	if metadata.timedOut {
		return profiles, context.DeadlineExceeded
	}
	if err == nil && ctx.Err() != nil {
		return profiles, ctx.Err()
	}
	return profiles, err
}

// A planning-time PATH probe cannot establish candidate-local runner
// availability. Only recognized passing execution against unchanged source
// resolves this uncertainty; omitted commands and other coverage gaps remain.
func confirmExecutedRunner(s *testselection.Selection, c testselection.Command) {
	if s == nil {
		return
	}
	blocker := "required command runner unavailable or unverified: " + strings.Join(c.Command, " ")
	remaining := s.Blocking[:0]
	resolved := false
	for _, reason := range s.Blocking {
		if !resolved && !c.ToolAvailable && c.Tier == testselection.TierRequired && reason == blocker {
			resolved = true
			continue
		}
		remaining = append(remaining, reason)
	}
	s.Blocking = remaining
	for i := range s.Commands {
		if s.Commands[i].ID == c.ID {
			s.Commands[i].ToolAvailable = true
		}
	}
	if resolved && len(s.Blocking) == 0 {
		if len(s.Uncovered) > 0 {
			s.Explanation = "Selected commands have a direct relationship to the change, but some changed files have no established test relationship; they are listed as uncovered."
		} else {
			s.Explanation = "Every changed source file has a selected direct or fallback test relationship; this is static evidence, not complete behavioral coverage."
		}
	}
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
		runResult, runErr := evidence.RunCandidateBudget(ctx, root, cp, *o.Plan, o.Command, o.CWD, o.Timeout, func() bool { return o.budget == nil || o.budget.take(1) })
		observeErr = runErr
		if runErr == nil {
			observation = runResult.Observation
		}
		if runErr == nil {
			ev.PlanRecord = &runResult.Record
		}
	} else {
		if o.ObserveCoverage && o.coverageReport != nil {
			observation, ev.observedFiles, observeErr = evidence.ObserveCandidateDeclaredCoverage(ctx, root, o.CWD, o.Command, o.report, *o.coverageReport, o.Timeout)
			if observeErr == nil {
				provider := indexer.NewCommit(root, r.CandidateCommit)
				ev.observedFiles, observeErr = coverage.ResolveOwned(ctx, o.coverageReport.Format, ev.observedFiles, provider)
				_ = provider.Close()
			}
		} else if o.ObserveCoverage {
			observation, ev.observedFiles, observeErr = evidence.ObserveCandidateCoverage(ctx, root, o.CWD, o.Command, o.report, o.Timeout)
		} else {
			observation, observeErr = evidence.ObserveCandidateReport(ctx, root, o.CWD, o.Command, o.report, o.Timeout)
		}
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
	artifacts := append(evidence.CandidateArtifacts(o.Plan), o.artifacts...)
	if o.report != "" && !match {
		artifacts = append(artifacts, path.Join(o.CWD, o.report))
	}
	after, sourceErr := evidence.InspectCandidateSource(sourceCtx, root, cp, artifacts)
	if sourceErr != nil {
		ev.SourceAfterExecution = "unknown"
		if ev.Status != model.StatusFailed {
			ev.Status = model.StatusError
		}
		ev.ExecutionError = evidence.PreparationDiagnostic(root, sourceErr)
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
	if strings.ContainsAny(tool, "/\\") {
		pathTool := tool
		if runtime.GOOS == "windows" {
			pathTool = filepath.ToSlash(tool)
		}
		path, e := pathutil.ResolveInside(dir, pathTool)
		if errors.Is(e, fs.ErrNotExist) {
			if strings.Contains(tool, "node_modules/") {
				return "runner " + tool + " is absent from the private candidate (untracked dependency directories such as node_modules are not copied)"
			}
			return "runner " + tool + " is absent from the private candidate (not committed, or removed by a branch)"
		}
		if e != nil {
			return "runner " + tool + " is outside the candidate"
		}
		info, statErr := os.Stat(path)
		if statErr != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode()&0111 == 0 {
			return "runner " + tool + " is absent from the private candidate (untracked dependency directories such as node_modules are not copied)"
		}
		if why := evidence.WrapperUnavailable(dir, c.Command); why != "" {
			return why
		}
		if runtime.GOOS == "windows" {
			if why := windowsRunnerUnavailable(path); why != "" {
				return why
			}
			if strings.Contains(tool, "\\") {
				return "runner path uses unsupported backslash separators; use snapshot-relative forward slashes"
			}
			return ""
		}
		return ""
	}
	resolved, e := exec.LookPath(tool)
	if e != nil {
		return "runner " + tool + " is not on PATH"
	}
	resolvedCommand := append([]string{}, c.Command...)
	resolvedCommand[0] = resolved
	if why := evidence.WrapperUnavailable(dir, resolvedCommand); why != "" {
		return why
	}
	if runtime.GOOS == "windows" {
		return windowsRunnerUnavailable(resolved)
	}
	return ""
}

// Windows native executables do not carry Unix execute bits. Batch files can
// invoke the command interpreter with parsing rules that differ from argv; keep
// that unsupported boundary explicit instead of relying on implicit shell parsing.
func windowsRunnerUnavailable(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".exe", ".com":
		return ""
	case ".cmd", ".bat":
		return "runner is a Windows batch script; direct batch execution is unsupported (declare an explicit launcher command)"
	default:
		return "runner does not have a supported native Windows executable extension (.exe or .com)"
	}
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
	case d != nil && d.Kind == "python_import_unverified":
		return "Use a runner profile prefix and declared setup to create a Python environment inside the candidate, or deactivate the external virtual environment and remove import paths, editable or local-path installs and import hooks that point outside the candidate and installed copies of repository projects not built from it, then rerun the same gate. The combined source was not tested."
	case d.Environment():
		return fmt.Sprintf("Install or activate %s where you run radar (Radar copies committed files; dependency installation requires declared setup and execution opt-in), then rerun the same gate. The combined source was not tested.", d.Name)
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

func planSelected(c testselection.Command) bool {
	for _, reason := range c.EvidenceReasons {
		if reason.Code == "declared_plan" || strings.HasPrefix(reason.Code, "plan_") {
			return true
		}
	}
	return false
}
