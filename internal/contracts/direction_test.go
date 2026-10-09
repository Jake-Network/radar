package contracts

import (
	"context"
	"testing"
)

func TestDirectionalTypeCompatibility(t *testing.T) {
	for _, c := range []struct {
		old, new, direction string
		want                string
	}{{"integer", "number", "request", "compatible"}, {"number", "integer", "response", "compatible"}, {"number", "integer", "request", "breaking"}, {"integer", "number", "response", "breaking"}, {"integer", "number", "", "risk"}, {"", "string", "response", "compatible"}, {"string", "", "request", "compatible"}} {
		change := Change{Kind: "type_changed", BeforeType: c.old, AfterType: c.new}
		if got := classify(change, c.direction); got != c.want {
			t.Errorf("%+v got %s", c, got)
		}
	}
}
func TestCompatibleCommittedTypeChangeIsNotBreaking(t *testing.T) {
	for _, c := range []struct{ direction, old, new string }{{"request", "integer", "number"}, {"response", "number", "integer"}} {
		t.Run(c.direction, func(t *testing.T) {
			root, _ := fixture(t)
			write(t, root, "openapi.json", `{"components":{"schemas":{"Export":{"type":"object","properties":{"total":{"type":"`+c.old+`"}}}}}}`)
			write(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"export","schema":"openapi.json","pointer":"/components/schemas/Export","producer":"backend.py","consumer":"consumer.ts","direction":"`+c.direction+`","fields":["total"]}]}`)
			commit(t, root, "direction baseline")
			base := gitRun(t, root, "rev-parse", "HEAD")
			write(t, root, "openapi.json", `{"components":{"schemas":{"Export":{"type":"object","properties":{"total":{"type":"`+c.new+`"}}}}}}`)
			commit(t, root, "compatible type change")
			r, e := Impact(context.Background(), root, base, "HEAD")
			if e != nil || len(r.Findings) != 0 {
				t.Fatalf("compatible change reported: %+v %v", r, e)
			}
		})
	}
}
func TestRequiredRemovalDirection(t *testing.T) {
	a := doc(t, `{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`)
	b := doc(t, `{"type":"object","properties":{"x":{"type":"string"}}}`)
	cs, e := Compare(a, b, "")
	if e != nil || len(cs) != 1 || cs[0].Kind != "required_removed" {
		t.Fatal(cs, e)
	}
	if classify(cs[0], "request") != "compatible" || classify(cs[0], "response") != "breaking" {
		t.Fatal("required direction wrong")
	}
	if classify(Change{Kind: "field_removed"}, "request") != "risk" {
		t.Fatal("request removal falsely confirmed")
	}
}
