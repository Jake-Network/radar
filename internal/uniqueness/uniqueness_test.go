package uniqueness

import (
	"context"
	"github.com/Jake-Network/radar/internal/migrations"
	"github.com/Jake-Network/radar/internal/model"
	"strings"
	"testing"
)

func evaluateFixture(t *testing.T, before, after map[string]string, changed []string) Result {
	t.Helper()
	trees := []migrations.Tree{}
	blobs := map[string][]byte{}
	for i, files := range []map[string]string{before, after} {
		tree := migrations.Tree{Label: []string{"base", "combined"}[i], Revision: []string{"base", "combined"}[i]}
		for p, b := range files {
			oid := tree.Label + p
			tree.Files = append(tree.Files, migrations.File{Path: p, OID: oid})
			blobs[oid] = []byte(b)
		}
		trees = append(trees, tree)
	}
	r, e := Evaluate(context.Background(), trees, changed, func(oid string) ([]byte, error) { return blobs[oid], nil })
	if e != nil {
		t.Fatal(e)
	}
	return r
}

const regexConfig = `{"version":1,"rules":[{"name":"error code","files":["src/**/*.txt"],"pattern":"E[0-9]+"}]}`

func TestRegexDuplicateAndBase(t *testing.T) {
	before := map[string]string{ManifestPath: regexConfig, "src/a.txt": "E1001"}
	after := map[string]string{ManifestPath: regexConfig, "src/a.txt": "E1001", "src/nested/b.txt": "E1001"}
	r := evaluateFixture(t, before, after, []string{"src/nested/b.txt"})
	if r.Status != model.StatusFailed || len(r.Findings) != 1 || len(r.Findings[0].Locations) != 2 {
		t.Fatalf("%+v", r)
	}
	r = evaluateFixture(t, after, after, []string{"src/a.txt"})
	if r.Status != model.StatusPassed || r.Findings[0].Severity == model.SeverityError {
		t.Fatalf("base issue: %+v", r)
	}
}
func TestJSONPointerDuplicates(t *testing.T) {
	config := `{"version":1,"rules":[{"name":"flag","files":["*.json"],"json_pointer":"/flags"}]}`
	before := map[string]string{ManifestPath: config, "a.json": `{"flags":["x"]}`}
	after := map[string]string{ManifestPath: config, "a.json": `{"flags":["x","x"]}`}
	r := evaluateFixture(t, before, after, []string{"a.json"})
	if r.Status != model.StatusFailed {
		t.Fatalf("%+v", r)
	}
}
func TestMalformedAndRemovedConfigUnknown(t *testing.T) {
	for _, c := range []string{"", `{"version":2,"rules":[]}`, `{"version":1,"rules":[{"name":"a","files":["../*"],"pattern":"x"}]}`, `{"version":1,"rules":[{"name":"a","files":["*"],"pattern":"["}]}`} {
		after := map[string]string{"src/a.txt": "E1"}
		if c != "" {
			after[ManifestPath] = c
		}
		r := evaluateFixture(t, map[string]string{ManifestPath: regexConfig}, after, []string{ManifestPath})
		if r.Status != model.StatusUnknown {
			t.Fatalf("%q %+v", c, r)
		}
	}
}
func TestMalformedJSONUnknown(t *testing.T) {
	config := `{"version":1,"rules":[{"name":"flag","files":["*.json"],"json_pointer":"/flags"}]}`
	r := evaluateFixture(t, map[string]string{ManifestPath: config}, map[string]string{ManifestPath: config, "a.json": "{"}, []string{"a.json"})
	if r.Status != model.StatusUnknown {
		t.Fatalf("%+v", r)
	}
}
func TestIndependentApplicationsDoNotConflict(t *testing.T) {
	before := map[string]string{"a.py": "from fastapi import FastAPI\napp = FastAPI()\n@app.get(\"/items\")\ndef a(): pass\n"}
	after := map[string]string{"a.py": before["a.py"], "b.py": "from fastapi import FastAPI\napp = FastAPI()\n@app.get(\"/items\")\ndef b(): pass\n"}
	r := evaluateFixture(t, before, after, []string{"b.py"})
	if r.Status == model.StatusFailed {
		t.Fatalf("independent apps %+v", r)
	}
}
func TestFastAPIRegisteredRoutesWithoutSchemas(t *testing.T) {
	main := "from fastapi import FastAPI\nfrom a import router as a\nfrom b import router as b\napp = FastAPI()\napp.include_router(a)\napp.include_router(b)\n"
	a := "from fastapi import APIRouter\nrouter = APIRouter()\n@router.get(\"/items\")\ndef a(): pass\n"
	b := "from fastapi import APIRouter\nrouter = APIRouter()\n@router.get(\"/items\")\ndef b(): pass\n"
	before := map[string]string{"main.py": main, "a.py": a, "b.py": "from fastapi import APIRouter\nrouter=APIRouter()"}
	after := map[string]string{"main.py": main, "a.py": a, "b.py": b}
	r := evaluateFixture(t, before, after, []string{"b.py"})
	if r.Status != model.StatusFailed || len(r.Findings) != 1 {
		t.Fatalf("%+v", r)
	}
}
func TestExistingDuplicateShiftingLinesIsWarning(t *testing.T) {
	before := map[string]string{ManifestPath: regexConfig, "src/a.txt": "E1\nE1\n"}
	after := map[string]string{ManifestPath: regexConfig, "src/a.txt": "comment\nE1\nE1\n"}
	r := evaluateFixture(t, before, after, []string{"src/a.txt"})
	if r.Status != model.StatusPassed || len(r.Findings) != 1 || r.Findings[0].Severity == model.SeverityError {
		t.Fatalf("%+v", r)
	}
}
func TestUnrelatedConfigSkipsExistingDynamicRoutes(t *testing.T) {
	route := "from fastapi import FastAPI, APIRouter\napp=FastAPI()\nrouter=APIRouter(prefix=prefix)\n@router.get(\"/x\")\ndef a(): pass\napp.include_router(router)\n"
	before := map[string]string{"routes.py": route, "config.py": "REDIS_VERSION=1\n"}
	after := map[string]string{"routes.py": route, "config.py": "REDIS_VERSION=2\n"}
	r := evaluateFixture(t, before, after, []string{"config.py"})
	if r.Applicable {
		t.Fatalf("unrelated config made route check applicable: %+v", r)
	}
}
func TestUnchangedRegistrationBodyEditSkipsRouteScan(t *testing.T) {
	before := map[string]string{"app.py": "from fastapi import FastAPI\napp=FastAPI()\n@app.get(\"/x\")\ndef a(): return 1\n"}
	after := map[string]string{"app.py": "from fastapi import FastAPI\napp=FastAPI()\n@app.get(\"/x\")\ndef a(): return 2\n"}
	r := evaluateFixture(t, before, after, []string{"app.py"})
	if r.Applicable {
		t.Fatalf("handler-body edit made route check applicable: %+v", r)
	}
}
func TestMultilineRegistrationPathChangesRemainRelevant(t *testing.T) {
	before := map[string]string{"app.py": "from fastapi import FastAPI\napp=FastAPI()\n@app.get(\n\"/x\", response_model=Model)\ndef a(): return 1\n"}
	after := map[string]string{"app.py": "from fastapi import FastAPI\napp=FastAPI()\n@app.get(\n\"/y\", response_model=Model)\ndef a(): return 1\n"}
	r := evaluateFixture(t, before, after, []string{"app.py"})
	if !r.Applicable {
		t.Fatalf("multiline path change skipped: %+v", r)
	}
}
func TestExistingJSONArrayDuplicateShiftingIndexesIsWarning(t *testing.T) {
	config := `{"version":1,"rules":[{"name":"flag","files":["*.json"],"json_pointer":"/flags"}]}`
	before := map[string]string{ManifestPath: config, "a.json": `{"flags":["x","x"]}`}
	after := map[string]string{ManifestPath: config, "a.json": `{"flags":["new","x","x"]}`}
	r := evaluateFixture(t, before, after, []string{"a.json"})
	if r.Status != model.StatusPassed || len(r.Findings) != 1 || r.Findings[0].Severity == model.SeverityError {
		t.Fatalf("%+v", r)
	}
}

func TestRepeatedRegexKeysExhaustTotalObservationBudget(t *testing.T) {
	rule := Rule{Name: "repeated", Files: []string{"*.txt"}, Pattern: "x"}
	tree := migrations.Tree{Revision: "candidate", Files: []migrations.File{{Path: "a.txt", OID: "a"}}}
	locations, reason := extract(tree, rule, func(string) ([]byte, error) { return []byte(strings.Repeat("x", maxKeyObservations+1)), nil })
	if reason != "key observation count exceeds limit" || len(locations) != 0 {
		t.Fatalf("budget reason=%q allocated keys=%d", reason, len(locations))
	}
}
func TestRepeatedKeysBudgetSpansFiles(t *testing.T) {
	rule := Rule{Name: "repeated", Files: []string{"*.txt"}, Pattern: "x"}
	tree := migrations.Tree{Revision: "candidate", Files: []migrations.File{{Path: "a.txt", OID: "a"}, {Path: "b.txt", OID: "b"}}}
	first := strings.Repeat("x", maxKeyObservations-1)
	locations, reason := extract(tree, rule, func(oid string) ([]byte, error) {
		if oid == "a" {
			return []byte(first), nil
		}
		return []byte("xx"), nil
	})
	if reason != "key observation count exceeds limit" || len(locations["x"]) != maxKeyObservations-1 {
		t.Fatalf("budget reason=%q locations=%d", reason, len(locations["x"]))
	}
}
func TestRepeatedJSONArrayKeysExhaustTotalObservationBudget(t *testing.T) {
	pointer := "/flags"
	rule := Rule{Name: "repeated", Files: []string{"*.json"}, JSONPointer: &pointer}
	tree := migrations.Tree{Revision: "candidate", Files: []migrations.File{{Path: "a.json", OID: "a"}}}
	content := []byte(`{"flags":[` + strings.Repeat(`"x",`, maxKeyObservations) + `"x"]}`)
	locations, reason := extract(tree, rule, func(string) ([]byte, error) { return content, nil })
	if reason != "key observation count exceeds limit" || len(locations) != 0 {
		t.Fatalf("budget reason=%q allocated keys=%d", reason, len(locations))
	}
}
func TestObservedDuplicateFailureWinsOverRuleBudgetExhaustion(t *testing.T) {
	config := `{"version":1,"rules":[{"name":"known","files":["small.txt"],"pattern":"D"},{"name":"bounded","files":["huge.txt"],"pattern":"x"}]}`
	before := map[string]string{ManifestPath: config, "small.txt": "D", "huge.txt": "x"}
	after := map[string]string{ManifestPath: config, "small.txt": "DD", "huge.txt": strings.Repeat("x", maxKeyObservations+1)}
	r := evaluateFixture(t, before, after, []string{"small.txt", "huge.txt"})
	if r.Status != model.StatusFailed || r.Evidence != model.VerifiedStatic || len(r.Findings) != 1 || len(r.Diagnostics) == 0 {
		t.Fatalf("%+v", r)
	}
}
