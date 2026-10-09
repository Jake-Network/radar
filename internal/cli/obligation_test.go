package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// gateRun is invoke with an optional exit-code expectation: -1 accepts any
// nonzero exit (fail-closed verdicts that differ by command) and -2 any exit
// (legacy no-policy exit semantics, where only the gate verdict is asserted).
func gateRun(t *testing.T, root string, want int, args ...string) map[string]any {
	t.Helper()
	if want >= 0 {
		return invoke(t, root, want, args...)
	}
	var out, errs bytes.Buffer
	if code := Run(context.Background(), append([]string{"--root", root, "--json"}, args...), &out, &errs); code == 0 && want == -1 {
		t.Fatalf("%v passed: %s", args, out.String())
	}
	var v map[string]any
	if e := json.Unmarshal(out.Bytes(), &v); e != nil {
		t.Fatalf("non JSON %v: %s %s", e, out.String(), errs.String())
	}
	return v
}

// Removing or invalidating a declared verification obligation must never turn
// a failing or unverified contract requirement into a passing one.
const obligationManifest = `{"version":1,"bindings":[{"id":"summary","schema":"schema.json","pointer":"","producer":"api.py","consumer":"consumer.ts","fields":["total"],"direction":"response"},{"id":"other","schema":"other.json","pointer":"","consumer":"other.ts","fields":["id"],"direction":"response"}]}`

func obligationRepo(t *testing.T) (string, string) {
	t.Helper()
	root := gitRepo(t)
	put(t, root, "schema.json", `{"type":"object","properties":{"total":{"type":"integer"},"name":{"type":"string"}}}`)
	put(t, root, "other.json", `{"type":"object","properties":{"id":{"type":"string"}}}`)
	put(t, root, "api.py", "def summary(): return {'total': 1, 'name': 'x'}\n")
	put(t, root, "consumer.ts", "export function use(x:any){return x.total};\n")
	put(t, root, "other.ts", "export const id = (x:any) => x.id;\n")
	put(t, root, ".radar/contracts.json", obligationManifest)
	put(t, root, ".radar/policy.json", `{"version":1,"require":["no_breaking_contracts"]}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	return root, gitTest(t, root, "rev-parse", "HEAD")
}

const removedTotal = `{"type":"object","properties":{"name":{"type":"string"}}}`

func TestRemovedObligationCannotBypassContractPolicy(t *testing.T) {
	cases := []struct {
		name    string
		edit    func(t *testing.T, root string)
		verdict string
		code    int
	}{
		{"unchanged manifest", func(t *testing.T, root string) {
			put(t, root, "schema.json", removedTotal)
		}, "fail", 1},
		{"manifest deleted", func(t *testing.T, root string) {
			put(t, root, "schema.json", removedTotal)
			gitTest(t, root, "rm", "-q", ".radar/contracts.json")
		}, "fail", 1},
		{"empty binding list", func(t *testing.T, root string) {
			put(t, root, "schema.json", removedTotal)
			put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[]}`)
		}, "fail", 1},
		{"binding removed", func(t *testing.T, root string) {
			put(t, root, "schema.json", removedTotal)
			put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"other","schema":"other.json","pointer":"","consumer":"other.ts","fields":["id"],"direction":"response"}]}`)
		}, "fail", 1},
		{"consumed field undeclared", func(t *testing.T, root string) {
			put(t, root, "schema.json", removedTotal)
			put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"summary","schema":"schema.json","pointer":"","consumer":"consumer.ts","fields":[],"direction":"response"},{"id":"other","schema":"other.json","pointer":"","consumer":"other.ts","fields":["id"],"direction":"response"}]}`)
		}, "fail", 1},
		{"consumer reference removed", func(t *testing.T, root string) {
			put(t, root, "schema.json", removedTotal)
			put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"summary","schema":"schema.json","pointer":"","fields":["total"],"direction":"response"},{"id":"other","schema":"other.json","pointer":"","consumer":"other.ts","fields":["id"],"direction":"response"}]}`)
		}, "fail", 1},
		{"schema deleted with manifest", func(t *testing.T, root string) {
			gitTest(t, root, "rm", "-q", "schema.json", ".radar/contracts.json")
		}, "fail", 1},
		{"binding moved to invalid pointer", func(t *testing.T, root string) {
			put(t, root, "schema.json", removedTotal)
			put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"summary","schema":"schema.json","pointer":"/missing","consumer":"consumer.ts","fields":["total"],"direction":"response"},{"id":"other","schema":"other.json","pointer":"","consumer":"other.ts","fields":["id"],"direction":"response"}]}`)
		}, "fail", 1},
		{"invalid pointer without schema change", func(t *testing.T, root string) {
			put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"summary","schema":"schema.json","pointer":"/missing","consumer":"consumer.ts","fields":["total"],"direction":"response"},{"id":"other","schema":"other.json","pointer":"","consumer":"other.ts","fields":["id"],"direction":"response"}]}`)
		}, "blocked", 1},
		{"unsupported schema at head", func(t *testing.T, root string) {
			put(t, root, "schema.json", `{"type":"object","properties":{"total":{"type":"integer","not":{"const":0}},"name":{"type":"string"}}}`)
		}, "blocked", 1},
		{"manifest parse error", func(t *testing.T, root string) {
			put(t, root, "schema.json", removedTotal)
			put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[`)
		}, "blocked|error", -1},
		{"declared consumer deleted", func(t *testing.T, root string) {
			put(t, root, "schema.json", removedTotal)
			gitTest(t, root, "rm", "-q", "consumer.ts")
		}, "blocked", 1},
		{"binding removed without schema change", func(t *testing.T, root string) {
			put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"other","schema":"other.json","pointer":"","consumer":"other.ts","fields":["id"],"direction":"response"}]}`)
		}, "blocked", 1},
		{"manifest deleted without schema change", func(t *testing.T, root string) {
			gitTest(t, root, "rm", "-q", ".radar/contracts.json")
		}, "blocked", 1},
		{"binding and consumer removed together", func(t *testing.T, root string) {
			put(t, root, "schema.json", removedTotal)
			gitTest(t, root, "rm", "-q", "consumer.ts")
			put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"other","schema":"other.json","pointer":"","consumer":"other.ts","fields":["id"],"direction":"response"}]}`)
		}, "pass", 0},
		{"explicit retirement", func(t *testing.T, root string) {
			put(t, root, "schema.json", removedTotal)
			put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"other","schema":"other.json","pointer":"","consumer":"other.ts","fields":["id"],"direction":"response"}],"retired":[{"id":"summary","reason":"summary.total is replaced by summary.amount in consumer.ts"}]}`)
		}, "pass", 0},
		{"retirement without reason", func(t *testing.T, root string) {
			put(t, root, "schema.json", removedTotal)
			put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[],"retired":[{"id":"summary"}]}`)
		}, "blocked|error", -1},
		{"retirement of another field", func(t *testing.T, root string) {
			put(t, root, "schema.json", removedTotal)
			put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"summary","schema":"schema.json","pointer":"","consumer":"consumer.ts","fields":[],"direction":"response"},{"id":"other","schema":"other.json","pointer":"","consumer":"other.ts","fields":["id"],"direction":"response"}],"retired":[{"id":"summary","fields":["name"],"reason":"name dropped"}]}`)
		}, "fail", 1},
		{"compatible addition", func(t *testing.T, root string) {
			put(t, root, "schema.json", `{"type":"object","properties":{"total":{"type":"integer"},"name":{"type":"string"},"extra":{"type":"string"}}}`)
		}, "pass", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root, base := obligationRepo(t)
			tc.edit(t, root)
			gitTest(t, root, "add", "-A", ".")
			gitTest(t, root, "commit", "-qm", tc.name)
			for _, args := range [][]string{
				{"check", "--base", base, "--head", "HEAD", "--policy", ".radar/policy.json"},
				{"merge-check", "--base", base, "--branches", "HEAD", "--policy", ".radar/policy.json"},
				{"check", "--base", base, "--head", "HEAD"},
				{"merge-check", "--base", base, "--branches", "HEAD"},
			} {
				want := tc.code
				if args[len(args)-1] != ".radar/policy.json" {
					want = -2
				}
				r := gateRun(t, root, want, args...)
				got, _ := r["gate"].(map[string]any)["verdict"].(string)
				if !strings.Contains("|"+tc.verdict+"|", "|"+got+"|") {
					t.Fatalf("%v verdict %q, want %s\n%v\n%v", args, got, tc.verdict, r["checks"], r["findings"])
				}
			}
		})
	}
}

// A repository that never declared contracts keeps manifest-free analysis.
func TestNoManifestRepositoryStillPassesSupportedGate(t *testing.T) {
	root := gitRepo(t)
	put(t, root, "schema.json", `{"type":"object","properties":{"total":{"type":"integer"}}}`)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	base := gitTest(t, root, "rev-parse", "HEAD")
	put(t, root, "schema.json", `{"type":"object","properties":{}}`)
	gitTest(t, root, "commit", "-qam", "change")
	r := invoke(t, root, 0, "check", "--base", base, "--head", "HEAD")
	if r["gate"].(map[string]any)["verdict"] != "pass" {
		t.Fatal(r["gate"])
	}
}

// A cross-branch scan must see a consumer branch that silently drops its
// binding while a producer branch removes the consumed field.
func TestScanSeesObligationRemovedOnAnotherBranch(t *testing.T) {
	root, base := obligationRepo(t)
	gitTest(t, root, "checkout", "-qb", "producer")
	put(t, root, "schema.json", removedTotal)
	gitTest(t, root, "commit", "-qam", "producer")
	gitTest(t, root, "checkout", "-q", base)
	gitTest(t, root, "checkout", "-qb", "consumer")
	put(t, root, ".radar/contracts.json", `{"version":1,"bindings":[{"id":"other","schema":"other.json","pointer":"","consumer":"other.ts","fields":["id"],"direction":"response"}]}`)
	gitTest(t, root, "commit", "-qam", "consumer drops binding")
	invoke(t, root, 0, "init")
	r := invoke(t, root, 1, "scan", "--base", base, "--branches", "producer,consumer")
	if r["status"] != "failed" {
		t.Fatal(r)
	}
	m := invoke(t, root, 1, "merge-check", "--base", base, "--branches", "producer,consumer")
	if m["gate"].(map[string]any)["verdict"] == "pass" {
		t.Fatal(m["gate"])
	}
}

// A conflicting merge produces no candidate, so contract compatibility is
// unanalyzed and cannot pass even under a contracts-only policy.
func TestConflictingMergeCannotPassContractRequirement(t *testing.T) {
	root, base := obligationRepo(t)
	gitTest(t, root, "checkout", "-qb", "left")
	put(t, root, "api.py", "def summary(): return {'total': 2}\n")
	gitTest(t, root, "commit", "-qam", "left")
	gitTest(t, root, "checkout", "-q", base)
	gitTest(t, root, "checkout", "-qb", "right")
	put(t, root, "api.py", "def summary(): return {'total': 3}\n")
	gitTest(t, root, "commit", "-qam", "right")
	r := invoke(t, root, 1, "merge-check", "--base", base, "--branches", "left,right", "--policy", ".radar/policy.json")
	if r["gate"].(map[string]any)["verdict"] != "blocked" {
		t.Fatal(r["gate"])
	}
}
