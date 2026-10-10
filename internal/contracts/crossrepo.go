package contracts

import (
	"fmt"
	"strings"

	"github.com/Jake-Network/radar/internal/model"
)

// LinkInput contains committed documents and explicit consumer declarations.
// Empty link states mean present, for callers that have no declaration delta.
// Retirements must be newly introduced, reviewed declarations, not base entries.
type LinkInput struct {
	ID                     string         `json:"id"`
	Direction              string         `json:"direction,omitempty"`
	Pointer                string         `json:"pointer"`
	ProducerBase           map[string]any `json:"producer_base"`
	ProducerCandidate      map[string]any `json:"producer_candidate"`
	ProducerBaseErr        error          `json:"-"`
	ProducerCandidateErr   error          `json:"-"`
	ConsumerBase           []string       `json:"consumer_base"`
	ConsumerCandidate      []string       `json:"consumer_candidate"`
	ConsumerBaseState      string         `json:"consumer_base_state"`
	ConsumerCandidateState string         `json:"consumer_candidate_state"`
	LinkBaseState          string         `json:"link_base_state,omitempty"`
	LinkCandidateState     string         `json:"link_candidate_state,omitempty"`
	Retirements            []Retirement   `json:"retirements,omitempty"`
}

type CellResult struct {
	Producer string          `json:"producer"`
	Consumer string          `json:"consumer"`
	Status   model.Status    `json:"status"`
	Findings []model.Finding `json:"findings"`
	Reason   string          `json:"reason,omitempty"`
}

type LinkResult struct {
	ID    string        `json:"id"`
	Cells [4]CellResult `json:"cells"`
	// Status uses candidate+candidate only; intermediate failures are rollout hints.
	Status      model.Status       `json:"status"`
	Obligations []ObligationChange `json:"obligations,omitempty"`
}

// CheckLink checks the four producer/consumer combinations without executing
// repository code. A field declaration is structural evidence, not runtime usage.
func CheckLink(in LinkInput) LinkResult {
	r := LinkResult{ID: in.ID, Obligations: []ObligationChange{}}
	docs := []map[string]any{in.ProducerBase, in.ProducerCandidate}
	errs := []error{in.ProducerBaseErr, in.ProducerCandidateErr}
	fields := [][]string{in.ConsumerBase, in.ConsumerCandidate}
	states := []string{in.ConsumerBaseState, in.ConsumerCandidateState}
	linkStates := []string{in.LinkBaseState, in.LinkCandidateState}
	for i := range linkStates {
		if linkStates[i] == "" {
			linkStates[i] = "present"
		}
	}
	retired := func(field string) bool {
		for _, ret := range in.Retirements {
			if ret.ID != in.ID || strings.TrimSpace(ret.Reason) == "" {
				continue
			}
			if len(ret.Fields) == 0 {
				return true
			}
			for _, f := range ret.Fields {
				// Retirement must cover the entire dropped obligation. A
				// descendant retires only part of its enclosing object.
				declared := strings.ReplaceAll(field, "[]", "")
				coverage := strings.ReplaceAll(f, "[]", "")
				if field != "" && (declared == coverage || strings.HasPrefix(declared, coverage+".")) {
					return true
				}
			}
		}
		return false
	}
	obligationReason := ""
	if linkStates[0] == "present" && (linkStates[1] == "absent" || states[1] == "absent") {
		kind, explanation := "removed", "link or consumer declaration removed without an explicit retirement record"
		if retired("") {
			kind, explanation = "retired", "link obligation explicitly retired"
		} else {
			obligationReason = explanation
		}
		r.Obligations = append(r.Obligations, ObligationChange{Binding: in.ID, Head: "candidate", Kind: kind, Explanation: explanation})
	} else if linkStates[0] == "present" && states[0] == "present" && states[1] == "present" {
		var dropped []string
		for _, f := range in.ConsumerBase {
			kept := false
			for _, g := range in.ConsumerCandidate {
				kept = kept || g == f
			}
			if !kept {
				dropped = append(dropped, f)
				if !retired(f) {
					obligationReason = "consumer fields narrowed without an explicit retirement record"
				}
			}
		}
		if len(dropped) > 0 {
			r.Obligations = append(r.Obligations, ObligationChange{Binding: in.ID, Head: "candidate", Kind: "narrowed", Explanation: "consumer fields no longer declared: " + strings.Join(dropped, ", ")})
		}
	}
	var changes []Change
	var diffErr error
	if errs[0] == nil && errs[1] == nil && docs[0] != nil && docs[1] != nil {
		changes, diffErr = Compare(docs[0], docs[1], in.Pointer)
	} else {
		diffErr = fmt.Errorf("producer baseline or candidate document unavailable")
	}
	for i, pair := range [4][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
		p, c := pair[0], pair[1]
		side := []string{"base", "candidate"}
		cell := CellResult{Producer: side[p], Consumer: side[c], Status: model.StatusPassed, Findings: []model.Finding{}}
		incomplete := func(reason string) {
			cell.Status = model.StatusIncomplete
			if cell.Reason == "" {
				cell.Reason = reason
			}
		}
		switch {
		case linkStates[c] == "absent" && c == 1 && retired(""):
			cell.Reason = "link obligation explicitly retired"
		case linkStates[c] != "present":
			incomplete("link declaration is " + linkStates[c])
		case states[c] == "absent" && c == 1 && retired(""):
			cell.Reason = "consumer obligation explicitly retired"
		case states[c] != "present":
			incomplete("consumer declaration is " + states[c])
		case errs[p] != nil:
			incomplete("producer document unavailable: " + errs[p].Error())
		case docs[p] == nil:
			incomplete("producer document absent")
		default:
			schema, err := Analyzable(docs[p], in.Pointer)
			if err != nil {
				incomplete(err.Error())
				break
			}
			if notes := schema.Notes(""); len(notes) > 0 {
				incomplete(strings.Join(notes, "; "))
				break
			}
			add := func(kind, field, explanation, classification string) {
				f := model.Finding{ID: model.StableID("cross-repo", in.ID, cell.Producer, cell.Consumer, kind, field), Code: "contract_" + kind, Contract: in.ID, Severity: model.SeverityError, Evidence: model.VerifiedStatic, Explanation: explanation + "; consumer fields explicitly declared (runtime usage is not proven)", Remediation: "Coordinate producer and consumer migration, then rerun the workspace gate."}
				if classification == "risk" {
					f.Severity = model.SeverityWarning
					f.Evidence = model.Inferred
				}
				cell.Findings = append(cell.Findings, f)
			}
			for _, f := range fields[c] {
				if _, exists := schema.Field(f); !exists {
					add("field_missing", f, "declared consumer field "+f+" is missing", "breaking")
				}
			}
			if p == 1 {
				if diffErr != nil {
					incomplete(diffErr.Error())
				} else {
					for _, change := range changes {
						if change.Kind == "unanalyzed" {
							incomplete(change.Explanation)
							continue
						}
						if change.Kind != "required_added" && !overlapsAny(fields[c], change.Field) {
							continue
						}
						classification := classify(change, in.Direction)
						if classification != "compatible" {
							add(change.Kind, change.Field, change.Explanation, classification)
						}
					}
				}
			}
			// Known failures remain failures even when another part is not analyzed.
			for _, f := range cell.Findings {
				if f.Severity == model.SeverityError {
					cell.Status = model.StatusFailed
					break
				}
			}
			if cell.Status == model.StatusPassed && len(cell.Findings) > 0 {
				cell.Status = model.StatusWarning
			}
		}
		if c == 1 && obligationReason != "" {
			cell.Reason = obligationReason
			if cell.Status != model.StatusFailed {
				cell.Status = model.StatusIncomplete
			}
		}
		if c == 1 && (states[0] == "invalid" || linkStates[0] == "invalid") {
			// Candidate declarations cannot reconstruct obligations hidden by
			// a malformed baseline declaration.
			cell.Reason = "baseline declaration is invalid; prior obligations are unknown"
			if cell.Status != model.StatusFailed {
				cell.Status = model.StatusIncomplete
			}
		}
		r.Cells[i] = cell
	}
	r.Status = r.Cells[3].Status
	return r
}
