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
}

var failedCase = regexp.MustCompile(`(?m)^(?:FAIL|ERROR): ([A-Za-z_][A-Za-z0-9_]*) \(([A-Za-z_][A-Za-z0-9_.]*)\)$`)
var dependencyError = regexp.MustCompile(`(?m)^(?:ModuleNotFoundError|ImportError):`)
var pythonLocation = regexp.MustCompile(`File "([^"]+)", line ([0-9]+)`)

// ObserveCandidate requires explicit caller authorization and an already
// isolated candidate. It never consumes preexisting branch evidence.
func ObserveCandidate(ctx context.Context, dir string, argv []string, timeout time.Duration) (CandidateObservation, error) {
	return ObserveCandidateAt(ctx, dir, ".", argv, timeout)
}

// ObserveCandidateAt executes only inside a validated candidate subdirectory.
func ObserveCandidateAt(ctx context.Context, root, cwd string, argv []string, timeout time.Duration) (CandidateObservation, error) {
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
	home, e := os.MkdirTemp("", "radar-integration-home-")
	if e != nil {
		return r, e
	}
	defer os.RemoveAll(home)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out output
	state, e := execute(runCtx, argv, dir, environment(home, nil), &out)
	r.OutputDigest = hex.EncodeToString(out.sum())
	if e != nil {
		return r, nil
	}
	r.ExitCode = state.ExitCode()
	result := harnessCounts(argv, out.b.Bytes())
	r.TestsRun = result.Run
	r.TestsFailed = result.Failed
	r.TestsSkipped = result.Skipped
	r.Harness = result.Harness
	r.Status = outcome(runCtx.Err() != nil, r.ExitCode, result, out.exceeded, argv, out.b.Bytes())
	decorateCandidateObservation(&r, argv, out.b.Bytes(), root, dir, cwd)
	return r, nil
}

func decorateCandidateObservation(r *CandidateObservation, argv []string, data []byte, root, dir, cwd string) {
	var e error
	if r.TestsFailed > 0 && isUnittest(argv) {
		text := string(data)
		for _, match := range failedCase.FindAllStringSubmatch(text, -1) {
			name := match[2]
			if !strings.HasSuffix(name, "."+match[1]) {
				name += "." + match[1]
			}
			r.FailedCases = append(r.FailedCases, name)
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
	// Harness-like text printed by arbitrary programs is not an authentication
	// boundary. These are observations of explicitly trusted execution.
	if isUnittest(argv) && dependencyError.Match(data) {
		r.Status = model.StatusError
	}
}
