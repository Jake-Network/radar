package contracts

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	for _, bad := range []string{`{"properties":[]}`, `{"type":17}`, `{"required":[1]}`, `{"properties":{"x":false}}`, `{"properties":{"x":{"minimum":1}}}`, `{"$ref":"https://example.com/schema"}`, `{"$ref":"#"}`} {
		if _, e := Compare(a, doc(t, bad), ""); e == nil {
			t.Errorf("accepted unsupported/malformed %s", bad)
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
	write(t, root, "openapi.json", `{"components":{"schemas":{"Export":{"oneOf":[{"type":"object"}]}}}}`)
	commit(t, root, "unsupported union")
	r, e = Impact(context.Background(), root, base, "HEAD")
	if e != nil || len(r.Findings) != 0 || len(r.Diagnostics) == 0 {
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
			severity := "warning"
			if direction == "request" {
				severity = "error"
			}
			if r.Findings[0].Severity != severity {
				t.Fatalf("incorrect direction severity: %+v", r)
			}
		})
	}
}
