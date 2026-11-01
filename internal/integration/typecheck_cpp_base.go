package integration

import (
	"context"
	"path"

	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/typecheck"
)

// A built-in compiler command embeds compilation database flags. When the
// database changes, replaying those candidate flags on base would make a new
// configuration error look pre-existing. Resolve the same translation unit
// from base's database instead; multiple compile variants are ambiguous.
func baseCPPCommand(ctx context.Context, root, revision string, target typecheck.Target, changed []string) ([]string, bool) {
	provider := indexer.NewCommit(root, revision)
	defer provider.Close()
	plan, err := typecheck.Select(ctx, provider, changed)
	if err != nil {
		return nil, false
	}
	// A different project's database can change in the same candidate. An
	// unchanged automatic command remains its own exact base counterpart,
	// including targets selected solely through a changed header.
	for _, base := range plan.Targets {
		if base.Tool == "cpp" && base.CWD == target.CWD && base.ID == target.ID {
			return base.Command, true
		}
	}
	var source string
	for _, file := range target.Changed {
		if typecheck.CPPSource(file) && !typecheck.CPPHeader(file) {
			if source != "" && source != file {
				return nil, false
			}
			source = file
		}
	}
	if source == "" {
		return nil, false
	}
	var command []string
	for _, base := range plan.Targets {
		if base.Tool != "cpp" || base.CWD != target.CWD {
			continue
		}
		for _, file := range base.Changed {
			if file == source {
				if command != nil {
					return nil, false
				}
				command = base.Command
				break
			}
		}
	}
	return command, command != nil
}

func compilationDatabaseChanged(changed []string) bool {
	for _, file := range changed {
		if path.Base(file) == "compile_commands.json" {
			return true
		}
	}
	return false
}
