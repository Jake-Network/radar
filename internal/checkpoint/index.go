// Package checkpoint indexes immutable Git objects without checking out user files.
package checkpoint

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/radar-engine/radar/internal/contractgraph"
	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/indexer"
	"github.com/radar-engine/radar/internal/languages"
	"github.com/radar-engine/radar/internal/model"
)

const MaxTotalSourceBytes = 64 << 20
const MaxSourceFiles = 10000

// Index pins ref once. Node identities use the real repository root, just as the
// working-tree index does, while all evidence revisions contain the resolved SHA.
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
	entries, err := gitrepo.Entries(ctx, absolute, sha)
	if err != nil {
		return model.Snapshot{}, err
	}
	result := model.Snapshot{Repository: absolute, Revision: sha, Nodes: []model.Node{}, Edges: []model.Edge{}, Diagnostics: []model.Diagnostic{}}
	rid := model.StableID(absolute, "repository")
	provenance := model.Provenance{Repository: absolute, Revision: sha, Method: "git_object", Evidence: model.VerifiedStatic}
	result.Nodes = append(result.Nodes, model.Node{ID: rid, Kind: "repository", Name: filepath.Base(absolute), Provenance: provenance})
	ids := map[string]bool{rid: true}
	var total int64
	count := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return model.Snapshot{}, err
		}
		if indexer.ExcludedPath(entry.Path) {
			continue
		}
		diagnostic := func(message string) {
			result.Diagnostics = append(result.Diagnostics, model.Diagnostic{Path: entry.Path, Severity: "warning", Message: message})
		}
		if entry.Mode == "160000" {
			diagnostic("submodule skipped; external repository not indexed")
			continue
		}
		adapter, ok := languages.ForPath(entry.Path)
		if !ok {
			continue
		}
		if entry.Mode == "120000" {
			diagnostic("symlink source skipped to preserve repository boundary")
			continue
		}
		if entry.Type != "blob" || (entry.Mode != "100644" && entry.Mode != "100755") {
			diagnostic("non-regular source skipped")
			continue
		}
		if entry.Size > indexer.MaxSourceBytes {
			diagnostic("source exceeds 2 MiB limit; skipped")
			continue
		}
		if count >= MaxSourceFiles || total+entry.Size > MaxTotalSourceBytes {
			diagnostic("checkpoint source budget exceeded; snapshot is partial")
			continue
		}
		content, err := gitrepo.ReadBlob(ctx, absolute, entry.OID)
		if err != nil {
			return model.Snapshot{}, fmt.Errorf("read checkpoint %s: %w", entry.Path, err)
		}
		count++
		total += int64(len(content))
		parsed, err := adapter.Parse(ctx, languages.Source{Repository: absolute, Revision: sha, Path: entry.Path, Content: content})
		if err != nil {
			if ctx.Err() != nil {
				return model.Snapshot{}, ctx.Err()
			}
			diagnostic("structural analysis unavailable: " + err.Error())
			continue
		}
		digest := sha256.Sum256(content)
		for _, node := range parsed.Nodes {
			if node.Kind == "file" {
				node.Properties = map[string]string{"content_sha256": hex.EncodeToString(digest[:])}
			}
			if !ids[node.ID] {
				ids[node.ID] = true
				result.Nodes = append(result.Nodes, node)
			}
		}
		result.Edges = append(result.Edges, parsed.Edges...)
		result.Diagnostics = append(result.Diagnostics, parsed.Diagnostics...)
		p := provenance
		p.Path = entry.Path
		fid := model.StableID(absolute, "file", entry.Path)
		result.Edges = append(result.Edges, model.Edge{ID: model.StableID(rid, "DEFINES", fid), From: rid, To: fid, Kind: "DEFINES", Provenance: p})
	}
	if err := contractgraph.AugmentAt(ctx, &result, sha); err != nil {
		return model.Snapshot{}, err
	}
	if err := ctx.Err(); err != nil {
		return model.Snapshot{}, err
	}
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	sort.Slice(result.Edges, func(i, j int) bool { return result.Edges[i].ID < result.Edges[j].ID })
	return result, nil
}
