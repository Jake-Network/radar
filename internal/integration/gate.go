package integration

import (
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/migrations"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/symbols"
)

func applyGate(r *Report, o Options) {
	noBreaking := gate.NoBreaking(r.Findings, r.contractUnverified)
	for _, c := range r.Checks {
		// Configuration gaps never mask a confirmed incompatibility, except an
		// unreadable configuration, which is an execution-grade error.
		if c.ID == "declared_contract_configuration" && c.Status != model.StatusPassed && (noBreaking.Status != model.StatusFailed && (noBreaking.Status == model.StatusPassed || c.Status == model.StatusError)) {
			noBreaking.Status = c.Status
			noBreaking.Evidence = model.Unknown
			noBreaking.Explanation = c.Explanation
		}
	}
	// This is an actual derived observation, not a policy missing-evidence
	// placeholder. Keep it in the report so later configuration evaluation
	// uses the same evidence as this gate evaluation.
	r.Checks = append(r.Checks, noBreaking)
	p := gate.Policy{Version: 1, Name: "supported-integration", Require: []string{"textual_merge", "no_breaking_contracts"}}
	// Migration order is required only where the branches changed a
	// recognized migration history; elsewhere there is nothing to check.
	for _, c := range r.Checks {
		switch c.ID {
		case migrations.CheckID, symbols.CheckID, "lockfile_consistency", "unique_keys":
			p.Require = append(p.Require, c.ID)
		}
	}
	if o.Verify {
		for _, c := range r.Checks {
			if c.ID == "generated_drift" {
				p.Require = append(p.Require, c.ID)
			}
		}
		p.Require = append(p.Require, "integration_execution")
		if o.Suite != "" {
			p.Require = append(p.Require, "test_selection")
		}
	}
	if o.Policy != nil {
		p = *o.Policy
	}
	r.Gate = gate.Evaluate(p, r.Checks)
	r.Coverage = gate.CoverageFor(r.Checks, r.Limitations)
}
