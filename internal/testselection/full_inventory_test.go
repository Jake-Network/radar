package testselection

import (
	"github.com/Jake-Network/radar/internal/model"
	"testing"
)

func TestFullInventoryAccountsForEveryFramework(t *testing.T) {
	inv := Inventory{Manifests: []string{"rust/Cargo.toml"}, Tests: []Test{
		{Path: "py/tests/test_a.py", Framework: "unittest", PackageRoot: "py"},
		{Path: "py/deep/test_b.py", Framework: "pytest", PackageRoot: "py"},
		{Path: "web/deep/a.test.mjs", Framework: "node-test", PackageRoot: "web"},
		{Path: "web/b.test.ts", Framework: "node-test", PackageRoot: "web"},
		{Path: "web/c.test.js", Framework: "javascript-unknown", PackageRoot: "web"},
		{Path: "jest/a.test.ts", Framework: "jest", PackageRoot: "jest"},
		{Path: "vite/a.test.ts", Framework: "vitest", PackageRoot: "vite"},
		{Path: "go/a_test.go", Framework: "go", PackageRoot: "go"},
		{Path: "rust/src/lib.rs", Framework: "cargo", PackageRoot: "rust"},
	}}
	p, err := Select(inv, model.Snapshot{}, []string{"py/tests/test_a.py", "web/deep/a.test.mjs"}, Options{ToolAvailable: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Plan(p, nil, ModeFull, 256)
	if err != nil {
		t.Fatal(err)
	}
	accounted := map[string]bool{}
	for _, c := range s.Commands {
		for _, f := range c.TestFiles {
			accounted[f] = true
		}
		if c.Framework == "node-test" && (c.CWD != "web" || len(c.Command) != 4) {
			t.Fatalf("node command: %+v", c)
		}
	}
	for _, o := range s.Omitted {
		for _, f := range o.TestFiles {
			accounted[f] = true
		}
		if o.Reason != "unsupported_configuration" {
			t.Fatal(o)
		}
	}
	for _, tt := range inv.Tests {
		if !accounted[tt.Path] {
			t.Fatalf("silently omitted %s", tt.Path)
		}
	}
	if len(s.Omitted) != 2 || len(s.Blocking) < 2 {
		t.Fatalf("unsupported tests did not block: %+v", s)
	}
}

func TestFullInventoryDiagnosticsBlock(t *testing.T) {
	s, err := Plan(Proposal{Inventory: Inventory{Diagnostics: []model.Diagnostic{{Path: "tests/test_big.py", Message: "read budget exceeded"}}}}, nil, ModeFull, 16)
	if err != nil || len(s.Blocking) < 2 {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestChangedUnsupportedTestCannotHideBehindPassingSelection(t *testing.T) {
	inv := Inventory{Tests: []Test{{ID: "unknown", Path: "web/a.test.ts", Framework: "node-test", PackageRoot: "web"}, {ID: "known", Path: "web/b.test.js", Framework: "node-test", PackageRoot: "web"}}}
	p, err := Select(inv, model.Snapshot{}, []string{"web/a.test.ts", "web/b.test.js"}, Options{ToolAvailable: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{ModeTargeted, ModeBalanced, ModeFull} {
		s, err := Plan(p, nil, mode, 16)
		if err != nil || len(s.Commands) != 1 || len(s.Omitted) != 1 || len(s.Blocking) == 0 {
			t.Fatalf("%s unsupported changed test lost: %+v %v", mode, s, err)
		}
	}
}

func TestRecommendationLimitRetainsRequiredOmissions(t *testing.T) {
	inv := Inventory{Tests: []Test{{ID: "a", Path: "a/test_a.py", Framework: "unittest", PackageRoot: "."}, {ID: "b", Path: "b/test_b.py", Framework: "unittest", PackageRoot: "."}}}
	p, err := Select(inv, model.Snapshot{}, []string{"a/test_a.py", "b/test_b.py"}, Options{Limit: 1, ToolAvailable: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	s, err := Plan(p, nil, ModeBalanced, 16)
	if err != nil || len(s.Commands) != 1 || len(s.Omitted) != 1 || len(s.Blocking) != 1 || s.Omitted[0].Reason != "recommendation_limit" {
		t.Fatalf("%+v %v", s, err)
	}
}
