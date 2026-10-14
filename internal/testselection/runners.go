package testselection

import (
	"context"
	"strings"

	"github.com/Jake-Network/radar/internal/coverage"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
	"github.com/Jake-Network/radar/internal/runners"
)

func RecommendObserved(ctx context.Context, root, ref string, snapshot model.Snapshot, changed []string, plan *planning.Plan, edges []coverage.Edge) (Proposal, error) {
	inv, err := Discover(ctx, root, ref)
	if err != nil {
		return Proposal{}, err
	}
	return Select(inv, snapshot, changed, Options{Plan: plan, Observed: edges})
}

// ApplyRunnerProfiles rewrites only inferred test commands. Explicit plan
// commands retain their own reviewed execution declaration.
func ApplyRunnerProfiles(s *Selection, c runners.Config) {
	for i := range s.Commands {
		command := &s.Commands[i]
		declared := false
		for _, r := range command.EvidenceReasons {
			if r.Code == "declared_plan" || strings.HasPrefix(r.Code, "plan_") {
				declared = true
			}
		}
		if declared {
			continue
		}
		p := c.Match(command.CWD)
		if p == nil {
			continue
		}
		old := strings.Join(command.Command, " ")
		command.Command = p.Rewrite(command.Command)
		command.ID = commandID(*command)
		command.ToolAvailable = false
		for j, b := range s.Blocking {
			if b == "required command runner unavailable or unverified: "+old {
				s.Blocking[j] = "required command runner unavailable or unverified: " + strings.Join(command.Command, " ")
			}
		}
	}
}
