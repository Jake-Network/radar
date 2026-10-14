package contracts

import (
	"strings"
	"testing"
)

func TestIDLBreakingChanges(t *testing.T) {
	cases := []struct{ name, path, before, after, kind string }{
		{"number reuse", "x.proto", `syntax = "proto3"; message Item { string old = 3; }`, `syntax = "proto3"; message Item { string replacement = 3; }`, "protobuf_number_reused"},
		{"wire type", "x.proto", `message Item { string value = 1; }`, `message Item { int32 value = 1; }`, "protobuf_type_changed"},
		{"reserved", "x.proto", `message Item { reserved 3; string value = 1; }`, `message Item { string value = 1; string added = 3; }`, "protobuf_reserved_violation"},
		{"required", "x.proto", `message Item { optional string value = 1; }`, `message Item { required string value = 1; }`, "protobuf_type_changed"},
		{"field removal", "x.graphql", `type Query { item: String }`, `type Query { other: String }`, "idl_field_removed"},
		{"argument tightened", "x.graphql", `type Query { item(id: ID): String }`, `type Query { item(id: ID!): String }`, "graphql_argument_changed"},
		{"enum removal", "x.graphql", `enum Color { RED BLUE }`, `enum Color { RED }`, "idl_enum_value_removed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, e := DecodeDocumentAt(tc.path, []byte(tc.before))
			if e != nil {
				t.Fatal(e)
			}
			b, e := DecodeDocumentAt(tc.path, []byte(tc.after))
			if e != nil {
				t.Fatal(e)
			}
			if s, e := Analyzable(a, ""); e != nil || len(s.Notes("")) != 0 {
				t.Fatalf("analyzable: %v %v", s, e)
			}
			changes, e := Compare(a, b, "")
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, c := range changes {
				if c.Kind == tc.kind {
					found = true
					if classify(c, "") != "breaking" {
						t.Fatalf("protocol incompatibility downgraded: %v", c)
					}
				}
			}
			if !found {
				t.Fatalf("missing %s: %+v", tc.kind, changes)
			}
		})
	}
}
func TestIDLRejectsUnsupportedAndMalformed(t *testing.T) {
	for _, tc := range []struct{ path, src string }{
		{"x.proto", `message Item { string x = 1; string y = 1; }`},
		{"x.proto", `message Item { string x = 1;`},
		{"x.proto", `message Item { map<string, string> x = 1; }`},
		{"x.proto", `import "other.proto"; message Item { string x = 1; }`},
		{"x.proto", `message Item { oneof a { string x = 1; } }`},
		{"x.graphql", `type Query { x: String`},
		{"x.graphql", `type Query { x: String @deprecated }`},
		{"x.graphql", `extend type Query { x: String }`},
		{"x.graphql", `input Item { x: String = "default" }`},
	} {
		t.Run(tc.src, func(t *testing.T) {
			if _, e := DecodeDocumentAt(tc.path, []byte(tc.src)); e == nil {
				t.Fatal("unsupported input became analyzed")
			}
		})
	}
}
func TestIDLCommentsAndNestedFields(t *testing.T) {
	doc, e := DecodeDocumentAt("x.proto", []byte(`// message Fake { string x = 3; }
 message Item { /* fake */ message Nested { string value = 1; } string real = 3; }`))
	if e != nil {
		t.Fatal(e)
	}
	s, e := Analyzable(doc, "")
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := s.Field("Item.Nested.value"); !ok {
		t.Fatal("nested declared consumer absent")
	}
	if _, ok := s.Field("Fake.x"); ok {
		t.Fatal("comment became declaration")
	}
	changes, e := Compare(doc, doc, "")
	if e != nil || len(changes) != 0 {
		t.Fatalf("unchanged contract: %v %v", changes, e)
	}
	doc2, e := DecodeDocumentAt("x.graphql", []byte("# fake\ntype Query { list: [String!]! }"))
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(canonical(doc2), "[String!]!") {
		t.Fatal("list nullability lost")
	}
}

func TestGraphQLCompatibleNullabilityChanges(t *testing.T) {
	for _, pair := range [][2]string{{`type Query { x: String }`, `type Query { x: String! }`}, {`input Item { x: String! }`, `input Item { x: String }`}, {`type Query { x(id: ID!): String }`, `type Query { x(id: ID): String }`}} {
		a, e := DecodeDocumentAt("x.graphql", []byte(pair[0]))
		if e != nil {
			t.Fatal(e)
		}
		b, e := DecodeDocumentAt("x.graphql", []byte(pair[1]))
		if e != nil {
			t.Fatal(e)
		}
		changes, e := Compare(a, b, "")
		if e != nil {
			t.Fatal(e)
		}
		for _, c := range changes {
			if classify(c, "") != "compatible" {
				t.Fatalf("compatible change became %v", c)
			}
		}
	}
}

func TestIDLCrossRepositoryLink(t *testing.T) {
	a, e := DecodeDocumentAt("api.proto", []byte(`message Item { string old = 3; }`))
	if e != nil {
		t.Fatal(e)
	}
	b, e := DecodeDocumentAt("api.proto", []byte(`message Item { string replacement = 3; }`))
	if e != nil {
		t.Fatal(e)
	}
	r := CheckLink(LinkInput{ID: "proto", ProducerBase: a, ProducerCandidate: b, ConsumerBase: []string{"Item.old"}, ConsumerCandidate: []string{"Item.old"}, ConsumerBaseState: "present", ConsumerCandidateState: "present"})
	if string(r.Status) != "failed" {
		t.Fatalf("protobuf link became %+v", r)
	}
	found := false
	for _, f := range r.Cells[3].Findings {
		found = found || f.Code == "contract_protobuf_number_reused"
	}
	if !found {
		t.Fatalf("field number reuse absent %+v", r)
	}
}

func TestProtoDeclarationKindChangeIsBreaking(t *testing.T) {
	a, e := DecodeDocumentAt("x.proto", []byte(`message X {} message Y { X value = 1; }`))
	if e != nil {
		t.Fatal(e)
	}
	b, e := DecodeDocumentAt("x.proto", []byte(`enum X { ZERO = 0; } message Y { X value = 1; }`))
	if e != nil {
		t.Fatal(e)
	}
	changes, e := Compare(a, b, "")
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, c := range changes {
		found = found || c.Kind == "idl_definition_kind_changed" && classify(c, "") == "breaking"
	}
	if !found {
		t.Fatalf("message to enum wire change was lost: %+v", changes)
	}
}
func TestIDLRejectsInvalidTypeReferencesAndProto3Required(t *testing.T) {
	for _, tc := range []struct{ path, src string }{{"x.proto", `syntax="proto3"; message X { required string value = 1; }`}, {"x.proto", `message X { Missing value = 1; }`}, {"x.graphql", `type Query { x: UnknownType }`}, {"x.graphql", `type Output { x: String } type Query { x(value: Output): String }`}, {"x.graphql", `input Input { x: String } type Query { x: Input }`}} {
		if _, e := DecodeDocumentAt(tc.path, []byte(tc.src)); e == nil {
			t.Fatalf("invalid referenced schema became analyzed: %s", tc.src)
		}
	}
}
func TestIDLLocalTypeReferencesRemainSupported(t *testing.T) {
	for _, tc := range []struct{ path, src string }{{"x.proto", `package demo; message X { message Nested { string value = 1; } Nested child = 1; demo.X other = 2; }`}, {"x.graphql", `scalar Date input Filter { date: Date } type Item { value: String } type Query { item(filter: Filter): Item }`}} {
		if _, e := DecodeDocumentAt(tc.path, []byte(tc.src)); e != nil {
			t.Fatalf("local type refs rejected %s: %v", tc.src, e)
		}
	}
}
func TestProtoKindChangePropagatesThroughConsumerPointer(t *testing.T) {
	a, e := DecodeDocumentAt("x.proto", []byte(`message X {} message Y { X value = 1; }`))
	if e != nil {
		t.Fatal(e)
	}
	b, e := DecodeDocumentAt("x.proto", []byte(`enum X { ZERO = 0; } message Y { X value = 1; }`))
	if e != nil {
		t.Fatal(e)
	}
	changes, e := Compare(a, b, "/properties/Y")
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, c := range changes {
		found = found || c.Kind == "protobuf_type_changed" && c.Field == "value"
	}
	if !found {
		t.Fatalf("named type change outside consumer pointer was ignored: %+v", changes)
	}
}
func TestIDLNestedNamedConsumerFields(t *testing.T) {
	for _, tc := range []struct{ path, src, field string }{{"x.proto", `message X { string name = 1; } message Y { X value = 1; }`, "Y.value.name"}, {"x.graphql", `type Item { name: String } type Query { items: [Item!]! }`, "Query.items[].name"}} {
		doc, e := DecodeDocumentAt(tc.path, []byte(tc.src))
		if e != nil {
			t.Fatal(e)
		}
		s, e := Analyzable(doc, "")
		if e != nil {
			t.Fatal(e)
		}
		if _, ok := s.Field(tc.field); !ok {
			t.Fatalf("known local named nested field lost %s", tc.field)
		}
		r := CheckLink(LinkInput{ID: "nested", ProducerBase: doc, ProducerCandidate: doc, ConsumerBase: []string{tc.field}, ConsumerCandidate: []string{tc.field}, ConsumerBaseState: "present", ConsumerCandidateState: "present"})
		if string(r.Status) != "passed" {
			t.Fatalf("valid named consumer falsely failed: %+v", r)
		}
	}
}
func TestIDLRecursiveNamedShapeRemainsUnknown(t *testing.T) {
	doc, e := DecodeDocumentAt("x.proto", []byte(`message Item { Item next = 1; }`))
	if e != nil {
		t.Fatal(e)
	}
	s, e := Analyzable(doc, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(s.Notes("")) == 0 {
		t.Fatal("recursive shape silently became fully analyzed")
	}
}
func TestIDLNestedNamedFieldRemovalReachesDeclaredConsumer(t *testing.T) {
	a, e := DecodeDocumentAt("x.proto", []byte(`message X { string name = 1; } message Y { X value = 1; }`))
	if e != nil {
		t.Fatal(e)
	}
	b, e := DecodeDocumentAt("x.proto", []byte(`message X {} message Y { X value = 1; }`))
	if e != nil {
		t.Fatal(e)
	}
	changes, e := Compare(a, b, "/properties/Y")
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, c := range changes {
		found = found || c.Kind == "idl_field_removed" && c.Field == "value.name"
	}
	if !found {
		t.Fatalf("nested named field removal lost: %+v", changes)
	}
}

func TestProtoEnumRejectsMalformedNumbers(t *testing.T) {
	for _, src := range []string{
		`enum E { FIRST = 1; ALIAS = 1; }`,
		`enum E { FIRST = 0; LARGE = 2147483648; }`,
		`enum E { FIRST = -2147483649; }`,
		`syntax="proto3"; enum E { FIRST = 1; ZERO = 0; }`,
		`syntax="proto3"; message M { enum E { FIRST = 1; ZERO = 0; } }`,
		`enum E { option allow_alias = true; FIRST = 0; ALIAS = 0; }`,
		`message M { reserved 0; }`,
		`message M { reserved 536870912; }`,
		`message M { reserved 536870910 to 536870912; }`,
		`message M { string x = 0; }`,
		`message M { string x = 536870912; }`,
		`message M { string x = 19000; }`,
		`message M { string x = 19999; }`,
		`message M { string x = 010; }`,
	} {
		if _, e := DecodeDocumentAt("x.proto", []byte(src)); e == nil {
			t.Fatalf("malformed or unsupported protobuf numeric rule accepted: %s", src)
		}
	}
}
func TestProtoNumericBoundariesRemainSupported(t *testing.T) {
	for _, src := range []string{
		`enum E { MIN = -2147483648; MAX = 2147483647; }`,
		`syntax="proto3"; enum E { ZERO = 0; MIN = -2147483648; MAX = 2147483647; }`,
		`message M { string first = 1; string before_system = 18999; string after_system = 20000; string maximum = 536870911; }`,
		`message M { reserved 1, 536870911; }`,
		`message M { reserved 19000 to 19999; }`,
		`message M { reserved 536870910 to max; }`,
	} {
		if _, e := DecodeDocumentAt("x.proto", []byte(src)); e != nil {
			t.Fatalf("valid protobuf numeric boundary rejected: %s: %v", src, e)
		}
	}
}

func TestIDLReferencedProtocolChangesReachConsumers(t *testing.T) {
	cases := []struct{ name, path, before, after, pointer, field, kind string }{
		{"renumber", "x.proto", `message X { string name = 1; } message Y { X value = 1; }`, `message X { string name = 2; } message Y { X value = 1; }`, "/properties/Y", "value.name", "protobuf_number_changed"},
		{"wire type", "x.proto", `message X { string name = 1; } message Y { X value = 1; }`, `message X { int32 name = 1; } message Y { X value = 1; }`, "/properties/Y", "value.name", "protobuf_type_changed"},
		{"same normalized type", "x.proto", `message X { int32 name = 1; } message Y { X value = 1; }`, `message X { sint32 name = 1; } message Y { X value = 1; }`, "/properties/Y", "value.name", "protobuf_type_changed"},
		{"output nullability", "x.graphql", `type Item { name: String! } type Query { item: Item }`, `type Item { name: String } type Query { item: Item }`, "/properties/Query", "item.name", "graphql_output_weakened"},
		{"required argument", "x.graphql", `type Item { name: String } type Query { item: Item }`, `type Item { name(id: ID!): String } type Query { item: Item }`, "/properties/Query", "item.name", "graphql_argument_required"},
		{"repeated named type", "x.proto", `message X { string name = 1; } message Y { repeated X value = 1; }`, `message X { string name = 2; } message Y { repeated X value = 1; }`, "/properties/Y", "value[].name", "protobuf_number_changed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, e := DecodeDocumentAt(c.path, []byte(c.before))
			if e != nil {
				t.Fatal(e)
			}
			b, e := DecodeDocumentAt(c.path, []byte(c.after))
			if e != nil {
				t.Fatal(e)
			}
			changes, e := Compare(a, b, c.pointer)
			if e != nil {
				t.Fatal(e)
			}
			found := false
			for _, change := range changes {
				found = found || change.Kind == c.kind && change.Field == c.field
			}
			if !found {
				t.Fatalf("missing referenced protocol change: %+v", changes)
			}
			r := CheckLink(LinkInput{ID: "nested", ProducerBase: a, ProducerCandidate: b, Pointer: c.pointer, ConsumerBase: []string{c.field}, ConsumerCandidate: []string{c.field}, ConsumerBaseState: "present", ConsumerCandidateState: "present"})
			if string(r.Status) != "failed" {
				t.Fatalf("breaking referenced contract: %+v", r)
			}
		})
	}
}

func TestIDLReferencedCompatibleChange(t *testing.T) {
	a, e := DecodeDocumentAt("x.graphql", []byte(`type Item { name: String } type Query { item: Item }`))
	if e != nil {
		t.Fatal(e)
	}
	b, e := DecodeDocumentAt("x.graphql", []byte(`type Item { name: String! } type Query { item: Item }`))
	if e != nil {
		t.Fatal(e)
	}
	changes, e := Compare(a, b, "/properties/Query")
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range changes {
		if classify(c, "") != "compatible" {
			t.Fatalf("compatible nested change: %+v", changes)
		}
	}
	r := CheckLink(LinkInput{ID: "nested", ProducerBase: a, ProducerCandidate: b, Pointer: "/properties/Query", ConsumerBase: []string{"item.name"}, ConsumerCandidate: []string{"item.name"}, ConsumerBaseState: "present", ConsumerCandidateState: "present"})
	if string(r.Status) != "passed" {
		t.Fatalf("compatible referenced contract: %+v", r)
	}
}
