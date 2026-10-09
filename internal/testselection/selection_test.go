package testselection

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/planning"
)

func TestMultilingualMonorepoInventoryAndRanking(t *testing.T) {
	root := filepath.Join("testdata", "monorepo")
	inv, e := Discover(context.Background(), root, "")
	if e != nil {
		t.Fatal(e)
	}
	frameworks := map[string]bool{}
	for _, test := range inv.Tests {
		frameworks[test.Framework] = true
	}
	for _, f := range []string{"pytest", "unittest", "go", "cargo", "vitest", "node-test"} {
		if !frameworks[f] {
			t.Fatalf("missing %s: %+v", f, inv)
		}
	}
	snapshot, e := indexer.Index(context.Background(), root, "WORKTREE")
	if e != nil {
		t.Fatal(e)
	}
	p, e := Select(inv, snapshot, []string{"backend/service.py", "worker/pkg/worker.go", "frontend/src/view.ts", "codec/src/lib.rs"}, Options{ToolAvailable: func(string) bool { return true }})
	if e != nil {
		t.Fatal(e)
	}
	if p.Status != model.StatusIncomplete {
		t.Fatal("recommendation asserted complete coverage", p.Status)
	}
	byFramework := map[string]Command{}
	integration := false
	for _, cmd := range p.Commands {
		byFramework[cmd.Framework] = cmd
		for _, file := range cmd.TestFiles {
			if strings.HasPrefix(file, "unrelated/") {
				t.Fatal("unrelated package recommended", cmd)
			}
			if strings.Contains(file, "test_integration") {
				integration = true
				if cmd.Priority < 85 {
					t.Fatal("integration not prioritized", cmd)
				}
			}
		}
		if cmd.Framework == "vitest" && cmd.ToolAvailable {
			t.Fatal("isolated runner availability fabricated", cmd)
		}
	}
	if !integration {
		t.Fatal("cross-component integration candidate omitted", p.Commands)
	}
	for _, f := range []string{"pytest", "unittest", "go", "cargo", "vitest", "node-test"} {
		if _, ok := byFramework[f]; !ok {
			t.Fatal("missing recommendation", f, p.Commands)
		}
	}
	if byFramework["go"].CWD != "worker" || !reflect.DeepEqual(byFramework["go"].Command, []string{"go", "test", "-json", "./pkg"}) {
		t.Fatal("wrong nested Go command", byFramework["go"])
	}
	if byFramework["pytest"].CWD != "backend" || byFramework["vitest"].CWD != "frontend" {
		t.Fatal("wrong nested CWD", byFramework)
	}
	if _, e := os.Stat(filepath.Join(root, "frontend", "MUST_NOT_EXECUTE")); !os.IsNotExist(e) {
		t.Fatal("package script executed")
	}
}
func TestDeclaredCommandsFirstMissingToolsAndDeterminism(t *testing.T) {
	inv, e := Discover(context.Background(), filepath.Join("testdata", "monorepo"), "")
	if e != nil {
		t.Fatal(e)
	}
	plan := planning.Plan{Acceptance: []planning.Criterion{{ID: "full", Rule: &planning.Rule{Kind: "test_run", Command: []string{"missing-tool", "test"}}}}}
	opts := Options{Plan: &plan, ToolAvailable: func(string) bool { return false }}
	snapshot := model.Snapshot{Edges: []model.Edge{{Kind: "DEPENDS_ON", From: model.FileID("backend/tests/test_service.py"), To: model.FileID("backend/service.py")}}}
	first, e := Select(inv, snapshot, []string{"backend/service.py", "frontend/src/view.ts"}, opts)
	if e != nil {
		t.Fatal(e)
	}
	if first.Commands[0].Framework != "plan-declared" || first.Commands[0].Priority != 100 || first.Commands[0].ToolAvailable {
		t.Fatal(first.Commands)
	}
	for i := 0; i < 20; i++ {
		next, e := Select(inv, snapshot, []string{"frontend/src/view.ts", "backend/service.py"}, opts)
		if e != nil {
			t.Fatal(e)
		}
		a, _ := json.Marshal(first)
		b, _ := json.Marshal(next)
		if string(a) != string(b) {
			t.Fatal("nondeterministic output", string(a), string(b))
		}
	}
	opts.Limit = 1
	limited, e := Select(inv, snapshot, []string{"backend/service.py"}, opts)
	if e != nil || len(limited.Commands) != 1 {
		t.Fatal(limited, e)
	}
}
func TestUnknownRunnerAndNoCodeExecution(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"package.json": `{"scripts":{"test":"touch EXECUTED"}}`, "test_sideeffect.py": "from pathlib import Path\nPath('EXECUTED').write_text('unsafe')\ndef test_example(): pass\n", "unknown.test.js": "runSomeCustomTests();", "source.py": "VALUE = 1\n"}
	for p, content := range files {
		if e := os.WriteFile(filepath.Join(root, p), []byte(content), 0600); e != nil {
			t.Fatal(e)
		}
	}
	inv, e := Discover(context.Background(), root, "")
	if e != nil {
		t.Fatal(e)
	}
	p, e := Select(inv, model.Snapshot{}, []string{"source.py"}, Options{ToolAvailable: func(string) bool { return false }})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(root, "EXECUTED")); !os.IsNotExist(e) {
		t.Fatal("discovery evaluated repository code")
	}
	for _, c := range p.Commands {
		if c.Framework == "javascript-unknown" {
			t.Fatal("unknown framework got runnable command")
		}
	}
	if !strings.Contains(strings.Join(p.Limitations, " "), "No safely recognized runner") {
		t.Fatal("unknown runner omitted", p)
	}
	if _, e := Select(inv, model.Snapshot{}, []string{"../escape"}, Options{}); e == nil {
		t.Fatal("unsafe changed path accepted")
	}
}
func TestSymlinkAndOversizeDiscoveryCoverage(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "test_secret.py")
	if e := os.WriteFile(outside, []byte("def test_secret(): pass"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(root, "test_link.py")); e != nil {
		t.Skip(e)
	}
	if e := os.WriteFile(filepath.Join(root, "test_large.py"), make([]byte, MaxFileBytes+1), 0600); e != nil {
		t.Fatal(e)
	}
	inv, e := Discover(context.Background(), root, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(inv.Tests) != 0 || len(inv.Diagnostics) != 2 {
		t.Fatal(inv)
	}
	p, e := Select(inv, model.Snapshot{}, []string{"source.py"}, Options{})
	if e != nil {
		t.Fatal(e)
	}
	if p.Status != model.StatusIncomplete || len(p.Commands) != 0 {
		t.Fatal(p)
	}
}
func TestSelectionNeverInventsDependencyProof(t *testing.T) {
	inv := Inventory{Tests: []Test{{Path: "svc/test_api.py", Framework: "pytest", PackageRoot: "svc"}}}
	p, e := Select(inv, model.Snapshot{}, []string{"svc/api.py"}, Options{ToolAvailable: func(string) bool { return true }})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Commands) != 1 || p.Commands[0].Priority != 30 || p.Commands[0].EvidenceReasons[0].Code != "package_fallback" || p.Commands[0].EvidenceReasons[0].Evidence != model.Inferred {
		t.Fatal(p)
	}
	p, e = Select(inv, model.Snapshot{}, []string{"other/api.py"}, Options{})
	if e != nil || len(p.Commands) != 0 {
		t.Fatal("invented unrelated relationship", p, e)
	}
}

func TestDependencyPathEvidenceAndMalformedMetadata(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"package.json": "{ malformed", "candidate.test.js": "test('unknown', () => {});"}
	for p, data := range files {
		if e := os.WriteFile(filepath.Join(root, p), []byte(data), 0600); e != nil {
			t.Fatal(e)
		}
	}
	inv, e := Discover(context.Background(), root, "")
	if e != nil {
		t.Fatal(e)
	}
	if len(inv.Diagnostics) != 1 || inv.Diagnostics[0].Path != "package.json" || !strings.Contains(inv.Diagnostics[0].Message, "malformed") {
		t.Fatal(inv)
	}
	inv = Inventory{Tests: []Test{{Path: "svc/test_api.py", PackageRoot: "svc", Framework: "pytest", Evidence: model.Provenance{Path: "svc/test_api.py", Evidence: model.Inferred}}}}
	snapshot := model.Snapshot{Edges: []model.Edge{
		{ID: "second", Kind: "DEPENDS_ON", From: model.FileID("svc/test_api.py"), To: model.FileID("svc/api.py"), Provenance: model.Provenance{Path: "svc/test_api.py", Line: 2, Method: "import_path_resolution:python", Evidence: model.Inferred}},
		{ID: "first", Kind: "DEPENDS_ON", From: model.FileID("svc/api.py"), To: model.FileID("svc/model.py"), Provenance: model.Provenance{Path: "svc/api.py", Line: 4, Method: "import_path_resolution:python", Evidence: model.Inferred}},
	}}
	p, e := Select(inv, snapshot, []string{"svc/model.py"}, Options{})
	if e != nil {
		t.Fatal(e)
	}
	loc := p.Commands[0].EvidenceReasons[0].Locations
	if len(loc) != 3 || loc[1].Path != "svc/api.py" || loc[1].Line != 4 || loc[2].Path != "svc/test_api.py" || loc[2].Line != 2 {
		t.Fatal("missing dependency-chain source evidence", loc)
	}
	if p.Commands[0].EvidenceReasons[0].Evidence != model.Inferred {
		t.Fatal("inferred chain promoted")
	}
}

func TestSelectRejectsUntrustedInventoryPaths(t *testing.T) {
	for _, test := range []Test{{Path: "../escape_test.py", PackageRoot: ".", Framework: "pytest"}, {Path: "ok_test.py", PackageRoot: "../escape", Framework: "pytest"}, {Path: "elsewhere/test_ok.py", PackageRoot: "service", Framework: "pytest"}} {
		if _, e := Select(Inventory{Tests: []Test{test}}, model.Snapshot{}, []string{"ok.py"}, Options{}); e == nil {
			t.Fatal("unsafe inventory accepted", test)
		}
	}
}

func TestGoSelectionAvoidsUnrelatedRootPackages(t *testing.T) {
	inv := Inventory{Tests: []Test{{Path: "internal/cli/cli_test.go", PackageRoot: ".", Framework: "go"}, {Path: "internal/other/other_test.go", PackageRoot: ".", Framework: "go"}}}
	p, e := Select(inv, model.Snapshot{}, []string{"internal/cli/cli.go"}, Options{})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Commands) != 1 || !reflect.DeepEqual(p.Commands[0].Command, []string{"go", "test", "-json", "./internal/cli"}) {
		t.Fatal("unrelated Go packages inflated suite", p.Commands)
	}
	p, e = Select(inv, model.Snapshot{}, []string{"go.mod"}, Options{})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Commands) != 1 || !reflect.DeepEqual(p.Commands[0].Command, []string{"go", "test", "-json", "./..."}) || len(p.Commands[0].TestFiles) != 2 {
		t.Fatal("module fallback should be one broad command", p.Commands)
	}
}
func TestNodeRecommendationUsesDeterministicTAPReporter(t *testing.T) {
	argv, _ := commandFor(Test{Path: "test/example.test.js", PackageRoot: ".", Framework: "node-test"})
	if !has(argv, "--test-reporter=tap") {
		t.Fatal("Node default reporter varies across versions", argv)
	}
}

func TestNestedSingleLetterPackageOwnersBeatRepositoryRoot(t *testing.T) {
	tests := []Test{{Path: "integration/test_integration.py", PackageRoot: ".", Framework: "unittest"}, {Path: "a/test_api.py", PackageRoot: "a", Framework: "pytest"}, {Path: "b/test_api.py", PackageRoot: "b", Framework: "pytest"}}
	first, e := Select(Inventory{Tests: tests}, model.Snapshot{}, []string{"a/source.py", "b/source.py"}, Options{})
	if e != nil {
		t.Fatal(e)
	}
	if len(first.Commands) != 3 {
		t.Fatal("nested owners omitted", first.Commands)
	}
	found := false
	for _, cmd := range first.Commands {
		if cmd.Framework == "unittest" {
			found = true
			if cmd.Priority < 85 {
				t.Fatal("root integration not boosted", cmd)
			}
		}
	}
	if !found {
		t.Fatal("root integration missing")
	}
	reversed := []Test{tests[2], tests[1], tests[0]}
	next, e := Select(Inventory{Tests: reversed}, model.Snapshot{}, []string{"b/source.py", "a/source.py"}, Options{})
	if e != nil {
		t.Fatal(e)
	}
	a, _ := json.Marshal(first.Commands)
	b, _ := json.Marshal(next.Commands)
	if string(a) != string(b) {
		t.Fatal("package ownership depends on inventory order", string(a), string(b))
	}
}

func TestRootIntegrationDirectoriesPrioritizedWithoutFilenameMarker(t *testing.T) {
	inv := Inventory{Tests: []Test{{Path: "integration/test_api.py", PackageRoot: ".", Framework: "unittest"}, {Path: "e2e/test_api.py", PackageRoot: ".", Framework: "unittest"}, {Path: "backend/test_api.py", PackageRoot: "backend", Framework: "pytest"}, {Path: "frontend/test_api.py", PackageRoot: "frontend", Framework: "pytest"}}}
	p, e := Select(inv, model.Snapshot{}, []string{"backend/api.py", "frontend/api.py"}, Options{})
	if e != nil {
		t.Fatal(e)
	}
	seen := map[string]bool{}
	for _, cmd := range p.Commands {
		if cmd.Priority >= 85 {
			for _, file := range cmd.TestFiles {
				seen[file] = true
			}
		}
	}
	for _, file := range []string{"integration/test_api.py", "e2e/test_api.py"} {
		if !seen[file] {
			t.Fatal("root integration directory omitted", file, p.Commands)
		}
	}
}

func TestPlanDeclaredWorkingDirectoriesPreserved(t *testing.T) {
	plan := planning.Plan{Acceptance: []planning.Criterion{{Rule: &planning.Rule{Kind: "test_run", Command: []string{"go", "test", "-json", "."}, CWD: "worker"}}, {Rule: &planning.Rule{Kind: "test_run", Command: []string{"go", "test", "-json", "."}}}}}
	p, e := Select(Inventory{}, model.Snapshot{}, nil, Options{Plan: &plan})
	if e != nil {
		t.Fatal(e)
	}
	if len(p.Commands) != 2 || p.Commands[0].ID == p.Commands[1].ID {
		t.Fatal("different working directory commands collapsed", p.Commands)
	}
	dirs := map[string]bool{}
	for _, command := range p.Commands {
		dirs[command.CWD] = true
	}
	if !dirs["worker"] || !dirs["."] {
		t.Fatal("plan cwd ignored", p.Commands)
	}
	plan.Acceptance[0].Rule.CWD = "../outside"
	if _, e := Select(Inventory{}, model.Snapshot{}, nil, Options{Plan: &plan}); e == nil {
		t.Fatal("unsafe plan CWD accepted")
	}
}

func TestNodeTypedAndJSXTestsRemainUnrunnableCandidates(t *testing.T) {
	for _, ext := range []string{".ts", ".tsx", ".mts", ".cts", ".jsx"} {
		argv, _ := commandFor(Test{Path: "example.test" + ext, PackageRoot: ".", Framework: "node-test"})
		if len(argv) != 0 {
			t.Fatal("unconfigured transpilation/runtime support invented", ext, argv)
		}
	}
}

func TestPythonFrameworkUsesTestCaseAncestryInsteadOfMockImports(t *testing.T) {
	for _, test := range []struct{ name, source, want string }{
		{"pytest-mock", "from unittest import mock\nimport pytest\n@pytest.mark.parametrize('x',[1])\ndef test_value(x): assert x==1\n", "pytest"},
		{"pytest-unittest-module", "import unittest\ndef test_value(): assert True\n", "pytest"},
		{"actual-case-with-pytest", "import unittest\nimport pytest\nclass Real(unittest.TestCase):\n def test_value(self): self.assertTrue(True)\n", "unittest"},
		{"module-alias", "import unittest as ut\nclass Real(ut.TestCase):\n def test_value(self): self.assertTrue(True)\n", "unittest"},
		{"base-alias", "from unittest import TestCase as Base\nclass Real(Base):\n def test_value(self): self.assertTrue(True)\n", "unittest"},
		{"mock-only", "from unittest import mock\nvalue=mock.Mock()\n", ""},
		{"comments-and-strings", "# import unittest\nexample='class FalseCase(unittest.TestCase):'\ndef test_actual(): assert True\n", "pytest"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := classify("tests/test_example.py", test.source, map[string]string{}); got != test.want {
				t.Fatalf("got %q want %q", got, test.want)
			}
		})
	}
}

func TestDeclaredContractImpactReachesConsumerTestsWithoutInventedLinks(t *testing.T) {
	manifest := model.Provenance{Path: ".radar/contracts.json", Line: 1, Method: "explicit_declared_dependency", Evidence: model.VerifiedStatic}
	schema := model.Provenance{Path: "schemas/orders.json", Method: "explicit_json_schema", Evidence: model.VerifiedStatic}
	order := model.ContractID("schemas/orders.json", "/Order")
	field := model.SchemaFieldID("schemas/orders.json", "/Order/properties/total")
	other := model.ContractID("schemas/other.json", "/Other")
	snapshot := model.Snapshot{Nodes: []model.Node{{ID: order, Kind: "response_schema", Provenance: schema}, {ID: field, Kind: "schema_field", Provenance: schema}}, Edges: []model.Edge{
		{ID: "model-to-producer", Kind: "DEPENDS_ON", From: model.FileID("backend/api.py"), To: model.FileID("backend/model.py"), Provenance: model.Provenance{Path: "backend/api.py", Line: 2, Evidence: model.Inferred}},
		{ID: "exposes", Kind: "EXPOSES", From: model.FileID("backend/api.py"), To: order, Provenance: manifest},
		{ID: "consumes", Kind: "CONSUMES", From: model.FileID("frontend/client.ts"), To: order, Provenance: manifest},
		{ID: "defines-field", Kind: "DEFINES", From: order, To: field, Provenance: schema},
		{ID: "field-consumes", Kind: "CONSUMES", From: model.FileID("frontend/field-client.ts"), To: field, Provenance: manifest},
		{ID: "consumer-test", Kind: "DEPENDS_ON", From: model.FileID("frontend/client.test.ts"), To: model.FileID("frontend/client.ts"), Provenance: model.Provenance{Path: "frontend/client.test.ts", Line: 3, Evidence: model.Inferred}},
		{ID: "field-test", Kind: "DEPENDS_ON", From: model.FileID("frontend/field.test.ts"), To: model.FileID("frontend/field-client.ts"), Provenance: model.Provenance{Path: "frontend/field.test.ts", Line: 3, Evidence: model.Inferred}},
		{ID: "unrelated", Kind: "CONSUMES", From: model.FileID("other/client.ts"), To: other, Provenance: manifest},
		{ID: "unrelated-test", Kind: "DEPENDS_ON", From: model.FileID("other/client.test.ts"), To: model.FileID("other/client.ts")},
		{ID: "proposed-consumer", Kind: "CONSUMES", From: model.FileID("proposed/client.ts"), To: order, Provenance: model.Provenance{Evidence: model.Proposed}},
		{ID: "proposed-test", Kind: "DEPENDS_ON", From: model.FileID("proposed/client.test.ts"), To: model.FileID("proposed/client.ts")},
	}}
	inv := Inventory{Tests: []Test{{Path: "frontend/client.test.ts", PackageRoot: "frontend", Framework: "vitest"}, {Path: "frontend/field.test.ts", PackageRoot: "frontend", Framework: "vitest"}, {Path: "other/client.test.ts", PackageRoot: "other", Framework: "vitest"}, {Path: "proposed/client.test.ts", PackageRoot: "proposed", Framework: "vitest"}}}
	for _, changed := range []string{"backend/model.py", "schemas/orders.json"} {
		p, e := Select(inv, snapshot, []string{changed}, Options{})
		if e != nil {
			t.Fatal(e)
		}
		if len(p.Commands) != 2 {
			t.Fatal("declared consumers missing or unrelated/proposed links invented", changed, p.Commands)
		}
		for _, command := range p.Commands {
			reason := command.EvidenceReasons[0]
			if reason.Code != "declared_contract_impact" || reason.Evidence != model.Inferred {
				t.Fatal("wrong impact confidence", reason)
			}
			found := false
			for _, loc := range reason.Locations {
				if loc.Path == ".radar/contracts.json" {
					found = true
				}
			}
			if !found {
				t.Fatal("missing binding manifest evidence", reason)
			}
			if !strings.Contains(reason.Explanation, "unproven") {
				t.Fatal("runtime usage implied", reason)
			}
		}
	}
}
