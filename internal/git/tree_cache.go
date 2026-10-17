package git

import (
	"context"
	"sync"
)

type treeCacheKey struct{}
type treeCache struct {
	mu      sync.Mutex
	entries map[string][]TreeEntry
	count   int
}

// WithTreeCache shares immutable commit inventories during one bounded
// inspection. Mutable refs are resolved on every request and returned slices
// never alias the cache. No errors or canceled reads become cached evidence.
func WithTreeCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, treeCacheKey{}, &treeCache{entries: map[string][]TreeEntry{}})
}
func cachedTree(ctx context.Context, root, sha string) ([]TreeEntry, bool) {
	c, _ := ctx.Value(treeCacheKey{}).(*treeCache)
	if c == nil || ctx.Err() != nil || validOID(sha) != nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, ok := c.entries[root+"\x00"+sha]
	if !ok {
		return nil, false
	}
	copyEntries := make([]TreeEntry, len(entries))
	copy(copyEntries, entries)
	return copyEntries, true
}
func cacheTree(ctx context.Context, root, sha string, entries []TreeEntry) {
	c, _ := ctx.Value(treeCacheKey{}).(*treeCache)
	if c == nil || ctx.Err() != nil || validOID(sha) != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := root + "\x00" + sha
	if _, ok := c.entries[key]; ok || c.count+len(entries) > 64000 || len(c.entries) >= 128 {
		return
	}
	c.entries[key] = append([]TreeEntry(nil), entries...)
	c.count += len(entries)
}
