package git

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ShallowHistoryError reports that a shallow clone lacks the history Git
// needs to combine commits. Radar never fetches, so the user must.
type ShallowHistoryError struct{ Err error }

func (e *ShallowHistoryError) Error() string {
	return "this repository is a shallow clone and lacks the history needed to combine these commits (" + e.Err.Error() + "); fetch the full history with `git fetch --unshallow` (in GitHub Actions, actions/checkout with fetch-depth: 0) and rerun"
}
func (e *ShallowHistoryError) Unwrap() error { return e.Err }

// ShallowBoundary returns the commits at which a shallow clone's history is
// cut, or nil for a complete repository. A private repository holding objects
// copied from a shallow clone needs the same boundary, or Git walks into
// parents that were never copied.
func ShallowBoundary(ctx context.Context, root string) ([]string, error) {
	out, err := run(ctx, root, "rev-parse", "--is-shallow-repository")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(out)) != "true" {
		return nil, nil
	}
	out, err = run(ctx, root, "rev-parse", "--git-path", "shallow")
	if err != nil {
		return nil, err
	}
	path := strings.TrimSpace(string(out))
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, errors.New("shallow boundary exceeds analysis limit")
	}
	var boundary []string
	for _, line := range strings.Fields(string(data)) {
		if _, e := hex.DecodeString(line); e != nil || (len(line) != 40 && len(line) != 64) {
			return nil, fmt.Errorf("malformed shallow boundary entry %q", line)
		}
		boundary = append(boundary, line)
	}
	return boundary, nil
}

// ExplainShallow turns a history error from a shallow clone into a
// ShallowHistoryError that says how to fix it; other errors pass unchanged.
func ExplainShallow(ctx context.Context, root string, err error) error {
	if err == nil {
		return nil
	}
	if boundary, e := ShallowBoundary(ctx, root); e == nil && len(boundary) > 0 {
		return &ShallowHistoryError{Err: err}
	}
	return err
}
