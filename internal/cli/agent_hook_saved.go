package cli

import (
	"context"
	"strings"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
)

// A conflicted combined tree cannot be analyzed. The current saved snapshot
// can still be checked relative to its own HEAD without resolving peer edits.
// This proves only this branch-local static failure, never combined validity.
func hookSavedFailure(ctx context.Context, root string, in watchInput, cache *indexer.ParseCache) (agentHookResponse, error) {
	for _, snapshot := range in.Snapshots {
		if snapshot.Worktree != root {
			continue
		}
		snapshot.InputIndex = 0
		candidate, err := integration.BuildDirtyCandidate(ctx, root, snapshot.Head, []string{snapshot.Head}, []integration.DirtySnapshot{snapshot})
		if candidate != nil {
			defer candidate.Close()
		}
		if err != nil {
			return agentHookResponse{}, err
		}
		report, err := integration.Analyze(ctx, candidate, integration.Options{Policy: in.config.Policy, ParseCache: cache})
		if err != nil {
			return agentHookResponse{}, err
		}
		in.config.applyIntegration(&report)
		if report.Gate.Verdict != gate.Fail {
			return agentHookResponse{}, nil
		}
		lines := []string{"Radar found a supported static failure in your saved changes relative to this branch's HEAD. Fix it before completing. The conflicted combined candidate remains NOT VERIFIED; no tests were run."}
		for _, finding := range report.Findings {
			if finding.Severity == model.SeverityError {
				lines = append(lines, finding.Code+": "+finding.Explanation)
			}
			if len(lines) >= 8 {
				break
			}
		}
		if len(lines) == 1 {
			for _, check := range report.Gate.Required {
				if check.Status == model.StatusFailed {
					lines = append(lines, check.ID+": "+check.Explanation)
				}
				if len(lines) >= 8 {
					break
				}
			}
		}
		return agentHookResponse{Decision: "block", Reason: hookText(strings.Join(lines, "\n"))}, nil
	}
	return agentHookResponse{}, nil
}
