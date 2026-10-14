package indexer

import (
	"context"
	"reflect"
	"testing"

	"github.com/Jake-Network/radar/internal/languages"
	"github.com/Jake-Network/radar/internal/model"
)

type countingAdapter struct {
	calls   int
	adapter languages.Adapter
}

func (a *countingAdapter) Capability() languages.Capability { return a.adapter.Capability() }
func (a *countingAdapter) Parse(ctx context.Context, s languages.Source) (languages.Result, error) {
	a.calls++
	return a.adapter.Parse(ctx, s)
}
func TestParseCacheContentProvenanceAndAliasing(t *testing.T) {
	underlying, _ := languages.ForPath("a.py")
	adapter := &countingAdapter{adapter: underlying}
	cache := NewParseCache()
	source := languages.Source{Repository: "repo", Revision: "first", Path: "a.py", Content: []byte("from lib import run\ndef foo(): pass\n")}
	first, err := cache.Parse(context.Background(), adapter, source)
	if err != nil {
		t.Fatal(err)
	}
	source.Revision = "second"
	second, err := cache.Parse(context.Background(), adapter, source)
	if err != nil {
		t.Fatal(err)
	}
	if adapter.calls != 1 {
		t.Fatal("unchanged source reparsed")
	}
	for _, node := range second.Nodes {
		if node.Provenance.Revision != "second" || node.Provenance.Repository != "repo" {
			t.Fatal("stale provenance", node)
		}
	}
	for _, edge := range second.Edges {
		if edge.Provenance.Revision != "second" {
			t.Fatal("stale edge", edge)
		}
	}
	second.Nodes[0].Name = "mutated"
	if len(second.Imports) > 0 && len(second.Imports[0].Names) > 0 {
		second.Imports[0].Names[0] = "mutated"
	}
	for _, node := range second.Nodes {
		if node.Properties != nil {
			node.Properties["mutated"] = "true"
		}
	}
	source.Revision = "first"
	again, err := cache.Parse(context.Background(), adapter, source)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, again) {
		t.Fatal("cache aliases returned results")
	}
	source.Content = []byte("def changed(): pass\n")
	if _, err := cache.Parse(context.Background(), adapter, source); err != nil {
		t.Fatal(err)
	}
	if adapter.calls != 2 {
		t.Fatal("changed source reused stale parse")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := cache.Parse(ctx, adapter, source); err == nil {
		t.Fatal("canceled cache hit succeeded")
	}
}
func TestParseCacheBoundedAndNilReceiver(t *testing.T) {
	cache := NewParseCache()
	parsed := languages.Result{Nodes: []model.Node{{ID: "file:a", Kind: "file"}}}
	cache.put("large", parsed, MaxTotalSourceBytes)
	if len(cache.entries) != 0 {
		t.Fatal("cache exceeded memory bound")
	}
	var absent *ParseCache
	adapter, _ := languages.ForPath("a.py")
	if _, err := absent.Parse(context.Background(), adapter, languages.Source{Repository: "repo", Revision: "one", Path: "a.py", Content: []byte("pass\n")}); err != nil {
		t.Fatal(err)
	}
}
