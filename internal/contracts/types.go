package contracts

import "github.com/radar-engine/radar/internal/model"

type Binding struct {
	ID        string   `json:"id"`
	Schema    string   `json:"schema"`
	Pointer   string   `json:"pointer"`
	Producer  string   `json:"producer"`
	Consumer  string   `json:"consumer"`
	Fields    []string `json:"fields"`
	Direction string   `json:"direction,omitempty"`
}
type Manifest struct {
	Version  int       `json:"version"`
	Bindings []Binding `json:"bindings"`
}

// Report is the outcome of a committed or working-tree contract comparison.
// Status is "incomplete" when a binding could not be analyzed, so a clean
// finding list never hides skipped contracts.
type Report struct {
	Status      model.Status       `json:"status"`
	Checkpoint  string             `json:"checkpoint"`
	Base        string             `json:"base"`
	Heads       []string           `json:"heads"`
	Bindings    int                `json:"bindings"`
	Analyzed    int                `json:"analyzed"`
	Findings    []model.Finding    `json:"findings"`
	Diagnostics []model.Diagnostic `json:"diagnostics"`
}
type Change struct {
	BeforeType  string   `json:"before_type,omitempty"`
	AfterType   string   `json:"after_type,omitempty"`
	Field       string   `json:"field"`
	Kind        string   `json:"kind"`
	Values      []string `json:"values,omitempty"`
	Explanation string   `json:"explanation"`
}
