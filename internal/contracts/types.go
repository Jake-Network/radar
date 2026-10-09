package contracts

import (
	"fmt"

	"github.com/Jake-Network/radar/internal/model"
)

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
	Version  int          `json:"version"`
	Bindings []Binding    `json:"bindings"`
	Retired  []Retirement `json:"retired,omitempty"`
}

// Retirement explicitly withdraws a previously declared obligation, or some of
// its fields, so its removal is a reviewable manifest change rather than a
// silent loss of verification coverage. Radar records but cannot verify who
// reviewed it; protect .radar/ with code ownership where that matters.
type Retirement struct {
	ID     string   `json:"id"`
	Fields []string `json:"fields,omitempty"`
	Reason string   `json:"reason"`
}

// ObligationChange records how a binding declared at base differs at a head.
type ObligationChange struct {
	Binding string `json:"binding"`
	Head    string `json:"head"`
	// Kind is removed, narrowed, moved, retired, consumer_removed or invalid.
	Kind        string `json:"kind"`
	Explanation string `json:"explanation"`
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
	// Obligations lists base declarations removed, narrowed, moved or retired.
	Obligations []ObligationChange `json:"obligation_changes"`
	// Unverified lists reasons no-breaking-contracts cannot be established.
	Unverified []string `json:"unverified_obligations"`
}

// Unestablished returns every reason the report cannot support a passing
// no-breaking-contracts verdict, independent of the finding list.
func (r Report) Unestablished() []string {
	out := append([]string(nil), r.Unverified...)
	if r.Analyzed < r.Bindings {
		out = append(out, fmt.Sprintf("%d of %d declared bindings could not be analyzed", r.Bindings-r.Analyzed, r.Bindings))
	}
	return out
}

type Change struct {
	BeforeType  string   `json:"before_type,omitempty"`
	AfterType   string   `json:"after_type,omitempty"`
	Field       string   `json:"field"`
	Kind        string   `json:"kind"`
	Values      []string `json:"values,omitempty"`
	Explanation string   `json:"explanation"`
}
