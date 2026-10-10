package languages

import (
	"context"
	"errors"
	"github.com/Jake-Network/radar/internal/model"
	"strings"
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

func TestJavaDeclarations(t *testing.T) {
	a, ok := ForPath("src/main/java/com/shop/Inventory.java")
	if !ok || a.Capability().Language != "java" {
		t.Fatal("java adapter missing")
	}
	source := `package com.shop;

import java.util.List;
import com.shop.model.Item;
import com.shop.util.*;
import static com.shop.util.Strings.trim;
import static org.junit.jupiter.api.Assertions.*;

public class Inventory {
    static final int LIMIT = 3;
    private final List<Item> items;
    public Inventory(List<Item> items) { this.items = items; }
    public Inventory() { this(List.of()); }
    public int reserve(String sku) { int local = 1; return local; }
    public int reserve(Item item) { return 2; }
    record Hold(String sku, int count) {
        Hold { if (count < 0) throw new IllegalArgumentException(); }
    }
    enum State { OPEN, CLOSED }
    interface Listener { void changed(Item item); }
    @interface Audited { String value(); }
    static class Batch { void flush() {} }
}
`
	r, err := a.Parse(context.Background(), Source{"repo", "rev", "src/main/java/com/shop/Inventory.java", []byte(source)})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Diagnostics) > 0 {
		t.Fatalf("valid Java rejected: %+v", r.Diagnostics)
	}
	if r.Package != "com.shop" {
		t.Fatalf("package %q", r.Package)
	}
	got := map[string]string{}
	for _, n := range r.Nodes {
		if n.Kind != "file" && n.Kind != "module" {
			got[n.Properties["qualified_name"]+"/"+n.ID[strings.LastIndex(n.ID, "#")+1:]] = n.Kind
		}
	}
	want := map[string]string{
		"com.shop/com.shop":                                     "package",
		"Inventory/Inventory":                                   "class",
		"Inventory.LIMIT/Inventory.LIMIT":                       "symbol",
		"Inventory.items/Inventory.items":                       "symbol",
		"Inventory.Inventory/Inventory.Inventory":               "function",
		"Inventory.Inventory/Inventory.Inventory~1":             "function",
		"Inventory.reserve/Inventory.reserve":                   "function",
		"Inventory.reserve/Inventory.reserve~1":                 "function",
		"Inventory.Hold/Inventory.Hold":                         "type",
		"Inventory.Hold.Hold/Inventory.Hold.Hold":               "function",
		"Inventory.State/Inventory.State":                       "type",
		"Inventory.Listener/Inventory.Listener":                 "interface",
		"Inventory.Listener.changed/Inventory.Listener.changed": "function",
		"Inventory.Audited/Inventory.Audited":                   "interface",
		"Inventory.Batch/Inventory.Batch":                       "class",
		"Inventory.Batch.flush/Inventory.Batch.flush":           "function",
	}
	for k, kind := range want {
		if got[k] != kind {
			t.Errorf("%s: got %q want %q", k, got[k], kind)
		}
	}
	for k := range got {
		if strings.Contains(k, "local") {
			t.Errorf("method local indexed as entity: %s", k)
		}
	}
	var modules []string
	for _, imp := range r.Imports {
		modules = append(modules, imp.Module)
	}
	if strings.Join(modules, " ") != "java.util.List com.shop.model.Item com.shop.util.* com.shop.util.Strings.trim org.junit.jupiter.api.Assertions.*" {
		t.Fatalf("imports %v", modules)
	}
}

func TestJavaPackageInfoAndMalformed(t *testing.T) {
	a, _ := ForPath("x/package-info.java")
	r, err := a.Parse(context.Background(), Source{"repo", "rev", "x/package-info.java", []byte("@Deprecated\npackage com.shop.legacy;\n")})
	if err != nil || r.Package != "com.shop.legacy" {
		t.Fatalf("annotated package %q %v", r.Package, err)
	}
	r, err = a.Parse(context.Background(), Source{"repo", "rev", "x/Broken.java", []byte("package a;\nclass Broken { void run( { }\n")})
	if err != nil || len(r.Diagnostics) == 0 {
		t.Fatalf("malformed Java accepted: %+v %v", r.Diagnostics, err)
	}
	for _, n := range r.Nodes {
		if n.Name == "run" {
			t.Fatal("malformed method advertised")
		}
	}
}
