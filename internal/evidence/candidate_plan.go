package evidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"time"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/planning"
)

type CandidateCheckpoint struct {
	Repository string   `json:"repository"`
	Base       string   `json:"base"`
	Revision   string   `json:"revision"`
	Tree       string   `json:"tree"`
	Inputs     []string `json:"inputs"`
}
type CandidateBinding struct {
	Checkpoint      CandidateCheckpoint `json:"checkpoint"`
	SourceBefore    string              `json:"source_before"`
	SourceAfter     string              `json:"source_after"`
	SourceUnchanged bool                `json:"source_unchanged"`
	ConfigDigest    string              `json:"config_digest"`
	ReviewDigest    string              `json:"review_digest"`
}
type CandidateRun struct {
	Record      Record               `json:"record"`
	Observation CandidateObservation `json:"observation"`
}
type SourceState struct {
	Digest  string
	Matches bool
}

// InspectCandidateSource compares every tracked byte against immutable objects
// and walks untracked inputs without truncated Git listings. Generated/cache
// directories and explicitly declared fresh JUnit artifacts are excluded only
// from NEW input detection; tracked files are always checked, including caches.
func InspectCandidateSource(ctx context.Context, root string, c CandidateCheckpoint, artifacts []string) (SourceState, error) {
	state := SourceState{Matches: true}
	head, e := gitrepo.Resolve(ctx, root, "HEAD")
	if e != nil {
		return state, e
	}
	if head != c.Revision {
		state.Matches = false
	}
	entries, e := gitrepo.Entries(ctx, root, c.Revision)
	if e != nil {
		return state, e
	}
	if len(entries) > maxSnapshotFiles {
		return state, errors.New("candidate source exceeds file limit")
	}
	reader, e := gitrepo.OpenBlobReader(ctx, root)
	if e != nil {
		return state, e
	}
	defer reader.Close()
	hash := sha256.New()
	tracked := map[string]bool{}
	var total, actualTotal int64
	for _, entry := range entries {
		if e = ctx.Err(); e != nil {
			return state, e
		}
		tracked[entry.Path] = true
		if entry.Mode != "100644" && entry.Mode != "100755" {
			return state, errors.New("candidate contains non-regular source")
		}
		content, e := reader.Read(entry.OID, maxSnapshot-total)
		if e != nil {
			return state, e
		}
		total += int64(len(content))
		full, e := pathutil.StatePath(root, entry.Path)
		if e != nil {
			state.Matches = false
			continue
		}
		info, e := os.Lstat(filepath.Join(root, filepath.FromSlash(entry.Path)))
		if e != nil || !info.Mode().IsRegular() {
			state.Matches = false
			continue
		}
		file, openErr := os.Open(full)
		if openErr != nil {
			return state, openErr
		}
		actual, e := io.ReadAll(io.LimitReader(file, maxSnapshot+1))
		_ = file.Close()
		if e != nil {
			return state, e
		}
		actualTotal += int64(len(actual))
		if actualTotal > maxSnapshot {
			return state, errors.New("candidate source exceeds byte limit")
		}
		actualExecutable := info.Mode().Perm()&0111 != 0
		if runtime.GOOS != "windows" && actualExecutable != (entry.Mode == "100755") {
			state.Matches = false
		}
		fmt.Fprintf(hash, "%s\x00%s\x00%d\x00%t\x00", entry.Path, entry.Mode, len(actual), actualExecutable)
		hash.Write(actual)
		if !bytes.Equal(actual, content) {
			state.Matches = false
		}
	}
	allowed := map[string]bool{}
	for _, p := range artifacts {
		allowed[p] = true
	}
	count := 0
	e = filepath.WalkDir(root, func(full string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		if full == root {
			return nil
		}
		rel, e := filepath.Rel(root, full)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() && (rel == ".git" || indexer.ExcludedPath(rel)) {
			return filepath.SkipDir
		}
		count++
		if count > maxSnapshotFiles*2 {
			return errors.New("candidate input inventory exceeds file limit")
		}
		if !d.IsDir() && !tracked[rel] && !allowed[rel] && !indexer.ExcludedPath(rel) {
			state.Matches = false
			fmt.Fprintf(hash, "untracked:%s\x00", rel)
		}
		return nil
	})
	if e != nil {
		return state, e
	}
	state.Digest = hex.EncodeToString(hash.Sum(nil))
	return state, nil
}

func executionDirectory(root, cwd string) (string, error) {
	if cwd == "" {
		cwd = "."
	}
	dir, e := pathutil.ResolveInside(root, cwd)
	if e != nil {
		return "", e
	}
	info, e := os.Stat(dir)
	if e != nil || !info.IsDir() {
		return "", errors.New("verification cwd is not a directory")
	}
	return dir, nil
}
func normalizeCWD(cwd string) string {
	if cwd == "" {
		return "."
	}
	return cwd
}
func configDigest(spec execution, argv []string) string {
	return checksum(struct {
		Execution execution
		Command   []string
	}{spec, argv})
}

// MatchesCandidateCommand distinguishes generic observations from reviewed
// plan criteria. It does not authorize execution or create a human approval.
func declaredCandidate(p planning.Plan, argv []string, cwd string) ([]string, execution, error) {
	filtered := p
	filtered.Acceptance = nil
	filtered.Constraints = nil
	for _, c := range p.Acceptance {
		if c.Rule != nil && normalizeCWD(c.Rule.CWD) == normalizeCWD(cwd) {
			filtered.Acceptance = append(filtered.Acceptance, c)
		}
	}
	for _, c := range p.Constraints {
		if c.Rule != nil && normalizeCWD(c.Rule.CWD) == normalizeCWD(cwd) {
			filtered.Constraints = append(filtered.Constraints, c)
		}
	}
	return declared(filtered, argv)
}
func CandidateDeclaration(p planning.Plan, argv []string, cwd string) (bool, error) {
	if !planning.Approved(p) {
		return false, nil
	}
	matched := false
	for _, c := range p.Acceptance {
		if c.Rule != nil && c.Rule.Kind == "test_run" && reflect.DeepEqual(c.Rule.Command, argv) && normalizeCWD(c.Rule.CWD) == normalizeCWD(cwd) {
			matched = true
		}
	}
	for _, c := range p.Constraints {
		if c.Rule != nil && c.Rule.Kind == "test_run" && reflect.DeepEqual(c.Rule.Command, argv) && normalizeCWD(c.Rule.CWD) == normalizeCWD(cwd) {
			matched = true
		}
	}
	if !matched {
		return false, nil
	}
	_, _, err := declaredCandidate(p, argv, cwd)
	return true, err
}
func MatchesCandidateCommand(p planning.Plan, argv []string, cwd string) bool {
	match, err := CandidateDeclaration(p, argv, cwd)
	return match && err == nil
}

// RunCandidate runs exact reviewed plan declarations exclusively inside an
// already private candidate. Host dependency links are deliberately unsupported.
func RunCandidate(ctx context.Context, root string, c CandidateCheckpoint, p planning.Plan, argv []string, cwd string, timeout time.Duration) (CandidateRun, error) {
	var result CandidateRun
	if !planning.Approved(p) {
		return result, errors.New("candidate criterion evidence requires a valid reviewed plan")
	}
	if timeout <= 0 || timeout > 30*time.Minute {
		return result, errors.New("invalid candidate timeout")
	}
	if e := checkExecutable(argv); e != nil {
		return result, e
	}
	criteria, spec, e := declaredCandidate(p, argv, cwd)
	if e != nil {
		return result, e
	}
	if normalizeCWD(spec.CWD) != normalizeCWD(cwd) {
		return result, errors.New("candidate command cwd differs from reviewed declaration")
	}
	if len(spec.Link) > 0 {
		return result, errors.New("candidate plan dependency links are unsupported; install dependencies through reviewed setup in private state")
	}
	for _, name := range spec.Env {
		if _, ok := os.LookupEnv(name); !ok {
			return result, fmt.Errorf("required environment variable %s is unavailable", name)
		}
	}
	for _, step := range spec.Setup {
		if e := checkExecutable(step); e != nil {
			return result, e
		}
	}
	resolved, resolveErr := gitrepo.Resolve(ctx, root, c.Revision)
	if resolveErr != nil || resolved != c.Revision {
		return result, errors.New("candidate revision must be an exact commit")
	}
	if len(c.Inputs) == 0 || len(c.Inputs) > 256 {
		return result, errors.New("candidate requires 1 to 256 exact input commits")
	}
	for _, input := range append([]string{c.Base}, c.Inputs...) {
		resolved, err := gitrepo.Resolve(ctx, root, input)
		if err != nil || resolved != input {
			return result, errors.New("candidate input is not an exact local commit")
		}
		ancestor, err := gitrepo.IsAncestor(ctx, root, input, c.Revision)
		if err != nil || !ancestor {
			return result, errors.New("candidate does not contain the declared input commit")
		}
	}
	treeCtx, treeCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer treeCancel()
	treeCommand := exec.CommandContext(treeCtx, "git", "-C", root, "rev-parse", "--verify", "--end-of-options", c.Revision+"^{tree}")
	treeCommand.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + os.DevNull, "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1"}
	treeOutput, treeErr := treeCommand.Output()
	if treeErr != nil || strings.TrimSpace(string(treeOutput)) != c.Tree {
		return result, errors.New("candidate tree differs from checkpoint")
	}
	identity, e := RepositoryIdentity(ctx, root)
	if e != nil {
		return result, e
	}
	if identity != c.Repository {
		return result, errors.New("candidate repository identity mismatch")
	}
	dir, e := executionDirectory(root, cwd)
	if e != nil {
		return result, e
	}
	var artifacts []string
	if spec.JUnit != "" {
		report := filepath.ToSlash(filepath.Join(normalizeCWD(cwd), spec.JUnit))
		if _, e = pathutil.RepoRelative(report); e != nil {
			return result, e
		}
		if _, e = os.Lstat(filepath.Join(root, filepath.FromSlash(report))); e == nil {
			return result, errors.New("stale JUnit report exists before candidate execution")
		} else if !os.IsNotExist(e) {
			return result, e
		}
		artifacts = append(artifacts, report)
	}
	before, e := InspectCandidateSource(ctx, root, c, artifacts)
	if e != nil {
		return result, e
	}
	if !before.Matches {
		return result, errors.New("candidate source differs before execution")
	}
	r := Record{SchemaVersion: "1", Repository: c.Repository, Revision: c.Revision, PlanDigest: planning.Digest(p), Command: append([]string(nil), argv...), Criteria: criteria, Env: append([]string(nil), spec.Env...), CWD: normalizeCWD(cwd), StartedAt: time.Now().UTC().Format(time.RFC3339Nano), ExitCode: -1}
	r.Candidate = &CandidateBinding{Checkpoint: c, SourceBefore: before.Digest, ConfigDigest: configDigest(spec, argv), ReviewDigest: checksum(p.Approval)}
	home, e := os.MkdirTemp("", "radar-candidate-plan-home-")
	if e != nil {
		return result, e
	}
	defer os.RemoveAll(home)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	env := environment(home, spec.Env)
	var out output
	finish := func(status model.Status) CandidateRun {
		r.Status = status
		r.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		r.OutputDigest = hex.EncodeToString(out.sum())
		sourceCtx, sourceCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer sourceCancel()
		after, sourceErr := InspectCandidateSource(sourceCtx, root, c, artifacts)
		r.Candidate.SourceAfter = after.Digest
		r.Candidate.SourceUnchanged = sourceErr == nil && after.Matches && after.Digest == before.Digest
		if sourceErr != nil {
			r.Status = model.StatusError
		} else if !r.Candidate.SourceUnchanged && r.Status == model.StatusPassed {
			r.Status = model.StatusUnknown
		}
		r.IntegrityDigest = integrity(r)
		r.ID = "evidence:" + r.IntegrityDigest
		observation := CandidateObservation{Status: r.Status, ExitCode: r.ExitCode, TestsRun: r.TestsRun, TestsFailed: r.TestsFailed, TestsSkipped: r.TestsSkipped, Harness: r.Harness, OutputDigest: r.OutputDigest}
		decorateCandidateObservation(&observation, argv, out.b.Bytes(), root, dir, cwd)
		return CandidateRun{Record: r, Observation: observation}
	}
	for _, step := range spec.Setup {
		state, startErr := execute(runCtx, step, dir, env, &out)
		r.Phase = "setup"
		if runCtx.Err() != nil {
			return finish(model.StatusTimeout), nil
		}
		if startErr != nil {
			return finish(model.StatusError), nil
		}
		r.ExitCode = state.ExitCode()
		if runCtx.Err() != nil {
			return finish(model.StatusTimeout), nil
		}
		if r.ExitCode != 0 {
			return finish(model.StatusError), nil
		}
	}
	// A setup-generated report also cannot certify a command that never ran tests.
	if spec.JUnit != "" {
		if _, e = os.Lstat(filepath.Join(dir, filepath.FromSlash(spec.JUnit))); e == nil {
			return finish(model.StatusError), nil
		} else if !os.IsNotExist(e) {
			return finish(model.StatusError), nil
		}
	}
	r.Phase = "test"
	out = output{}
	state, startErr := execute(runCtx, argv, dir, env, &out)
	if runCtx.Err() != nil {
		return finish(model.StatusTimeout), nil
	}
	if startErr != nil {
		return finish(model.StatusError), nil
	}
	r.ExitCode = state.ExitCode()
	counts := harnessCounts(argv, out.b.Bytes())
	if spec.JUnit != "" {
		junit, junitErr := readJUnit(dir, spec.JUnit)
		if junitErr != nil {
			return finish(model.StatusError), nil
		}
		counts = junit
	}
	r.TestsRun = counts.Run
	r.TestsFailed = counts.Failed
	r.TestsSkipped = counts.Skipped
	r.Harness = counts.Harness
	status := outcome(runCtx.Err() != nil, r.ExitCode, counts, out.exceeded, argv, out.b.Bytes())
	if environmentFailure(argv, out.b.Bytes()) {
		status = model.StatusError
	}
	return finish(status), nil
}

// ValidateCandidate prevents branch records and observations of mutated source
// from satisfying candidate criteria. Approval is a local declaration, not auth.
func ValidateCandidate(r Record, root string, c CandidateCheckpoint, p planning.Plan, criterion string, argv []string) error {
	if e := Validate(r, root, c.Revision, p, criterion, argv); e != nil {
		return e
	}
	if !planning.Approved(p) || r.Candidate == nil {
		return errors.New("candidate evidence requires the same reviewed plan")
	}
	if !reflect.DeepEqual(r.Candidate.Checkpoint, c) || !r.Candidate.SourceUnchanged || r.Candidate.SourceBefore == "" || r.Candidate.SourceBefore != r.Candidate.SourceAfter {
		return errors.New("candidate checkpoint or source integrity mismatch")
	}
	_, spec, e := declaredCandidate(p, argv, r.CWD)
	if e != nil {
		return e
	}
	if r.Candidate.ConfigDigest != configDigest(spec, argv) || r.Candidate.ReviewDigest != checksum(p.Approval) || !slices.Equal(r.Env, spec.Env) || normalizeCWD(r.CWD) != normalizeCWD(spec.CWD) {
		return errors.New("candidate execution configuration or review mismatch")
	}
	return nil
}

// CandidateArtifacts enumerates only exact declared JUnit output paths. It does
// not exempt tracked inputs or arbitrary directories from source validation.
func CandidateArtifacts(p *planning.Plan) []string {
	var result []string
	if p == nil {
		return result
	}
	for _, c := range p.Acceptance {
		if c.Rule != nil && c.Rule.Kind == "test_run" && c.Rule.JUnit != "" {
			result = append(result, strings.TrimPrefix(filepath.ToSlash(filepath.Join(normalizeCWD(c.Rule.CWD), c.Rule.JUnit)), "./"))
		}
	}
	for _, c := range p.Constraints {
		if c.Rule != nil && c.Rule.Kind == "test_run" && c.Rule.JUnit != "" {
			result = append(result, strings.TrimPrefix(filepath.ToSlash(filepath.Join(normalizeCWD(c.Rule.CWD), c.Rule.JUnit)), "./"))
		}
	}
	return result
}

// InvalidateCandidateRecord revokes eligibility after a later suite command
// mutates the shared source state. Its checksum remains inspectable metadata.
func InvalidateCandidateRecord(r Record) Record {
	if r.Candidate != nil {
		binding := *r.Candidate
		binding.SourceUnchanged = false
		r.Candidate = &binding
	}
	if r.Status == model.StatusPassed {
		r.Status = model.StatusUnknown
	}
	r.IntegrityDigest = integrity(r)
	r.ID = "evidence:" + r.IntegrityDigest
	return r
}
