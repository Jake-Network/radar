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
type Report struct {
	Checkpoint  string             `json:"checkpoint"`
	Base        string             `json:"base"`
	Heads       []string           `json:"heads"`
	Findings    []model.Finding    `json:"findings"`
	Diagnostics []model.Diagnostic `json:"diagnostics"`
}
type Change struct {
	BeforeType  string `json:"before_type,omitempty"`
	AfterType   string `json:"after_type,omitempty"`
	Field       string `json:"field"`
	Kind        string `json:"kind"`
	Explanation string `json:"explanation"`
}
