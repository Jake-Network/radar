// Package integration previews merges in a private object database. It never
// shares writable Git state with the user's repository. This is filesystem
// isolation, not an OS security sandbox; executing candidate code is opt-in.
//
// The Candidate built here is the one "candidate" across Radar: evidence pins
// its identity as a CandidateCheckpoint when recording executions, and
// verification accepts only records bound to that exact checkpoint.
package integration

import (
	"context"
	"errors"
	"time"

	"github.com/Jake-Network/radar/internal/contracts"
	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/generated"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
	"github.com/Jake-Network/radar/internal/runners"
	"github.com/Jake-Network/radar/internal/symbols"
	"github.com/Jake-Network/radar/internal/testselection"
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
	ObserveCoverage        bool
	Command                []string
	Timeout                time.Duration
	PlanDigest             string
	// MaxCommands bounds a selected suite after grouping (default 16).
	MaxCommands int
	// AttributionRuns, when positive, reruns each failed command on the base
	// plus subsets of the branches, within the same time budget, spending at
	// most this many executions. It requires Verify. Set zero for Fast Gate,
	// then call RunObservedAttribution separately after publishing its result.
	AttributionRuns int
	// Progress, when set, hears what the gate is doing so a terminal can
	// show it live. It is presentation only and never affects the report.
	Progress func(Step)
	// report is the selected command's runner-written JUnit path.
	report         string
	artifacts      []string
	budget         *commandBudget
	coverageReport *runners.Coverage
}

// Step is one progress notification.
type Step struct {
	Repo  string // workspace repository ID, set by multi-repository callers
	Stage string // StageCombine, StageAnalyze or StageTest
	// Index and Total place a test command (1-based) in the selection.
	Index, Total int
	Command      []string
}

const (
	StageCombine = "combine"
	StageAnalyze = "analyze"
	StageTest    = "test"
	// StageAttribute reruns a failed command on fewer branches.
	StageAttribute = "attribute"
)

func (o Options) step(s Step) {
	if o.Progress != nil {
		o.Progress(s)
	}
}

type Check = gate.Check

// ExecutionEvidence binds an observation to the combined tree and ordered
// inputs. Individual branch evidence is deliberately never consumed.
// BaseComparison records the automatic, budgeted baseline check of a failed
// command. The original candidate observation is retained without alteration.
type BaseComparison struct {
	Execution     *ExecutionEvidence `json:"execution,omitempty"`
	ExistingCases []string           `json:"existing_cases,omitempty"`
	Explanation   string             `json:"explanation"`
	AllExisting   bool               `json:"all_existing"`
}

type ExecutionEvidence struct {
	BaseComparison       *BaseComparison               `json:"base_comparison,omitempty"`
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
	observedFiles        []string
}
type Report struct {
	RunnerPreparations   []evidence.RunnerPreparation `json:"runner_preparations,omitempty"`
	VerificationProposal *testselection.Proposal      `json:"verification_proposal,omitempty"`
	// Selection is the bounded execution plan for a --suite mode, including
	// omitted commands and the reasons a required command did not run.
	Selection           *testselection.Selection `json:"selection,omitempty"`
	Selected            []string                 `json:"selected_tests,omitempty"`
	GeneratedExecutions []generated.Execution    `json:"generated_executions,omitempty"`
	Executions          []ExecutionEvidence      `json:"executions,omitempty"`
	Gate                gate.Result              `json:"gate"`
	Coverage            []gate.Coverage          `json:"coverage"`
	Plan                *planning.Report         `json:"plan,omitempty"`
	Status              model.Status             `json:"status"`
	Base                string                   `json:"base"`
	Inputs              []string                 `json:"inputs"`
	CandidateCommit     string                   `json:"candidate_commit,omitempty"`
	CandidateTree       string                   `json:"candidate_tree,omitempty"`
	Conflicts           []string                 `json:"conflicts"`
	Changed             []string                 `json:"changed"`
	Affected            []string                 `json:"affected"`
	// AffectedBy maps each affected file to the changed files it reaches
	// through inferred import dependencies.
	AffectedBy  map[string][]string `json:"affected_by,omitempty"`
	Checks      []Check             `json:"checks"`
	Findings    []model.Finding     `json:"findings"`
	Diagnostics []model.Diagnostic  `json:"diagnostics"`
	Execution   *ExecutionEvidence  `json:"execution,omitempty"`
	// Attributions rerun failed commands on subsets of the branches.
	Attributions []Attribution `json:"observed_attributions,omitempty"`
	Limitations  []string      `json:"limitations"`
	// ContractObligations lists base contract declarations the candidate
	// removed, narrowed, moved or retired; see contracts.ObligationChange.
	ContractObligations []contracts.ObligationChange `json:"contract_obligations,omitempty"`
	// DeclaredBindings counts the declared contract bindings analyzed, so a
	// passing contract check with nothing declared is not shown as a check.
	DeclaredBindings   int `json:"-"`
	contractUnverified []string
}

func newReport() Report {
	return Report{Status: model.StatusIncomplete, Inputs: []string{}, Conflicts: []string{}, Changed: []string{}, Affected: []string{}, Checks: []Check{}, Findings: []model.Finding{}, Diagnostics: []model.Diagnostic{}, Limitations: []string{"Static imports do not prove runtime dependency compatibility.", "Only the explicitly selected verification commands are observed; no comprehensive test coverage claim.", "Preview is a private filesystem, not an OS sandbox. Verification code inherits host privileges."}}
}

// validate checks execution options and fills defaults. It does not use the
// candidate, so invocation errors precede any Git work.
func validate(o Options) (Options, error) {
	if o.Suite != "" {
		if _, e := testselection.NormalizeMode(o.Suite); e != nil {
			return o, e
		}
	}
	if o.MaxCommands < 0 || o.MaxCommands > 256 {
		return o, errors.New("max commands must be between 1 and 256")
	}
	if o.Suite != "" && len(o.Command) > 0 {
		return o, errors.New("--suite cannot be combined with an explicit command")
	}
	if o.AttributionRuns < 0 || o.AttributionRuns > 64 {
		return o, errors.New("attribution runs must be between 0 and 64")
	}
	if o.ObserveCoverage && !o.Verify {
		return o, errors.New("--observe-coverage requires explicit execution authorization")
	}
	if o.AttributionRuns > 0 && !o.Verify {
		return o, errors.New("attribution reruns failed commands and requires --verify")
	}
	if o.Verify && (!o.AllowExecution || (len(o.Command) == 0 && o.Suite == "")) {
		return o, errors.New("--verify requires --allow-execution and a command after --")
	}
	if !o.Verify && (o.AllowExecution || len(o.Command) > 0 || o.Suite != "") {
		return o, errors.New("execution arguments require --verify")
	}
	if o.Timeout == 0 {
		o.Timeout = 2 * time.Minute
	}
	if o.Timeout <= 0 || o.Timeout > 30*time.Minute {
		return o, errors.New("timeout must be positive and at most 30 minutes")
	}
	if o.Plan != nil {
		o.PlanDigest = planning.Digest(*o.Plan)
	}
	return o, nil
}

// Preview builds the candidate for o.Base and o.Branches, analyzes it and
// removes the private repository before returning.
func Preview(ctx context.Context, root string, o Options) (Report, error) {
	r := newReport()
	if len(o.Branches) < 1 {
		return r, errors.New("at least one branch required")
	}
	o, e := validate(o)
	if e != nil {
		return r, e
	}
	o.step(Step{Stage: StageCombine})
	c, e := BuildCandidate(ctx, root, o.Base, o.Branches)
	defer c.Close()
	if e != nil {
		r.Base = c.Base
		r.Inputs = c.Inputs
		return r, e
	}
	return Analyze(ctx, c, o)
}

// Analyze checks a built candidate: contracts, discovered relationships,
// dependency impact, test selection and, when authorized, execution. o.Base
// and o.Branches are ignored; the candidate's pinned inputs are analyzed.
func Analyze(ctx context.Context, c *Candidate, o Options) (Report, error) {
	r := newReport()
	o, e := validate(o)
	if e != nil {
		return r, e
	}
	if c == nil || (c.Dir == "" && len(c.Conflicts) == 0) {
		return r, errors.New("candidate is not available")
	}
	temp := c.Dir
	if o.Verify && o.budget == nil {
		max := o.MaxCommands
		if max == 0 {
			max = testselection.DefaultMaxCommands
		}
		o.budget = &commandBudget{left: max, parent: ctx, timeout: o.Timeout}
		defer o.budget.close()
	}
	r.Base = c.Base
	r.Inputs = append(r.Inputs, c.Inputs...)
	if len(c.Conflicts) > 0 {
		reportConflicts(&r, c.Conflicts)
		applyGate(&r, o)
		return r, nil
	}
	r.CandidateCommit, r.CandidateTree = c.Commit, c.Tree
	o.step(Step{Stage: StageAnalyze})
	r.Checks = append(r.Checks, Check{ID: "textual_merge", Status: model.StatusPassed, Evidence: model.VerifiedTool, Explanation: "Git combined all selected commits without textual conflicts."})
	r.Changed, e = gitrepo.ChangedFiles(ctx, temp, r.Base, r.CandidateCommit)
	if e != nil {
		return r, e
	}
	if e = analyzeContracts(ctx, temp, &r, o); e != nil {
		return r, e
	}
	if e = analyzeGenerated(ctx, temp, &r, o); e != nil {
		return r, e
	}
	if e = analyzeMigrations(ctx, temp, &r); e != nil {
		return r, e
	}
	if e = analyzeTreeRules(ctx, temp, &r); e != nil {
		return r, e
	}
	parseCache := indexer.NewParseCache()
	snapshot, e := indexAndDiscover(ctx, temp, &r, parseCache)
	if e != nil {
		return r, e
	}
	r.Diagnostics = append(r.Diagnostics, snapshot.Diagnostics...)
	symbolResult, symbolErr := symbols.AnalyzeWithCache(ctx, temp, r.Base, r.CandidateCommit, r.Changed, &snapshot, parseCache)
	if symbolErr != nil {
		return r, symbolErr
	}
	if symbolResult.Applicable {
		r.Findings = append(r.Findings, symbolResult.Findings...)
		r.Diagnostics = append(r.Diagnostics, symbolResult.Diagnostics...)
		r.Checks = append(r.Checks, Check{ID: symbols.CheckID, Status: symbolResult.SupportedStatus, Evidence: symbolResult.SupportedEvidence, Explanation: symbolResult.Explanation})
	}
	if symbolResult.UnknownCount > 0 {
		if !symbolResult.Applicable {
			r.Diagnostics = append(r.Diagnostics, symbolResult.Diagnostics...)
		}
		r.Checks = append(r.Checks, Check{ID: "symbol_resolution", Status: model.StatusUnknown, Evidence: model.Unknown, Explanation: "Some symbol relationships are unsupported or ambiguous; inspect diagnostics. These are excluded from the supported symbol-conflict scope."})
	}
	if e = analyzeImpact(&r, snapshot); e != nil {
		return r, e
	}
	if o.SuggestTests || o.Suite != "" {
		observed := loadCoverage(ctx, c.Source, temp, &r)
		proposal, err := testselection.RecommendObserved(ctx, temp, r.CandidateCommit, snapshot, r.Changed, o.Plan, observed)
		if err != nil {
			return r, err
		}
		r.VerificationProposal = &proposal
	}
	executionCtx := ctx
	if o.Verify {
		executionCtx = o.budget.context()
		if e = runVerification(executionCtx, temp, &r, o); e != nil {
			return r, e
		}
	} else {
		r.Checks = append(r.Checks, Check{ID: "integration_execution", Status: model.StatusUnknown, Evidence: model.Unknown, Explanation: "No combined test evidence. Authorize an explicit command or --suite recommended."})
	}
	records, sourceUnchanged := executionRecords(r.Executions)
	if !sourceUnchanged {
		invalidateExecutions(&r, o)
	}
	if o.ObserveCoverage && sourceUnchanged && r.Selection != nil {
		for _, ev := range r.Executions {
			for _, selected := range r.Selection.Commands {
				if selected.ID == ev.SelectionID {
					saveCoverage(ctx, temp, &r, ev, selected.TestFiles, ev.observedFiles)
					break
				}
			}
		}
	}
	if o.Plan != nil {
		if e = verifyPlan(ctx, temp, &r, *o.Plan, snapshot, records, sourceUnchanged); e != nil {
			return r, e
		}
	}
	r.Status = overallStatus(r.Checks)
	applyGate(&r, o)
	if o.AttributionRuns > 0 && sourceUnchanged {
		r.Attributions, e = RunObservedAttribution(executionCtx, c, r, o)
		if e != nil {
			return r, e
		}
		if len(r.Attributions) > 0 {
			r.Limitations = append(r.Limitations, attributionLimitation)
			r.Coverage = gate.CoverageFor(r.Checks, r.Limitations)
		}
	}
	return r, nil
}

// overallStatus folds check statuses: error dominates, then failure, then
// any non-passing check makes the report incomplete.
func overallStatus(checks []Check) model.Status {
	status := model.StatusPassed
	for _, c := range checks {
		if c.Status == model.StatusError || c.Status == model.StatusTimeout {
			status = model.StatusError
			continue
		}
		if status == model.StatusError {
			continue
		}
		if c.Status == model.StatusFailed {
			status = model.StatusFailed
			continue
		}
		if status != model.StatusFailed && c.Status != model.StatusPassed {
			status = model.StatusIncomplete
		}
	}
	return status
}
