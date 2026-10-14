package discovery

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func TestPythonFrameworkPrefilterKeepsLocalWrappers(t *testing.T) {
	sources := map[string]string{"framework.py": "from fastapi import FastAPI as App\nfrom pydantic import BaseModel as Model\n", "models.py": "from framework import Model\nclass User(Model):\n    email: str\n", "app.py": "from framework import App\nfrom models import User\napp=App()\n@app.get('/users',response_model=User)\ndef users():\n    pass\n"}
	schemas, endpoints, diagnostics, err := resolvePython(context.Background(), sources, "revision")
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 1 || len(endpoints) != 1 {
		t.Fatalf("wrapper facts omitted: schemas=%+v endpoints=%+v diagnostics=%+v", schemas, endpoints, diagnostics)
	}
}
func TestPythonFrameworkPrefilterDoesNotFabricateFrameworkFacts(t *testing.T) {
	sources := map[string]string{"framework.py": "class Model:\n    pass\nclass App:\n    pass\n", "models.py": "from framework import Model\nclass User(Model):\n    email: str\n", "app.py": "from framework import App\nfrom models import User\napp=App()\n@app.get('/users',response_model=User)\ndef users():\n    pass\n"}
	schemas, endpoints, _, err := resolvePython(context.Background(), sources, "revision")
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 0 || len(endpoints) != 0 {
		t.Fatalf("framework facts fabricated: %v %v", schemas, endpoints)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := resolvePython(ctx, sources, "revision"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
func TestNoPythonFrameworkPreservesJSONAndTypedConsumerDiscovery(t *testing.T) {
	root := fixture(t)
	write(t, root, "api.py", "from framework import App\napp=App()\n")
	write(t, root, "framework.py", "class App:\n    pass\n")
	write(t, root, "openapi.json", `{"openapi":"3.1.0","components":{"schemas":{"User":{"properties":{"id":{"type":"integer"},"email":{"type":"string"}}}}},"paths":{"/users":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/User"}}}}}}}}}`)
	r, err := Discover(context.Background(), root, "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != model.StatusIncomplete || len(r.Candidates) != 1 || r.Candidates[0].Consumer != "client.ts" {
		t.Fatalf("JSON/TS discovery scope changed: %+v", r)
	}
	if err := os.Remove(filepath.Join(root, "openapi.json")); err != nil {
		t.Fatal(err)
	}
	r, err = Discover(context.Background(), root, "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != model.StatusIncomplete || len(r.Candidates) != 0 {
		t.Fatalf("no-framework result claimed completion: %+v", r)
	}
}
