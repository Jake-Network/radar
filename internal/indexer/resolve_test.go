package indexer

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/radar-engine/radar/internal/model"
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
	} {
		if !deps[want] {
			t.Errorf("missing %s; have %v", want, deps)
		}
	}
	if deps["cmd/main.go -> internal/store/a_test.go"] || len(deps) != 10 {
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
