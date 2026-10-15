package cli

import (
	"context"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/overlap"
)

func hookConflictHunks(ctx context.Context, candidate *integration.Candidate, path string) ([]string, error) {
	return overlap.ConflictHunks(ctx, candidate, path)
}

func hookHasNewConflicts(ctx context.Context, candidate, committed *integration.Candidate) (bool, error) {
	prior := map[string]bool{}
	for _, path := range committed.Conflicts {
		prior[path] = true
	}
	for _, path := range candidate.Conflicts {
		if !prior[path] {
			return true, nil
		}
		before, err := hookConflictHunks(ctx, committed, path)
		if err != nil {
			return false, err
		}
		after, err := hookConflictHunks(ctx, candidate, path)
		if err != nil {
			return false, err
		}
		counts := map[string]int{}
		for _, hunk := range before {
			counts[hunk]++
		}
		for _, hunk := range after {
			if counts[hunk] == 0 {
				return true, nil
			}
			counts[hunk]--
		}
	}
	return false, nil
}
