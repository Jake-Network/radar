// Package discovery extracts conservative, inspectable static contract candidates.
// Candidates never replace or automatically update authoritative manifests.
package discovery

import (
	"github.com/Jake-Network/radar/internal/contracts"
	"github.com/Jake-Network/radar/internal/model"
)

type Schema struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	Path     string           `json:"path"`
	Pointer  string           `json:"pointer"`
	Fields   []string         `json:"fields"`
	Evidence model.Provenance `json:"evidence"`
}
type Candidate struct {
	ID          string             `json:"id"`
	Producer    string             `json:"producer"`
	Consumer    string             `json:"consumer,omitempty"`
	Contract    string             `json:"contract"`
	Endpoint    string             `json:"endpoint,omitempty"`
	Fields      []string           `json:"fields"`
	Evidence    model.Evidence     `json:"evidence"`
	Locations   []model.Provenance `json:"locations"`
	Ambiguities []string           `json:"ambiguities,omitempty"`
}
type Report struct {
	Status           model.Status       `json:"status"`
	Revision         string             `json:"revision"`
	Schemas          []Schema           `json:"schemas"`
	Candidates       []Candidate        `json:"candidates"`
	ProposedManifest contracts.Manifest `json:"proposed_manifest"`
	Diagnostics      []model.Diagnostic `json:"diagnostics"`
	Limitations      []string           `json:"limitations"`
}
type Comparison struct {
	Status      model.Status       `json:"status"`
	Base        string             `json:"base"`
	Head        string             `json:"head"`
	Findings    []model.Finding    `json:"findings"`
	Diagnostics []model.Diagnostic `json:"diagnostics"`
	Limitations []string           `json:"limitations"`
}
