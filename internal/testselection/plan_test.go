package testselection

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func pytestCandidate(file, reason string, priority int) Command {
	c := Command{Command: []string{"python3", "-m", "pytest", "./" + file}, CWD: ".", Framework: "pytest", TestFiles: []string{file}, Priority: priority, ToolAvailable: true, EvidenceReasons: []Reason{{Code: reason, RelatedFiles: []string{"src/core.py"}}}}
	c.ID = commandID(c)
	return c
}

// More than 16 per-file recommendations become one grouped, executable command.
func TestPlanGroupsLargeRecommendationSets(t *testing.T) {
	p := Proposal{}
	for i := 0; i < 43; i++ {
		f := fmt.Sprintf("tests/test_%02d.py", i)
		p.Commands = append(p.Commands, pytestCandidate(f, "dependency_impact", 80))
		p.Inventory.Tests = append(p.Inventory.Tests, Test{Path: f, Framework: "pytest", PackageRoot: "."})
	}
	s, err := Plan(p, []string{"src/core.py"}, "recommended", 0)
	if err != nil {
		t.Fatal(err)
	}
	if s.Mode != ModeBalanced || len(s.Commands) != 1 || len(s.Blocking) != 0 || len(s.Omitted) != 0 || s.TestFiles != 43 {
		t.Fatalf("%+v", s)
	}
	c := s.Commands[0]
	if strings.Join(c.Command[:3], " ") != "python3 -m pytest" || len(c.Command) != 46 || len(c.GroupedFrom) != 43 || c.Tier != TierRequired {
		t.Fatalf("%+v", c)
	}
	if len(s.Uncovered) != 0 {
		t.Fatal("covered change reported uncovered", s.Uncovered)
	}
}

func TestPlanNeverSilentlyDropsRequiredCommands(t *testing.T) {
	p := Proposal{}
	// unittest discovery cannot be grouped, so each file stays one command.
	for i := 0; i < 5; i++ {
		f := fmt.Sprintf("tests/test_%d.py", i)
		c := Command{Command: []string{"python3", "-m", "unittest", "discover", "-s", "tests", "-p", f}, CWD: ".", Framework: "unittest", TestFiles: []string{f}, Priority: 80, ToolAvailable: true, EvidenceReasons: []Reason{{Code: "dependency_impact"}}}
		c.ID = commandID(c)
		p.Commands = append(p.Commands, c)
	}
	s, err := Plan(p, nil, ModeTargeted, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Commands) != 3 || len(s.Omitted) != 2 || len(s.Blocking) != 2 {
		t.Fatalf("%+v", s)
	}
	for _, o := range s.Omitted {
		if o.Reason != "budget_exceeded" || o.Tier != TierRequired || len(o.TestFiles) != 1 {
			t.Fatalf("%+v", o)
		}
	}
}

func TestTargetedModeExcludesOnlyFallbacksAndReportsUncovered(t *testing.T) {
	p := Proposal{Commands: []Command{
		pytestCandidate("tests/test_direct.py", "dependency_impact", 80),
		pytestCandidate("tests/test_other.py", "package_fallback", 30),
	}}
	p.Commands[1].EvidenceReasons[0].RelatedFiles = []string{"src/unrelated.py"}
	changed := []string{"src/core.py", "src/unrelated.py", "README.md"}
	targeted, _ := Plan(p, changed, ModeTargeted, 0)
	if len(targeted.Commands) != 1 || len(targeted.Omitted) != 1 || targeted.Omitted[0].Reason != "mode_excluded" || len(targeted.Blocking) != 0 {
		t.Fatalf("%+v", targeted)
	}
	if len(targeted.Uncovered) != 1 || targeted.Uncovered[0] != "src/unrelated.py" {
		t.Fatal("uncovered change hidden", targeted.Uncovered)
	}
	balanced, _ := Plan(p, changed, ModeBalanced, 0)
	// Required and optional tiers are never merged into one command.
	if len(balanced.Commands) != 2 || balanced.Commands[0].Tier != TierRequired || balanced.Commands[1].Tier != TierOptional {
		t.Fatalf("%+v", balanced.Commands)
	}
}

func TestPlanDeclaredCommandsAreNotGroupedAndFullModeUsesSuites(t *testing.T) {
	plan := Command{Command: []string{"python3", "-m", "pytest", "./tests/test_a.py"}, CWD: ".", Framework: "plan-declared", Priority: 100, ToolAvailable: true, EvidenceReasons: []Reason{{Code: "plan_declared"}}}
	plan.ID = commandID(plan)
	p := Proposal{Commands: []Command{plan, pytestCandidate("tests/test_a.py", "changed_test", 90), pytestCandidate("tests/test_b.py", "dependency_impact", 80)}}
	p.Inventory.Tests = []Test{{Path: "tests/test_a.py", Framework: "pytest", PackageRoot: "."}, {Path: "go/x_test.go", Framework: "go", PackageRoot: "go"}, {Path: "web/a.test.ts", Framework: "jest", PackageRoot: "web"}}
	s, _ := Plan(p, nil, ModeBalanced, 0)
	if len(s.Commands) != 2 || s.Commands[0].ID != plan.ID {
		t.Fatalf("plan command altered: %+v", s.Commands)
	}
	full, _ := Plan(p, nil, ModeFull, 0)
	got := []string{}
	for _, c := range full.Commands {
		got = append(got, c.CWD+":"+strings.Join(c.Command, " "))
	}
	sort.Strings(got[1:])
	want := ".:python3 -m pytest ./tests/test_a.py|.:python3 -m pytest ./tests/test_a.py|go:go test -json ./...|web:./node_modules/.bin/jest --runInBand"
	if strings.Join(got, "|") != want {
		t.Fatal(got)
	}
	// jest availability is never assumed from a manifest, so full mode blocks.
	if len(full.Blocking) != 1 || !strings.Contains(full.Blocking[0], "jest") {
		t.Fatal(full.Blocking)
	}
}

func TestGoGroupingCollapsesIntoModuleWildcard(t *testing.T) {
	mk := func(arg, reason string) Command {
		c := Command{Command: []string{"go", "test", "-json", arg}, CWD: ".", Framework: "go", Priority: 70, ToolAvailable: true, EvidenceReasons: []Reason{{Code: reason}}}
		c.ID = commandID(c)
		return c
	}
	out := group([]Command{mk("./a", "go_package_companion"), mk("./b", "go_package_companion"), mk("./...", "dependency_impact")})
	if len(out) != 1 || strings.Join(out[0].Command, " ") != "go test -json ./..." || len(out[0].GroupedFrom) != 3 {
		t.Fatalf("%+v", out)
	}
}

func TestUnknownModeRejected(t *testing.T) {
	if _, err := Plan(Proposal{}, nil, "everything", 0); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestFileReferencesAndDocumentationChanges(t *testing.T) {
	inv := Inventory{Tests: []Test{
		{Path: "integration/test_contract.py", Framework: "unittest", PackageRoot: ".", text: `schema = ROOT / "contracts" / "order-summary.schema.json"`},
		{Path: "tests/test_unrelated.py", Framework: "unittest", PackageRoot: ".", text: "import os"},
	}}
	always := func(string) bool { return true }
	p, err := Select(inv, model.Snapshot{}, []string{"contracts/order-summary.schema.json"}, Options{ToolAvailable: always})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := Plan(p, []string{"contracts/order-summary.schema.json"}, ModeTargeted, 0)
	if len(s.Commands) != 1 || s.Commands[0].TestFiles[0] != "integration/test_contract.py" || s.Commands[0].EvidenceReasons[0].Code != "file_reference" || len(s.Uncovered) != 0 {
		t.Fatalf("%+v", s)
	}
	// Documentation alone triggers neither fallbacks nor blocking.
	p, _ = Select(inv, model.Snapshot{}, []string{"README.md"}, Options{ToolAvailable: always})
	s, _ = Plan(p, []string{"README.md"}, ModeBalanced, 0)
	if len(p.Commands) != 0 || len(s.Blocking) != 0 || len(s.Uncovered) != 0 {
		t.Fatalf("%+v %+v", p.Commands, s)
	}
	// An unreferenced data file stays visible as uncovered in targeted mode.
	p, _ = Select(inv, model.Snapshot{}, []string{"config/settings.yaml"}, Options{ToolAvailable: always})
	s, _ = Plan(p, []string{"config/settings.yaml"}, ModeTargeted, 0)
	if len(s.Uncovered) != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestChangedTestDoesNotFallBackToSiblings(t *testing.T) {
	inv := Inventory{Tests: []Test{
		{Path: "tests/test_a.py", Framework: "pytest", PackageRoot: "."},
		{Path: "tests/test_b.py", Framework: "pytest", PackageRoot: "."},
	}}
	always := func(string) bool { return true }
	p, _ := Select(inv, model.Snapshot{}, []string{"tests/test_a.py"}, Options{ToolAvailable: always})
	if len(p.Commands) != 1 || p.Commands[0].TestFiles[0] != "tests/test_a.py" {
		t.Fatalf("%+v", p.Commands)
	}
	// Shared fixtures are not test files and still reach every sibling.
	p, _ = Select(inv, model.Snapshot{}, []string{"tests/conftest.py"}, Options{ToolAvailable: always})
	if len(p.Commands) != 2 {
		t.Fatalf("%+v", p.Commands)
	}
}
