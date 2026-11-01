package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/testselection"
	"github.com/Jake-Network/radar/internal/typecheck"
)

// TypecheckExecution binds one type-check observation to the combined tree.
// Status is the integration result after the base comparison: failed only
// when the combined source has an error base does not have.
type TypecheckExecution struct {
	ID       string   `json:"id"`
	TargetID string   `json:"target_id"`
	Tool     string   `json:"tool"`
	Root     string   `json:"root"`
	CWD      string   `json:"cwd"`
	Command  []string `json:"command"`
	// ExecutionCommand records fixed compiler instrumentation when it adds
	EnvironmentNames   []string `json:"environment_names,omitempty"`
	CompilerCacheScope string   `json:"compiler_cache_scope,omitempty"`
	// arguments to Command; base comparison recreates its own instrumentation.
	ExecutionCommand []string                       `json:"execution_command,omitempty"`
	CandidateCommit  string                         `json:"candidate_commit"`
	CandidateTree    string                         `json:"candidate_tree"`
	Base             string                         `json:"base"`
	Inputs           []string                       `json:"inputs"`
	Status           model.Status                   `json:"status"`
	Observation      evidence.TypecheckObservation  `json:"observation"`
	BaseComparison   *TypecheckBaseComparison       `json:"base_comparison,omitempty"`
	NewDiagnostics   []evidence.TypecheckDiagnostic `json:"new_diagnostics,omitempty"`
	// HiddenImports are imports of modules a branch deleted that mypy is
	// configured to ignore when missing, found by reading the checked files.
	// They are inferred, not tool output, and keep the run from passing.
	HiddenImports        []HiddenImport `json:"hidden_missing_imports,omitempty"`
	SourceAfterExecution string         `json:"source_after_execution"`
	ExecutionError       string         `json:"execution_error,omitempty"`
	StartedAt            string         `json:"started_at"`
	DurationMS           int64          `json:"duration_ms"`
	compiled             map[string]bool
	compiledErr          error
}

// HiddenImport is a checked file's import of a deleted project module.
type HiddenImport = evidence.MypyMissingImport

// TypecheckBaseComparison records the same command on the base. Compared is
// false when base could not be checked; the candidate failure then stands.
// Unresolved counts candidate errors that cannot be told apart from errors
// the tool hid on base (go vet reports one type error per package).
type TypecheckBaseComparison struct {
	// Command is the base command when it differs: a Python command is
	// given only the files that exist on base.
	Command     []string                       `json:"command,omitempty"`
	Preparation *evidence.RunnerPreparation    `json:"preparation,omitempty"`
	Observation *evidence.TypecheckObservation `json:"observation,omitempty"`
	Compared    bool                           `json:"compared"`
	New         int                            `json:"new"`
	Existing    int                            `json:"existing"`
	Unresolved  int                            `json:"unresolved"`
	Explanation string                         `json:"explanation"`
}

// TypecheckOmission is a selected target that did not run. It blocks the
// type-check requirement; it is never a pass or a failure.
type TypecheckOmission struct {
	TargetID    string   `json:"target_id"`
	Command     []string `json:"command"`
	CWD         string   `json:"cwd"`
	Reason      string   `json:"reason"`
	Explanation string   `json:"explanation"`
}

// StageTypecheck runs a type-check command on the combined tree.
const StageTypecheck = "typecheck"

const maxTypecheckLocations = 20

// runTypecheck type-checks the projects owning the changed files in a
// private copy of the candidate, so a tool that writes files can never change
// what later tests observe. It requires explicit execution opt-in.
func runTypecheck(ctx context.Context, temp string, r *Report, o Options) error {
	if !o.Typecheck || !o.Verify || !o.AllowExecution {
		return nil
	}
	provider := indexer.NewCommit(temp, r.CandidateCommit)
	plan, err := typecheck.Select(ctx, provider, r.Changed)
	profiles, profileErr := typecheckProfiles(ctx, provider, &plan, r.Changed)
	provider.Close()
	if profileErr != nil {
		return fmt.Errorf("type-check profiles: %w", profileErr)
	}
	if err != nil {
		if ctx.Err() != nil {
			r.Checks = append(r.Checks, Check{ID: typecheck.CheckID, Status: model.StatusTimeout, Evidence: model.Unknown, Explanation: "The verification budget expired while choosing type-check targets; nothing was type-checked."})
			return nil
		}
		return fmt.Errorf("type-check targets: %w", err)
	}
	r.TypecheckGaps = plan.Gaps
	r.TypecheckRequested = true
	r.TypecheckUnconfigured = plan.Unconfigured
	if len(plan.Unconfigured) > 0 {
		r.Limitations = append(r.Limitations, typecheckUnconfiguredLimitation(plan.Unconfigured))
	}
	defer func() {
		if len(r.TypecheckGaps) > 0 {
			r.Limitations = append(r.Limitations, typecheckGapLimitation(r.TypecheckGaps))
		}
	}()
	local := make([]evidence.GoModule, 0, len(plan.LocalModules))
	for _, m := range plan.LocalModules {
		local = append(local, evidence.GoModule{Path: m.Path, Dir: m.Dir})
	}
	if len(plan.Targets) == 0 {
		if len(plan.Gaps) > 0 {
			// Changed source exists that nothing could type-check.
			r.Checks = append(r.Checks, typecheckCheck(model.StatusPassed, r, 0))
			return nil
		}
		// Nothing supported to type-check is not a pass: no check is
		// recorded, so no requirement can be satisfied by it.
		r.Limitations = append(r.Limitations, "Type checking was requested, but no changed file belongs to a Go module or to a Python project with mypy or ruff configuration; nothing was type-checked.")
		return nil
	}
	aggregate := model.StatusPassed
	omit := func(t typecheck.Target, reason, explanation string) {
		r.TypecheckOmitted = append(r.TypecheckOmitted, TypecheckOmission{TargetID: t.ID, Command: t.Command, CWD: t.CWD, Reason: reason, Explanation: explanation})
		r.Checks = append(r.Checks, Check{ID: "typecheck:" + t.ID, Status: model.StatusBlocked, Evidence: model.Unknown, Explanation: "Not executed (" + reason + "): " + explanation})
		aggregate = aggregateExecution(aggregate, model.StatusIncomplete)
	}
	c, err := BuildCandidate(ctx, temp, r.CandidateCommit, nil)
	if err != nil {
		if ctx.Err() != nil {
			for _, t := range plan.Targets {
				omit(t, "time_budget_exhausted", "the verification budget expired before the type-check copy was ready")
			}
			r.Checks = append(r.Checks, typecheckCheck(aggregate, r, len(plan.Targets)))
			return nil
		}
		return fmt.Errorf("isolate type checking: %w", err)
	}
	defer c.Close()
	if c.Commit != r.CandidateCommit || c.Tree != r.CandidateTree {
		return errors.New("isolated type-check candidate does not match the selected commit and tree")
	}
	identity, err := evidence.RepositoryIdentity(ctx, c.Dir)
	if err != nil {
		return err
	}
	cp := evidence.CandidateCheckpoint{Repository: identity, Base: r.Base, Revision: r.CandidateCommit, Tree: r.CandidateTree, Inputs: r.Inputs}
	preparation := newTypecheckPreparation(profiles)
	tainted := false
	type run struct {
		index  int
		target typecheck.Target
	}
	var ran []run
	cppChanged, cppCompiled := map[string]bool{}, map[string]bool{}
	for i, t := range plan.Targets {
		switch {
		case ctx.Err() != nil:
			omit(t, "time_budget_exhausted", fmt.Sprintf("the total verification timeout of %s elapsed before this command started", o.Timeout))
			continue
		case tainted:
			omit(t, "candidate_source_changed", "an earlier type-check command changed the private copy, so later results would not describe the committed candidate")
			continue
		}
		var why, explanation string
		t, why, explanation = preparation.prepare(ctx, c.Dir, cp, t, r, o)
		if why != "" {
			omit(t, why, explanation)
			if why == "runner_setup_unavailable" {
				tainted = true
			}
			continue
		}
		var probe evidence.MypyProbe
		if t.Tool == typecheck.ToolMypy {
			var err error
			t, probe, err = mypyTarget(ctx, c.Dir, t, o.Timeout)
			if err != nil {
				omit(t, "environment_unavailable", err.Error())
				continue
			}
		}
		if o.budget != nil && !o.budget.take(1) {
			omit(t, "command_budget_exhausted", "type checking, generators, declared setup and selected tests share the command budget")
			continue
		}
		if why := unavailable(c.Dir, testselection.Command{Command: t.Command, CWD: t.CWD}); why != "" {
			omit(t, "environment_unavailable", why)
			continue
		}
		o.step(Step{Stage: StageTypecheck, Index: i + 1, Total: len(plan.Targets), Command: t.Command})
		ev := observeTypecheck(ctx, c.Dir, cp, t, local, *r, o.Timeout)
		if ev.SourceAfterExecution != "unchanged" {
			tainted = true
		}
		if t.Tool == typecheck.ToolMypy && (ev.Status == model.StatusPassed || ev.Status == model.StatusFailed) {
			recordUnchecked(r, c.Dir, t, probe.Sources, nil)
			ev.HiddenImports = append([]HiddenImport(nil), probe.HiddenImports[:min(len(probe.HiddenImports), maxTypecheckLocations)]...)
			if len(probe.HiddenImports) > 0 && ev.Status == model.StatusPassed {
				ev.Status = model.StatusIncomplete
			}
			if len(probe.Hidden) > 0 {
				r.Limitations = append(r.Limitations, fmt.Sprintf("mypy in %s ignores missing imports of %s; deleted-module references are checked statically and are inferred, not tool diagnostics.", t.CWD, strings.Join(probe.Hidden[:min(len(probe.Hidden), 5)], ", ")))
			}
		}
		if t.Tool == "cpp" {
			for _, f := range t.Changed {
				cppChanged[f] = true
			}
			if ev.SourceAfterExecution == "unchanged" {
				for f := range ev.compiled {
					cppCompiled[f] = true
				}
			}
		} else {
			recordUncompiled(r, c.Dir, t, ev)
		}
		recordDeclaredTypecheckCoverage(r, c.Dir, t, &ev)
		r.TypecheckExecutions = append(r.TypecheckExecutions, ev)
		ran = append(ran, run{len(r.TypecheckExecutions) - 1, t})
	}
	if len(cppChanged) > 0 {
		var changed []string
		for f := range cppChanged {
			changed = append(changed, f)
		}
		sort.Strings(changed)
		recordUncompiled(r, c.Dir, typecheck.Target{Tool: "cpp", Changed: changed}, TypecheckExecution{compiled: cppCompiled})
	}
	base := typecheckBase{local: local, preparation: newTypecheckPreparation(profiles), gone: func(p string) bool {
		_, err := os.Lstat(filepath.Join(c.Dir, filepath.FromSlash(p)))
		return errors.Is(err, os.ErrNotExist)
	}}
	defer base.close()
	for _, done := range ran {
		ev := &r.TypecheckExecutions[done.index]
		if ev.Status == model.StatusFailed {
			ev.BaseComparison = base.compare(ctx, temp, *r, *ev, done.target, o)
			ev.Status, ev.NewDiagnostics = settleTypecheck(*ev, base.gone)
		}
		ev.ID = ""
		data, _ := json.Marshal(ev)
		ev.ID = model.StableID("typecheck-evidence-v1", string(data))
		evidenceKind := model.VerifiedTool
		if ev.Status != model.StatusPassed && ev.Status != model.StatusFailed {
			evidenceKind = model.Unknown
		}
		r.Checks = append(r.Checks, Check{ID: "typecheck:" + ev.TargetID, Status: ev.Status, Evidence: evidenceKind, Explanation: typecheckExplanation(*ev)})
		aggregate = aggregateExecution(aggregate, ev.Status)
		if ev.Status != model.StatusPassed {
			r.Findings = append(r.Findings, typecheckFinding(*ev, *r))
		}
	}
	r.Checks = append(r.Checks, typecheckCheck(aggregate, r, len(plan.Targets)))
	return nil
}

func observeTypecheck(ctx context.Context, dir string, cp evidence.CandidateCheckpoint, t typecheck.Target, local []evidence.GoModule, r Report, timeout time.Duration) TypecheckExecution {
	started := time.Now()
	ev := TypecheckExecution{TargetID: t.ID, Tool: t.Tool, Root: t.Root, CWD: t.CWD, Command: append([]string(nil), t.Command...), CandidateCommit: r.CandidateCommit, CandidateTree: r.CandidateTree, Base: r.Base, Inputs: append([]string{}, r.Inputs...), StartedAt: started.UTC().Format(time.RFC3339Nano)}
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	scope := evidence.TypecheckScope{GoModules: local, PythonPackages: t.Packages, PythonModules: t.Modules}
	argv := t.Command
	jvmInventory := ""
	if t.Tool == "java" && len(argv) > 0 && (filepath.Base(argv[0]) == "mvn" || filepath.Base(argv[0]) == "mvnw") {
		if err := evidence.ClearMavenTypecheckInventory(ctx, dir); err != nil {
			ev.Status, ev.Observation.Status = model.StatusIncomplete, model.StatusIncomplete
			ev.ExecutionError = "fresh Maven compiler inputs could not be established"
			ev.SourceAfterExecution, ev.ExecutionError = inspectTypecheckSource(ctx, dir, cp, ev.ExecutionError, t.Artifacts)
			return ev
		}
	}
	if t.Tool == "java" {
		var artifacts []string
		var err error
		argv, jvmInventory, artifacts, err = evidence.PrepareJVMTypecheck(dir, t.CWD, t.Command)
		t.Artifacts = append(append([]string(nil), t.Artifacts...), artifacts...)
		if err != nil {
			ev.Status, ev.Observation.Status = model.StatusError, model.StatusError
			ev.ExecutionError = "the JVM compiler input observer could not be prepared"
			ev.SourceAfterExecution, ev.ExecutionError = inspectTypecheckSource(ctx, dir, cp, ev.ExecutionError, t.Artifacts)
			return ev
		}
		if strings.Join(argv, "\x00") != strings.Join(t.Command, "\x00") {
			ev.ExecutionCommand = append([]string(nil), argv...)
		}
	}
	runOptions := evidence.TypecheckOptions{}
	if t.Tool == "cargo" {
		target, prepareErr := evidence.PrepareCargoTarget(ctx, dir)
		if prepareErr != nil {
			ev.Status, ev.Observation.Status = model.StatusError, model.StatusError
			ev.ExecutionError = "the source-bound Cargo compiler cache could not be prepared"
			ev.SourceAfterExecution, ev.ExecutionError = inspectTypecheckSource(ctx, dir, cp, ev.ExecutionError, t.Artifacts)
			return ev
		}
		runOptions.CargoTargetDir = target
		ev.EnvironmentNames = []string{"CARGO_TARGET_DIR"}
		ev.CompilerCacheScope = cp.Tree
	}
	obs, err := evidence.ObserveTypecheckWithOptions(ctx, dir, t.CWD, t.Tool, scope, argv, max(timeout, time.Millisecond), runOptions)
	ev.Observation, ev.Status = obs, obs.Status
	if err != nil {
		ev.Status, ev.ExecutionError = model.StatusError, err.Error()
		ev.Observation.Status = model.StatusError
	}
	if t.Tool == typecheck.ToolGo && (len(t.Changed) > 0 || t.Custom) && (ev.Status == model.StatusPassed || ev.Status == model.StatusFailed) {
		// The toolchain that ran says which changed files it compiled;
		// build constraints and cgo depend on its environment, not Radar's.
		remaining := timeout - time.Since(started)
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < remaining {
			remaining = time.Until(deadline)
		}
		if remaining > 0 {
			ev.compiled, ev.compiledErr = evidence.GoTypecheckFiles(ctx, dir, t.CWD, t.Command, remaining)
		} else {
			ev.compiledErr = errors.New("time budget exhausted")
		}
	}
	if (t.Tool == "cargo" || t.Tool == "java") && (len(t.Changed) > 0 || t.Custom) && (ev.Status == model.StatusPassed || ev.Status == model.StatusFailed) {
		if t.Tool == "cargo" {
			ev.compiled, ev.compiledErr = evidence.CargoArtifactFiles(dir, runOptions.CargoTargetDir, obs.CargoArtifacts())
		} else if jvmInventory != "" {
			ev.compiled, ev.compiledErr = evidence.JVMTypecheckFiles(dir, jvmInventory)
		} else {
			ev.compiled, ev.compiledErr = evidence.CompiledTypecheckFiles(dir, t.Tool)
		}
	}
	if t.Tool == typecheck.ToolTSC && (len(t.Changed) > 0 || t.Custom) && (ev.Status == model.StatusPassed || ev.Status == model.StatusFailed) {
		ev.compiled, ev.compiledErr = evidence.TypeScriptSources(ctx, dir, t.CWD, t.Command, typecheckRemaining(ctx, timeout-time.Since(started)))
	}
	if t.Tool == "cpp" && (len(t.Changed) > 0 || t.Custom) && (ev.Status == model.StatusPassed || ev.Status == model.StatusFailed) {
		ev.compiled, ev.compiledErr = evidence.CPPCompiledFiles(ctx, dir, t.CWD, t.Command, typecheckRemaining(ctx, timeout-time.Since(started)))
	}
	if t.Tool == typecheck.ToolRuff && (ev.Status == model.StatusPassed || ev.Status == model.StatusFailed) {
		ev.compiled, ev.compiledErr = evidence.RuffCheckedFiles(ctx, dir, t.CWD, t.Command, typecheckRemaining(ctx, timeout-time.Since(started)))
	}
	if ev.compiledErr != nil && ev.Status == model.StatusPassed {
		ev.Status = model.StatusIncomplete
	}
	ev.DurationMS = time.Since(started).Milliseconds()
	ev.SourceAfterExecution, ev.ExecutionError = inspectTypecheckSource(ctx, dir, cp, ev.ExecutionError, t.Artifacts)
	switch ev.SourceAfterExecution {
	case "unchanged":
	case "modified":
		// An observation of a changed tree describes neither the candidate's
		// passing nor its failing: both are invalid.
		ev.Status = model.StatusIncomplete
	default:
		ev.Status = model.StatusError
	}
	return ev
}

// recordUncompiled adds a gap for each changed Go file of t that exists in
// the candidate but that the type-checking toolchain did not compile.
func recordUncompiled(r *Report, dir string, t typecheck.Target, ev TypecheckExecution) {
	if ev.compiled == nil && ev.compiledErr == nil {
		return
	}
	seen := map[string]bool{}
	for _, g := range r.TypecheckGaps {
		seen[g.Path] = true
	}
	for _, f := range t.Changed {
		if seen[f] {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil || !info.Mode().IsRegular() {
			continue
		}
		reason := ""
		switch {
		case ev.compiledErr != nil:
			if t.Tool == typecheck.ToolGo {
				reason = "go list could not confirm that go vet compiled it"
			} else {
				reason = "the compiler could not confirm which source files it checked"
			}
		case !ev.compiled[f]:
			if t.Tool == typecheck.ToolGo {
				reason = "go vet ./... did not compile it here (build constraints or custom tags, cgo without a C compiler, or a directory the pattern skips)"
			} else {
				reason = "the compiler did not report this changed source file as an input"
			}
		}
		if reason != "" {
			seen[f] = true
			r.TypecheckGaps = append(r.TypecheckGaps, typecheck.Gap{Path: f, Reason: reason})
		}
	}
	sort.Slice(r.TypecheckGaps, func(i, j int) bool { return r.TypecheckGaps[i].Path < r.TypecheckGaps[j].Path })
}

func inspectTypecheckSource(ctx context.Context, dir string, cp evidence.CandidateCheckpoint, executionError string, artifacts ...[]string) (string, string) {
	sourceCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	var excluded []string
	for _, paths := range artifacts {
		excluded = append(excluded, paths...)
	}
	after, err := evidence.InspectCandidateSource(sourceCtx, dir, cp, excluded)
	switch {
	case err != nil:
		if executionError == "" {
			executionError = evidence.PreparationDiagnostic(dir, err)
		}
		return "unknown", executionError
	case !after.Matches:
		return "modified", executionError
	}
	return "unchanged", executionError
}

// typecheckBase shares one private base checkout across comparisons. Once a
// command changes it, it is not used again.
type typecheckBase struct {
	candidate   *Candidate
	initialized bool
	unavailable string
	local       []evidence.GoModule
	preparation *typecheckPreparation
	gone        func(string) bool
}

func (b *typecheckBase) close() {
	if b.candidate != nil {
		_ = b.candidate.Close()
	}
}

func (b *typecheckBase) compare(ctx context.Context, temp string, r Report, ev TypecheckExecution, t typecheck.Target, o Options) *TypecheckBaseComparison {
	cmp := &TypecheckBaseComparison{Explanation: "Base comparison unavailable; every candidate type error is retained."}
	switch {
	case ctx.Err() != nil:
		cmp.Explanation += " Verification time budget exhausted."
		return cmp
	case !b.initialized:
		b.initialized = true
		var err error
		if b.candidate, err = BuildCandidate(ctx, temp, r.Base, nil); err != nil {
			b.unavailable = "Could not build the private base candidate."
		}
	}
	if b.unavailable != "" {
		cmp.Explanation += " " + b.unavailable
		return cmp
	}
	if _, err := pathutil.RepoRelative(ev.CWD); err != nil && ev.CWD != "." {
		cmp.Explanation += " The type-checked directory is not a repository path."
		return cmp
	}
	if info, err := os.Lstat(filepath.Join(b.candidate.Dir, filepath.FromSlash(ev.CWD))); err != nil || !info.IsDir() {
		// A moved project may have carried its errors along, so a missing
		// directory does not show that base is free of them.
		cmp.Explanation += " The type-checked directory does not exist on base."
		return cmp
	}
	command := t.LogicalCommand
	if len(command) == 0 {
		command = t.Command
	}
	if len(t.Files) > 0 {
		files := baseFiles(b.candidate.Dir, t)
		if len(files) == 0 && t.Tool == typecheck.ToolRuff {
			// ruff checks each file alone, and none of these existed on
			// base, as themselves or as files a branch moved.
			cmp.Compared, cmp.New = true, len(ev.Observation.All())
			cmp.Explanation = "ruff checks each file alone, and none of the checked files exist on base; every candidate error is new."
			return cmp
		}
		if len(files) == 0 {
			cmp.Explanation += " None of the checked paths exist on base."
			return cmp
		}
		prefix := command[:len(command)-len(t.Files)]
		command = append(append([]string(nil), prefix...), files...)
		if strings.Join(command, "\x00") != strings.Join(ev.Command, "\x00") {
			cmp.Command = command
		}
	}
	if o.budget != nil && !o.budget.take(1) {
		cmp.Explanation += " Shared command budget exhausted."
		return cmp
	}
	identity, err := evidence.RepositoryIdentity(ctx, b.candidate.Dir)
	if err != nil {
		cmp.Explanation += " Base repository identity unavailable."
		return cmp
	}
	cp := evidence.CandidateCheckpoint{Repository: identity, Base: b.candidate.Base, Revision: b.candidate.Commit, Tree: b.candidate.Tree, Inputs: b.candidate.Inputs}
	target := t
	target.Command, target.LogicalCommand = command, command
	if b.preparation != nil {
		var reason, why string
		target, reason, why = b.preparation.prepare(ctx, b.candidate.Dir, cp, target, &r, o)
		cmp.Preparation = b.preparation.last
		if reason != "" {
			cmp.Explanation += " " + why
			return cmp
		}
	}
	if target.Tool == typecheck.ToolMypy {
		var err error
		target, _, err = mypyTarget(ctx, b.candidate.Dir, target, o.Timeout)
		if err != nil {
			cmp.Explanation += " mypy could not verify its base search paths."
			return cmp
		}
	}
	baseRun := observeTypecheck(ctx, b.candidate.Dir, cp, target, b.local, Report{Base: b.candidate.Base, Inputs: b.candidate.Inputs, CandidateCommit: b.candidate.Commit, CandidateTree: b.candidate.Tree}, o.Timeout)
	cmp.Observation = &baseRun.Observation
	if baseRun.SourceAfterExecution != "unchanged" {
		b.unavailable = "Base type checking changed source or source integrity is unknown."
		cmp.Explanation += " " + b.unavailable
		return cmp
	}
	switch baseRun.Status {
	case model.StatusPassed:
		cmp.Compared, cmp.New = true, len(ev.Observation.All())
		cmp.Explanation = "The same command passes on base; every candidate error is new."
	case model.StatusFailed:
		newDiags, existing, unresolved := compareTypecheck(baseRun.Observation.Partial, ev.Observation.All(), baseRun.Observation.All(), b.gone)
		cmp.Compared, cmp.New, cmp.Existing, cmp.Unresolved = true, len(newDiags), existing, unresolved
		cmp.Explanation = fmt.Sprintf("The same command already fails on base: %d candidate error(s) are new, %d also occur on base", len(newDiags), existing)
		if unresolved > 0 {
			cmp.Explanation += fmt.Sprintf(", and %d cannot be told apart from errors the tool hides on base", unresolved)
		}
		cmp.Explanation += "."
	default:
		cmp.Explanation += fmt.Sprintf(" The same command returned %s on base.", baseRun.Status)
	}
	if cmp.Compared && cmp.Command != nil {
		cmp.Explanation += " On base it was given only the files that exist there."
	}
	return cmp
}

// compareTypecheck matches candidate diagnostics to base diagnostics by
// fingerprint, as a multiset. When the base run was partial (go vet reports
// only the first type error of a package and skips its importers; mypy
// stops at a syntax error), an unmatched candidate error may have been
// hidden on base: it is unresolved, never new.
func compareTypecheck(hides bool, candidate, base []evidence.TypecheckDiagnostic, gone func(path string) bool) (fresh []evidence.TypecheckDiagnostic, existing, unresolved int) {
	remaining := map[string]int{}
	for _, d := range base {
		remaining[d.Fingerprint]++
	}
	var unmatched []evidence.TypecheckDiagnostic
	for _, d := range candidate {
		if remaining[d.Fingerprint] > 0 {
			remaining[d.Fingerprint]--
			existing++
			continue
		}
		unmatched = append(unmatched, d)
	}
	// A base error in a file the combined tree no longer has may have moved
	// with the file: the same message in another file matches it. That can
	// only lower a failure to an unverified result, never pass one.
	moved := map[string]int{}
	for _, d := range base {
		if remaining[d.Fingerprint] > 0 && d.MessageKey() != "" && gone != nil && gone(d.Path) {
			remaining[d.Fingerprint]--
			moved[d.MessageKey()]++
		}
	}
	for _, d := range unmatched {
		switch {
		case d.MessageKey() != "" && moved[d.MessageKey()] > 0:
			moved[d.MessageKey()]--
			existing++
		case hides:
			unresolved++
		default:
			fresh = append(fresh, d)
		}
	}
	return fresh, existing, unresolved
}

// settleTypecheck turns a failed observation into the integration result.
// gone reports a repository path the combined tree does not have.
func settleTypecheck(ev TypecheckExecution, gone func(string) bool) (model.Status, []evidence.TypecheckDiagnostic) {
	cmp := ev.BaseComparison
	all := ev.Observation.All()
	if cmp == nil || !cmp.Compared {
		// Unavailable base evidence cannot clear a failure, and cannot show
		// which errors are new either.
		return model.StatusFailed, nil
	}
	if cmp.Observation == nil || cmp.Observation.Status == model.StatusPassed {
		return model.StatusFailed, bounded(all)
	}
	fresh, _, _ := compareTypecheck(cmp.Observation.Partial, all, cmp.Observation.All(), gone)
	if len(fresh) == 0 {
		// Base already fails this way; new errors cannot be ruled out.
		return model.StatusIncomplete, nil
	}
	return model.StatusFailed, bounded(fresh)
}

// baseFiles is what a Python command is given on base: its paths and the
// paths only base has (files a branch deleted or moved), where they exist.
func baseFiles(baseDir string, t typecheck.Target) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range append(append([]string(nil), t.Files...), t.BaseOnly...) {
		if seen[f] {
			continue
		}
		seen[f] = true
		full, err := pathutil.ResolveInside(baseDir, filepath.ToSlash(filepath.Join(t.CWD, f)))
		if err != nil {
			continue
		}
		if _, err := os.Lstat(full); err == nil {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// mypyTarget resolves import scope and inventory with the actual launcher,
// configuration and search bases, independently for candidate and base.
func mypyTarget(ctx context.Context, dir string, t typecheck.Target, timeout time.Duration) (typecheck.Target, evidence.MypyProbe, error) {
	var probe evidence.MypyProbe
	if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	if timeout <= 0 {
		return t, probe, errors.New("time budget exhausted before mypy inventory")
	}
	probe, err := evidence.RunMypyProbeCommand(ctx, dir, t.CWD, t.Command, t.Deleted, timeout)
	if err != nil {
		return t, probe, errors.New("mypy could not confirm its checked sources and search paths")
	}
	if len(probe.Outside) > 0 {
		return t, probe, errors.New("mypy searches for project code outside the private candidate; configure repository-relative search paths")
	}
	t.Modules = typecheck.ModulesUnder(probe.Bases, t.Paths)
	// Top-level ownership is also derived from the real search bases. A
	// nested directory named like a dependency does not make it local.
	t.Packages = nil
	var names []string
	for _, m := range t.Modules {
		if !strings.Contains(m, ".") {
			names = append(names, m)
		}
	}
	if name := evidence.MypyShadow(probe.PackageDirs, names); name != "" {
		return t, probe, fmt.Errorf("mypy can read an installed copy of project package %s outside the private candidate; remove that shadow copy before verifying the combined source", name)
	}
	return t, probe, nil
}

// recordUnchecked adds a gap for each changed file of a mypy target that
// exists in the candidate but that mypy does not check.
func recordUnchecked(r *Report, dir string, t typecheck.Target, sources map[string]string, sourcesErr error) {
	seen := map[string]bool{}
	for _, g := range r.TypecheckGaps {
		seen[g.Path] = true
	}
	for _, f := range t.Changed {
		_, checked := sources[f]
		if seen[f] || sourcesErr == nil && checked {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil || !info.Mode().IsRegular() {
			continue
		}
		reason := "mypy does not check it (its configuration leaves it out or ignores its errors, or a .pyi stub beside it is checked instead)"
		if sourcesErr != nil {
			reason = "mypy could not report which files it checks"
		}
		seen[f] = true
		r.TypecheckGaps = append(r.TypecheckGaps, typecheck.Gap{Path: f, Reason: reason})
	}
	sort.Slice(r.TypecheckGaps, func(i, j int) bool { return r.TypecheckGaps[i].Path < r.TypecheckGaps[j].Path })
}

// shortCommand is the argv for explanations, with a long file list cut.
func shortCommand(argv []string) string {
	const shown = 12
	if len(argv) <= shown {
		return strings.Join(argv, " ")
	}
	return strings.Join(argv[:shown], " ") + fmt.Sprintf(" … (+%d more)", len(argv)-shown)
}

func bounded(d []evidence.TypecheckDiagnostic) []evidence.TypecheckDiagnostic {
	return append([]evidence.TypecheckDiagnostic(nil), d[:min(len(d), maxTypecheckLocations)]...)
}

func typecheckCheck(status model.Status, r *Report, targets int) Check {
	if status == model.StatusPassed && len(r.TypecheckGaps) > 0 {
		status = model.StatusIncomplete
	}
	evidenceKind := model.VerifiedTool
	if status != model.StatusPassed && status != model.StatusFailed {
		evidenceKind = model.Unknown
	}
	explanation := fmt.Sprintf("Type-checked %d of %d target(s) on the combined candidate in a private copy; output is hashed, not exposed. A type-check pass is not observed behavior.", len(r.TypecheckExecutions), targets)
	if len(r.TypecheckOmitted) > 0 {
		explanation += fmt.Sprintf(" %d target(s) did not run; type checking is incomplete.", len(r.TypecheckOmitted))
	}
	if len(r.TypecheckGaps) > 0 {
		explanation += fmt.Sprintf(" %d changed source file(s) were not type-checked, so type checking is incomplete.", len(r.TypecheckGaps))
	}
	return Check{ID: typecheck.CheckID, Status: status, Evidence: evidenceKind, Explanation: explanation}
}

func typecheckExplanation(ev TypecheckExecution) string {
	command := shortCommand(ev.Command)
	switch ev.Status {
	case model.StatusPassed:
		return fmt.Sprintf("%s in %s reported no type errors on the combined candidate.", command, ev.CWD)
	case model.StatusFailed:
		if ev.BaseComparison != nil && ev.BaseComparison.Compared {
			return fmt.Sprintf("%s in %s reports %d type error(s) the base does not have.", command, ev.CWD, ev.BaseComparison.New)
		}
		return fmt.Sprintf("%s in %s reports %d type error(s); base comparison was unavailable, so some may already exist on base.", command, ev.CWD, ev.Observation.DiagnosticCount)
	}
	if len(ev.HiddenImports) > 0 {
		return fmt.Sprintf("%s in %s did not establish that imports of deleted project modules are valid; inspect inferred missing imports.", command, ev.CWD)
	}
	if ev.BaseComparison != nil && ev.BaseComparison.Compared {
		return fmt.Sprintf("%s in %s fails on the combined candidate, but every located error also occurs on base or may be hidden there; new errors cannot be ruled out.", command, ev.CWD)
	}
	return fmt.Sprintf("%s in %s returned %s; the combined source was not type-checked to completion.", command, ev.CWD, ev.Status)
}

func typecheckFinding(ev TypecheckExecution, r Report) model.Finding {
	command := shortCommand(ev.Command)
	base := ev.BaseComparison != nil && ev.BaseComparison.Compared
	var f model.Finding
	switch {
	case ev.Status == model.StatusFailed:
		located := ev.NewDiagnostics
		if base {
			f = model.NewFinding("typecheck_failed", fmt.Sprintf("Type checking %q in %q reports %d error(s) in the combined source that base does not have.", command, ev.CWD, ev.BaseComparison.New), model.VerifiedTool)
		} else {
			located = bounded(ev.Observation.All())
			f = model.NewFinding("typecheck_failed", fmt.Sprintf("Type checking %q in %q reports %d error(s) in the combined source; base comparison was unavailable, so some may already exist on base.", command, ev.CWD, ev.Observation.DiagnosticCount), model.VerifiedTool)
		}
		f.Severity = model.SeverityError
		f.Remediation = "The branches do not type-check together. Open the reported location in a checkout of the combined branches, then update the branch that still uses the old code or restore what another branch renamed or removed. Commit and rerun the same gate."
		for _, d := range located {
			f.Locations = append(f.Locations, model.Provenance{Path: d.Path, Line: d.Line, Revision: r.CandidateCommit, Method: "observed_typecheck_error", Evidence: model.VerifiedTool})
		}
	case ev.Status == model.StatusIncomplete && ev.SourceAfterExecution == "unchanged" && len(ev.HiddenImports) > 0:
		f = model.NewFinding("typecheck_hidden_missing_import", "The combined source imports a deleted project module; mypy did not establish that this import resolves. These references are inferred from syntax, not tool failures.", model.Inferred)
		for _, imp := range ev.HiddenImports {
			f.Locations = append(f.Locations, model.Provenance{Path: imp.Path, Line: imp.Line, Revision: r.CandidateCommit, Method: "python_deleted_module_import", Evidence: model.Inferred})
		}
		f.Remediation = "Update imports after deleting or moving local modules, or restore the missing modules; then rerun the same gate. Do not rely on ignored import errors as type-check evidence."
	case ev.Status == model.StatusIncomplete && base:
		f = model.NewFinding("typecheck_base_failure", fmt.Sprintf("Type checking %q in %q already fails on base; every located combined error also occurs there or may be hidden there.", command, ev.CWD), model.Unknown)
		f.Severity = model.SeverityWarning
		f.Remediation = "Repair the type errors on base, then rerun; while base fails the same way, the combination is not reported as passing."
		if ev.Tool == typecheck.ToolGo {
			f.Remediation = "Repair the type errors on base, then rerun; go vet stops at the first error of a package and skips packages that import it, so existing base errors can hide new ones in the same module."
		}
	default:
		f = model.NewFinding("typecheck_"+string(ev.Status), fmt.Sprintf("Type checking %q in %q returned %s.", command, ev.CWD, ev.Status), model.Unknown)
		f.Severity = model.SeverityWarning
		if ev.Status == model.StatusError || ev.Status == model.StatusTimeout {
			f.Severity = model.SeverityError
		}
		f.Remediation = typecheckRemediation(ev)
	}
	if d := ev.Observation.Diagnosis; d != nil && ev.Status != model.StatusIncomplete {
		f.Explanation += " Cause: " + d.Message + "."
	}
	if ev.BaseComparison != nil {
		f.Explanation += " " + ev.BaseComparison.Explanation
	}
	f.Verification = "Repeat radar gate --run --typecheck with the same branches after repair; individual branch results are insufficient."
	f.ID = model.StableID(r.CandidateTree, ev.CWD, strings.Join(ev.Command, "\x00"), f.Code)
	return f
}

func typecheckRemediation(ev TypecheckExecution) string {
	d := ev.Observation.Diagnosis
	switch {
	case ev.SourceAfterExecution == "modified":
		return "The type-check command changed files in its private copy, so its result was discarded. Configure the tool not to write into the source tree, then rerun the same gate."
	case d != nil && d.Kind == "module_missing" && ev.Tool == typecheck.ToolGo && (d.Name == "cgo" || strings.HasSuffix(d.Name, ".h")):
		return "Install the C compiler or headers cgo needs where you run radar (Radar does not install them), then rerun the same gate. The combined source was not type-checked."
	case d != nil && d.Kind == "module_missing" && ev.Tool == typecheck.ToolMypy:
		return fmt.Sprintf("Install the missing package or its type stubs (%s) in the Python environment you run radar from, for example by activating the project's virtual environment, or configure mypy to ignore that import; then rerun the same gate. The combined source was not type-checked.", d.Name)
	case d != nil && d.Kind == "module_missing" && ev.Tool == typecheck.ToolGo:
		return "Download the module's dependencies where you run radar (for example go mod download in the repository), then rerun the same gate; Radar disables module downloads during checks. The combined source was not type-checked."
	case d.Environment():
		return fmt.Sprintf("Install or activate %s where you run radar, then rerun the same gate. The combined source was not type-checked.", d.Name)
	case ev.Status == model.StatusTimeout:
		return "Type checking did not finish in time. Raise --timeout if the project is large, then rerun the same gate."
	case ev.Status == model.StatusUnknown:
		return "The type checker found nothing to check in this directory. Check the project layout, then rerun the same gate."
	case ev.Status == model.StatusIncomplete:
		return "The type-check output exceeded Radar's limits and cannot be compared with base. Reduce existing errors on base, then rerun the same gate."
	case ev.ExecutionError != "":
		return "Radar could not run or check the type-check command (" + ev.ExecutionError + "). Fix that, then rerun the same gate."
	}
	if ev.Tool == typecheck.ToolMypy {
		return "mypy stopped without a result Radar recognizes. Run the command yourself in a checkout of the combined branches to see its output (Radar keeps only a digest). If it reports duplicate module names or an invalid package name, set files or exclude in the mypy configuration; then rerun the same gate."
	}
	return "The type checker stopped without a result Radar recognizes. Run the command yourself in a checkout of the combined branches to see its output (Radar keeps only a digest), then rerun the same gate."
}

func typecheckUnconfiguredLimitation(files []string) string {
	text := fmt.Sprintf("Type checking does not cover %d changed Python file(s) that no mypy or ruff configuration encloses: %s", len(files), strings.Join(files[:min(len(files), 5)], ", "))
	if len(files) > 5 {
		text += fmt.Sprintf(" and %d more", len(files)-5)
	}
	return text + "."
}

func typecheckGapLimitation(gaps []typecheck.Gap) string {
	var names []string
	for _, g := range gaps[:min(len(gaps), 5)] {
		names = append(names, filepath.ToSlash(g.Path)+" ("+g.Reason+")")
	}
	text := "Type checking does not cover " + strings.Join(names, ", ")
	if len(gaps) > 5 {
		text += fmt.Sprintf(" and %d more", len(gaps)-5)
	}
	return text + "."
}
