package graph

import (
	"github.com/radar-engine/radar/internal/model"
	"testing"
)

func TestGraphQueries(t *testing.T) {
	s := model.Snapshot{Nodes: []model.Node{{ID: "a", Kind: "file", Name: "a"}, {ID: "b", Kind: "symbol", Name: "b"}, {ID: "c", Kind: "symbol", Name: "c"}}, Edges: []model.Edge{{ID: "ab", From: "a", To: "b", Kind: "DEFINES"}, {ID: "bc", From: "b", To: "c", Kind: "REFERENCES"}, {ID: "ca", From: "c", To: "a", Kind: "REFERENCES"}}}
	g, e := New(s)
	if e != nil {
		t.Fatal(e)
	}
	if len(g.Reachable("a", "", false)) != 2 {
		t.Fatal("cycle traversal failed")
	}
	if len(g.Reachable("a", "DEFINES", false)) != 1 {
		t.Fatal("edge filter failed")
	}
	if len(g.Select("symbol", "")) != 2 {
		t.Fatal("selection failed")
	}
	if g.Mermaid() != g.Mermaid() {
		t.Fatal("nondeterministic")
	}
	s.Edges[0].To = "absent"
	if _, e = New(s); e == nil {
		t.Fatal("dangling edge accepted")
	}
}
