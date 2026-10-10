package contracts

import (
	"errors"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func linkInput(t *testing.T, before, after string) LinkInput {
	t.Helper()
	return LinkInput{ID: "order", Direction: "response", ProducerBase: doc(t, before), ProducerCandidate: doc(t, after), ConsumerBase: []string{"x"}, ConsumerCandidate: []string{"x"}, ConsumerBaseState: "present", ConsumerCandidateState: "present"}
}

func TestCheckLinkDirectionalMatrix(t *testing.T) {
	const plain = `{"type":"object","properties":{"x":{"type":"number"},"other":{"type":"string"}}}`
	const required = `{"type":"object","properties":{"x":{"type":"number"},"other":{"type":"string"}},"required":["x"]}`
	const integer = `{"type":"object","properties":{"x":{"type":"integer"},"other":{"type":"string"}}}`
	const enumA = `{"type":"object","properties":{"x":{"type":"number","enum":[1]},"other":{"type":"string"}}}`
	const enumAB = `{"type":"object","properties":{"x":{"type":"number","enum":[1,2]},"other":{"type":"string"}}}`
	cases := []struct {
		name, before, after string
		request, response   model.Status
	}{
		{"field_removed", plain, `{"type":"object","properties":{"other":{"type":"string"}}}`, model.StatusFailed, model.StatusFailed},
		{"required_added", plain, required, model.StatusFailed, model.StatusPassed},
		{"required_removed", required, plain, model.StatusPassed, model.StatusFailed},
		{"type_narrowed", plain, integer, model.StatusFailed, model.StatusPassed},
		{"type_widened", integer, plain, model.StatusPassed, model.StatusFailed},
		{"enum_value_added", enumA, enumAB, model.StatusPassed, model.StatusWarning},
		{"enum_value_removed", enumAB, enumA, model.StatusFailed, model.StatusPassed},
		{"enum_added", plain, enumA, model.StatusFailed, model.StatusPassed},
		{"enum_removed", enumA, plain, model.StatusPassed, model.StatusWarning},
	}
	for _, tc := range cases {
		for _, direction := range []string{"request", "response"} {
			t.Run(tc.name+"/"+direction, func(t *testing.T) {
				in := linkInput(t, tc.before, tc.after)
				in.Direction = direction
				r := CheckLink(in)
				want := tc.response
				if direction == "request" {
					want = tc.request
				}
				for i, c := range r.Cells {
					expected := model.StatusPassed
					if i == 1 || i == 3 {
						expected = want
					}
					if c.Status != expected {
						t.Errorf("cell %d = %s, want %s: %+v", i, c.Status, expected, c)
					}
				}
				if r.Status != want {
					t.Fatalf("link = %s, want %s", r.Status, want)
				}
			})
		}
	}
}

func TestCheckLinkConsumerMatrix(t *testing.T) {
	in := linkInput(t, `{"properties":{"x":{"type":"string"}}}`, `{"properties":{"x":{"type":"string"},"y":{"type":"string"}}}`)
	in.ConsumerCandidate = []string{"x", "y"}
	r := CheckLink(in)
	for i, c := range r.Cells {
		want := model.StatusPassed
		if i == 2 {
			want = model.StatusFailed
		}
		if c.Status != want {
			t.Errorf("cell %d: %+v", i, c)
		}
	}
	if r.Status != model.StatusPassed {
		t.Fatal("intermediate failure affected development verdict", r)
	}
	in.ProducerCandidate = in.ProducerBase
	if r := CheckLink(in); r.Status != model.StatusFailed {
		t.Fatal("consumer-only new missing expectation must fail", r)
	}
}

func TestCheckLinkOverlapAndRequiredAddition(t *testing.T) {
	in := linkInput(t, `{"properties":{"x":{"type":"string"},"other":{"type":"string"}}}`, `{"properties":{"x":{"type":"string"}}}`)
	if r := CheckLink(in); r.Status != model.StatusPassed {
		t.Fatal("unrelated removal", r)
	}
	in.Direction = "request"
	in.ProducerCandidate = doc(t, `{"properties":{"x":{"type":"string"},"other":{"type":"string"}},"required":["other"]}`)
	if r := CheckLink(in); r.Status != model.StatusFailed {
		t.Fatal("required addition ignored", r)
	}
	in = linkInput(t, `{"properties":{"x":{"type":"array","items":{"properties":{"id":{"type":"string"}}}}}}`, `{"properties":{"x":{"type":"array","items":{"properties":{}}}}}`)
	in.ConsumerBase = []string{"x[].id"}
	in.ConsumerCandidate = []string{"x[].id"}
	if r := CheckLink(in); r.Status != model.StatusFailed {
		t.Fatal("nested array removal ignored", r)
	}
}

func TestCheckLinkIncompleteInputs(t *testing.T) {
	const schema = `{"properties":{"x":{"type":"string"}}}`
	for _, tc := range []struct {
		name   string
		mutate func(*LinkInput)
	}{
		{"candidate absent", func(in *LinkInput) { in.ProducerCandidate = nil }},
		{"candidate unreadable", func(in *LinkInput) { in.ProducerCandidateErr = errors.New("invalid JSON") }},
		{"base unreadable", func(in *LinkInput) { in.ProducerBaseErr = errors.New("invalid JSON") }},
		{"pointer missing", func(in *LinkInput) { in.Pointer = "/missing" }},
		{"pointer malformed", func(in *LinkInput) { in.Pointer = "bad" }},
		{"consumer invalid", func(in *LinkInput) { in.ConsumerCandidateState = "invalid" }},
		{"link invalid", func(in *LinkInput) { in.LinkCandidateState = "invalid" }},
		{"unsupported unchanged", func(in *LinkInput) {
			in.ProducerBase = doc(t, `{"oneOf":[{"properties":{"x":{}}},{"properties":{"y":{}}}]}`)
			in.ProducerCandidate = in.ProducerBase
		}},
		{"unsupported changed", func(in *LinkInput) { in.ProducerCandidate = doc(t, `{"properties":{"x":{"not":{"type":"number"}}}}`) }},
		{"ignored schema keyword", func(in *LinkInput) {
			in.ProducerCandidate = doc(t, `{"properties":{"x":{"type":"string","customConstraint":true}}}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := linkInput(t, schema, schema)
			tc.mutate(&in)
			r := CheckLink(in)
			if r.Status != model.StatusIncomplete || r.Cells[3].Reason == "" {
				t.Fatalf("%+v", r)
			}
		})
	}
}

func TestCheckLinkObligationRetirement(t *testing.T) {
	const schema = `{"properties":{"x":{"type":"string"},"y":{"type":"string"}}}`
	for _, kind := range []string{"removed_link", "removed_consumes", "narrowed"} {
		for _, retired := range []bool{false, true} {
			t.Run(kind+"/"+map[bool]string{false: "unretired", true: "retired"}[retired], func(t *testing.T) {
				in := linkInput(t, schema, schema)
				in.ConsumerBase = []string{"x", "y"}
				in.ConsumerCandidate = []string{"x", "y"}
				switch kind {
				case "removed_link":
					in.LinkCandidateState = "absent"
				case "removed_consumes":
					in.ConsumerCandidateState = "absent"
				case "narrowed":
					in.ConsumerCandidate = []string{"x"}
				}
				if retired {
					in.Retirements = []Retirement{{ID: in.ID, Reason: "consumer migrated"}}
					if kind == "narrowed" {
						in.Retirements[0].Fields = []string{"y"}
					}
				}
				r := CheckLink(in)
				want := model.StatusIncomplete
				if retired {
					want = model.StatusPassed
				}
				if r.Status != want || len(r.Obligations) != 1 {
					t.Fatalf("%+v want %s", r, want)
				}
			})
		}
	}
	// Neither a wrong ID, empty reason, nor a field-only retirement retires a link.
	for _, ret := range []Retirement{{ID: "other", Reason: "reviewed"}, {ID: "order"}, {ID: "order", Fields: []string{"x"}, Reason: "reviewed"}} {
		in := linkInput(t, schema, schema)
		in.LinkCandidateState = "absent"
		in.Retirements = []Retirement{ret}
		if r := CheckLink(in); r.Status != model.StatusIncomplete {
			t.Fatal("invalid retirement accepted", r)
		}
	}
}

func TestCheckLinkNewDeclarationAndMissingDirection(t *testing.T) {
	in := linkInput(t, `{"properties":{"x":{"type":"string"}}}`, `{"properties":{"x":{"type":"number"}}}`)
	in.Direction = ""
	if r := CheckLink(in); r.Status != model.StatusWarning {
		t.Fatal("directionless change must be risk", r)
	}
	in.ProducerCandidate = in.ProducerBase
	in.LinkBaseState = "absent"
	in.ConsumerBaseState = "absent"
	if r := CheckLink(in); r.Status != model.StatusPassed || len(r.Obligations) != 0 {
		t.Fatal("new link should use candidate declaration", r)
	}
}

func TestCheckLinkMalformedBaselineCannotPass(t *testing.T) {
	const schema = `{"properties":{"x":{"type":"string"}}}`
	for _, side := range []string{"consumer", "link"} {
		t.Run(side, func(t *testing.T) {
			in := linkInput(t, schema, schema)
			if side == "consumer" {
				in.ConsumerBaseState = "invalid"
			} else {
				in.LinkBaseState = "invalid"
			}
			r := CheckLink(in)
			if r.Status != model.StatusIncomplete || r.Cells[3].Reason == "" {
				t.Fatalf("malformed baseline hidden by candidate: %+v", r)
			}
		})
	}
}

func TestCheckLinkRetirementMustCoverDroppedObligation(t *testing.T) {
	const schema = `{"properties":{"order":{"properties":{"name":{"type":"string"},"total":{"type":"number"}}},"x":{"type":"string"}}}`
	for _, tc := range []struct {
		name, dropped, retired string
		want                   model.Status
	}{
		{"descendant cannot retire parent", "order", "order.name", model.StatusIncomplete},
		{"same field retires obligation", "order", "order", model.StatusPassed},
		{"parent retires descendant", "order.total", "order", model.StatusPassed},
		{"sibling does not retire field", "order.total", "order.name", model.StatusIncomplete},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := linkInput(t, schema, schema)
			in.ConsumerBase = []string{"x", tc.dropped}
			in.ConsumerCandidate = []string{"x"}
			in.Retirements = []Retirement{{ID: in.ID, Fields: []string{tc.retired}, Reason: "consumer migrated"}}
			if r := CheckLink(in); r.Status != tc.want {
				t.Fatalf("retirement coverage: %+v, want %s", r, tc.want)
			}
		})
	}
}
