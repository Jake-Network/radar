// Package indexer assembles deterministic repository snapshots without executing repository code.
package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/radar-engine/radar/internal/languages"
	"github.com/radar-engine/radar/internal/model"
)

const MaxSourceBytes = 2 * 1024 * 1024
const MaxSourceFiles = 10000
const MaxTotalSourceBytes = 64 << 20

var excluded = map[string]bool{".git": true, ".omx": true, ".agents": true, ".codex": true, ".aws": true, ".radar": true, "node_modules": true, "vendor": true, "target": true, "dist": true, "build": true, ".venv": true, "venv": true, "__pycache__": true, ".next": true, "coverage": true}

func Index(ctx context.Context, root, revision string) (model.Snapshot, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return model.Snapshot{}, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return model.Snapshot{}, err
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return model.Snapshot{}, err
	}
	if !info.IsDir() {
		return model.Snapshot{}, fmt.Errorf("repository root is not a directory")
	}
	result := model.Snapshot{Repository: absolute, Revision: revision, Nodes: []model.Node{}, Edges: []model.Edge{}, Diagnostics: []model.Diagnostic{}}
	repositoryID := model.StableID(absolute, "repository")
	result.Nodes = append(result.Nodes, model.Node{ID: repositoryID, Kind: "repository", Name: filepath.Base(absolute), Provenance: model.Provenance{Repository: absolute, Revision: revision, Method: "filesystem", Evidence: model.VerifiedStatic}})
	ids := map[string]bool{repositoryID: true}
	sourceFiles, totalBytes := 0, int64(0)
	err = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != absolute && excluded[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		relative, err := filepath.Rel(absolute, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		adapter, ok := languages.ForPath(relative)
		if !ok {
			return nil
		}
		diagnostic := func(message string) {
			result.Diagnostics = append(result.Diagnostics, model.Diagnostic{Path: relative, Severity: "warning", Message: message})
		}
		if entry.Type()&os.ModeSymlink != 0 {
			diagnostic("symlink source skipped to preserve repository boundary")
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			diagnostic("non-regular source skipped")
			return nil
		}
		if info.Size() > MaxSourceBytes {
			diagnostic("source exceeds 2 MiB limit; skipped")
			return nil
		}
		if sourceFiles >= MaxSourceFiles || totalBytes+info.Size() > MaxTotalSourceBytes {
			diagnostic("working-tree source budget exceeded; index is partial")
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		content, readErr := io.ReadAll(io.LimitReader(file, MaxSourceBytes+1))
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if len(content) > MaxSourceBytes {
			diagnostic("source exceeds 2 MiB limit; skipped")
			return nil
		}
		sourceFiles++
		totalBytes += int64(len(content))
		parsed, err := adapter.Parse(ctx, languages.Source{Repository: absolute, Revision: revision, Path: relative, Content: content})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			diagnostic("structural analysis unavailable: " + err.Error())
			return nil
		}
		digest := sha256.Sum256(content)
		for i := range parsed.Nodes {
			if parsed.Nodes[i].Kind == "file" {
				parsed.Nodes[i].Properties = map[string]string{"content_sha256": hex.EncodeToString(digest[:])}
			}
		}
		for _, node := range parsed.Nodes {
			if !ids[node.ID] {
				ids[node.ID] = true
				result.Nodes = append(result.Nodes, node)
			}
		}
		result.Edges = append(result.Edges, parsed.Edges...)
		result.Diagnostics = append(result.Diagnostics, parsed.Diagnostics...)
		fileID := model.StableID(absolute, "file", relative)
		result.Edges = append(result.Edges, model.Edge{ID: model.StableID(repositoryID, "DEFINES", fileID), From: repositoryID, To: fileID, Kind: "DEFINES", Provenance: model.Provenance{Repository: absolute, Revision: revision, Path: relative, Method: "filesystem", Evidence: model.VerifiedStatic}})
		return nil
	})
	if err != nil {
		return model.Snapshot{}, err
	}
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	sort.Slice(result.Edges, func(i, j int) bool { return result.Edges[i].ID < result.Edges[j].ID })
	return result, nil
}

// ExcludedPath applies the same generated and private directory policy to Git trees.
func ExcludedPath(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if excluded[part] {
			return true
		}
	}
	return false
}
