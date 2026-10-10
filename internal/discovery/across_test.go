package discovery

import (
	"context"
	"reflect"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func TestDiscoverExportsOnlyUnmatchedTypedConsumers(t *testing.T) {
	root := fixture(t)
	r, err := Discover(context.Background(), root, "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Consumers) != 0 {
		t.Fatalf("matched local consumer exported: %+v", r.Consumers)
	}
	write(t, root, "api.py", "# no producer\n")
	r, err = Discover(context.Background(), root, "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Consumers) != 1 {
		t.Fatalf("missing unmatched consumer: %+v", r)
	}
	use := r.Consumers[0]
	if use.Path != "client.ts" || use.Endpoint != "/users" || use.Method != "GET" || use.Type != "types.ts#User" || !reflect.DeepEqual(use.Fields, []string{"email"}) || len(use.Locations) < 2 {
		t.Fatalf("incorrect consumer evidence: %+v", use)
	}
}

func TestMatchAcrossLiteralMethodPathOnly(t *testing.T) {
	producer := Report{
		Schemas:    []Schema{{ID: "schema", Path: "openapi.json", Pointer: "/components/schemas/User", Fields: []string{"id"}}},
		Candidates: []Candidate{{Contract: "schema", Producer: "openapi.json", Endpoint: "/users", Method: "GET", Evidence: model.Proposed}},
	}
	use := ConsumerUse{Path: "client.ts", Endpoint: "/users", Method: "GET", Type: "types.ts#DifferentName", Fields: []string{"missing", "id"}, Locations: []model.Provenance{{Path: "client.ts", Line: 3}}}
	for _, tc := range []struct {
		name, endpoint, method string
		want                   int
	}{
		{"literal match with different type name", "/users", "GET", 1},
		{"same name different path", "/other", "GET", 0},
		{"different method", "/users", "POST", 0},
		{"unresolved method", "/users", "", 0},
		{"origin is not inferred", "https://example.com/users", "GET", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			current := use
			current.Endpoint, current.Method = tc.endpoint, tc.method
			got := MatchAcross(producer, Report{Consumers: []ConsumerUse{current}})
			if len(got) != tc.want {
				t.Fatalf("unexpected matches: %+v", got)
			}
			if len(got) == 1 && (got[0].Evidence != model.Proposed || got[0].SchemaPath != "openapi.json" || got[0].Pointer != "/components/schemas/User" || !reflect.DeepEqual(got[0].Fields, []string{"id", "missing"})) {
				t.Fatalf("incorrect proposal: %+v", got[0])
			}
		})
	}
	consumer := Report{Consumers: []ConsumerUse{use, use}}
	producer.Candidates = append(producer.Candidates, producer.Candidates[0])
	got := MatchAcross(producer, consumer)
	if len(got) != 1 {
		t.Fatalf("duplicate endpoint generated duplicate suggestion: %+v", got)
	}
	got[0].Fields[0] = "mutated"
	got[0].Locations[0].Path = "mutated"
	if use.Fields[0] != "missing" || use.Locations[0].Path != "client.ts" {
		t.Fatal("matching mutated the input")
	}
}

func TestMatchAcrossSeparateDiscoveredRepositories(t *testing.T) {
	producerRoot, consumerRoot := fixture(t), fixture(t)
	write(t, producerRoot, "client.ts", "// no consumer\n")
	write(t, producerRoot, "api.py", "# schema is OpenAPI\n")
	write(t, producerRoot, "openapi.json", `{"openapi":"3.1.0","components":{"schemas":{"User":{"properties":{"email":{"type":"string"}}}}},"paths":{"/users":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/User"}}}}}}}}}`)
	write(t, consumerRoot, "api.py", "# no producer\n")
	producer, err := Discover(context.Background(), producerRoot, "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := Discover(context.Background(), consumerRoot, "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	got := MatchAcross(producer, consumer)
	if len(got) != 1 || got[0].Consumer != "client.ts" || got[0].SchemaPath != "openapi.json" || got[0].Evidence != model.Proposed {
		t.Fatalf("unexpected cross-repository discovery: %+v", got)
	}
	if len(consumer.ProposedManifest.Bindings) != 0 || len(producer.ProposedManifest.Bindings) != 0 {
		t.Fatal("cross-repository suggestion promoted to manifest")
	}
}
