package evidence

import (
	"context"
	"encoding/hex"
	"errors"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CandidateObservation uses the same harness recognition, output bounds,
// private environment and process timeout policy as plan-declared tests. Raw
// output is deliberately absent; only source locations and identifier-shaped
// unittest case names are retained for agent repair guidance.
type CandidateObservation struct {
	Status       model.Status       `json:"status"`
	ExitCode     int                `json:"exit_code"`
	TestsRun     int                `json:"tests_run"`
	TestsFailed  int                `json:"tests_failed"`
	TestsSkipped int                `json:"tests_skipped"`
	Harness      string             `json:"harness,omitempty"`
	OutputDigest string             `json:"output_digest"`
	FailedCases  []string           `json:"failed_cases,omitempty"`
	Locations    []model.Provenance `json:"locations,omitempty"`
	Diagnosis    *Diagnosis         `json:"diagnosis,omitempty"`
}

var failedCase = regexp.MustCompile(`(?m)^(?:FAIL|ERROR): ([A-Za-z_][A-Za-z0-9_]*) \(([A-Za-z_][A-Za-z0-9_.]*)\)$`)

// dependencyError recognizes a module absent from the environment. The
// unittest loader prefixes every import failure with "ImportError: Failed to
// import test module", so ImportError alone (for example a name a branch
// removed) is a failure of the combined source, as it is under pytest.
var dependencyError = regexp.MustCompile(`(?m)^ModuleNotFoundError:`)
var pythonLocation = regexp.MustCompile(`File "([^"]+)", line ([0-9]+)`)

// ObserveCandidate requires explicit caller authorization and an already
// isolated candidate. It never consumes preexisting branch evidence.
func ObserveCandidate(ctx context.Context, dir string, argv []string, timeout time.Duration) (CandidateObservation, error) {
	return ObserveCandidateAt(ctx, dir, ".", argv, timeout)
}

// ObserveCandidateAt executes only inside a validated candidate subdirectory.
func ObserveCandidateAt(ctx context.Context, root, cwd string, argv []string, timeout time.Duration) (CandidateObservation, error) {
	return ObserveCandidateReport(ctx, root, cwd, argv, "", timeout)
}

// ObserveCandidateReport also reads the CWD-relative JUnit report the runner
// is known to write. The report must not exist before the command runs, so a
// committed or earlier report cannot stand in for this execution.
func ObserveCandidateReport(ctx context.Context, root, cwd string, argv []string, report string, timeout time.Duration) (CandidateObservation, error) {
	return observeCandidateReport(ctx, root, cwd, argv, report, timeout, nil)
}

func observeCandidateReport(ctx context.Context, root, cwd string, argv []string, report string, timeout time.Duration, observed *[]string) (CandidateObservation, error) {
	r := CandidateObservation{Status: model.StatusError, ExitCode: -1}
	dir := root
	if cwd != "" && cwd != "." {
		var err error
		dir, err = pathutil.ResolveInside(root, cwd)
		if err != nil {
			return r, err
		}
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return r, errors.New("verification working directory is unavailable")
	}
	if e := checkExecutable(argv); e != nil {
		return r, e
	}
	if timeout <= 0 || timeout > 30*time.Minute {
		return r, errors.New("invalid verification timeout")
	}
	if report != "" {
		clean, e := pathutil.RepoRelative(report)
		if e != nil {
			return r, e
		}
		if _, e = os.Lstat(filepath.Join(dir, filepath.FromSlash(clean))); e == nil {
			return r, errors.New("stale JUnit report exists before candidate execution")
		} else if !os.IsNotExist(e) {
			return r, e
		}
		report = clean
	}
	home, e := os.MkdirTemp("", "radar-integration-home-")
	if e != nil {
		return r, e
	}
	defer os.RemoveAll(home)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out output
	env := environment(home, nil)
	if diagnosis := pythonImportCheck(runCtx, root, dir, argv, env); diagnosis != nil {
		r.Diagnosis = diagnosis
		return r, nil
	}
	if observed != nil {
		env, e = prepareFileObservation(root, home, argv, env)
		if e != nil {
			return r, e
		}
	}
	state, e := execute(runCtx, argv, dir, env, &out)
	r.OutputDigest = hex.EncodeToString(out.sum())
	if e != nil {
		return r, nil
	}
	r.ExitCode = state.ExitCode()
	result := harnessCounts(argv, out.b.Bytes(), dir)
	if report != "" {
		// Without a readable report the output-based result stands: a
		// compile error is still observed, and no count is invented.
		if junit, e := readJUnit(dir, report); e == nil {
			result = supplementJUnit(result, junit)
		}
	}
	r.TestsRun = result.Run
	r.TestsFailed = result.Failed
	r.TestsSkipped = result.Skipped
	r.Harness = result.Harness
	r.Status = outcome(runCtx.Err() != nil, r.ExitCode, result, out.exceeded, argv, out.b.Bytes())
	decorateCandidateObservation(&r, argv, out.b.Bytes(), root, dir, cwd)
	if observed != nil {
		*observed, e = readFileObservation(root, home)
		if e != nil {
			return r, e
		}
	}
	return r, nil
}

// Parameterized IDs and arbitrary failure text are deliberately omitted: only
// bounded repository-relative Python test identities are persisted.
var pytestFailedCase = regexp.MustCompile(`(?m)^(?:FAILED|ERROR) ([A-Za-z0-9_./-]+\.py(?:::[A-Za-z_][A-Za-z0-9_]*)*)(?: -|$)`)

func decorateCandidateObservation(r *CandidateObservation, argv []string, data []byte, root, dir, cwd string) {
	var e error
	if r.TestsFailed > 0 && r.Harness == "pytest" {
		for _, m := range pytestFailedCase.FindAllStringSubmatch(ansi.ReplaceAllString(string(data), ""), -1) {
			parts := strings.SplitN(m[1], "::", 2)
			p := parts[0]
			if cwd != "" && cwd != "." {
				p = filepath.ToSlash(filepath.Join(cwd, p))
			}
			if _, err := pathutil.RepoRelative(p); err != nil {
				continue
			}
			if _, err := pathutil.ResolveInside(root, p); err != nil {
				continue
			}
			if len(parts) == 1 {
				// Collection errors identify a file, not a failure cause or test
				// case. A different error in that file on base cannot establish
				// that this candidate failure already existed. Retain only a
				// bounded source location for repair leads.
				if len(r.Locations) < 20 && !slices.ContainsFunc(r.Locations, func(loc model.Provenance) bool {
					return loc.Path == p && loc.Method == "observed_pytest_collection_error"
				}) {
					r.Locations = append(r.Locations, model.Provenance{Path: p, Method: "observed_pytest_collection_error", Evidence: model.ObservedTest})
				}
				continue
			}
			name := p
			if len(parts) > 1 {
				name += "::" + parts[1]
			}
			if len(name) > 512 {
				continue
			}
			if !slices.Contains(r.FailedCases, name) {
				r.FailedCases = append(r.FailedCases, name)
			}
			if len(r.FailedCases) >= 20 {
				break
			}
		}
	}
	if r.TestsFailed > 0 && isUnittest(argv) {
		text := string(data)
		for _, match := range failedCase.FindAllStringSubmatch(text, -1) {
			name := match[2]
			if !strings.HasSuffix(name, "."+match[1]) {
				name += "." + match[1]
			}
			if !slices.Contains(r.FailedCases, name) {
				r.FailedCases = append(r.FailedCases, name)
			}
			if len(r.FailedCases) >= 20 {
				break
			}
		}
		seen := map[string]bool{}
		for _, match := range pythonLocation.FindAllStringSubmatch(text, -1) {
			p := match[1]
			if filepath.IsAbs(p) {
				p, e = filepath.Rel(dir, p)
				if e != nil {
					continue
				}
			}
			if cwd != "" && cwd != "." {
				p = filepath.Join(cwd, p)
			}
			p = filepath.ToSlash(p)
			if _, e = pathutil.RepoRelative(p); e != nil {
				continue
			}
			if _, e = pathutil.ResolveInside(root, p); e != nil {
				continue
			}
			line, _ := strconv.Atoi(match[2])
			key := p + ":" + match[2]
			if seen[key] {
				continue
			}
			seen[key] = true
			r.Locations = append(r.Locations, model.Provenance{Path: p, Line: line, Method: "observed_unittest_traceback", Evidence: model.ObservedTest})
			if len(r.Locations) >= 20 {
				break
			}
		}
	}
	if r.Status == model.StatusFailed {
		var build []sourceBuildError
		for _, b := range harnessCounts(argv, data, dir).BuildErrors {
			if cwd != "" && cwd != "." {
				b.Path = filepath.ToSlash(filepath.Join(cwd, b.Path))
			}
			if _, e = pathutil.ResolveInside(root, b.Path); e != nil {
				continue
			}
			build = append(build, b)
			r.Locations = append(r.Locations, model.Provenance{Path: b.Path, Line: b.Line, Method: "observed_compiler_error", Evidence: model.ObservedTest})
		}
		if len(build) > 0 {
			// Name the subcommand (go test, cmake --build); a build program
			// or compiler is named alone, since its first argument is a flag
			// or a file.
			tool := filepath.Base(argv[0])
			if buildTool(argv) == "" && len(argv) > 1 && (!buildDriver(argv) || tool == "cmake") {
				tool += " " + argv[1]
			}
			r.Diagnosis = buildDiagnosis(tool, build)
		}
	}
	// Harness-like text printed by arbitrary programs is not an authentication
	// boundary. These are observations of explicitly trusted execution.
	if environmentFailure(argv, data) {
		r.Status = model.StatusError
	}
	if r.Status != model.StatusPassed && r.Status != model.StatusFailed && r.TestsRun == 0 && r.TestsFailed == 0 {
		r.Diagnosis = diagnose(argv, r.ExitCode, data)
	}
}
