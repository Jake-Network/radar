package cli

import (
	"context"
	gitrepo "github.com/Jake-Network/radar/internal/git"
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

// BuildCandidate stops at its first failed merge. Inputs after MERGE_HEAD were
// never inspected, even though Candidate.Inputs contains all pinned inputs.
func hookAllInputsInspected(ctx context.Context, candidate *integration.Candidate) (bool, error) {
	if len(candidate.Conflicts) == 0 {
		return true, nil
	}
	stopped, err := gitrepo.Resolve(ctx, candidate.Dir, "MERGE_HEAD")
	if err != nil {
		return false, err
	}
	for i, input := range candidate.Inputs {
		if input == stopped {
			return i == len(candidate.Inputs)-1, nil
		}
	}
	return false, nil
}
