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

// Java imports resolve through declared packages, not directory layout.
func TestJavaImportResolution(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"core/src/main/java/com/shop/Inventory.java":    "package com.shop;\nimport com.shop.model.Item;\nimport java.util.List;\npublic class Inventory {}\n",
		"core/src/main/java/com/shop/model/Item.java":   "package com.shop.model;\npublic record Item(String sku) {}\n",
		"core/src/main/java/com/shop/model/Price.java":  "package com.shop.model;\npublic class Price { public static class Tax {} }\n",
		"core/src/main/java/com/shop/util/Strings.java": "package com.shop.util;\npublic final class Strings { public static String trim(String s) { return s; } }\n",
		"api/src/main/java/com/shop/api/Handler.java":   "package com.shop.api;\nimport com.shop.model.*;\nimport static com.shop.util.Strings.trim;\nimport com.shop.model.Price.Tax;\nimport com.shop.model.Missing;\nclass Handler {}\n",
		"api/src/main/java/com/shop/api/Wild.java":      "package com.shop.api;\nimport static com.shop.util.Strings.*;\nimport com.shop.dup.Twin;\nclass Wild {}\n",
		"legacy/Misplaced.java":                         "package com.shop.model;\npublic class Misplaced {}\n",
		"a/src/main/java/com/shop/dup/Twin.java":        "package com.shop.dup;\npublic class Twin {}\n",
		"b/src/main/java/com/shop/dup/Twin.java":        "package com.shop.dup;\npublic class Twin {}\n",
		"api/src/main/java/com/shop/api/Unnamed.java":   "class Unnamed {}\n",
		"api/src/main/java/com/shop/api/UsesNamed.java": "package com.shop.api;\nimport Unnamed;\nclass UsesNamed {}\n",
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
	got := map[string]bool{}
	for _, e := range s.Edges {
		if e.Kind == "DEPENDS_ON" {
			if e.Provenance.Evidence != model.Inferred || e.Provenance.Method != "import_path_resolution:java" {
				t.Fatalf("edge provenance %+v", e.Provenance)
			}
			got[strings.TrimPrefix(e.From, "file:")+" -> "+strings.TrimPrefix(e.To, "file:")] = true
		}
	}
	want := []string{
		"core/src/main/java/com/shop/Inventory.java -> core/src/main/java/com/shop/model/Item.java",
		"api/src/main/java/com/shop/api/Handler.java -> core/src/main/java/com/shop/model/Item.java",
		"api/src/main/java/com/shop/api/Handler.java -> core/src/main/java/com/shop/model/Price.java",
		"api/src/main/java/com/shop/api/Handler.java -> legacy/Misplaced.java",
		"api/src/main/java/com/shop/api/Handler.java -> core/src/main/java/com/shop/util/Strings.java",
		"api/src/main/java/com/shop/api/Wild.java -> core/src/main/java/com/shop/util/Strings.java",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing %s", w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("unexpected edges: %v", got)
	}
}

func indexTree(t *testing.T, files map[string]string) map[string]string {
	t.Helper()
	root := t.TempDir()
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
	got := map[string]string{}
	for _, e := range s.Edges {
		if e.Kind == "DEPENDS_ON" {
			if e.Provenance.Evidence != model.Inferred {
				t.Fatalf("edge provenance %+v", e.Provenance)
			}
			got[strings.TrimPrefix(e.From, "file:")+" -> "+strings.TrimPrefix(e.To, "file:")] = e.Provenance.Method
		}
	}
	return got
}

func TestIncludeResolution(t *testing.T) {
	got := indexTree(t, map[string]string{
		"CMakeLists.txt":           "project(shop)\n",
		"include/shop/inventory.h": "#pragma once\nnamespace shop { class Inventory { public: int reserve(int); }; }\n",
		"src/inventory.cpp":        "#include \"shop/inventory.h\"\nint shop::Inventory::reserve(int n) { return n; }\n",
		"src/util/strings.h":       "int trim(void);\n",
		"src/util/strings.c":       "#include \"strings.h\"\nint trim(void) { return 0; }\n",
		"src/app/main.c":           "#include \"util/strings.h\"\n#include <stdio.h>\n#include <string.h>\nint main(void) { return trim(); }\n",
		"src/string.h":             "/* shadows nothing: system includes do not search here */\n",
		"tests/inventory_test.cpp": "#include <shop/inventory.h>\n#include \"missing.h\"\nint main() { return 0; }\n",
		"lib/CMakeLists.txt":       "project(lib)\n",
		"lib/include/both.h":       "int both(void);\n",
		"lib/src/both.h":           "int both(void);\n",
		"lib/tools/use.c":          "#include \"both.h\"\n",
		"pair/codec.h":             "int encode(void);\n",
		"pair/codec.c":             "int encode(void) { return 1; }\n",
		"pair/codec.cpp":           "int encode() { return 1; }\n",
	})
	want := map[string]string{
		"src/inventory.cpp -> include/shop/inventory.h":        "import_path_resolution:cpp",
		"src/util/strings.c -> src/util/strings.h":             "import_path_resolution:c",
		"src/app/main.c -> src/util/strings.h":                 "import_path_resolution:c",
		"tests/inventory_test.cpp -> include/shop/inventory.h": "import_path_resolution:cpp",
		"include/shop/inventory.h -> src/inventory.cpp":        "header_source_pairing:cpp",
		"src/util/strings.h -> src/util/strings.c":             "header_source_pairing:c",
	}
	for edge, method := range want {
		if got[edge] != method {
			t.Errorf("%s: got %q want %q", edge, got[edge], method)
		}
	}
	for edge := range got {
		if _, ok := want[edge]; !ok {
			t.Errorf("unexpected edge %s (%s)", edge, got[edge])
		}
	}
}

func TestCompileCommandsIncludeDirs(t *testing.T) {
	got := indexTree(t, map[string]string{
		"compile_commands.json": `[
  {"directory": "/home/ci/work/shop/build", "file": "/home/ci/work/shop/app/main.cpp", "command": "c++ -I/home/ci/work/shop/third/api -isystem /usr/include/boost -Ilocal -c ../app/main.cpp"},
  {"directory": "/elsewhere", "file": "/elsewhere/unknown.cpp", "arguments": ["c++", "-I", "/elsewhere/include"]}
]`,
		"app/main.cpp":      "#include <api.h>\n#include \"gen.h\"\nint main() {}\n",
		"third/api/api.h":   "int api();\n",
		"build/local/gen.h": "int gen();\n",
		"app/other.cpp":     "#include <api.h>\n",
	})
	if got["app/main.cpp -> third/api/api.h"] != "import_path_resolution:cpp" {
		t.Errorf("compile_commands include dir unused: %v", got)
	}
	if _, ok := got["app/other.cpp -> third/api/api.h"]; ok {
		t.Errorf("include dirs leaked to a file without a compile entry: %v", got)
	}
	for edge := range got {
		if strings.Contains(edge, "build/") {
			t.Errorf("excluded build directory resolved: %s", edge)
		}
	}
}
