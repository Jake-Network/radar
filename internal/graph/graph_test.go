package graph

import (
	"github.com/Jake-Network/radar/internal/model"
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

func TestResolveAndDistances(t *testing.T) {
	s := model.Snapshot{Nodes: []model.Node{
		{ID: "file:a.py", Kind: "file", Name: "a.py", Provenance: model.Provenance{Path: "a.py"}},
		{ID: "file:b.py", Kind: "file", Name: "b.py", Provenance: model.Provenance{Path: "b.py"}},
		{ID: "file:c.py", Kind: "file", Name: "c.py", Provenance: model.Provenance{Path: "c.py"}},
		{ID: "class:b.py#Order", Kind: "class", Name: "Order", Properties: map[string]string{"qualified_name": "Order"}, Provenance: model.Provenance{Path: "b.py"}},
	}, Edges: []model.Edge{
		{ID: "1", From: "file:a.py", To: "file:b.py", Kind: "DEPENDS_ON"},
		{ID: "2", From: "file:c.py", To: "file:a.py", Kind: "DEPENDS_ON"},
	}}
	g, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	hops := g.Distances("file:b.py", "DEPENDS_ON", true, 0)
	if len(hops) != 2 || hops[0].ID != "file:a.py" || hops[1].ID != "file:c.py" || hops[1].Depth != 2 || hops[1].Via != "file:a.py" {
		t.Fatalf("%+v", hops)
	}
	if len(g.Distances("file:b.py", "DEPENDS_ON", true, 1)) != 1 {
		t.Fatal("depth limit ignored")
	}
	for query, want := range map[string]string{"b.py#Order": "class:b.py#Order", "order": "class:b.py#Order", "a.py": "file:a.py", "file:c.py": "file:c.py"} {
		got := g.Resolve(query, "")
		if len(got) != 1 || got[0].ID != want {
			t.Errorf("%s: %+v", query, got)
		}
	}
}
