// Package model defines evidence-bearing values shared by Radar subsystems.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

type Evidence string

const (
	VerifiedStatic Evidence = "verified_static"
	VerifiedTool   Evidence = "verified_tool"
	ObservedTest   Evidence = "observed_test"
	Inferred       Evidence = "inferred"
	Proposed       Evidence = "proposed"
	Unknown        Evidence = "unknown"
)

type Provenance struct {
	Repository string   `json:"repository"`
	Revision   string   `json:"revision"`
	Path       string   `json:"path,omitempty"`
	Line       int      `json:"line,omitempty"`
	EndLine    int      `json:"end_line,omitempty"`
	Method     string   `json:"method"`
	Evidence   Evidence `json:"evidence"`
	Timestamp  string   `json:"timestamp,omitempty"`
}
type Node struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Language   string            `json:"language,omitempty"`
	Properties map[string]string `json:"properties,omitempty"`
	Provenance Provenance        `json:"provenance"`
}
type Edge struct {
	ID         string     `json:"id"`
	From       string     `json:"from"`
	To         string     `json:"to"`
	Kind       string     `json:"kind"`
	Provenance Provenance `json:"provenance"`
}
type Diagnostic struct {
	Path     string `json:"path,omitempty"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}
type Snapshot struct {
	Repository  string       `json:"repository"`
	Revision    string       `json:"revision"`
	Nodes       []Node       `json:"nodes"`
	Edges       []Edge       `json:"edges"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}
type Finding struct {
	ID           string       `json:"id"`
	Severity     string       `json:"severity"`
	Evidence     Evidence     `json:"evidence"`
	Code         string       `json:"code"`
	Explanation  string       `json:"explanation"`
	Contract     string       `json:"contract,omitempty"`
	Producer     string       `json:"producer,omitempty"`
	Consumer     string       `json:"consumer,omitempty"`
	Branches     []string     `json:"branches,omitempty"`
	Locations    []Provenance `json:"locations,omitempty"`
	Remediation  string       `json:"remediation,omitempty"`
	Verification string       `json:"verification,omitempty"`
}

func StableID(parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(h[:16])
}
func NewFinding(code, explanation string, evidence Evidence) Finding {
	return Finding{ID: StableID(code, explanation), Code: code, Explanation: explanation, Evidence: evidence, Severity: "warning"}
}
