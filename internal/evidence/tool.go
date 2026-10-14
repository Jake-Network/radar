package evidence

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
)

// ObserveToolCandidate observes an explicitly authorized bounded tool command.
// Successful tool execution is not a test observation and carries zero cases.
func ObserveToolCandidate(ctx context.Context, root, cwd string, argv []string, timeout time.Duration) (CandidateObservation, error) {
	r := CandidateObservation{Status: model.StatusError, ExitCode: -1, Harness: "tool"}
	if err := checkExecutable(argv); err != nil {
		return r, err
	}
	if timeout <= 0 || timeout > 30*time.Minute {
		return r, fmt.Errorf("invalid tool timeout")
	}
	if cwd == "" {
		cwd = "."
	}
	dir, err := pathutil.ResolveInside(root, cwd)
	if err != nil {
		return r, err
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return r, fmt.Errorf("tool working directory unavailable")
	}
	if strings.Contains(argv[0], "/") {
		if _, err := pathutil.ResolveInside(dir, argv[0]); err != nil {
			return r, err
		}
	}
	home, err := os.MkdirTemp("", "radar-tool-home-")
	if err != nil {
		return r, err
	}
	defer os.RemoveAll(home)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out output
	state, err := execute(runCtx, argv, dir, environment(home, nil), &out)
	r.OutputDigest = hex.EncodeToString(out.sum())
	if err != nil {
		return r, err
	}
	r.ExitCode = state.ExitCode()
	switch {
	case runCtx.Err() != nil:
		r.Status = model.StatusTimeout
	case out.exceeded:
		r.Status = model.StatusIncomplete
	case r.ExitCode != 0:
		r.Status = model.StatusFailed
	default:
		r.Status = model.StatusPassed
	}
	return r, nil
}
