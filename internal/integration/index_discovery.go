package integration

import (
	"context"

	"github.com/Jake-Network/radar/internal/checkpoint"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
)

// indexAndDiscover overlaps independent immutable-tree reads. The worker owns
// its report, joins before return, and contributes results in the same order as
// the serial pipeline. Neither path executes repository code or changes refs.
func indexAndDiscover(ctx context.Context, temp string, r *Report, cache *indexer.ParseCache) (model.Snapshot, error) {
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		report Report
		err    error
	}
	done := make(chan result, 1)
	base, commit := r.Base, r.CandidateCommit
	go func() {
		d := Report{Base: base, CandidateCommit: commit}
		err := analyzeDiscovered(child, temp, &d)
		done <- result{d, err}
	}()
	snapshot, err := checkpoint.IndexWithCache(child, temp, commit, cache)
	if err != nil {
		cancel()
	}
	d := <-done
	if err != nil {
		return model.Snapshot{}, err
	}
	if d.err != nil {
		return model.Snapshot{}, d.err
	}
	r.Findings = append(r.Findings, d.report.Findings...)
	r.Diagnostics = append(r.Diagnostics, d.report.Diagnostics...)
	r.Limitations = append(r.Limitations, d.report.Limitations...)
	r.Checks = append(r.Checks, d.report.Checks...)
	return snapshot, nil
}
