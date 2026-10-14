// Package indexer assembles deterministic repository snapshots without executing repository code.
package indexer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/languages"
	"github.com/Jake-Network/radar/internal/model"
)

const MaxSourceBytes = 2 * 1024 * 1024
const MaxSourceFiles = 10000
const MaxTotalSourceBytes = 64 << 20

// maxManifestBytes bounds auxiliary build manifests read for import resolution.
const maxManifestBytes = 1 << 20
const maxTotalManifestBytes = 8 << 20

var excluded = map[string]bool{".git": true, ".omx": true, ".agents": true, ".codex": true, ".aws": true, ".radar": true, "node_modules": true, ".gradle": true, "vendor": true, "target": true, "dist": true, "build": true, ".venv": true, "venv": true, "__pycache__": true, ".pytest_cache": true, ".next": true, "coverage": true}

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
	return BuildWithCache(ctx, repository, revision, p, nil)
}

// BuildWithCache reuses only syntax for identical repository-relative source bytes.
func BuildWithCache(ctx context.Context, repository, revision string, p Provider, cache *ParseCache) (model.Snapshot, error) {
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
	var manifestTotal int64
	manifestCount := 0
	type parseJob struct {
		entry   Entry
		adapter languages.Adapter
		content []byte
		digest  [32]byte
		parsed  languages.Result
		err     error
	}
	type fileRecord struct {
		diagnostics []model.Diagnostic
		job         *parseJob
	}
	records := make([]*fileRecord, 0, len(entries))
	parseCtx, cancelParsing := context.WithCancel(ctx)
	jobs := make(chan *parseJob, 4)
	var workers sync.WaitGroup
	closed := false
	defer func() {
		cancelParsing()
		if !closed {
			close(jobs)
		}
		workers.Wait()
	}()
	for i := 0; i < min(4, runtime.GOMAXPROCS(0)); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				job.parsed, job.err = cache.Parse(parseCtx, job.adapter, languages.Source{Repository: repository, Revision: revision, Path: job.entry.Path, Content: job.content})
				job.content = nil
			}
		}()
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return model.Snapshot{}, err
		}
		if ExcludedPath(entry.Path) {
			continue
		}
		record := &fileRecord{}
		records = append(records, record)
		diagnostic := func(message string) {
			record.diagnostics = append(record.diagnostics, model.Diagnostic{Path: entry.Path, Severity: model.SeverityWarning, Message: message})
		}
		if entry.Mode == ModeSubmodule {
			diagnostic("submodule skipped; external repository not indexed")
			continue
		}
		r.files[entry.Path] = true
		if isManifest(entry.Path) {
			switch {
			case entry.Mode != ModeRegular:
				diagnostic("workspace import resolution unknown: non-regular build manifest skipped")
			case entry.Size > maxManifestBytes:
				diagnostic("workspace import resolution unknown: build manifest exceeds 1 MiB limit")
			case manifestCount >= MaxSourceFiles || manifestTotal+entry.Size > maxTotalManifestBytes:
				diagnostic("workspace import resolution unknown: build manifest budget exhausted")
			default:
				content, err := p.Read(ctx, entry, maxManifestBytes)
				if err != nil {
					if ctx.Err() != nil {
						return model.Snapshot{}, ctx.Err()
					}
					diagnostic("workspace import resolution unknown: build manifest unavailable")
				} else {
					manifestCount++
					manifestTotal += int64(len(content))
					r.addManifest(entry.Path, content)
				}
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
		job := &parseJob{entry: entry, adapter: adapter, content: content, digest: sha256.Sum256(content)}
		record.job = job
		select {
		case jobs <- job:
		case <-ctx.Done():
			return model.Snapshot{}, ctx.Err()
		}
	}
	close(jobs)
	closed = true
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return model.Snapshot{}, err
	}
	for _, record := range records {
		result.Diagnostics = append(result.Diagnostics, record.diagnostics...)
		job := record.job
		if job == nil {
			continue
		}
		entry, parsed, digest := job.entry, job.parsed, job.digest
		if job.err != nil {
			result.Diagnostics = append(result.Diagnostics, model.Diagnostic{Path: entry.Path, Severity: model.SeverityWarning, Message: "structural analysis unavailable: " + job.err.Error()})
			continue
		}

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
		r.addSource(entry.Path, parsed)
		job.parsed = languages.Result{}
		fp := provenance
		fp.Path = entry.Path
		fileID := model.FileID(entry.Path)
		result.Edges = append(result.Edges, model.Edge{ID: model.StableID(model.RepositoryID, "DEFINES", fileID), From: model.RepositoryID, To: fileID, Kind: "DEFINES", Provenance: fp})
	}
	if skipped > 0 {
		result.Diagnostics = append(result.Diagnostics, model.Diagnostic{Severity: model.SeverityWarning, Message: fmt.Sprintf("%d source files skipped after reaching the %d-file / %d MiB source budget; index is partial", skipped, MaxSourceFiles, MaxTotalSourceBytes>>20)})
	}
	r.prepareWorkspace()
	result.Diagnostics = append(result.Diagnostics, r.workspace.diagnostics...)
	result.Edges = append(result.Edges, r.edges(repository, revision)...)
	sort.Slice(result.Nodes, func(i, j int) bool { return result.Nodes[i].ID < result.Nodes[j].ID })
	sort.Slice(result.Edges, func(i, j int) bool { return result.Edges[i].ID < result.Edges[j].ID })
	return result, nil
}
