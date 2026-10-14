package integration

import (
	"context"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/symbols"
)

func TestSymbolUnsupportedReceiversDoNotBlockSupportedDeclarations(t *testing.T) {
	for _, fixture := range []struct {
		name  string
		build func(*testing.T) string
	}{{"cpp", cppFixture}, {"java", javaFixture}} {
		t.Run(fixture.name, func(t *testing.T) {
			root := fixture.build(t)
			r, e := Preview(context.Background(), root, Options{Base: "baseline", Branches: []string{"rename"}})
			if e != nil {
				t.Fatal(e)
			}
			for _, c := range r.Checks {
				if c.ID == symbols.CheckID && c.Status != model.StatusPassed {
					t.Fatalf("supported declarations blocked: %+v; diagnostics: %+v", c, r.Diagnostics)
				}
			}
		})
	}
}
