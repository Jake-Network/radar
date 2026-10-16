package cli

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"

	"github.com/Jake-Network/radar/internal/contracts"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/languages"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/uniqueness"
)

// Selection is an acceleration policy, never a proof of independence. Peers
// without a resolved relation are omitted with an explicit NOT VERIFIED scope.
func selectHookPeers(ctx context.Context, root, base string, own map[string]bool, peers []hookPeer, cache *indexer.ParseCache) ([]hookPeer, int, error) {
	selected := []hookPeer{}
	omitted := 0
	var graph model.Snapshot
	graphReady := false
	declarations := loadHookDeclarations(ctx, root, base)
	declarations.merge(loadHookDeclarations(ctx, root, "WORKTREE"))
	ownRoutes := false
	routesReady := false
	paths := hookPeerPaths(ctx, base, peers)
	for i, peer := range peers {
		if err := ctx.Err(); err != nil {
			return selected, len(peers) - len(selected), err
		}
		if paths[i].err != nil {
			omitted++
			continue
		}
		changed := map[string]bool{}
		same := false
		for _, p := range paths[i].paths {
			changed[p] = true
			if own[p] {
				same = true
			}
		}
		if same {
			peer.priority = 0
			selected = append(selected, peer)
			continue
		}
		if len(changed) == 0 {
			omitted++
			continue
		}
		peerDeclarations := loadHookDeclarations(ctx, peer.worktree.Path, "WORKTREE")
		peerDeclarations.merge(declarations)
		related := peerDeclarations.related(own, changed) || hookMigrationPaths(own) && hookMigrationPaths(changed)
		if !related && hookSourcePaths(own) && hookSourcePaths(changed) {
			if !routesReady {
				ownRoutes = hookRoutePaths(ctx, root, base, own)
				routesReady = true
			}
			related = ownRoutes && hookRoutePaths(ctx, peer.worktree.Path, base, changed)
		}
		if !related && hookSourcePaths(own) && hookSourcePaths(changed) {
			// Resolve both saved trees through the common indexer. This sees a peer's
			// newly saved imports and reuses syntax only, never stale resolved edges.
			if !graphReady {
				var err error
				graph, err = hookSelectionGraph(ctx, root, cache)
				if err != nil {
					return selected, len(peers) - len(selected), err
				}
				graphReady = true
			}
			other, e := hookSelectionGraph(ctx, peer.worktree.Path, cache)
			if e != nil {
				omitted++
				continue
			}
			related = hookGraphRelated(own, changed, graph, other)
		}
		if related {
			peer.priority = 1
			selected = append(selected, peer)
		} else {
			omitted++
		}
	}
	return selected, omitted, nil
}
func hookSelectionGraph(ctx context.Context, root string, cache *indexer.ParseCache) (model.Snapshot, error) {
	provider, err := indexer.NewWorktree(root)
	if err != nil {
		return model.Snapshot{}, err
	}
	return indexer.BuildWithCache(ctx, root, "WORKTREE", provider, cache)
}
func hookSourcePaths(paths map[string]bool) bool {
	for p := range paths {
		if _, ok := languages.ForPath(p); ok {
			return true
		}
	}
	return false
}
func hookMigrationPaths(paths map[string]bool) bool {
	for p := range paths {
		if strings.Contains(p, "migration") || strings.Contains(p, "/versions/") {
			return true
		}
	}
	return false
}
func hookGraphRelated(left, right map[string]bool, graphs ...model.Snapshot) bool {
	edges := map[string][]string{}
	for _, graph := range graphs {
		for _, e := range graph.Edges {
			if e.Kind == "DEPENDS_ON" {
				edges[e.From] = append(edges[e.From], e.To)
				edges[e.To] = append(edges[e.To], e.From)
			}
		}
	}
	queue := []string{}
	seen := map[string]bool{}
	for p := range left {
		id := model.FileID(p)
		queue = append(queue, id)
		seen[id] = true
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if right[strings.TrimPrefix(id, "file:")] {
			return true
		}
		for _, next := range edges[id] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}

type hookDeclarations struct {
	bindings []contracts.Binding
	global   bool
}

func loadHookDeclarations(ctx context.Context, root, ref string) hookDeclarations {
	out := hookDeclarations{}
	// The hook has already inventoried its pinned base. Reuse that immutable
	// inventory so absent optional declarations do not each launch Git again.
	var tracked map[string]bool
	if ref != "WORKTREE" {
		if entries, err := gitrepo.Entries(ctx, root, ref); err == nil {
			tracked = make(map[string]bool, len(entries))
			for _, entry := range entries {
				tracked[entry.Path] = true
			}
		}
	}
	read := func(path string) ([]byte, error) {
		if tracked != nil && !tracked[path] {
			return nil, os.ErrNotExist
		}
		return gitrepo.ReadFile(ctx, root, ref, path)
	}
	if data, err := read(".radar/contracts.json"); err == nil {
		var manifest contracts.Manifest
		if json.Unmarshal(data, &manifest) == nil {
			out.bindings = manifest.Bindings
		}
	}
	for _, path := range []string{".radar/unique.json", ".radar/migrations.json"} {
		if data, err := read(path); err == nil && len(data) > 0 {
			out.global = true
		}
	}
	return out
}
func (d *hookDeclarations) merge(other hookDeclarations) {
	d.bindings = append(d.bindings, other.bindings...)
	d.global = d.global || other.global
}
func (d hookDeclarations) related(left, right map[string]bool) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	if d.global {
		return true
	}
	for _, path := range []string{".radar/contracts.json", ".radar/unique.json", ".radar/migrations.json"} {
		if left[path] || right[path] {
			return true
		}
	}
	for _, b := range d.bindings {
		a, z := false, false
		for _, p := range []string{b.Schema, b.Producer, b.Consumer} {
			a = a || left[p]
			z = z || right[p]
		}
		if a && z {
			return true
		}
	}
	return false
}

type hookPeerPathResult struct {
	paths []string
	err   error
}

func hookPeerPaths(ctx context.Context, base string, peers []hookPeer) []hookPeerPathResult {
	results := make([]hookPeerPathResult, len(peers))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(4, len(peers)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				results[i].paths, results[i].err = gitrepo.ChangedFiles(ctx, peers[i].worktree.Path, base, "WORKTREE")
			}
		}()
	}
	for i := range peers {
		select {
		case jobs <- i:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return results
		}
	}
	close(jobs)
	workers.Wait()
	return results
}

func hookRoutePaths(ctx context.Context, root, base string, paths map[string]bool) bool {
	for p := range paths {
		if _, ok := languages.ForPath(p); !ok {
			continue
		}
		for _, ref := range []string{"WORKTREE", base} {
			if err := ctx.Err(); err != nil {
				return false
			}
			source, err := gitrepo.ReadFile(ctx, root, ref, p)
			if err == nil && uniqueness.RouteRelevant(p, string(source)) {
				return true
			}
		}
	}
	return false
}
