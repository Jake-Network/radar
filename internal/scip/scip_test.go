package scip

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func bytesField(n int, b []byte) []byte {
	out := binary.AppendUvarint(nil, uint64(n<<3|2))
	out = binary.AppendUvarint(out, uint64(len(b)))
	return append(out, b...)
}
func number(n int, v uint64) []byte {
	return binary.AppendUvarint(binary.AppendUvarint(nil, uint64(n<<3)), v)
}
func fixture(typed bool) ([]byte, Binding, model.Snapshot) {
	occ := bytesField(2, []byte("scip-go gomod demo v1 Foo()."))
	occ = append(occ, number(3, 1)...)
	if typed {
		r := append(number(1, 0), number(2, 0)...)
		r = append(r, number(3, 3)...)
		occ = append(occ, bytesField(8, r)...)
	} else {
		occ = append(occ, bytesField(1, []byte{0, 0, 3})...)
	}
	doc := bytesField(1, []byte("main.go"))
	doc = append(doc, bytesField(2, occ)...)
	reference := bytesField(2, []byte("scip-go gomod demo v1 Foo()."))
	reference = append(reference, bytesField(1, []byte{1, 0, 3})...)
	doc = append(doc, bytesField(2, reference)...)
	data := bytesField(1, nil)
	data = append(data, bytesField(2, doc)...)
	sum := sha256.Sum256(data)
	b := Binding{Version: 1, Revision: "commit", IndexSHA256: hex.EncodeToString(sum[:]), Sources: map[string]string{"main.go": strings.Repeat("a", 64)}}
	s := model.Snapshot{Revision: "commit", Repository: "repo", Nodes: []model.Node{{ID: "file:main.go", Kind: "file", Properties: map[string]string{"content_sha256": strings.Repeat("a", 64)}, Provenance: model.Provenance{Path: "main.go"}}}}
	return data, b, s
}
func TestStandardSCIPOccurrences(t *testing.T) {
	for _, typed := range []bool{false, true} {
		data, b, s := fixture(typed)
		if err := Augment(context.Background(), &s, data, b); err != nil {
			t.Fatal(err)
		}
		if len(s.Nodes) != 2 || len(s.Edges) != 2 {
			t.Fatal(s)
		}
		for _, e := range s.Edges {
			if e.Provenance.Evidence != model.VerifiedTool || e.Provenance.Method != "scip-import" {
				t.Fatal(e)
			}
		}
	}
}
func TestRejectStaleMalformedAndPathEscape(t *testing.T) {
	for _, kind := range []string{"truncated", "stale", "revision", "escape", "metadata", "encoding"} {
		t.Run(kind, func(t *testing.T) {
			data, b, s := fixture(false)
			switch kind {
			case "truncated":
				data = data[:len(data)-1]
			case "stale":
				b.Sources["main.go"] = "changed"
			case "revision":
				b.Revision = "other"
			case "escape":
				data = append(bytesField(1, nil), bytesField(2, bytesField(1, []byte("../main.go")))...)
			case "metadata":
				data = bytesField(2, bytesField(1, []byte("main.go")))
			case "encoding":
				doc := append(bytesField(1, []byte("main.go")), number(6, 42)...)
				data = append(bytesField(1, nil), bytesField(2, doc)...)
			}
			sum := sha256.Sum256(data)
			b.IndexSHA256 = hex.EncodeToString(sum[:])
			before := append([]model.Node(nil), s.Nodes...)
			if err := Augment(context.Background(), &s, data, b); err == nil {
				t.Fatal("accepted invalid index")
			}
			if !reflect.DeepEqual(before, s.Nodes) || len(s.Edges) != 0 || len(s.Diagnostics) != 1 {
				t.Fatal("partial imported graph", s)
			}
		})
	}
}
func TestWireDecoderRejectsMalformed(t *testing.T) {
	for _, b := range [][]byte{{0}, {0x80}, {0x0b}, {0x0a, 0xff}, {0x09, 1}, {0x0d, 1}} {
		if _, err := fields(b); err == nil {
			t.Fatalf("accepted %x", b)
		}
	}
}

func TestKnownNestedMessagesRejectMalformedAtomically(t *testing.T) {
	cases := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"external-tag", func(data []byte) []byte { return append(data, bytesField(3, []byte{0})...) }},
		{"external-truncated", func(data []byte) []byte { return append(data, bytesField(3, []byte{0x0a, 5, 1})...) }},
		{"external-wrong-wire", func(data []byte) []byte { return append(data, number(3, 1)...) }},
		{"metadata-tool-tag", func(data []byte) []byte { return append(bytesField(1, bytesField(2, []byte{0})), data[2:]...) }},
		{"metadata-truncated", func(data []byte) []byte { return append(bytesField(1, []byte{0x12, 3, 1}), data[2:]...) }},
		{"document-symbol", func(data []byte) []byte {
			fs, _ := fields(data)
			doc := append(fs[1].bytes, bytesField(3, []byte{0})...)
			return append(bytesField(1, nil), bytesField(2, doc)...)
		}},
		{"signature-nested", func(data []byte) []byte {
			symbol := bytesField(7, bytesField(2, []byte{0}))
			return append(data, bytesField(3, symbol)...)
		}},
		{"relationship-nested", func(data []byte) []byte {
			symbol := bytesField(4, bytesField(1, []byte{0xff}))
			return append(data, bytesField(3, symbol)...)
		}},
		{"diagnostic-packed", func(data []byte) []byte {
			fs, _ := fields(data)
			occ := append(bytesField(1, []byte{0, 0, 1}), bytesField(6, bytesField(5, []byte{0x80}))...)
			doc := append(fs[1].bytes, bytesField(2, occ)...)
			return append(bytesField(1, nil), bytesField(2, doc)...)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data, b, s := fixture(false)
			data = tc.mutate(data)
			sum := sha256.Sum256(data)
			b.IndexSHA256 = hex.EncodeToString(sum[:])
			nodes := append([]model.Node(nil), s.Nodes...)
			if err := Augment(context.Background(), &s, data, b); err == nil {
				t.Fatal("accepted malformed nested protobuf")
			}
			if !reflect.DeepEqual(nodes, s.Nodes) || len(s.Edges) != 0 || len(s.Diagnostics) != 1 {
				t.Fatal("partially imported invalid index", s)
			}
		})
	}
}
func TestOrdinaryIgnoredSCIPMetadataRemainsCompatible(t *testing.T) {
	data, b, s := fixture(false)
	tool := append(bytesField(1, []byte("scip-go")), bytesField(2, []byte("v1"))...)
	tool = append(tool, bytesField(3, []byte("index"))...)
	meta := append(bytesField(2, tool), bytesField(3, []byte("file:///old/clone"))...)
	meta = append(meta, number(4, 1)...)
	data = append(bytesField(1, meta), data[2:]...)
	rel := append(bytesField(1, []byte("external symbol")), number(2, 1)...)
	signature := append(bytesField(4, []byte("go")), bytesField(5, []byte("func Foo()"))...)
	symbol := bytesField(1, []byte("external symbol"))
	symbol = append(symbol, bytesField(3, []byte("documentation"))...)
	symbol = append(symbol, bytesField(4, rel)...)
	symbol = append(symbol, number(5, 17)...)
	symbol = append(symbol, bytesField(7, signature)...)
	data = append(data, bytesField(3, symbol)...)
	// Unknown future bytes cannot be presumed to contain a nested message.
	data = append(data, bytesField(99, []byte{0xff, 0})...)
	sum := sha256.Sum256(data)
	b.IndexSHA256 = hex.EncodeToString(sum[:])
	if err := Augment(context.Background(), &s, data, b); err != nil {
		t.Fatal(err)
	}
	if len(s.Edges) != 2 {
		t.Fatal(s)
	}
}
