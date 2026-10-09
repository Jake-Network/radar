// Package planning validates declared intent against evidence, without inventing a design.
package planning

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// MaxBytes bounds policy/plan input reads and parsing.
const MaxBytes = 4 << 20

func Load(path string) (Plan, error) {
	var p Plan
	f, err := os.Open(path)
	if err != nil {
		return p, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return p, err
	}
	return Parse(data)
}

// Parse decodes exact captured plan bytes without reopening an input file.
func Parse(data []byte) (Plan, error) {
	var p Plan
	if len(data) > MaxBytes {
		return p, fmt.Errorf("plan exceeds 4 MiB analysis limit")
	}
	d := json.NewDecoder(strings.NewReader(string(data)))
	d.DisallowUnknownFields()
	if err := d.Decode(&p); err != nil {
		return p, fmt.Errorf("invalid plan: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return p, fmt.Errorf("plan must contain one JSON object")
	}
	return p, nil
}
func Digest(p Plan) string {
	p.Approval = nil
	// Evidence references are attached after a run and do not change design intent.
	// Copy slices and rules so digest computation never mutates the caller's plan.
	p.Acceptance = append([]Criterion(nil), p.Acceptance...)
	for i, c := range p.Acceptance {
		if c.Rule != nil {
			rule := *c.Rule
			rule.EvidenceID = ""
			p.Acceptance[i].Rule = &rule
		}
	}
	p.Constraints = append([]Constraint(nil), p.Constraints...)
	for i, c := range p.Constraints {
		if c.Rule != nil {
			rule := *c.Rule
			rule.EvidenceID = ""
			p.Constraints[i].Rule = &rule
		}
	}
	b, _ := json.Marshal(p)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func Approved(p Plan) bool {
	a := p.Approval
	if a == nil {
		return false
	}
	_, err := time.Parse(time.RFC3339, a.ReviewedAt)
	return err == nil && strings.TrimSpace(a.Reviewer) != "" && a.Checkpoint != "" && a.Checkpoint == p.BaseRevision && a.PlanDigest == Digest(p) && len(p.Incomplete) == 0
}
