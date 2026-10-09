// Package indexer assembles deterministic repository snapshots without executing repository code.
package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/languages"
	"github.com/Jake-Network/radar/internal/model"
)

const MaxSourceBytes = 2 * 1024 * 1024
const MaxSourceFiles = 10000
const MaxTotalSourceBytes = 64 << 20

// maxManifestBytes bounds auxiliary build manifests read for import resolution.
const maxManifestBytes = 1 << 20

var excluded = map[string]bool{".git": true, ".omx": true, ".agents": true, ".codex": true, ".aws": true, ".radar": true, "node_modules": true, "vendor": true, "target": true, "dist": true, "build": true, ".venv": true, "venv": true, "__pycache__": true, ".next": true, "coverage": true}

// ExcludedPath applies one generated and private directory policy to every source.
func ExcludedPath(p string) bool {
	for _, part := range strings.Split(p, "/") {
		if excluded[part] {
			return true
		}
	}
	return false
}

// Index snapshots the working tree below root. Inside a Git work tree the file
// list honors .gitignore; otherwise the directory is walked.
func Index(ctx context.Context, root, revision string) (model.Snapshot, error) {
	w, err := newWorktree(root)
	if err != nil {
		return model.Snapshot{}, err
	}
	return Build(ctx, w.root, revision, w)
}

// Build parses every supported file a provider exposes. Working-tree and
// committed snapshots share this loop, limits and diagnostics.
func Build(ctx context.Context, repository, revision string, p Provider) (model.Snapshot, error) {
	entries, err := p.Entries(ctx)
	if err != nil {
		return model.Snapshot{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	result := model.Snapshot{Repository: repository, Revision: revision, Nodes: []model.Node{}, Edges: []model.Edge{}, Diagnostics: []model.Diagnostic{}}
	provenance := model.Provenance{Repository: repository, Revision: revision, Method: p.Method(), Evidence: model.VerifiedStatic}
	result.Nodes = append(result.Nodes, model.Node{ID: model.RepositoryID, Kind: "repository", Name: path.Base(repository), Provenance: provenance})
	ids := map[string]bool{model.RepositoryID: true}
	r := newResolver()
	count, skipped := 0, 0
	var total int64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return model.Snapshot{}, err
		}
		if ExcludedPath(entry.Path) {
			continue
		}
		diagnostic := func(message string) {
			result.Diagnostics = append(result.Diagnostics, model.Diagnostic{Path: entry.Path, Severity: model.SeverityWarning, Message: message})
		}
		if entry.Mode == ModeSubmodule {
			diagnostic("submodule skipped; external repository not indexed")
			continue
		}
		r.files[entry.Path] = true
		if path.Base(entry.Path) == "go.mod" && entry.Mode == ModeRegular {
			if content, err := p.Read(ctx, entry, maxManifestBytes); err == nil {
				r.addGoModule(entry.Path, content)
			}
		}
		adapter, ok := languages.ForPath(entry.Path)
		if !ok {
			continue
		}
		switch {
		case entry.Mode == ModeSymlink:
			diagnostic("symlink source skipped to preserve repository boundary")
			continue
		case entry.Mode != ModeRegular:
			diagnostic("non-regular source skipped")
			continue
		case entry.Size > MaxSourceBytes:
			diagnostic("source exceeds 2 MiB limit; skipped")
			continue
		case count >= MaxSourceFiles || total+entry.Size > MaxTotalSourceBytes:
			skipped++
			continue
		}
		content, err := p.Read(ctx, entry, MaxSourceBytes)
		if errors.Is(err, gitrepo.ErrBlobTooLarge) || errors.Is(err, errTooLarge) {
			diagnostic("source exceeds 2 MiB limit; skipped")
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				return model.Snapshot{}, ctx.Err()
			}
			return model.Snapshot{}, fmt.Errorf("read %s: %w", entry.Path, err)
		}
		count++
		total += int64(len(content))
		parsed, err := adapter.Parse(ctx, languages.Source{Repository: repository, Revision: revision, Path: entry.Path, Content: content})
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
		r.addSource(entry.Path, parsed.Imports)
		fp := provenance
		fp.Path = entry.Path
		fileID := model.FileID(entry.Path)
		result.Edges = append(result.Edges, model.Edge{ID: model.StableID(model.RepositoryID, "DEFINES", fileID), From: model.RepositoryID, To: fileID, Kind: "DEFINES", Provenance: fp})
	}
	if skipped > 0 {
		result.Diagnostics = append(result.Diagnostics, model.Diagnostic{Severity: model.SeverityWarning, Message: fmt.Sprintf("%d source files skipped after reaching the %d-file / %d MiB source budget; index is partial", skipped, MaxSourceFiles, MaxTotalSourceBytes>>20)})
	}
	result.Edges = append(result.Edges, r.edges(repository, revision)...)
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	sort.Slice(result.Edges, func(i, j int) bool { return result.Edges[i].ID < result.Edges[j].ID })
	return result, nil
}
