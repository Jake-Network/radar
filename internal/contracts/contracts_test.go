package contracts

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func doc(t *testing.T, s string) map[string]any {
	t.Helper()
	d, e := DecodeDocument([]byte(s))
	if e != nil {
		t.Fatal(e)
	}
	return d
}
func TestCompare(t *testing.T) {
	a := doc(t, `{"type":"object","properties":{"total":{"type":"integer"}}}`)
	b := doc(t, `{"type":"object","properties":{"total_cents":{"type":"integer"}}}`)
	changes, e := Compare(a, b, "")
	if e != nil || len(changes) != 1 || changes[0].Field != "total" || changes[0].Kind != "field_removed" {
		t.Fatalf("%+v %v", changes, e)
	}
	for _, bad := range []string{`{"properties":[]}`, `{"type":17}`, `{"required":[1]}`, `{"properties":{"x":false}}`, `{"$ref":"#/missing"}`, `{"enum":"x"}`} {
		if _, e := Compare(a, doc(t, bad), ""); e == nil {
			t.Errorf("accepted malformed %s", bad)
		}
	}
	// Unsupported but valid constructs are compared locally, not rejected.
	for _, unsupported := range []string{`{"$ref":"https://example.com/schema"}`, `{"$ref":"#"}`, `{"not":{"type":"string"}}`} {
		cs, e := Compare(a, doc(t, unsupported), "")
		if e != nil || len(cs) != 1 || cs[0].Kind != "unanalyzed" {
			t.Errorf("%s: %+v %v", unsupported, cs, e)
		}
	}
	if _, e := DecodeDocument([]byte(`[]`)); e == nil {
		t.Fatal("accepted array document")
	}
}
func TestPointer(t *testing.T) {
	d := doc(t, `{"a/b":{"~key":{"type":"string"}}}`)
	if _, e := Pointer(d, "/a~1b/~0key"); e != nil {
		t.Fatal(e)
	}
	if _, e := Pointer(d, "/missing"); e == nil {
		t.Fatal("missing pointer accepted")
	}
}
func gitRun(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	b, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
	return strings.TrimSpace(string(b))
}
func write(t *testing.T, root, path, content string) {
	t.Helper()
	p := filepath.Join(root, path)
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(content), 0644); e != nil {
		t.Fatal(e)
	}
}
func fixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	gitRun(t, root, "init", "-q")
	gitRun(t, root, "config", "user.name", "Radar Test")
	gitRun(t, root, "config", "user.email", "test@example.invalid")
	write(t, root, "openapi.json", `{"openapi":"3.1.0","components":{"schemas":{"Export":{"type":"object","properties":{"total":{"type":"integer"}}}}}}`)
	write(t, root, "backend.py", "class Export: pass\n")
	write(t, root, "consumer.ts", "export const total = (x: {total: number}) => x.total;\n")
	m := Manifest{Version: 1, Bindings: []Binding{{ID: "export", Schema: "openapi.json", Pointer: "/components/schemas/Export", Producer: "backend.py", Consumer: "consumer.ts", Fields: []string{"total"}, Direction: "response"}}}
	b, _ := json.Marshal(m)
	write(t, root, ".radar/contracts.json", string(b))
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "base")
	return root, gitRun(t, root, "rev-parse", "HEAD")
}
func commit(t *testing.T, root, msg string) {
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", msg)
}
func TestCrossBranchAndCheckpoints(t *testing.T) {
	root, base := fixture(t)
	ctx := context.Background()
	gitRun(t, root, "checkout", "-qb", "producer")
	write(t, root, "openapi.json", `{"components":{"schemas":{"Export":{"type":"object","properties":{"total_cents":{"type":"integer"}}}}}}`)
	commit(t, root, "rename response field")
	producer := gitRun(t, root, "rev-parse", "HEAD")
	gitRun(t, root, "checkout", "-qb", "consumer", base)
	write(t, root, "consumer.ts", "export const formatted = (x: {total: number}) => String(x.total);\n")
	commit(t, root, "format total")
	r, e := Scan(ctx, root, base, []string{"producer", "consumer"})
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, f := range r.Findings {
		if f.Code == "contract_field_removed" && f.Consumer == "consumer.ts" && f.Branches[0] == producer && f.Branches[1] == r.Heads[1] {
			found = true
			if f.Severity != "error" || len(f.Locations) != 3 {
				t.Fatalf("missing evidence: %+v", f)
			}
		}
	}
	if !found {
		t.Fatalf("missing conflict: %+v", r)
	}
	write(t, root, "openapi.json", `{"components":{"schemas":{"Export":{"type":"object","properties":{}}}}}`)
	r, e = Impact(ctx, root, base, "WORKTREE")
	if e != nil || r.Checkpoint != "informational" || len(r.Findings) != 1 || r.Findings[0].Severity != "warning" {
		t.Fatalf("%+v %v", r, e)
	}
	write(t, root, "openapi.json", "{")
	r, e = Impact(ctx, root, base, "WORKTREE")
	if e != nil || len(r.Findings) != 0 || len(r.Diagnostics) == 0 {
		t.Fatalf("malformed worktree falsely authoritative: %+v %v", r, e)
	}
}
func TestIndependentChanges(t *testing.T) {
	root, base := fixture(t)
	gitRun(t, root, "checkout", "-qb", "a")
	write(t, root, "a.go", "package a\n")
	commit(t, root, "a")
	gitRun(t, root, "checkout", "-qb", "b", base)
	write(t, root, "b.rs", "fn main() {}\n")
	commit(t, root, "b")
	r, e := Scan(context.Background(), root, base, []string{"a", "b"})
	if e != nil || len(r.Findings) != 0 || len(r.Diagnostics) != 0 {
		t.Fatalf("false positive: %+v %v", r, e)
	}
}
func TestRequiredResponseAddition(t *testing.T) {
	root, base := fixture(t)
	write(t, root, "openapi.json", `{"components":{"schemas":{"Export":{"type":"object","properties":{"total":{"type":"integer"}},"required":["total"]}}}}`)
	commit(t, root, "guarantee response total")
	r, e := Impact(context.Background(), root, base, "HEAD")
	if e != nil || len(r.Findings) != 0 {
		t.Fatalf("response guarantee is not breaking: %+v %v", r, e)
	}
}
func TestDeletedAndUnsupportedContract(t *testing.T) {
	root, base := fixture(t)
	if e := os.Remove(filepath.Join(root, "openapi.json")); e != nil {
		t.Fatal(e)
	}
	commit(t, root, "remove contract")
	r, e := Impact(context.Background(), root, base, "HEAD")
	if e != nil || len(r.Findings) != 1 || r.Findings[0].Code != "contract_contract_removed" {
		t.Fatalf("deleted contract: %+v %v", r, e)
	}
	gitRun(t, root, "checkout", "--detach", base)
	write(t, root, "openapi.json", `{"components":{"schemas":{"Export":{"oneOf":[{"type":"object","properties":{"total":{"type":"integer"}}},{"type":"object","properties":{"count":{"type":"integer"}}}]}}}}`)
	commit(t, root, "unsupported union")
	r, e = Impact(context.Background(), root, base, "HEAD")
	if e != nil || len(r.Findings) != 0 || len(r.Diagnostics) == 0 || r.Status != model.StatusIncomplete || r.Analyzed != 0 {
		t.Fatalf("unsupported falsely passed: %+v %v", r, e)
	}
	if _, e = Impact(context.Background(), root, base, "missing-reference"); e == nil {
		t.Fatal("missing ref accepted")
	}
}

func TestRequiredAdditionDirection(t *testing.T) {
	for _, direction := range []string{"request", ""} {
		t.Run(direction, func(t *testing.T) {
			root, base := fixture(t)
			m, err := LoadManifest(context.Background(), root, base)
			if err != nil {
				t.Fatal(err)
			}
			m.Bindings[0].Direction = direction
			b, _ := json.Marshal(m)
			write(t, root, ".radar/contracts.json", string(b))
			commit(t, root, "set direction")
			base = gitRun(t, root, "rev-parse", "HEAD")
			write(t, root, "openapi.json", `{"components":{"schemas":{"Export":{"type":"object","properties":{"total":{"type":"integer"}},"required":["total"]}}}}`)
			commit(t, root, "require total")
			r, e := Impact(context.Background(), root, base, "HEAD")
			if e != nil || len(r.Findings) != 1 {
				t.Fatalf("%+v %v", r, e)
			}
			severity := model.SeverityWarning
			if direction == "request" {
				severity = model.SeverityError
			}
			if r.Findings[0].Severity != severity {
				t.Fatalf("incorrect direction severity: %+v", r)
			}
		})
	}
}

func TestAnnotationsDoNotDisableComparison(t *testing.T) {
	root, base := fixture(t)
	// Regression: an unrelated format keyword used to skip the whole binding.
	write(t, root, "openapi.json", `{"components":{"schemas":{"Export":{"type":"object","properties":{"id":{"type":"string","format":"uuid","maxLength":36}}}}}}`)
	commit(t, root, "remove total, add annotated id")
	r, e := Impact(context.Background(), root, base, "HEAD")
	if e != nil || r.Status != model.StatusFailed || len(r.Findings) != 1 || r.Findings[0].Code != "contract_field_removed" {
		t.Fatalf("annotation hid a breaking removal: %+v %v", r, e)
	}
}
func TestNullableAndEnumDirection(t *testing.T) {
	base := doc(t, `{"type":"object","properties":{"total":{"type":"integer"},"state":{"type":"string","enum":["a","b"]}}}`)
	nullable := doc(t, `{"type":"object","properties":{"total":{"anyOf":[{"type":"integer"},{"type":"null"}]},"state":{"type":"string","enum":["a","b"]}}}`)
	cs, e := Compare(base, nullable, "")
	if e != nil || len(cs) != 1 || cs[0].Kind != "type_changed" || cs[0].AfterType != "integer|null" {
		t.Fatalf("%+v %v", cs, e)
	}
	if classify(cs[0], "response") != "breaking" || classify(cs[0], "request") != "compatible" {
		t.Fatal("nullable direction wrong")
	}
	oas30 := doc(t, `{"type":"object","properties":{"total":{"type":"integer","nullable":true},"state":{"type":"string","enum":["a","b"]}}}`)
	if cs2, _ := Compare(nullable, oas30, ""); len(cs2) != 0 {
		t.Fatalf("OAS 3.0 nullable differs from 3.1 form: %+v", cs2)
	}
	widened := doc(t, `{"type":"object","properties":{"total":{"type":"integer"},"state":{"type":"string","enum":["a","b","c"]}}}`)
	cs, _ = Compare(base, widened, "")
	if len(cs) != 1 || cs[0].Kind != "enum_value_added" || classify(cs[0], "response") != "risk" || classify(cs[0], "request") != "compatible" {
		t.Fatalf("%+v", cs)
	}
}
func TestRefsAllOfAndRecursion(t *testing.T) {
	a := doc(t, `{"$defs":{"Base":{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]},"Node":{"type":"object","properties":{"children":{"type":"array","items":{"$ref":"#/$defs/Node"}}}}},"allOf":[{"$ref":"#/$defs/Base"},{"properties":{"tree":{"$ref":"#/$defs/Node"}}}]}`)
	b := doc(t, `{"$defs":{"Base":{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]},"Node":{"type":"object","properties":{"children":{"type":"array","items":{"$ref":"#/$defs/Node"}}}}},"allOf":[{"$ref":"#/$defs/Base"},{"properties":{"tree":{"$ref":"#/$defs/Node"}}}]}`)
	cs, e := Compare(a, b, "")
	if e != nil {
		t.Fatal(e)
	}
	kinds := map[string]bool{}
	for _, c := range cs {
		kinds[c.Kind+":"+c.Field] = true
	}
	// required_removed for id is subsumed by field_removed.
	if !kinds["field_removed:id"] || !kinds["required_added:key"] || len(cs) != 2 {
		t.Fatalf("%+v", cs)
	}
}
func TestYAMLAndNestedConsumerFields(t *testing.T) {
	d, e := DecodeDocumentAt("api.yaml", []byte("components:\n  schemas:\n    S:\n      type: object\n      properties:\n        a:\n          type: object\n          properties:\n            b: {type: integer}\n"))
	if e != nil {
		t.Fatal(e)
	}
	s, e := Analyzable(d, "/components/schemas/S")
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := s.Field("a.b"); !ok {
		t.Fatal("nested YAML field missing")
	}
	if !FieldOverlaps("a", "a.b") || !FieldOverlaps("a.b", "a") || !FieldOverlaps("items[].id", "items.id") || FieldOverlaps("ab", "a") {
		t.Fatal("field overlap rules")
	}
	if missing := MissingConsumerFields([]byte("const x = s.total;"), []string{"total", "summary.currency"}); len(missing) != 1 || missing[0] != "summary.currency" {
		t.Fatal(missing)
	}
}
func TestLint(t *testing.T) {
	root, base := fixture(t)
	r := Lint(context.Background(), root, base)
	if r.Status != model.StatusPassed || len(r.Bindings) != 1 {
		t.Fatalf("%+v", r)
	}
	write(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"export","schema":"openapi.json","pointer":"/components/schemas/Export","producer":"backend.py","consumer":"consumer.ts","fields":["total","currency"]}]}`)
	r = Lint(context.Background(), root, "WORKTREE")
	if r.Status != model.StatusWarning || len(r.Bindings[0].Issues) != 2 {
		t.Fatalf("%+v", r)
	}
}

func TestScanIgnoresBranchesThatLeaveConsumersUntouched(t *testing.T) {
	root, base := fixture(t)
	gitRun(t, root, "checkout", "-qb", "producer")
	write(t, root, "openapi.json", `{"components":{"schemas":{"Export":{"type":"object","properties":{"total_cents":{"type":"integer"}}}}}}`)
	commit(t, root, "rename")
	producer := gitRun(t, root, "rev-parse", "HEAD")
	gitRun(t, root, "checkout", "-qb", "unrelated", base)
	write(t, root, "backend.py", "class Export: pass\n# docs\n")
	commit(t, root, "unrelated producer-side docs")
	gitRun(t, root, "checkout", "-qb", "consumer", base)
	write(t, root, "consumer.ts", "export const shown = (x: {total: number}) => x.total.toFixed(0);\n")
	commit(t, root, "consumer edit")
	r, e := Scan(context.Background(), root, base, []string{"producer", "unrelated", "consumer"})
	if e != nil {
		t.Fatal(e)
	}
	pairs := map[string]bool{}
	for _, f := range r.Findings {
		pairs[f.Branches[0]+">"+f.Branches[1]] = true
	}
	consumer := r.Heads[2]
	if !pairs[producer+">"+producer] || !pairs[producer+">"+consumer] || pairs[producer+">"+r.Heads[1]] || len(r.Findings) != 2 {
		t.Fatalf("expected self and consumer-branch findings only: %+v", r.Findings)
	}
}
