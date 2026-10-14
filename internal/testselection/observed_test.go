package testselection

import (
	"github.com/Jake-Network/radar/internal/coverage"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/runners"
	"testing"
)

func TestObservedRuntimeFileImpactIsUnionAndStaleExcluded(t *testing.T) {
	inv := Inventory{Revision: "base", Tests: []Test{{ID: "test", Path: "tests/test_limits.py", Framework: "pytest", PackageRoot: "."}}, Manifests: []string{"pyproject.toml"}}
	changed := []string{"config/limits.json"}
	snapshot := model.Snapshot{}
	proposal, err := Select(inv, snapshot, changed, Options{ToolAvailable: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	selection, err := Plan(proposal, changed, ModeTargeted, 16)
	if err != nil || len(selection.Uncovered) != 1 {
		t.Fatal(selection, err)
	}
	edges := []coverage.Edge{{Test: "tests/test_limits.py", File: "config/limits.json", Commit: "observed", Command: "run"}}
	proposal, err = Select(inv, snapshot, changed, Options{Observed: edges, ToolAvailable: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	selection, err = Plan(proposal, changed, ModeTargeted, 16)
	if err != nil || len(selection.Uncovered) != 0 || len(selection.Commands) != 1 {
		t.Fatal(selection, err)
	}
	if r := selection.Commands[0].EvidenceReasons[0]; r.Code != "observed_coverage" || r.Evidence != model.ObservedTest {
		t.Fatal(r)
	}
	edges[0].Stale = true
	proposal, err = Select(inv, snapshot, changed, Options{Observed: edges, ToolAvailable: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	selection, err = Plan(proposal, changed, ModeTargeted, 16)
	if err != nil || len(selection.Uncovered) != 1 {
		t.Fatal("stale coverage became evidence", selection, err)
	}
}

func TestProfileRewriteIncludesFullModeCommandsAndPreservesDeclared(t *testing.T) {
	selection := Selection{Commands: []Command{{ID: "original", Command: []string{"python3", "-m", "pytest"}, CWD: ".", Tier: TierRequired, ToolAvailable: false}, {ID: "declared", Command: []string{"python3", "custom.py"}, CWD: ".", EvidenceReasons: []Reason{{Code: "plan_declared"}}}}, Blocking: []string{"required command runner unavailable or unverified: python3 -m pytest"}}
	cfg := runners.Config{Profiles: []runners.Profile{{Root: ".", Prefix: []string{"uv", "run", "--frozen"}}}}
	ApplyRunnerProfiles(&selection, cfg)
	if selection.Commands[0].Command[0] != "uv" || selection.Commands[0].ID == "original" || selection.Blocking[0] != "required command runner unavailable or unverified: uv run --frozen python3 -m pytest" {
		t.Fatal(selection)
	}
	if selection.Commands[1].Command[0] != "python3" || selection.Commands[1].ID != "declared" {
		t.Fatal("rewrote reviewed declaration", selection)
	}
}
