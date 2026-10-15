package cli

import (
	"os"
	"path/filepath"

	"github.com/Jake-Network/radar/internal/composition"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/project"
)

// Syntax acceleration never skips inventory, imports, manifests or resolution.
// Corrupt/unsupported state is discarded and source analysis proceeds normally.
func savedSyntaxCache(root string) (*indexer.ParseCache, func()) {
	empty := func() (*indexer.ParseCache, func()) { return indexer.NewParseCache(), func() {} }
	state, err := composition.StateDir()
	if err != nil {
		return empty()
	}
	dir, err := project.SafePath(state, "syntax-cache")
	if err != nil {
		return empty()
	}
	if os.MkdirAll(dir, 0700) != nil {
		return empty()
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return empty()
	}
	name, err := project.SafePath(dir, configurationDigest([]byte(root))+".json")
	if err != nil {
		return empty()
	}
	cache, err := indexer.LoadParseCache(name)
	if err != nil {
		cache = indexer.NewParseCache()
	}
	return cache, func() { _ = cache.Save(name) }
}
