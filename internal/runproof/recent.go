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
	return nil
}

// Recent returns digest-checked historical reports, newest first. Corruption is
// an error rather than a false absence of a failing run.
func Recent(state, repository string, limit int) ([]RunSummary, error) {
	if limit < 1 || limit > 50 {
		return nil, errors.New("history limit must be 1..50")
	}
	dir, err := recentDir(state, repository)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []RunSummary{}, nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries) > 10000 {
		return nil, errors.New("gate history inspection limit exceeded")
	}
	out := []RunSummary{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		p, err := pathutil.StatePath(state, filepath.Join("gate-history", filepath.Base(dir), entry.Name()))
		if err != nil {
			return nil, err
		}
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() || st.Size() > 4096 {
			return nil, errors.New("invalid history metadata")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var s RunSummary
		if json.Unmarshal(b, &s) != nil || summaryName(s) != entry.Name() {
			return nil, errors.New("history metadata digest mismatch")
		}
		if _, err = Load(state, repository, s.ID); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].ID > out[j].ID
		}
		left, _ := time.Parse(time.RFC3339Nano, out[i].CreatedAt)
		right, _ := time.Parse(time.RFC3339Nano, out[j].CreatedAt)
		return left.After(right)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
