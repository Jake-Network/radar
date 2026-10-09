package languages

import (
	"context"
	"errors"
	"github.com/Jake-Network/radar/internal/model"
	"testing"
)

func TestLanguages(t *testing.T) {
	cases := []struct {
		path, source, name, kind string
		imports                  int
	}{
		{"a.ts", "import { b } from './b'; export interface Order { total: number }; export function total(o: Order) { return o.total; }", "Order", "interface", 1},
		{"a.tsx", "export function View() { return <div />; }", "View", "function", 0},
		{"a.js", "import {b} from './b.js'; export const run = () => b();", "run", "function", 1},
		{"c.js", "const b = require('./b'); const p = require(path); export const run = async () => (await import('./lazy')).go(b);", "run", "function", 2},
		{"c.ts", "export const load = (name: string) => import(name);", "load", "function", 0},
		{"a.py", "from models import Order\nclass Service:\n    def run(self):\n        return 1\n", "Service", "class", 1},
		{"a.go", "package main\nimport \"fmt\"\ntype Service struct {}\nfunc main() { fmt.Println(1) }\n", "Service", "type", 1},
		{"a.rs", "use std::fmt;\npub struct Service {}\nimpl Service { pub fn run(&self) {} }", "Service", "type", 1},
	}
	for _, tt := range cases {
		t.Run(tt.path, func(t *testing.T) {
			a, ok := ForPath(tt.path)
			if !ok {
				t.Fatal("adapter missing")
			}
			r, err := a.Parse(context.Background(), Source{"repo", "revision", tt.path, []byte(tt.source)})
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Diagnostics) > 0 {
				t.Fatalf("valid syntax rejected: %+v", r.Diagnostics)
			}
			found := false
			imports := 0
			for _, n := range r.Nodes {
				if n.Name == tt.name && n.Kind == tt.kind {
					found = true
					if n.Provenance.Line < 1 || n.Provenance.Evidence != model.VerifiedStatic {
						t.Fatal("missing provenance")
					}
				}
			}
			for _, e := range r.Edges {
				if e.Kind == "IMPORTS" {
					imports++
				}
			}
			if !found || imports != tt.imports {
				t.Fatalf("found=%v imports=%d nodes=%+v", found, imports, r.Nodes)
			}
			if a.Capability().Semantic {
				t.Fatal("unsupported semantic claim")
			}
		})
	}
}
func TestMalformedSyntax(t *testing.T) {
	a, _ := ForPath("x.ts")
	r, err := a.Parse(context.Background(), Source{"repo", "rev", "x.ts", []byte("export function broken( {\n")})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Diagnostics) == 0 {
		t.Fatal("expected diagnostics")
	}
	for _, n := range r.Nodes {
		if n.Name == "broken" {
			t.Fatal("malformed definition advertised as verified")
		}
	}
}
func TestScopedStableIdentities(t *testing.T) {
	a, _ := ForPath("x.py")
	source := Source{"repo", "rev", "x.py", []byte("class A:\n    def run(self):\n        pass\nclass B:\n    def run(self):\n        pass\n")}
	r, err := a.Parse(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, n := range r.Nodes {
		if n.Name == "run" {
			ids[n.ID] = true
		}
	}
	if len(ids) != 2 {
		t.Fatalf("method identities collide: %+v", r.Nodes)
	}
	source.Revision = "new"
	r2, _ := a.Parse(context.Background(), source)
	for _, n := range r2.Nodes {
		if n.Name == "run" && !ids[n.ID] {
			t.Fatal("IDs depend on revision")
		}
	}
}
func TestCancellationAndBounds(t *testing.T) {
	a, _ := ForPath("x.go")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := a.Parse(ctx, Source{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	_, err = a.Parse(context.Background(), Source{Content: make([]byte, 2*1024*1024+1)})
	if err == nil {
		t.Fatal("oversize source accepted")
	}
}

// Repeated parser teardown used to race upstream ParseCtx's cancellation goroutine.
func TestRepeatedParserTeardown(t *testing.T) {
	a, _ := ForPath("x.ts")
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		_, err := a.Parse(ctx, Source{"repo", "rev", "x.ts", []byte("export const run = () => 1;")})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestGoReceiverIdentity(t *testing.T) {
	a, _ := ForPath("x.go")
	base := "package sample\ntype A struct{}\ntype B struct{}\n"
	first, _ := a.Parse(context.Background(), Source{"repo", "rev", "x.go", []byte(base + "func(a A) Run(){}\nfunc(b B) Run(){}\n")})
	second, _ := a.Parse(context.Background(), Source{"repo", "rev", "x.go", []byte(base + "func(z B) Run(){}\nfunc(y A) Run(){}\n")})
	ids := map[string]bool{}
	for _, n := range first.Nodes {
		if n.Name == "Run" {
			ids[n.ID] = true
		}
	}
	if len(ids) != 2 {
		t.Fatal("receiver methods missing")
	}
	for _, n := range second.Nodes {
		if n.Name == "Run" && !ids[n.ID] {
			t.Fatal("receiver identity depends on order or variable name")
		}
	}
	found := false
	for _, n := range first.Nodes {
		if n.Kind == "package" && n.Name == "sample" {
			found = true
		}
	}
	if !found {
		t.Fatal("Go package clause unindexed")
	}
}
