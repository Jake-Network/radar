package cli

import (
	"errors"

	"github.com/Jake-Network/radar/internal/composition"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/runproof"
)

func (a *app) persistSingleGateReport(g gateReport) error {
	state, err := composition.StateDir()
	if err != nil {
		return err
	}
	identity := gitrepo.Identity(a.ctx, a.root)
	if identity == "" {
		return errors.New("cannot bind gate history to repository")
	}
	rec, err := runproof.Save(state, identity, gateProof{Report: g, DeclaredBindings: g.DeclaredBindings})
	if err != nil {
		return err
	}

	return runproof.Remember(state, identity, runproof.RunSummary{ID: rec.ID, Verdict: string(g.Gate.Verdict), InputKey: rec.ID})
}
