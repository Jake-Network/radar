package cli

import (
	"errors"
	"flag"
	"fmt"
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/planning"
	"github.com/Jake-Network/radar/internal/project"
	"github.com/Jake-Network/radar/internal/testselection"
	"io"
	"os"
	"strings"
	"time"
)

func mergeCheckCommand() command {
	return command{name: "merge-check", usage: "radar merge-check --base REF --branches REF,REF [--plan PATH] [--policy PATH] [--suggest-tests] [--verify --allow-execution --suite targeted|balanced|full [--max-commands N] | --cwd DIR -- COMMAND ARGS]", summary: "Preview the combined branches in private Git state and optionally verify that candidate.", flags: func(fs *flag.FlagSet, o *options) {
		fs.StringVar(&o.base, "base", "", "baseline commit or ref")
		fs.StringVar(&o.branches, "branches", "", "ordered comma-separated branch refs")
		planFlag(fs, o)
		policyFlag(fs, o)
		fs.BoolVar(&o.suggestTests, "suggest-tests", false, "recommend relevant candidate tests without execution")
		fs.StringVar(&o.cwd, "cwd", ".", "repository-relative directory for an explicit verification command")
		fs.StringVar(&o.suite, "suite", "", "verification selection mode: targeted, balanced (alias recommended) or full (requires --verify --allow-execution)")
		fs.IntVar(&o.maxCommands, "max-commands", testselection.DefaultMaxCommands, "maximum grouped commands a --suite run executes; omitted required commands block the gate")
		fs.StringVar(&o.output, "evidence-output", "", "new path under .radar/evidence/ to save execution metadata (default JSON output only)")
		fs.BoolVar(&o.verify, "verify", false, "execute the supplied command against the combined candidate")
		fs.BoolVar(&o.allow, "allow-execution", false, "authorize candidate code execution with host privileges")
		fs.BoolVar(&o.strict, "require-complete", false, "exit 1 when any property remains unknown or incomplete")
		fs.DurationVar(&o.timeout, "timeout", 2*time.Minute, "verification timeout, maximum 30m")
	}, run: (*app).mergeCheck}
}
func (a *app) mergeCheck(o options) int {
	if o.cwd != "." && o.cwd != "" {
		if !o.verify || o.suite != "" {
			return a.fail(errors.New("--cwd applies only to an explicit --verify command"))
		}
		if _, err := pathutil.RepoRelative(o.cwd); err != nil {
			return a.fail(err)
		}
	}
	configuration, err := a.captureConfiguration(o)
	if err != nil {
		return a.fail(err)
	}
	policy := configuration.Policy
	if o.output != "" {
		clean, e := pathutil.RepoRelative(o.output)
		if e != nil || !strings.HasPrefix(clean, ".radar/evidence/") || !o.verify {
			return a.fail(errors.New("--evidence-output requires --verify and a new path under .radar/evidence/"))
		}
		path, e := project.SafePath(a.root, clean)
		if e != nil {
			return a.fail(e)
		}
		if _, e = os.Lstat(path); e == nil {
			return a.fail(errors.New("evidence output already exists; choose a new path"))
		} else if !os.IsNotExist(e) {
			return a.fail(e)
		}
		o.output = clean
	}
	if e := a.repository(); e != nil {
		return a.fail(e)
	}
	digest := ""
	plan := configuration.Plan
	if plan != nil {
		digest = planning.Digest(*plan)
	}
	refs := strings.Split(o.branches, ",")
	for i := range refs {
		refs[i] = strings.TrimSpace(refs[i])
	}
	r, e := integration.Preview(a.ctx, a.root, integration.Options{CWD: o.cwd, SuggestTests: o.suggestTests, Suite: o.suite, Policy: policy, Plan: plan, Base: o.base, Branches: refs, Verify: o.verify, AllowExecution: o.allow, Command: o.args, Timeout: o.timeout, PlanDigest: digest, MaxCommands: o.maxCommands})
	if e != nil {
		return a.fail(e)
	}
	configuration.applyIntegration(&r)
	if o.output != "" && configurationUnstable(configuration.Inputs) {
		r.Limitations = append(r.Limitations, "Evidence output omitted: selected configuration changed during this invocation. Restore stable artifacts and rerun; actual execution observations remain in this report.")
	}
	if len(r.Executions) > 0 && o.output != "" && !configurationUnstable(configuration.Inputs) {
		// Only explicit artifact output may write repository files; default
		// preview and verification leave the user worktree entirely untouched.
		path, e := project.SafePath(a.root, o.output)
		if e != nil {
			return a.fail(e)
		}
		var artifact any = r.Execution
		if artifact == nil {
			artifact = r.Executions
		}
		if e = writeNewJSON(path, artifact); e != nil {
			return a.fail(e)
		}
	}
	report := struct {
		integration.Report
		Configuration []ConfigurationInput `json:"configuration_inputs,omitempty"`
	}{r, configuration.Inputs}
	a.report(report, func(w io.Writer) {
		p := paletteOf(w)
		fmt.Fprintf(w, "%s %s — %s\n", p.bold("Gate:"), p.verdict(r.Gate.Verdict, string(r.Gate.Verdict)), r.Gate.Explanation)
		fmt.Fprintf(w, "Analysis: %s\nBase: %s\n", r.Status, r.Base)
		if o.output != "" && configurationUnstable(configuration.Inputs) {
			fmt.Fprintln(w, "Evidence output omitted: selected configuration changed. Restore stable artifacts and rerun.")
		}
		if r.CandidateTree != "" {
			fmt.Fprintf(w, "Candidate tree: %s\n", r.CandidateTree)
		}
		renderProposal(w, r.VerificationProposal)
		renderSelection(w, r.Selection)
		for _, c := range r.Checks {
			fmt.Fprintf(w, "  %s%s %s: %s\n", p.status(c.Status, string(c.Status)), strings.Repeat(" ", max(0, 12-len(c.Status))), c.ID, c.Explanation)
		}
		for _, p := range r.Conflicts {
			fmt.Fprintf(w, "  Conflict: %s\n", p)
		}
		for _, f := range r.Findings {
			fmt.Fprintf(w, "  %s [%s]: %s\n    Repair: %s\n", f.Code, f.Evidence, f.Explanation, f.Remediation)
		}
		fmt.Fprintf(w, "Changed: %d files; affected import dependents: %d\n", len(r.Changed), len(r.Affected))
	})
	if policy != nil || configurationUnstable(configuration.Inputs) {
		return gate.Exit(r.Gate)
	}
	if o.strict {
		fmt.Fprintln(a.errout, "--require-complete retains legacy whole-analysis strictness; use --policy to gate only required checks.")
	}
	if r.Status == model.StatusError {
		return 2
	}
	if r.Status == model.StatusFailed || (o.strict && r.Status != model.StatusPassed) {
		return 1
	}
	return 0
}
