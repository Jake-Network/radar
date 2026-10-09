// Package integration previews merges in a private object database. It never
// shares writable Git state with the user's repository. This is filesystem
// isolation, not an OS security sandbox; executing candidate code is opt-in.
package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/checkpoint"
	"github.com/Jake-Network/radar/internal/contracts"
	"github.com/Jake-Network/radar/internal/discovery"
	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/gate"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/graph"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/planning"
	"github.com/Jake-Network/radar/internal/testselection"
	"github.com/Jake-Network/radar/internal/verification"
)

type Options struct {
	SuggestTests           bool
	Suite                  string
	CWD                    string
	Policy                 *gate.Policy
	Plan                   *planning.Plan
	Base                   string
	Branches               []string
	Verify, AllowExecution bool
	Command                []string
	Timeout                time.Duration
	PlanDigest             string
	// MaxCommands bounds a selected suite after grouping (default 16).
	MaxCommands int
}
type Check = gate.Check

// ExecutionEvidence binds an observation to the combined tree and ordered
// inputs. Individual branch evidence is deliberately never consumed.
type ExecutionEvidence struct {
	PlanRecord           *evidence.Record              `json:"plan_record,omitempty"`
	ExecutionError       string                        `json:"execution_error,omitempty"`
	SelectionID          string                        `json:"selection_id,omitempty"`
	CWD                  string                        `json:"cwd,omitempty"`
	Observation          evidence.CandidateObservation `json:"observation"`
	ID                   string                        `json:"id"`
	CandidateCommit      string                        `json:"candidate_commit"`
	CandidateTree        string                        `json:"candidate_tree"`
	Base                 string                        `json:"base"`
	Inputs               []string                      `json:"inputs"`
	PlanDigest           string                        `json:"plan_digest,omitempty"`
	Command              []string                      `json:"command"`
	Status               model.Status                  `json:"status"`
	ExitCode             int                           `json:"exit_code"`
	OutputDigest         string                        `json:"output_digest"`
	StartedAt            string                        `json:"started_at"`
	DurationMS           int64                         `json:"duration_ms"`
	SourceAfterExecution string                        `json:"source_after_execution"`
}
type Report struct {
	VerificationProposal *testselection.Proposal `json:"verification_proposal,omitempty"`
	// Selection is the bounded execution plan for a --suite mode, including
	// omitted commands and the reasons a required command did not run.
	Selection       *testselection.Selection `json:"selection,omitempty"`
	Selected        []string                 `json:"selected_tests,omitempty"`
	Executions      []ExecutionEvidence      `json:"executions,omitempty"`
	Gate            gate.Result              `json:"gate"`
	Coverage        []gate.Coverage          `json:"coverage"`
	Plan            *planning.Report         `json:"plan,omitempty"`
	Status          model.Status             `json:"status"`
	Base            string                   `json:"base"`
	Inputs          []string                 `json:"inputs"`
	CandidateCommit string                   `json:"candidate_commit,omitempty"`
	CandidateTree   string                   `json:"candidate_tree,omitempty"`
	Conflicts       []string                 `json:"conflicts"`
	Changed         []string                 `json:"changed"`
	Affected        []string                 `json:"affected"`
	// AffectedBy maps each affected file to the changed files it reaches
	// through inferred import dependencies.
	AffectedBy  map[string][]string `json:"affected_by,omitempty"`
	Checks      []Check             `json:"checks"`
	Findings    []model.Finding     `json:"findings"`
	Diagnostics []model.Diagnostic  `json:"diagnostics"`
	Execution   *ExecutionEvidence  `json:"execution,omitempty"`
	Limitations []string            `json:"limitations"`
	// ContractObligations lists base contract declarations the candidate
	// removed, narrowed, moved or retired; see contracts.ObligationChange.
	ContractObligations []contracts.ObligationChange `json:"contract_obligations,omitempty"`
	contractUnverified  []string
}

func environment() []string {
	var env []string
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			env = append(env, e)
		}
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0", "GIT_ATTR_NOSYSTEM=1", "GIT_NO_LAZY_FETCH=1", "GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z")
}
func command(ctx context.Context, root string, args ...string) *exec.Cmd {
	prefix := []string{"-C", root, "--no-pager", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-c", "core.attributesFile=" + os.DevNull, "-c", "commit.gpgSign=false", "-c", "protocol.allow=never", "-c", "user.name=Radar preview", "-c", "user.email=preview@radar.invalid"}
	c := exec.CommandContext(ctx, "git", append(prefix, args...)...)
	c.Env = environment()
	return c
}
func run(ctx context.Context, root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	c := command(ctx, root, args...)
	var out cappedWriter
	c.Stdout = &out
	c.Stderr = &out
	e := c.Run()
	if e != nil {
		return out.String(), fmt.Errorf("git %s: %w", args[0], e)
	}
	return strings.TrimSpace(out.String()), nil
}

// cappedWriter retains only bounded Git diagnostics.
// Verification output is never emitted: it can contain credentials or secrets.
type cappedWriter struct{ data []byte }

func (w *cappedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if len(w.data) < 8192 {
		remaining := 8192 - len(w.data)
		if len(p) > remaining {
			p = p[:remaining]
		}
		w.data = append(w.data, p...)
	}
	return n, nil
}
func (w *cappedWriter) String() string { return string(w.data) }

func validateTree(ctx context.Context, root, sha string) error {
	entries, e := gitrepo.Entries(ctx, root, sha)
	if e != nil {
		return e
	}
	for _, v := range entries {
		if _, e := pathutil.RepoRelative(v.Path); e != nil {
			return fmt.Errorf("unsafe tree path: %w", e)
		}
		if v.Mode != "100644" && v.Mode != "100755" {
			return fmt.Errorf("preview refuses symlink or submodule: %s", v.Path)
		}
	}
	return nil
}

func Preview(ctx context.Context, root string, o Options) (Report, error) {
	r := Report{Status: model.StatusIncomplete, Inputs: []string{}, Conflicts: []string{}, Changed: []string{}, Affected: []string{}, Checks: []Check{}, Findings: []model.Finding{}, Diagnostics: []model.Diagnostic{}, Limitations: []string{"Static imports do not prove runtime dependency compatibility.", "Only the explicitly selected verification commands are observed; no comprehensive test coverage claim.", "Preview is a private filesystem, not an OS sandbox. Verification code inherits host privileges."}}
	if len(o.Branches) < 1 {
		return r, errors.New("at least one branch required")
	}
	if o.Suite != "" {
		if _, e := testselection.NormalizeMode(o.Suite); e != nil {
			return r, e
		}
	}
	if o.MaxCommands < 0 || o.MaxCommands > 256 {
		return r, errors.New("max commands must be between 1 and 256")
	}
	if o.Suite != "" && len(o.Command) > 0 {
		return r, errors.New("--suite cannot be combined with an explicit command")
	}
	if o.Verify && (!o.AllowExecution || (len(o.Command) == 0 && o.Suite == "")) {
		return r, errors.New("--verify requires --allow-execution and a command after --")
	}
	if !o.Verify && (o.AllowExecution || len(o.Command) > 0 || o.Suite != "") {
		return r, errors.New("execution arguments require --verify")
	}
	if o.Timeout == 0 {
		o.Timeout = 2 * time.Minute
	}
	if o.Timeout <= 0 || o.Timeout > 30*time.Minute {
		return r, errors.New("timeout must be positive and at most 30 minutes")
	}
	if o.Plan != nil {
		o.PlanDigest = planning.Digest(*o.Plan)
	}
	var e error
	r.Base, e = gitrepo.Resolve(ctx, root, o.Base)
	if e != nil {
		return r, e
	}
	for _, ref := range o.Branches {
		sha, e := gitrepo.Resolve(ctx, root, ref)
		if e != nil {
			return r, e
		}
		r.Inputs = append(r.Inputs, sha)
	}
	for _, sha := range append([]string{r.Base}, r.Inputs...) {
		if e = validateTree(ctx, root, sha); e != nil {
			return r, e
		}
	}
	temp, e := os.MkdirTemp("", "radar-integration-")
	if e != nil {
		return r, e
	}
	defer os.RemoveAll(temp)
	if _, e = run(ctx, temp, "init", "--quiet"); e != nil {
		return r, e
	}
	// Copy immutable objects through a pack stream. No alternates, hard links,
	// clone hooks, source config copying, source refs or source objects are written.
	copyCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	producer := command(copyCtx, root, "pack-objects", "--stdout", "--revs")
	producer.Stdin = strings.NewReader(strings.Join(append([]string{r.Base}, r.Inputs...), "\n") + "\n")
	pipe, e := producer.StdoutPipe()
	if e != nil {
		return r, e
	}
	consumer := command(copyCtx, temp, "index-pack", "--stdin")
	consumer.Stdin = pipe
	consumer.Stdout = io.Discard
	consumer.Stderr = io.Discard
	producer.Stderr = io.Discard
	if e = consumer.Start(); e != nil {
		return r, e
	}
	if e = producer.Start(); e != nil {
		_ = pipe.Close()
		_ = consumer.Wait()
		return r, e
	}
	producerErr := producer.Wait()
	consumerErr := consumer.Wait()
	if producerErr != nil || consumerErr != nil {
		return r, errors.New("unable to copy integration objects")
	}
	if _, e = run(ctx, temp, "checkout", "--quiet", "--detach", r.Base); e != nil {
		return r, e
	}
	for _, sha := range r.Inputs {
		_, mergeErr := run(ctx, temp, "merge", "--no-ff", "--no-edit", "--no-verify", sha)
		if mergeErr != nil {
			unmerged, inspectErr := run(ctx, temp, "diff", "--name-only", "--diff-filter=U", "-z")
			if inspectErr != nil {
				return r, inspectErr
			}
			for _, p := range strings.Split(unmerged, "\x00") {
				if p != "" {
					r.Conflicts = append(r.Conflicts, p)
				}
			}
			if len(r.Conflicts) == 0 {
				return r, mergeErr
			}
			sort.Strings(r.Conflicts)
			r.Status = model.StatusFailed
			r.Checks = append(r.Checks, Check{ID: "textual_merge", Status: model.StatusFailed, Evidence: model.VerifiedTool, Explanation: "Git reported unmerged paths in the private candidate."})
			f := model.NewFinding("integration_textual_conflict", "Resolve overlapping edits before integration.", model.VerifiedTool)
			f.Severity = model.SeverityError
			f.Remediation = "Reconcile the reported paths on the feature branches and repeat merge-check."
			r.Findings = append(r.Findings, f)
			// No candidate exists, so no contract obligation was analyzed.
			r.contractUnverified = []string{"combined candidate could not be built because of textual conflicts"}
			applyGate(&r, o)
			return r, nil
		}
	}
	r.CandidateCommit, e = gitrepo.Resolve(ctx, temp, "HEAD")
	if e != nil {
		return r, e
	}
	r.CandidateTree, e = run(ctx, temp, "rev-parse", "HEAD^{tree}")
	if e != nil {
		return r, e
	}
	if e = validateTree(ctx, temp, r.CandidateCommit); e != nil {
		return r, e
	}
	r.Checks = append(r.Checks, Check{ID: "textual_merge", Status: model.StatusPassed, Evidence: model.VerifiedTool, Explanation: "Git combined all selected commits without textual conflicts."})
	r.Changed, e = gitrepo.ChangedFiles(ctx, temp, r.Base, r.CandidateCommit)
	if e != nil {
		return r, e
	}
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
		return r, e
	}
	if unestablished := impact.Unestablished(); configurationCheck.Status == model.StatusPassed && len(unestablished) > 0 {
		configurationCheck.Status = model.StatusIncomplete
		configurationCheck.Evidence = model.Unknown
		configurationCheck.Explanation = "Declared contract obligations are unverified: " + strings.Join(unestablished, "; ")
	}
	r.contractUnverified = impact.Unestablished()
	r.ContractObligations = impact.Obligations
	r.Checks = append(r.Checks, configurationCheck)
	r.Findings = append(r.Findings, impact.Findings...)
	r.Diagnostics = append(r.Diagnostics, impact.Diagnostics...)
	r.Checks = append(r.Checks, Check{ID: "declared_contracts", Status: impact.Status, Evidence: model.VerifiedStatic, Explanation: fmt.Sprintf("Analyzed %d of %d declared bindings.", impact.Analyzed, impact.Bindings)})
	discovered, e := discovery.Compare(ctx, temp, r.Base, r.CandidateCommit)
	if e != nil {
		return r, e
	}
	r.Findings = append(r.Findings, discovered.Findings...)
	r.Diagnostics = append(r.Diagnostics, discovered.Diagnostics...)
	r.Limitations = append(r.Limitations, discovered.Limitations...)
	r.Checks = append(r.Checks, Check{ID: "discovered_contracts", Status: discovered.Status, Evidence: model.Proposed, Explanation: "Compared supported discovered producer and consumer relationships; candidates remain proposed."})
	snapshot, e := checkpoint.Index(ctx, temp, r.CandidateCommit)
	if e != nil {
		return r, e
	}
	r.Diagnostics = append(r.Diagnostics, snapshot.Diagnostics...)

	g, e := graph.New(snapshot)
	if e != nil {
		return r, e
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
	if o.SuggestTests || o.Suite != "" {
		proposal, err := testselection.Recommend(ctx, temp, r.CandidateCommit, snapshot, r.Changed, o.Plan)
		if err != nil {
			return r, err
		}
		r.VerificationProposal = &proposal
	}
	if o.Verify {
		explicitCWD := o.CWD
		if explicitCWD == "" {
			explicitCWD = "."
		}
		selections := []testselection.Command{{ID: model.StableID("verification-command", explicitCWD, strings.Join(o.Command, "\x00")), Command: o.Command, CWD: explicitCWD, Tier: testselection.TierRequired}}
		if o.Suite != "" {
			selection, err := testselection.Plan(*r.VerificationProposal, r.Changed, o.Suite, o.MaxCommands)
			if err != nil {
				return r, err
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
		for _, selection := range selections {
			if executionCtx.Err() != nil {
				skipped(selection, "time_budget_exhausted", fmt.Sprintf("the total verification timeout of %s elapsed before this command started", o.Timeout))
				continue
			}
			// Explicit commands keep their observed execution-error semantics.
			if why := unavailable(temp, selection); o.Suite != "" && why != "" {
				skipped(selection, "environment_unavailable", why+"; Radar does not install dependencies. Prepare the environment or declare a reviewed plan test_run rule (with link for untracked dependency directories).")
				continue
			}
			executionOptions := o
			executionOptions.Command = selection.Command
			executionOptions.CWD = selection.CWD
			if deadline, ok := executionCtx.Deadline(); ok && time.Until(deadline) < executionOptions.Timeout {
				executionOptions.Timeout = time.Until(deadline)
			}
			ev := execute(executionCtx, temp, r, executionOptions)
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
				f.ID = model.StableID(r.CandidateTree, executionOptions.CWD, strings.Join(executionOptions.Command, "\x00"), strings.Join(ev.Observation.FailedCases, "\x00"), f.Code)
				f.Remediation = "Reproduce the supplied command on the combined changes, reconcile producer/consumer assumptions, and repair the failing invariant."
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
	} else {
		r.Checks = append(r.Checks, Check{ID: "integration_execution", Status: model.StatusUnknown, Evidence: model.Unknown, Explanation: "No combined test evidence. Authorize an explicit command or --suite recommended."})
	}

	sourceUnchanged := true
	var records []evidence.Record
	for _, ev := range r.Executions {
		if ev.SourceAfterExecution != "unchanged" {
			sourceUnchanged = false
		}
		if ev.PlanRecord != nil {
			records = append(records, *ev.PlanRecord)
		}
	}
	if !sourceUnchanged {
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
	if o.Plan != nil {
		identity, identityErr := evidence.RepositoryIdentity(ctx, temp)
		if identityErr != nil {
			return r, identityErr
		}
		cp := evidence.CandidateCheckpoint{Repository: identity, Base: r.Base, Revision: r.CandidateCommit, Tree: r.CandidateTree, Inputs: r.Inputs}
		planReport := verification.VerifyCandidateWithEvidence(ctx, *o.Plan, snapshot, temp, cp, records, sourceUnchanged)
		r.Plan = &planReport
		r.Findings = append(r.Findings, planReport.Findings...)
		r.Checks = append(r.Checks, Check{ID: "plan_verification", Status: planReport.Status, Evidence: model.VerifiedStatic, Explanation: "Reviewed exact-command criteria use only source-intact records from this combined candidate; unmatched observations remain informational."})
		for _, criterion := range planReport.Checks {
			r.Checks = append(r.Checks, Check{ID: "criterion:" + criterion.ID, Status: criterion.Status, Evidence: criterion.Evidence, Explanation: criterion.Explanation})
		}
	}

	r.Status = model.StatusPassed
	for _, c := range r.Checks {
		if c.Status == model.StatusError || c.Status == model.StatusTimeout {
			r.Status = model.StatusError
			continue
		}
		if r.Status == model.StatusError {
			continue
		}
		if c.Status == model.StatusFailed {
			r.Status = model.StatusFailed
			continue
		}
		if r.Status != model.StatusFailed && c.Status != model.StatusPassed {
			r.Status = model.StatusIncomplete
		}
	}
	applyGate(&r, o)
	return r, nil
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

func applyGate(r *Report, o Options) {
	checks := append([]gate.Check(nil), r.Checks...)
	noBreaking := gate.NoBreaking(r.Findings, r.contractUnverified)
	for _, c := range r.Checks {
		// Configuration gaps never mask a confirmed incompatibility, except an
		// unreadable configuration, which is an execution-grade error.
		if c.ID == "declared_contract_configuration" && c.Status != model.StatusPassed && (noBreaking.Status != model.StatusFailed && (noBreaking.Status == model.StatusPassed || c.Status == model.StatusError)) {
			noBreaking.Status = c.Status
			noBreaking.Evidence = model.Unknown
			noBreaking.Explanation = c.Explanation
		}
	}
	checks = append(checks, noBreaking)
	p := gate.Policy{Version: 1, Name: "supported-integration", Require: []string{"textual_merge", "no_breaking_contracts"}}
	if o.Verify {
		p.Require = append(p.Require, "integration_execution")
	}
	if o.Policy != nil {
		p = *o.Policy
	}
	r.Gate = gate.Evaluate(p, checks)
	r.Coverage = gate.CoverageFor(r.Checks, r.Limitations)
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

func aggregateExecution(current, next model.Status) model.Status {
	rank := map[model.Status]int{model.StatusPassed: 0, model.StatusUnknown: 1, model.StatusIncomplete: 1, model.StatusBlocked: 1, model.StatusWarning: 1, model.StatusFailed: 4, model.StatusTimeout: 3, model.StatusError: 3}
	if rank[next] > rank[current] {
		return next
	}
	return current
}
