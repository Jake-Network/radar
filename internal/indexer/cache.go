package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"github.com/Jake-Network/radar/internal/languages"
	"github.com/Jake-Network/radar/internal/model"
)

// ParseCache is a bounded cache of syntax results. Callers may keep it in memory
// or explicitly Load/Save it in private local state across analyses. Import
// resolution, manifest reads, source limits and diagnostics still run per tree.
// It is safe to share among concurrent builds; returned values never alias it.
type ParseCache struct {
	mu           sync.Mutex
	entries      map[string]languages.Result
	bytes        int64
	hits, misses uint64
}

func NewParseCache() *ParseCache { return &ParseCache{entries: map[string]languages.Result{}} }
func (c *ParseCache) get(key, repository, revision string) (languages.Result, bool) {
	if c == nil {
		return languages.Result{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	result, ok := c.entries[key]
	if !ok {
		c.misses++
		return languages.Result{}, false
	}
	c.hits++
	return cloneParseResult(result, repository, revision), true
}
func (c *ParseCache) put(key string, result languages.Result, sourceBytes int64) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; ok {
		return
	}
	cost := sourceBytes + parseResultBytes(result) + int64(len(key)+64)
	if len(c.entries) >= MaxSourceFiles || c.bytes+cost > MaxTotalSourceBytes {
		return
	}
	if c.entries == nil {
		c.entries = map[string]languages.Result{}
	}
	c.entries[key] = cloneParseResult(result, "", "")
	c.bytes += cost
}
func parseResultBytes(result languages.Result) int64 {
	n := int64(len(result.Package)) + int64(len(result.Nodes)*256+len(result.Edges)*256+len(result.Imports)*128+len(result.Diagnostics)*96)
	for _, node := range result.Nodes {
		n += int64(len(node.ID) + len(node.Kind) + len(node.Name) + len(node.Language) + len(node.Provenance.Path) + len(node.Provenance.Method))
		for k, v := range node.Properties {
			n += int64(len(k) + len(v) + 64)
		}
	}
	for _, edge := range result.Edges {
		n += int64(len(edge.ID) + len(edge.From) + len(edge.To) + len(edge.Kind) + len(edge.Provenance.Path) + len(edge.Provenance.Method))
	}
	for _, imp := range result.Imports {
		n += int64(len(imp.Module) + len(imp.Language))
		for _, name := range imp.Names {
			n += int64(len(name) + 16)
		}
	}
	for _, d := range result.Diagnostics {
		n += int64(len(d.Path) + len(d.Message))
	}
	return n
}
func cloneParseResult(in languages.Result, repository, revision string) languages.Result {
	out := in
	out.Nodes = append([]model.Node(nil), in.Nodes...)
	out.Edges = append([]model.Edge(nil), in.Edges...)
	out.Diagnostics = append([]model.Diagnostic(nil), in.Diagnostics...)
	out.Imports = append([]languages.Import(nil), in.Imports...)
	for i := range out.Nodes {
		node := &out.Nodes[i]
		node.Provenance.Repository = repository
		node.Provenance.Revision = revision
		if node.Properties != nil {
			props := map[string]string{}
			for k, v := range node.Properties {
				props[k] = v
			}
			node.Properties = props
		}
	}
	for i := range out.Edges {
		out.Edges[i].Provenance.Repository = repository
		out.Edges[i].Provenance.Revision = revision
	}
	for i := range out.Imports {
		out.Imports[i].Names = append([]string(nil), in.Imports[i].Names...)
	}
	return out
}

// Parse reuses a successful adapter result for identical relative paths and source
// bytes, including across private candidate directories. Repository and revision
// are provenance only and are rebound on every hit. A nil
// receiver performs the ordinary parse; canceled contexts never hit the cache.
func (c *ParseCache) Parse(ctx context.Context, adapter languages.Adapter, source languages.Source) (languages.Result, error) {
	if err := ctx.Err(); err != nil {
		return languages.Result{}, err
	}
	digest := sha256.Sum256(source.Content)
	capability := adapter.Capability()
	key := parserFingerprint() + "\x00" + source.Path + "\x00" + capability.Language + "\x00" + capability.Method + "\x00" + hex.EncodeToString(digest[:])
	if result, ok := c.get(key, source.Repository, source.Revision); ok {
		if err := ctx.Err(); err != nil {
			return languages.Result{}, err
		}
		return result, nil
	}
	result, err := adapter.Parse(ctx, source)
	if err == nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return languages.Result{}, contextErr
		}
		c.put(key, result, int64(len(source.Content)))
	}
	return result, err
}
