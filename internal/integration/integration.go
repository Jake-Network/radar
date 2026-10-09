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
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/graph"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/planning"
	"github.com/Jake-Network/radar/internal/verification"
)

type Options struct {
	Plan                   *planning.Plan
	Base                   string
	Branches               []string
	Verify, AllowExecution bool
	Command                []string
	Timeout                time.Duration
	PlanDigest             string
}
type Check struct {
	ID          string         `json:"id"`
	Status      model.Status   `json:"status"`
	Evidence    model.Evidence `json:"evidence"`
	Explanation string         `json:"explanation"`
}

// ExecutionEvidence binds an observation to the combined tree and ordered
// inputs. Individual branch evidence is deliberately never consumed.
type ExecutionEvidence struct {
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
	Plan            *planning.Report   `json:"plan,omitempty"`
	Status          model.Status       `json:"status"`
	Base            string             `json:"base"`
	Inputs          []string           `json:"inputs"`
	CandidateCommit string             `json:"candidate_commit,omitempty"`
	CandidateTree   string             `json:"candidate_tree,omitempty"`
	Conflicts       []string           `json:"conflicts"`
	Changed         []string           `json:"changed"`
	Affected        []string           `json:"affected"`
	Checks          []Check            `json:"checks"`
	Findings        []model.Finding    `json:"findings"`
	Diagnostics     []model.Diagnostic `json:"diagnostics"`
	Execution       *ExecutionEvidence `json:"execution,omitempty"`
	Limitations     []string           `json:"limitations"`
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
	r := Report{Status: model.StatusIncomplete, Inputs: []string{}, Conflicts: []string{}, Changed: []string{}, Affected: []string{}, Checks: []Check{}, Findings: []model.Finding{}, Diagnostics: []model.Diagnostic{}, Limitations: []string{"Static imports do not prove runtime dependency compatibility.", "Only the explicitly supplied verification command is observed; no comprehensive test coverage claim.", "Preview is a private filesystem, not an OS sandbox. Verification code inherits host privileges."}}
	if len(o.Branches) < 1 {
		return r, errors.New("at least one branch required")
	}
	if o.Verify && (!o.AllowExecution || len(o.Command) == 0) {
		return r, errors.New("--verify requires --allow-execution and a command after --")
	}
	if !o.Verify && (o.AllowExecution || len(o.Command) > 0) {
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
			r.Checks = append(r.Checks, Check{"textual_merge", model.StatusFailed, model.VerifiedTool, "Git reported unmerged paths in the private candidate."})
			f := model.NewFinding("integration_textual_conflict", "Resolve overlapping edits before integration.", model.VerifiedTool)
			f.Severity = model.SeverityError
			f.Remediation = "Reconcile the reported paths on the feature branches and repeat merge-check."
			r.Findings = append(r.Findings, f)
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
	r.Checks = append(r.Checks, Check{"textual_merge", model.StatusPassed, model.VerifiedTool, "Git combined all selected commits without textual conflicts."})
	r.Changed, e = gitrepo.ChangedFiles(ctx, temp, r.Base, r.CandidateCommit)
	if e != nil {
		return r, e
	}
	impact, e := contracts.Impact(ctx, temp, r.Base, r.CandidateCommit)
	if e != nil {
		return r, e
	}
	r.Findings = append(r.Findings, impact.Findings...)
	r.Diagnostics = append(r.Diagnostics, impact.Diagnostics...)
	r.Checks = append(r.Checks, Check{"declared_contracts", impact.Status, model.VerifiedStatic, fmt.Sprintf("Analyzed %d of %d declared bindings.", impact.Analyzed, impact.Bindings)})
	discovered, e := discovery.Compare(ctx, temp, r.Base, r.CandidateCommit)
	if e != nil {
		return r, e
	}
	r.Findings = append(r.Findings, discovered.Findings...)
	r.Diagnostics = append(r.Diagnostics, discovered.Diagnostics...)
	r.Limitations = append(r.Limitations, discovered.Limitations...)
	r.Checks = append(r.Checks, Check{"discovered_contracts", discovered.Status, model.Proposed, "Compared supported discovered producer and consumer relationships; candidates remain proposed."})
	snapshot, e := checkpoint.Index(ctx, temp, r.CandidateCommit)
	if e != nil {
		return r, e
	}
	r.Diagnostics = append(r.Diagnostics, snapshot.Diagnostics...)
	if o.Plan != nil {
		planReport := verification.VerifyWithEvidence(ctx, *o.Plan, snapshot, temp, nil)
		r.Plan = &planReport
		r.Findings = append(r.Findings, planReport.Findings...)
		r.Checks = append(r.Checks, Check{"plan_verification", planReport.Status, model.VerifiedStatic, "Verified supported plan rules against the combined snapshot; generic command observations do not satisfy plan-declared evidence criteria."})
	}
	g, e := graph.New(snapshot)
	if e != nil {
		return r, e
	}
	affected := map[string]bool{}
	for _, p := range r.Changed {
		if _, ok := g.Nodes[model.FileID(p)]; !ok {
			continue
		}
		for _, hop := range g.Distances(model.FileID(p), "DEPENDS_ON", true, 0) {
			if p := model.PathFromID(hop.ID); p != "" {
				affected[p] = true
			}
		}
	}
	for p := range affected {
		r.Affected = append(r.Affected, p)
	}
	sort.Strings(r.Affected)
	r.Checks = append(r.Checks, Check{"dependency_compatibility", model.StatusUnknown, model.Inferred, "Resolved static dependency neighborhood; runtime and build compatibility requires execution."})
	if o.Verify {
		ev := execute(ctx, temp, r, o)
		r.Execution = &ev
		r.Checks = append(r.Checks, Check{"integration_execution", ev.Status, model.ObservedTest, "Observed the supplied command against the combined candidate; output is hashed, not exposed."})
		if ev.Status != model.StatusPassed {
			f := model.NewFinding("integration_execution_"+string(ev.Status), fmt.Sprintf("Combined verification command %q returned %s (exit %d).", o.Command, ev.Status, ev.ExitCode), model.ObservedTest)
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
			f.ID = model.StableID(r.CandidateTree, strings.Join(o.Command, "\x00"), strings.Join(ev.Observation.FailedCases, "\x00"), f.Code)
			f.Remediation = "Reproduce the supplied command on the combined changes, reconcile producer/consumer assumptions, and repair the failing invariant."
			f.Verification = "Repeat merge-check --verify with the same command after repair; individual branch results are insufficient."
			r.Findings = append(r.Findings, f)
		}
	} else {
		r.Checks = append(r.Checks, Check{"integration_execution", model.StatusUnknown, model.Unknown, "No combined test evidence. Supply --verify --allow-execution -- COMMAND ARGS."})
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
	return r, nil
}

func execute(ctx context.Context, root string, r Report, o Options) ExecutionEvidence {
	started := time.Now()
	ev := ExecutionEvidence{CandidateCommit: r.CandidateCommit, CandidateTree: r.CandidateTree, Base: r.Base, Inputs: r.Inputs, PlanDigest: o.PlanDigest, Command: append([]string(nil), o.Command...), Status: model.StatusPassed, ExitCode: 0, StartedAt: started.UTC().Format(time.RFC3339Nano)}
	observation, observeErr := evidence.ObserveCandidate(ctx, root, o.Command, o.Timeout)
	ev.Observation = observation
	ev.Status = observation.Status
	ev.ExitCode = observation.ExitCode
	ev.OutputDigest = observation.OutputDigest
	if observeErr != nil {
		ev.Status = model.StatusError
		ev.ExitCode = -1
	}
	ev.DurationMS = time.Since(started).Milliseconds()
	// Capture whether the command changed tracked source. A passing command that
	// rewrites the candidate cannot establish the original candidate's behavior.
	after, err := run(context.WithoutCancel(ctx), root, "status", "--porcelain", "--untracked-files=no")
	untracked, untrackedErr := run(context.WithoutCancel(ctx), root, "ls-files", "--others", "-z")
	if untrackedErr != nil {
		err = untrackedErr
	}
	for _, p := range strings.Split(untracked, "\x00") {
		if p != "" && !indexer.ExcludedPath(p) {
			after += "untracked candidate input: " + p
		}
	}
	if err != nil {
		ev.SourceAfterExecution = "unknown"
		ev.Status = model.StatusError
	} else if head, headErr := gitrepo.Resolve(context.WithoutCancel(ctx), root, "HEAD"); headErr != nil || head != r.CandidateCommit || after != "" {
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
