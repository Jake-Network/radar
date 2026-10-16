package overlap

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"sync"

	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
)

type analysisCacheKey struct{}
type analysisCache struct {
	mu      sync.Mutex
	reports map[string]integration.Report
}

// WithAnalysisCache reuses completed static analysis of identical immutable
// inputs within one inspection. It never supplies execution evidence, persists
// results, or replaces the caller's final saved-input revalidation.
func WithAnalysisCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, analysisCacheKey{}, &analysisCache{reports: map[string]integration.Report{}})
}
func cachedAnalysis(ctx context.Context, root, base string, inputs []Input) (integration.Report, bool) {
	cache, _ := ctx.Value(analysisCacheKey{}).(*analysisCache)
	if cache == nil || ctx.Err() != nil || !immutableAnalysisInputs(base, inputs) {
		return integration.Report{}, false
	}
	raw, _ := json.Marshal([]any{root, base, inputs})
	cache.mu.Lock()
	defer cache.mu.Unlock()
	report, ok := cache.reports[string(raw)]
	return cloneAnalysis(report), ok
}
func cacheAnalysis(ctx context.Context, root, base string, inputs []Input, report integration.Report) {
	cache, _ := ctx.Value(analysisCacheKey{}).(*analysisCache)
	if cache == nil || ctx.Err() != nil || !immutableAnalysisInputs(base, inputs) {
		return
	}
	raw, _ := json.Marshal([]any{root, base, inputs})
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.reports) < 256 {
		cache.reports[string(raw)] = cloneAnalysis(report)
	}
}

// PrimeSingleInput shares a completed default-policy static preview with the
// identical standalone comparison. The caller must supply the exact pinned
// input used for the report, before applying CLI configuration or rendering.
// Conflicted trees need extra hunk inspection and cannot enter through here.
func PrimeSingleInput(ctx context.Context, root, base string, input Input, report integration.Report) {
	if report.Base != base || len(report.Inputs) != 1 || len(report.Conflicts) != 0 || report.CandidateCommit == "" {
		return
	}
	if input.Snapshot == nil && report.Inputs[0] != input.Head {
		return
	}
	cacheAnalysis(ctx, root, base, []Input{input}, report)
}

// Keep only fields the overlap comparison consumes. In particular, do not
// retain execution plans, or alias caller-mutable finding provenance slices.
func cloneAnalysis(report integration.Report) integration.Report {
	out := integration.Report{Conflicts: append([]string(nil), report.Conflicts...), Limitations: append([]string(nil), report.Limitations...), Findings: append([]model.Finding(nil), report.Findings...)}
	for i := range out.Findings {
		out.Findings[i].Branches = append([]string(nil), report.Findings[i].Branches...)
		out.Findings[i].Locations = append([]model.Provenance(nil), report.Findings[i].Locations...)
	}
	return out
}

func immutableAnalysisInputs(base string, inputs []Input) bool {
	immutable := func(value string) bool {
		if len(value) != 40 && len(value) != 64 {
			return false
		}
		_, err := hex.DecodeString(value)
		return err == nil
	}
	if !immutable(base) {
		return false
	}
	for _, input := range inputs {
		if !immutable(input.Head) {
			return false
		}
		if input.Snapshot != nil && (input.Snapshot.Head != input.Head || input.Snapshot.Digest == "") {
			return false
		}
	}
	return true
}
