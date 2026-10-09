package indexer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func TestImportResolution(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"web/app.ts":               "import { x } from './util'; import y from '../shared/index.js'; import z from 'react';\n",
		"web/util.ts":              "export const x = 1;\n",
		"shared/index.ts":          "export default 2;\n",
		"svc/api.py":               "from models import Summary\nfrom .handlers import run\nfrom pkg import helpers\nimport os\n",
		"svc/models.py":            "class Summary: pass\n",
		"svc/handlers.py":          "def run(): pass\n",
		"svc/__init__.py":          "",
		"pkg/__init__.py":          "",
		"pkg/helpers.py":           "def help(): pass\n",
		"go.mod":                   "module example.com/app\n\ngo 1.23\n",
		"cmd/main.go":              "package main\nimport (\n\t\"fmt\"\n\t\"example.com/app/internal/store\"\n)\nfunc main() { fmt.Println(store.X) }\n",
		"internal/store/a.go":      "package store\nconst X = 1\n",
		"internal/store/a_test.go": "package store\n",
		"engine/Cargo.toml":        "[package]\nname = \"engine\"\n",
		"engine/src/lib.rs":        "mod parser;\nuse crate::format::render;\n",
		"engine/src/parser.rs":     "use super::format::{render, Style};\npub fn parse() {}\n",
		"engine/src/format.rs":     "pub fn render() {}\n",
		// Nested Python project: tests import the package from the project root.
		"services/api/pyproject.toml":          "[project]\nname = \"api\"\n",
		"services/api/app/__init__.py":         "",
		"services/api/app/pricing.py":          "def total(): pass\n",
		"services/api/tests/test_pricing.py":   "from app.pricing import total\n",
		"services/web/src/storefront/views.py": "def view(): pass\n",
		"services/web/setup.cfg":               "[metadata]\nname = web\n",
		"services/web/tests/test_views.py":     "import storefront.views\n",
		"unmarked/tests/test_orphan.py":        "from app.pricing import total\n",
	}
	for p, content := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s, err := Index(context.Background(), root, "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	deps := map[string]bool{}
	for _, e := range s.Edges {
		if e.Kind == "DEPENDS_ON" {
			if e.Provenance.Evidence != model.Inferred {
				t.Fatal("path resolution claimed verified evidence", e)
			}
			deps[model.PathFromID(e.From)+" -> "+model.PathFromID(e.To)] = true
		}
	}
	for _, want := range []string{
		"web/app.ts -> web/util.ts",
		"web/app.ts -> shared/index.ts",
		"svc/api.py -> svc/models.py",
		"svc/api.py -> svc/handlers.py",
		"svc/api.py -> pkg/helpers.py",
		"svc/api.py -> pkg/__init__.py", // importing a package runs its __init__
		"cmd/main.go -> internal/store/a.go",
		"engine/src/lib.rs -> engine/src/parser.rs",
		"engine/src/lib.rs -> engine/src/format.rs",
		"engine/src/parser.rs -> engine/src/format.rs",
		"services/api/tests/test_pricing.py -> services/api/app/pricing.py",
		"services/web/tests/test_views.py -> services/web/src/storefront/views.py",
	} {
		if !deps[want] {
			t.Errorf("missing %s; have %v", want, deps)
		}
	}
	if deps["cmd/main.go -> internal/store/a_test.go"] || len(deps) != 12 || deps["unmarked/tests/test_orphan.py -> services/api/app/pricing.py"] {
		t.Errorf("unexpected dependencies %v", deps)
	}
}

func TestEntityIDsAreReadableAndLocationIndependent(t *testing.T) {
	ids := func() map[string]bool {
		root := t.TempDir()
		write(t, root, "svc/server.go", "package svc\ntype Server struct{}\nfunc (s *Server) Handle() {}\n")
		write(t, root, "svc/model.py", "class Order:\n    def total(self):\n        return 1\n")
		s, err := Index(context.Background(), root, "WORKTREE")
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, n := range s.Nodes {
			out[n.ID] = true
		}
		return out
	}
	a, b := ids(), ids()
	for _, want := range []string{"file:svc/server.go", "function:svc/server.go#Server.Handle", "type:svc/server.go#Server", "class:svc/model.py#Order", "function:svc/model.py#Order.total", "repository"} {
		if !a[want] || !b[want] {
			t.Errorf("missing %s in %v", want, a)
		}
	}
	if len(a) != len(b) {
		t.Fatal("identities depend on checkout location")
	}
}

func TestTypeScriptAliasAndWorkspaceResolution(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		// Shared base config with comments and trailing commas, extended by the app.
		"tsconfig.base.json":                 "{\n  // shared\n  \"compilerOptions\": {\n    \"baseUrl\": \".\", /* aliases resolve from the repository root */\n    \"paths\": {\n      \"@app/*\": [\"apps/web/src/*\"],\n      \"@app/config\": [\"apps/web/config/index.ts\"],\n    },\n  },\n}\n",
		"apps/web/tsconfig.json":             "{ \"extends\": \"../../tsconfig.base.json\", \"compilerOptions\": { \"strict\": true } }",
		"apps/web/src/main.ts":               "import { Button } from '@acme/ui';\nimport { format } from 'utils/format';\nimport { store } from '@app/state/store';\nimport cfg from '@app/config';\nimport { Header } from '@app/components/Header';\nimport React from 'react';\n",
		"apps/web/src/state/store.ts":        "export const store = 1;\n",
		"apps/web/src/components/Header.tsx": "export const Header = 1;\n",
		"apps/web/config/index.ts":           "export default 1;\n",
		"packages/ui/package.json":           `{"name":"@acme/ui","main":"dist/index.js","types":"dist/index.d.ts"}`,
		"packages/ui/src/index.ts":           "export const Button = 1;\n",
		"packages/utils/package.json":        `{"name":"utils","exports":{".":{"types":"./src/index.ts"},"./format":"./src/format.ts"}}`,
		"packages/utils/src/index.ts":        "export * from './format';\n",
		"packages/utils/src/format.ts":       "export const format = 1;\n",
		// Two packages claiming one name are ambiguous and never guessed.
		"dup/a/package.json":   `{"name":"dup"}`,
		"dup/a/index.ts":       "",
		"dup/b/package.json":   `{"name":"dup"}`,
		"dup/b/index.ts":       "",
		"legacy/jsconfig.json": `{"compilerOptions":{"baseUrl":"."}}`,
		"legacy/app.js":        "import helper from 'lib/helper';\nimport d from 'dup';\n",
		"legacy/lib/helper.js": "export default 1;\n",
	}
	for p, content := range files {
		write(t, root, p, content)
	}
	s, err := Index(context.Background(), root, "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	deps := map[string]bool{}
	for _, e := range s.Edges {
		if e.Kind == "DEPENDS_ON" {
			deps[model.PathFromID(e.From)+" -> "+model.PathFromID(e.To)] = true
		}
	}
	for _, want := range []string{
		"apps/web/src/main.ts -> packages/ui/src/index.ts",
		"apps/web/src/main.ts -> packages/utils/src/format.ts",
		"apps/web/src/main.ts -> apps/web/src/state/store.ts",
		"apps/web/src/main.ts -> apps/web/config/index.ts",
		"apps/web/src/main.ts -> apps/web/src/components/Header.tsx",
		"packages/utils/src/index.ts -> packages/utils/src/format.ts",
		"legacy/app.js -> legacy/lib/helper.js",
	} {
		if !deps[want] {
			t.Errorf("missing %s; have %v", want, deps)
		}
	}
	for d := range deps {
		if strings.HasPrefix(d, "legacy/app.js -> dup/") {
			t.Error("ambiguous package name resolved", d)
		}
	}
	if len(deps) != 7 {
		t.Errorf("unexpected dependencies %v", deps)
	}
}

func TestStripJSONC(t *testing.T) {
	in := "{\n \"a\": \"// not a comment\", // trailing\n \"b\": [1, 2, /* c */ ],\n \"c\": \"x,}\",\n}"
	var v map[string]any
	if err := json.Unmarshal(stripJSONC([]byte(in)), &v); err != nil {
		t.Fatal(err, string(stripJSONC([]byte(in))))
	}
	if v["a"] != "// not a comment" || len(v["b"].([]any)) != 2 || v["c"] != "x,}" {
		t.Fatal(v)
	}
}
