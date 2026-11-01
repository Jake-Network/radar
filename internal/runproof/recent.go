package runproof

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Jake-Network/radar/internal/pathutil"
)

// RunSummary is historical metadata, never evidence about current inputs.
type RunSummary struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	Verdict   string `json:"verdict"`
	InputKey  string `json:"input_key"`
}

func recentDir(state, repository string) (string, error) {
	if repository == "" {
		return "", errors.New("missing repository identity")
	}
	h := sha256.Sum256([]byte(repository))
	return pathutil.StatePath(state, filepath.Join("gate-history", hex.EncodeToString(h[:])))
}
func summaryName(s RunSummary) string {
	b, _ := json.Marshal(s)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]) + ".json"
}

// Remember publishes immutable metadata only after verifying its report exists.
func Remember(state, repository string, s RunSummary) error {
	if _, err := Load(state, repository, s.ID); err != nil {
		return err
	}
	switch s.Verdict {
	case "pass", "fail", "blocked", "error":
	default:
		return errors.New("invalid historical verdict")
	}
	if s.InputKey == "" || len(s.InputKey) > 256 {
		return errors.New("invalid historical inputs")
	}
	if s.CreatedAt == "" {
		s.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if _, err := time.Parse(time.RFC3339Nano, s.CreatedAt); err != nil {
		return err
	}
	dir, err := recentDir(state, repository)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	p, err := pathutil.StatePath(state, filepath.Join("gate-history", filepath.Base(dir), summaryName(s)))
	if err != nil {
		return err
	}
	b, _ := json.Marshal(s)
	f, err := os.CreateTemp(dir, ".history-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Link(f.Name(), p); err != nil && !os.IsExist(err) {
		return err
	}
	prune(state, repository, filepath.Base(p))
	return nil
}

// maxHistory bounds the summaries kept per repository. Older summaries are
// removed from the index; the run reports they point at are never deleted.
const maxHistory = 500

// maxHistoryInspection bounds one directory scan, including summaries a
// concurrent or interrupted run has not pruned yet.
const maxHistoryInspection = 10000

// Recent returns digest-checked historical reports, newest first. Corruption is
// an error rather than a false absence of a failing run.
func Recent(state, repository string, limit int) ([]RunSummary, error) {
	if limit < 1 || limit > 50 {
		return nil, errors.New("history limit must be 1..50")
	}
	out, _, err := summaries(state, repository, maxHistoryInspection)
	if err != nil {
		return nil, err
	}
	if len(out) > limit {
		out = out[:limit]
	}
	// Only the reports shown are read and digest-checked.
	for _, s := range out {
		if _, err = Load(state, repository, s.ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// summaries reads and verifies every summary of a repository, newest first,
// with the file name each was read from. inspection > 0 bounds the scan.
func summaries(state, repository string, inspection int) ([]RunSummary, []string, error) {
	dir, err := recentDir(state, repository)
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []RunSummary{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if inspection > 0 && len(entries) > inspection {
		return nil, nil, errors.New("gate history inspection limit exceeded")
	}
	type named struct {
		s    RunSummary
		name string
	}
	var found []named
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		p, err := pathutil.StatePath(state, filepath.Join("gate-history", filepath.Base(dir), entry.Name()))
		if err != nil {
			return nil, nil, err
		}
		st, err := os.Stat(p)
		if os.IsNotExist(err) {
			// Pruned by a concurrent run after the directory was listed.
			continue
		}
		if err != nil || !st.Mode().IsRegular() || st.Size() > 4096 {
			return nil, nil, errors.New("invalid history metadata")
		}
		b, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		var s RunSummary
		if json.Unmarshal(b, &s) != nil || summaryName(s) != entry.Name() {
			return nil, nil, errors.New("history metadata digest mismatch")
		}
		found = append(found, named{s, entry.Name()})
	}
	sort.Slice(found, func(i, j int) bool {
		a, b := found[i].s, found[j].s
		if a.CreatedAt == b.CreatedAt {
			return a.ID > b.ID
		}
		left, _ := time.Parse(time.RFC3339Nano, a.CreatedAt)
		right, _ := time.Parse(time.RFC3339Nano, b.CreatedAt)
		return left.After(right)
	})
	out := make([]RunSummary, 0, len(found))
	names := make([]string, 0, len(found))
	for _, f := range found {
		out = append(out, f.s)
		names = append(names, f.name)
	}
	return out, names, nil
}

// prune removes the oldest summaries beyond maxHistory, never keep (the one
// just remembered, even when a clock moved backwards). It is best effort: a
// summary it cannot remove stays listed, which is never a false absence.
func prune(state, repository, keep string) {
	dir, err := recentDir(state, repository)
	if err != nil {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) <= maxHistory {
		return
	}
	// Pruning reads past the inspection limit so that a directory left
	// over it by earlier versions recovers on the next gate run.
	_, names, err := summaries(state, repository, 0)
	if err != nil {
		return
	}
	for _, name := range names[min(len(names), maxHistory):] {
		if name == keep {
			continue
		}
		p, err := pathutil.StatePath(state, filepath.Join("gate-history", filepath.Base(dir), name))
		if err == nil {
			_ = os.Remove(p)
		}
	}
}
