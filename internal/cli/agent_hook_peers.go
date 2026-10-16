package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/overlap"
)

type hookDiagnostics struct {
	StagesMS        map[string]int64 `json:"stages_ms"`
	Self            string           `json:"self"`
	PeersTotal      int              `json:"peers_total"`
	Candidates      int              `json:"peer_candidates"`
	Checked         int              `json:"peers_checked"`
	Reused          int              `json:"peers_reused"`
	Omitted         int              `json:"peers_omitted"`
	OmissionReasons map[string]int   `json:"peer_omission_reasons,omitempty"`
}

type hookPeer struct {
	worktree gitrepo.WorktreeStatus
	priority int
}

func (a *app) hookPeers(ctx context.Context, o options, in watchInput, self agentHookResponse, cache *indexer.ParseCache, diag *hookDiagnostics) (result agentHookResponse) {
	incomplete := func(reason string) agentHookResponse {
		self.Message = hookText(strings.TrimSpace(self.Message + " Radar NOT VERIFIED: peer inspection incomplete: " + reason))
		diag.Omitted = diag.PeersTotal - diag.Checked - diag.Reused
		if remaining := diag.Omitted - hookOmissionCount(diag.OmissionReasons); remaining > 0 {
			if diag.OmissionReasons == nil {
				diag.OmissionReasons = map[string]int{}
			}
			diag.OmissionReasons["inspection_incomplete"] += remaining
		}
		return self
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return incomplete("no bounded inspection deadline")
	}
	// Leave the last two seconds of the normal 20-second budget for source
	// revalidation and protocol output. Reserve from the original configured
	// budget so a late self preview cannot shrink the response allowance.
	peerCtx, cancel := context.WithDeadline(ctx, hookPeerDeadline(deadline, o.timeout))
	defer cancel()
	worktrees, err := hookWorktrees(peerCtx, a.root)
	if err != nil {
		return incomplete("worktree listing unavailable")
	}
	var current gitrepo.WorktreeStatus
	peers := []hookPeer{}
	for _, w := range worktrees {
		if hookSamePath(w.Path, a.root) {
			current = w
			continue
		}
		diag.PeersTotal++
		if w.Detached {
			if diag.OmissionReasons == nil {
				diag.OmissionReasons = map[string]int{}
			}
			diag.OmissionReasons["detached"]++
			continue
		}
		peers = append(peers, hookPeer{worktree: w, priority: 1})
	}
	diag.Omitted = hookOmissionCount(diag.OmissionReasons)
	if detached := diag.OmissionReasons["detached"]; detached > 0 {
		self.Message = hookText(self.Message + fmt.Sprintf(" Radar NOT VERIFIED: %d detached peers excluded by default; saved changes were not inspected.", detached))
	}
	if len(peers) == 0 {
		return self
	}
	if current.Detached || current.Branch == "" {
		return incomplete("detached current worktree has no named peer pair scope")
	}
	own := overlap.Input{Branch: current.Branch, Head: current.Commit}
	for i := range in.Snapshots {
		if hookSamePath(in.Snapshots[i].Worktree, a.root) {
			own.Snapshot = &in.Snapshots[i]
			break
		}
	}
	// Explicit branch lists need not include HEAD. Capture it only after their
	// selected preview is complete, and keep it fixed for each pair.
	var ownPin integration.DirtySnapshot
	if own.Snapshot != nil {
		ownPin = *own.Snapshot
	}
	if own.Snapshot == nil {
		snap, e := integration.ObserveDirty(peerCtx, current)
		if e != nil {
			return incomplete("current saved snapshot unavailable")
		}
		ownPin = snap
		if len(snap.Staged)+len(snap.Unstaged)+len(snap.Untracked) > 0 {
			own.Snapshot = &snap
		}
	}
	currentSelected := false
	for _, worktree := range in.Worktrees {
		if hookSamePath(worktree.Path, a.root) {
			currentSelected = true
			break
		}
	}
	selfOnly := self
	actionablePeers := []integration.DirtySnapshot{}
	defer func() {
		for _, pin := range actionablePeers {
			if !hookCurrentPinned(ctx, pin) {
				result = selfOnly
				result.Message = hookText(result.Message + " Radar NOT VERIFIED: peer saved input changed or could not be revalidated before response")
				return
			}
		}
		// Selected worktrees are revalidated together by finish after this
		// helper returns. Only an explicitly unselected current needs its own
		// additional observation here.
		if !currentSelected && !hookCurrentPinned(ctx, ownPin) {
			result = selfOnly
			result.Message = hookText(result.Message + " Radar NOT VERIFIED: current saved input changed or could not be revalidated during peer analysis")
		}
	}()
	ownPaths, err := hookInputPaths(peerCtx, a.root, in.Base, own)
	if err != nil {
		return incomplete("current changed paths unavailable")
	}
	selectionStarted := time.Now()
	peers, omissions, err := selectHookPeers(peerCtx, a.root, in.Base, ownPaths, peers, cache)
	diag.StagesMS["peer_selection"] = time.Since(selectionStarted).Milliseconds()
	if diag.OmissionReasons == nil {
		diag.OmissionReasons = map[string]int{}
	}
	for reason, count := range omissions {
		diag.OmissionReasons[reason] += count
	}
	diag.Candidates = len(peers)
	if err != nil {
		return incomplete("peer relationship selection unavailable: " + err.Error())
	}
	if unresolved := hookOmissionCount(omissions); unresolved > 0 {
		reasons := []string{}
		if count := omissions["no_overlapping_paths"]; count > 0 {
			reasons = append(reasons, fmt.Sprintf("skipped: no overlapping paths or resolved dependencies: %d", count))
		}
		if count := omissions["no_changed_paths"]; count > 0 {
			reasons = append(reasons, fmt.Sprintf("skipped: no changed paths: %d", count))
		}
		if count := omissions["inspection_error"]; count > 0 {
			reasons = append(reasons, fmt.Sprintf("inspection unavailable: %d", count))
		}
		self.Message = hookText(self.Message + fmt.Sprintf(" Radar NOT VERIFIED: %d peers have unresolved relationships and were not checked (%s).", unresolved, strings.Join(reasons, "; ")))
	}
	sort.SliceStable(peers, func(i, j int) bool { return peers[i].priority < peers[j].priority })
	diag.Candidates = len(peers)
	diag.Omitted = diag.PeersTotal - diag.Candidates
	cursorRoot := a.root + "#hook-peer-cursor"
	cursorKey := hookCacheKey(ctx, a.root, "peer-rotation-v1")
	// Resume after the last attempted peer, even across priority classes.
	// Otherwise a single expensive same-file peer could starve every other one.
	previous, _ := loadHookCache(cursorRoot, cursorKey)
	for i := range peers {
		if peers[i].worktree.Branch == previous.Message {
			ordered := append([]hookPeer(nil), peers...)
			peers = append(ordered[i+1:], ordered[:i+1]...)
			break
		}
	}
	for _, p := range peers {
		if peerCtx.Err() != nil {
			return incomplete("static peer deadline exceeded; completed self preview retained")
		}
		saveHookCache(cursorRoot, cursorKey, agentHookResponse{Message: p.worktree.Branch})
		snapshotStarted := time.Now()
		snap, err := integration.ObserveDirty(peerCtx, p.worktree)
		diag.StagesMS["peer_snapshots"] += time.Since(snapshotStarted).Milliseconds()
		if err != nil {
			return incomplete("peer " + p.worktree.Branch + " saved snapshot unavailable: " + err.Error())
		}
		peer := overlap.Input{Branch: p.worktree.Branch, Head: snap.Head}
		if len(snap.Staged)+len(snap.Unstaged)+len(snap.Untracked) > 0 {
			peer.Snapshot = &snap
		}
		inputs := []overlap.Input{own, peer}
		raw, _ := json.Marshal([]any{"pair-v1", in.Base, inputs, in.Configuration})
		key := hookCacheKey(peerCtx, a.root, configurationDigest(raw))
		pairRoot := a.root + "#hook-pair-" + peer.Branch
		pair, cached := loadHookCache(pairRoot, key)
		if !cached {
			analysisStarted := time.Now()
			observed, e := overlap.AnalyzeCurrent(peerCtx, a.root, in.Base, current.Branch, inputs, cache)
			diag.StagesMS["peer_analysis"] += time.Since(analysisStarted).Milliseconds()
			if e != nil {
				return incomplete("peer " + peer.Branch + " analysis unavailable: " + e.Error())
			}
			var added []model.Finding
			complete := true
			for _, limitation := range observed.Limitations {
				if strings.HasPrefix(limitation, "Overlap incomplete:") {
					complete = false
				}
			}
			if !complete {
				return incomplete("peer " + peer.Branch + " combined input inspection incomplete; no new drift decision cached")
			}
			if complete {
				baselineStarted := time.Now()
				added, e = overlap.NewWithDirty(peerCtx, a.root, in.Base, current.Branch, inputs, observed, cache)
				diag.StagesMS["peer_baseline"] += time.Since(baselineStarted).Milliseconds()
			}
			if e != nil {
				return incomplete("peer " + peer.Branch + " dirty baseline comparison unavailable: " + e.Error())
			}
			// No result is cached or made actionable until all required standalone
			// and dirty baseline comparisons and the peer revalidation have completed.
			latest, e := integration.ReobserveDirty(peerCtx, snap)
			if e != nil || latest.Digest != snap.Digest {
				return incomplete("peer " + peer.Branch + " changed during analysis")
			}
			pair = hookPairResponse(observed, added)
			saveHookCache(pairRoot, key, pair)
			diag.Checked++
		} else {
			diag.Reused++
		}
		if pair.Decision == "block" {
			actionablePeers = append(actionablePeers, snap)
			self.Decision = "block"
			self.Reason = hookText(strings.TrimSpace(self.Reason + "\n" + pair.Reason))
		}
		if pair.Message != "" {
			self.Message = hookText(strings.TrimSpace(self.Message + "\n" + pair.Message))
		}
	}

	return self
}

func hookInputPaths(ctx context.Context, root, base string, in overlap.Input) (map[string]bool, error) {
	paths, err := gitrepo.ChangedFiles(ctx, root, base, in.Head)
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, p := range paths {
		out[p] = true
	}
	if in.Snapshot != nil {
		for _, group := range [][]string{in.Snapshot.Staged, in.Snapshot.Unstaged, in.Snapshot.Untracked} {
			for _, p := range group {
				out[p] = true
			}
		}
	}
	return out, nil
}

func hookPairResponse(observed overlap.Report, added []model.Finding) agentHookResponse {
	out := agentHookResponse{}
	describe := func(findings []model.Finding) string {
		lines := []string{}
		for _, f := range findings {
			lines = append(lines, f.Code+": "+f.Explanation+" ["+strings.Join(f.Branches, ", ")+"]")
			for _, p := range f.Locations {
				lines = append(lines, fmt.Sprintf("%s:%d", p.Path, p.Line))
			}
		}
		return strings.Join(lines, "\n")
	}
	if len(added) > 0 {
		out.Decision = "block"
		out.Reason = hookText("Radar found cross-worktree drift introduced by your saved changes. Reconcile these changes before completing; static inference only.\n" + describe(added))
	} else if len(observed.Findings) > 0 {
		out.Message = hookText("Radar peer worktree drift (informational; your saved changes did not introduce it):\n" + describe(observed.Findings))
	}
	for _, limitation := range observed.Limitations {
		if strings.HasPrefix(limitation, "Overlap incomplete:") {
			out.Message = hookText(out.Message + " Radar NOT VERIFIED: " + limitation)
		}
	}
	return out
}

func hookCurrentPinned(ctx context.Context, pin integration.DirtySnapshot) bool {
	latest, err := integration.ReobserveDirty(ctx, pin)
	return err == nil && latest.Digest == pin.Digest
}

// The normal budget reserves its final two seconds even if self inspection has
// already consumed the peer allowance. Short explicit budgets reserve ten percent.
func hookPeerDeadline(deadline time.Time, budget time.Duration) time.Time {
	if budget <= 0 {
		budget = 20 * time.Second
	}
	return deadline.Add(-min(2*time.Second, budget/10))
}

func hookOmissionCount(reasons map[string]int) int {
	total := 0
	for _, count := range reasons {
		total += count
	}
	return total
}
