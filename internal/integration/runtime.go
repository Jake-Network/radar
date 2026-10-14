package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Jake-Network/radar/internal/coverage"
	"github.com/Jake-Network/radar/internal/evidence"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
)

// Runtime state lives outside the candidate and never changes repository refs.
func runtimeState() (string, error) {
	if dir := os.Getenv("RADAR_STATE_DIR"); dir != "" {
		return filepath.Abs(dir)
	}
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "radar"), nil
}

func coveragePath() (string, error) {
	dir, err := runtimeState()
	if err != nil {
		return "", err
	}
	return pathutil.StatePath(dir, "coverage.db")
}

func coverageDiagnostic(r *Report, message string) {
	r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Severity: model.SeverityWarning, Message: message})
}

func loadCoverage(ctx context.Context, source, temp string, r *Report) []coverage.Edge {
	filename, err := coveragePath()
	if err != nil {
		coverageDiagnostic(r, "Observed coverage unavailable: "+err.Error())
		return nil
	}
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		coverageDiagnostic(r, "Observed coverage state cannot be read.")
		return nil
	}
	store, err := coverage.Open(filename)
	if err != nil {
		coverageDiagnostic(r, "Observed coverage state cannot be opened.")
		return nil
	}
	defer store.Close()
	identity, err := evidence.RepositoryIdentity(ctx, temp)
	if err != nil {
		coverageDiagnostic(r, "Observed coverage repository identity unavailable.")
		return nil
	}
	p := indexer.NewCommit(temp, r.Base)
	defer p.Close()
	edges, err := store.Load(ctx, p, identity)
	if err != nil {
		coverageDiagnostic(r, "Observed coverage cannot be validated: "+err.Error())
		return nil
	}
	ancestry := map[string]bool{}
	for i := range edges {
		baseline := edges[i].Baseline
		valid, seen := ancestry[baseline]
		if !seen {
			var historyErr error
			valid, historyErr = gitrepo.IsAncestor(ctx, source, baseline, r.Base)
			valid = valid && historyErr == nil
			ancestry[baseline] = valid
		}
		edges[i].Stale = edges[i].Stale || !valid
	}
	return edges
}

func saveCoverage(ctx context.Context, root string, r *Report, ev ExecutionEvidence, tests, files []string) {
	if ev.Status != model.StatusPassed || ev.SourceAfterExecution != "unchanged" || len(tests) == 0 || len(files) == 0 {
		return
	}
	filename, err := coveragePath()
	if err != nil {
		coverageDiagnostic(r, "Coverage was observed but its state path is unavailable.")
		return
	}
	store, err := coverage.Open(filename)
	if err != nil {
		coverageDiagnostic(r, "Coverage was observed but could not be stored.")
		return
	}
	defer store.Close()
	identity, err := evidence.RepositoryIdentity(ctx, root)
	if err != nil {
		coverageDiagnostic(r, "Coverage repository identity unavailable.")
		return
	}
	p := indexer.NewCommit(root, r.CandidateCommit)
	defer p.Close()
	entries, err := p.Entries(ctx)
	if err != nil {
		coverageDiagnostic(r, "Observed coverage source inventory unavailable.")
		return
	}
	files = coverage.Resolve(files, entries)
	if len(files) == 0 {
		coverageDiagnostic(r, "Coverage report contained no unambiguous committed file observations.")
		return
	}
	if err := store.RecordAtBaseline(ctx, p, identity, r.Base, r.CandidateCommit, ev.SelectionID, tests, files); err != nil {
		coverageDiagnostic(r, fmt.Sprintf("Coverage observations were not stored: %s", err))
	}
}
