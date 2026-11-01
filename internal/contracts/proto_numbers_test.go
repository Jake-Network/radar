package contracts

import "testing"

func TestProtobufNumberObservationRetainsDuplicatePositions(t *testing.T) {
	src := []byte("syntax = \"proto3\";\n// = 7;\nmessage Outer {\n string a = 7;\n string b = 7;\n message Inner {string c = 7;}\n}\nmessage Other {string d = 7;}\n")
	got, err := ProtobufFieldNumbers(src)
	if err != nil || len(got) != 4 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].Line != 4 || got[1].Line != 5 || got[0].Message != "Outer" || got[2].Message != "Outer.Inner" || got[3].Message != "Other" {
		t.Fatalf("%+v", got)
	}
	if _, err = decodeProto(src); err == nil {
		t.Fatal("contract parser accepted duplicate")
	}
}
func TestProtobufUnsupportedCannotSupplyNumberEvidence(t *testing.T) {
	for _, src := range []string{`message M {map<string,string> value = 1;}`, `message M {string a = 01;}`, `message M {string a = 1;`, `message M {oneof values {string a = 1;}}`, `import "other.proto"; message M {string a = 1;}`} {
		if got, err := ProtobufFieldNumbers([]byte(src)); err == nil || len(got) != 0 {
			t.Fatalf("%s: %+v %v", src, got, err)
		}
	}
}
