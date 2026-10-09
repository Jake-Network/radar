package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/model"
	"github.com/radar-engine/radar/internal/planning"
)

type Record struct {
	SchemaVersion   string       `json:"schema_version"`
	ID              string       `json:"id"`
	Repository      string       `json:"repository"`
	Revision        string       `json:"revision"`
	PlanDigest      string       `json:"plan_digest"`
	Command         []string     `json:"command"`
	Criteria        []string     `json:"criteria"`
	TestsRun        int          `json:"tests_run"`
	TestsFailed     int          `json:"tests_failed,omitempty"`
	TestsSkipped    int          `json:"tests_skipped,omitempty"`
	Harness         string       `json:"harness,omitempty"`
	Env             []string     `json:"env,omitempty"`
	Phase           string       `json:"phase,omitempty"`
	ExitCode        int          `json:"exit_code"`
	Status          model.Status `json:"status"`
	StartedAt       string       `json:"started_at"`
	FinishedAt      string       `json:"finished_at"`
	OutputDigest    string       `json:"output_digest"`
	IntegrityDigest string       `json:"integrity_digest"`
	// OutputTail is shown to the operator but never persisted or digested.
	OutputTail string `json:"-"`
}

func checksum(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func integrity(r Record) string { r.ID = ""; r.IntegrityDigest = ""; return checksum(r) }

func absolute(root string) (string, error) {
	a, e := filepath.Abs(root)
	if e != nil {
		return "", e
	}
	return filepath.EvalSymlinks(a)
}

var identities sync.Map

// RepositoryIdentity is shared by every clone and worktree of a repository
// (its root commits), so evidence recorded in an agent's worktree verifies in
// the main checkout. Directories without Git history fall back to their path.
func RepositoryIdentity(ctx context.Context, root string) (string, error) {
	abs, err := absolute(root)
	if err != nil {
		return "", err
	}
	if id, ok := identities.Load(abs); ok {
		return id.(string), nil
	}
	id := gitrepo.Identity(ctx, abs)
	if id == "" {
		id = abs
	}
	identities.Store(abs, id)
	return id, nil
}

// Validate binds a stored record to its exact plan, commit, command and criterion.
// The checksum detects accidental edits; it is not authentication against a malicious database writer.
func Validate(r Record, root, revision string, p planning.Plan, criterion string, command []string) error {
	identity, err := RepositoryIdentity(context.Background(), root)
	if err != nil {
		return err
	}
	path, err := absolute(root)
	if err != nil {
		return err
	}
	if r.SchemaVersion != "1" || r.IntegrityDigest != integrity(r) || r.ID != "evidence:"+r.IntegrityDigest {
		return errors.New("invalid evidence integrity")
	}
	// Records from Radar 0.1 used the checkout path as repository identity.
	if (r.Repository != identity && r.Repository != path) || r.Revision != revision || r.PlanDigest != planning.Digest(p) || !reflect.DeepEqual(r.Command, command) {
		return errors.New("evidence repository, revision, plan or command mismatch")
	}
	if _, e := time.Parse(time.RFC3339Nano, r.StartedAt); e != nil {
		return errors.New("invalid evidence timestamp")
	}
	if _, e := time.Parse(time.RFC3339Nano, r.FinishedAt); e != nil {
		return errors.New("invalid evidence timestamp")
	}
	switch r.Status {
	case model.StatusPassed, model.StatusFailed, model.StatusTimeout, model.StatusUnknown, model.StatusError:
	default:
		return errors.New("invalid evidence status")
	}
	if r.Status == model.StatusPassed && (r.ExitCode != 0 || r.TestsRun <= 0 || r.TestsFailed > 0) {
		return errors.New("passed evidence has nonzero exit code or failures")
	}
	found := false
	for _, id := range r.Criteria {
		if id == criterion {
			found = true
		}
	}
	declaredBy := false
	for _, c := range p.Acceptance {
		if c.ID == criterion && c.Rule != nil && c.Rule.Kind == "test_run" && reflect.DeepEqual(c.Rule.Command, command) {
			declaredBy = true
		}
	}
	for _, c := range p.Constraints {
		if "constraint:"+c.ID == criterion && c.Rule != nil && c.Rule.Kind == "test_run" && reflect.DeepEqual(c.Rule.Command, command) {
			declaredBy = true
		}
	}
	if !found || !declaredBy {
		return errors.New("evidence does not cover declared criterion")
	}
	return nil
}
