package cli

import (
	"testing"

	"github.com/Jake-Network/radar/internal/contracts"
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
)

func TestUndeclaredContractsAreNotShownAsChecked(t *testing.T) {
	passed := gate.Check{ID: "no_breaking_contracts", Status: model.StatusPassed}
	if !contractsUndeclared(passed, integration.Report{}) {
		t.Fatal("nothing declared shown as a passed contract check")
	}
	if contractsUndeclared(passed, integration.Report{DeclaredBindings: 1}) {
		t.Fatal("analyzed declared bindings hidden")
	}
	if contractsUndeclared(passed, integration.Report{ContractObligations: []contracts.ObligationChange{{}}}) {
		t.Fatal("base obligations hidden")
	}
	failed := gate.Check{ID: "no_breaking_contracts", Status: model.StatusFailed}
	if contractsUndeclared(failed, integration.Report{}) {
		t.Fatal("failed contract check relabelled")
	}
	if contractsUndeclared(gate.Check{ID: "textual_merge", Status: model.StatusPassed}, integration.Report{}) {
		t.Fatal("other checks relabelled")
	}
}
