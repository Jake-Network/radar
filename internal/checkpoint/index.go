// Package checkpoint indexes immutable Git objects without checking out user files.
package checkpoint

import (
	"context"
	"path/filepath"

	"github.com/Jake-Network/radar/internal/contractgraph"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
)

// Index pins ref once and parses the committed tree with the same rules as the
// working-tree index; all evidence revisions contain the resolved SHA.
func Index(ctx context.Context, root, ref string) (model.Snapshot, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return model.Snapshot{}, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return model.Snapshot{}, err
	}
	sha, err := gitrepo.Resolve(ctx, absolute, ref)
	if err != nil {
		return model.Snapshot{}, err
	}
	provider := indexer.NewCommit(absolute, sha)
	defer provider.Close()
	result, err := indexer.Build(ctx, absolute, sha, provider)
	if err != nil {
		return model.Snapshot{}, err
	}
	if err := contractgraph.AugmentAt(ctx, &result, sha); err != nil {
		return model.Snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return model.Snapshot{}, err
	}
	model.SortSnapshot(&result)
	return result, nil
}
