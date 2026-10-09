package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/radar-engine/radar/internal/planning"
	"path/filepath"
	"reflect"
	"time"
)

type Record struct {
	SchemaVersion   string   `json:"schema_version"`
	ID              string   `json:"id"`
	Repository      string   `json:"repository"`
	Revision        string   `json:"revision"`
	PlanDigest      string   `json:"plan_digest"`
	Command         []string `json:"command"`
	Criteria        []string `json:"criteria"`
	TestsRun        int      `json:"tests_run"`
	ExitCode        int      `json:"exit_code"`
	Status          string   `json:"status"`
	StartedAt       string   `json:"started_at"`
	FinishedAt      string   `json:"finished_at"`
	OutputDigest    string   `json:"output_digest"`
	IntegrityDigest string   `json:"integrity_digest"`
}

func checksum(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func integrity(r Record) string { r.ID = ""; r.IntegrityDigest = ""; return checksum(r) }
func Repository(root string) (string, error) {
	a, e := filepath.Abs(root)
	if e != nil {
		return "", e
	}
	return filepath.EvalSymlinks(a)
}

// Validate binds a stored record to its exact plan, commit, command and criterion.
// The checksum detects accidental edits; it is not authentication against a malicious database writer.
func Validate(r Record, root, revision string, p planning.Plan, criterion string, command []string) error {
	repository, err := Repository(root)
	if err != nil {
		return err
	}
	if r.SchemaVersion != "1" || r.IntegrityDigest != integrity(r) || r.ID != "evidence:"+r.IntegrityDigest {
		return errors.New("invalid evidence integrity")
	}
	if r.Repository != repository || r.Revision != revision || r.PlanDigest != planning.Digest(p) || !reflect.DeepEqual(r.Command, command) {
		return errors.New("evidence repository, revision, plan or command mismatch")
	}
	if _, e := time.Parse(time.RFC3339Nano, r.StartedAt); e != nil {
		return errors.New("invalid evidence timestamp")
	}
	if _, e := time.Parse(time.RFC3339Nano, r.FinishedAt); e != nil {
		return errors.New("invalid evidence timestamp")
	}
	if r.Status != "passed" && r.Status != "failed" && r.Status != "timeout" && r.Status != "unknown" {
		return errors.New("invalid evidence status")
	}
	if r.Status == "passed" && (r.ExitCode != 0 || r.TestsRun <= 0) {
		return errors.New("passed evidence has nonzero exit code")
	}
	found := false
	for _, id := range r.Criteria {
		if id == criterion {
			found = true
		}
	}
	declared := false
	for _, c := range p.Acceptance {
		if c.ID == criterion && c.Rule != nil && c.Rule.Kind == "test_run" && reflect.DeepEqual(c.Rule.Command, command) {
			declared = true
		}
	}
	for _, c := range p.Constraints {
		if "constraint:"+c.ID == criterion && c.Rule != nil && c.Rule.Kind == "test_run" && reflect.DeepEqual(c.Rule.Command, command) {
			declared = true
		}
	}
	if !found || !declared {
		return errors.New("evidence does not cover declared criterion")
	}
	return nil
}
