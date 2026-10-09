// Package model defines evidence-bearing values shared by Radar subsystems.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
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

// Status is the outcome of a check, report, task or evidence record.
type Status string

const (
	StatusPassed     Status = "passed"
	StatusFailed     Status = "failed"
	StatusWarning    Status = "warning"
	StatusUnknown    Status = "unknown"
	StatusBlocked    Status = "blocked"
	StatusIncomplete Status = "incomplete"
	StatusTimeout    Status = "timeout"
	StatusError      Status = "error"
)

// Severity ranks findings and diagnostics.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
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
	Path     string   `json:"path,omitempty"`
	Message  string   `json:"message"`
	Severity Severity `json:"severity"`
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
	Severity     Severity     `json:"severity"`
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
	return Finding{ID: StableID(code, explanation), Code: code, Explanation: explanation, Evidence: evidence, Severity: SeverityWarning}
}

// Entity identities are readable and independent of the checkout location, so
// plans, evidence and task packets remain valid across clones and Git worktrees.

// RepositoryID identifies the repository root node.
const RepositoryID = "repository"

// FileID identifies a repository-relative file.
func FileID(path string) string { return "file:" + path }

// EntityID identifies a declaration by kind, file and qualified scope. Repeated
// declarations of the same name receive an occurrence suffix (~1, ~2, ...).
func EntityID(kind, path, qualified string, occurrence int) string {
	id := kind + ":" + path + "#" + qualified
	if occurrence > 0 {
		id += "~" + strconv.Itoa(occurrence)
	}
	return id
}

// ModuleID identifies an import specifier as written in source.
func ModuleID(language, module string) string { return "module:" + language + ":" + module }

// ContractID identifies an explicitly bound schema object.
func ContractID(schema, pointer string) string { return "contract:" + schema + "#" + pointer }

// SchemaFieldID identifies a property of an explicitly bound schema object.
func SchemaFieldID(schema, pointer string) string { return "schema_field:" + schema + "#" + pointer }

// PathFromID returns the repository path encoded in a file, declaration or
// schema identity, or "" when the identity does not name a file.
func PathFromID(id string) string {
	kind, rest, ok := strings.Cut(id, ":")
	if !ok || kind == "module" || kind == "plan" {
		return ""
	}
	if kind == "file" {
		return rest
	}
	path, _, ok := strings.Cut(rest, "#")
	if !ok {
		return ""
	}
	return path
}

// SortSnapshot orders nodes and edges by identity for deterministic output.
func SortSnapshot(s *Snapshot) {
	sort.Slice(s.Nodes, func(i, j int) bool { return s.Nodes[i].ID < s.Nodes[j].ID })
	sort.Slice(s.Edges, func(i, j int) bool { return s.Edges[i].ID < s.Edges[j].ID })
}
