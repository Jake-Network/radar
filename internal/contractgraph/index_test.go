package contractgraph

import (
	"context"
	"github.com/Jake-Network/radar/internal/graph"
	"github.com/Jake-Network/radar/internal/indexer"
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitGraphDependencies(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(root, path)
		os.MkdirAll(filepath.Dir(full), 0700)
		if e := os.WriteFile(full, []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("api.py", "class Input:\n    value: int\n")
	write("consumer.ts", "export function send(value:number) {return value}\n")
	write("schema.json", `{"type":"object","properties":{"value":{"type":"integer"}}}`)
	write(".radar/contracts.json", `{"version":1,"bindings":[{"id":"input","schema":"schema.json","pointer":"","producer":"api.py","consumer":"consumer.ts","direction":"request","fields":["value"]}]}`)
	s, e := indexer.Index(context.Background(), root, "WORKTREE")
	if e != nil {
		t.Fatal(e)
	}
	if e = Augment(context.Background(), &s); e != nil {
		t.Fatal(e)
	}
	g, e := graph.New(s)
	if e != nil {
		t.Fatal(e)
	}
	contracts := g.Select("request_schema", "")
	if len(contracts) != 1 {
		t.Fatal("wrong contract entity kind")
	}
	consumers := g.Reachable(contracts[0].ID, "CONSUMES", true)
	if len(consumers) != 1 || consumers[0].Name != "consumer.ts" {
		t.Fatal("explicit consumer relationship lost")
	}
	for _, e := range g.Edges {
		if e.Kind == "CONSUMES" && e.Provenance.Path != ".radar/contracts.json" {
			t.Fatal("dependency declared as runtime code proof")
		}
	}
}
func TestNoContractGuessing(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "api.py"), []byte("def route():\n    return {'total': 1}\n"), 0600)
	s, e := indexer.Index(context.Background(), root, "WORKTREE")
	if e != nil {
		t.Fatal(e)
	}
	Augment(context.Background(), &s)
	g, e := graph.New(s)
	if e != nil {
		t.Fatal(e)
	}
	if len(g.Select("response_schema", "")) != 0 {
		t.Fatal("invented contract from keyword")
	}
	if len(s.Diagnostics) == 0 {
		t.Fatal("silent contract degradation")
	}
}
