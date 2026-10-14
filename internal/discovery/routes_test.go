package discovery

import (
	"context"
	"testing"
)

func TestRoutesWithoutResponseModel(t *testing.T) {
	sources := map[string]string{"app.py": "from fastapi import FastAPI\napp=FastAPI()\n@app.get(\"/x\")\ndef a(): pass\n@app.get(\"/x\")\ndef b(): pass\n"}
	r, _, e := Routes(context.Background(), sources, "tree")
	if e != nil || len(r) != 2 {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestRouteFrameworkScopes(t *testing.T) {
	sources := map[string]string{"a.js": "const express = require('express');\nconst app = express();\napp.get('/x', a);\napp.get('/x', b);\n", "b.js": "const express = require('express');\nconst app = express();\napp.get('/x', c);\n", "routes.go": "package x\nimport \"github.com/gin-gonic/gin\"\nfunc routes() {\nr := gin.Default()\nr.GET(\"/x\", a)\nr.GET(\"/x\", b)\n}"}
	rs, _, e := Routes(context.Background(), sources, "tree")
	if e != nil || len(rs) != 5 {
		t.Fatalf("%+v %v", rs, e)
	}
	if rs[0].Scope == rs[2].Scope {
		t.Fatalf("independent JS applications combined: %+v", rs)
	}
}
func TestDynamicPrefixUnknown(t *testing.T) {
	sources := map[string]string{"app.py": "from fastapi import FastAPI, APIRouter\napp=FastAPI()\nrouter=APIRouter(prefix=prefix)\n@router.get(\"/x\")\ndef a(): pass\napp.include_router(router)\n"}
	rs, ds, e := Routes(context.Background(), sources, "tree")
	if e != nil || len(rs) != 0 || len(ds) == 0 {
		t.Fatalf("routes=%+v diagnostics=%+v error=%v", rs, ds, e)
	}
}
func TestAxumLiteralRoutes(t *testing.T) {
	source := "use axum::{Router, routing::get};\nfn main(){\nlet app = Router::new()\n.route(\"/x\", get(first))\n.route(\"/x\", get(second));\n}"
	rs, ds, e := Routes(context.Background(), map[string]string{"app.rs": source}, "tree")
	if e != nil || len(rs) != 2 || len(ds) != 0 {
		t.Fatalf("%+v %+v %v", rs, ds, e)
	}
}
func TestCommentsAndDynamicLiteralFrameworkRoutes(t *testing.T) {
	source := "const express=require('express');\nconst app=express();\n/*\napp.get('/x',a);\n*/\napp.get(path,a);\n"
	rs, ds, e := Routes(context.Background(), map[string]string{"app.js": source}, "tree")
	if e != nil || len(rs) != 0 || len(ds) == 0 {
		t.Fatalf("%+v %+v %v", rs, ds, e)
	}
}
func TestRouteLikeTextInsideStringsDoesNotRegister(t *testing.T) {
	sources := map[string]string{
		"app.js":   "const express=require('express');\nconst app=express();\napp.get('/items',a);\nconst sample = `\napp.get('/items',b);\n`;\n",
		"app.go":   "package app\nimport \"github.com/gin-gonic/gin\"\nfunc main(){\nr:=gin.Default()\nr.GET(\"/items\",a)\nsample := `\nr.GET(\"/items\",b)\n`\n_ = sample\n}",
		"app.java": "import org.springframework.web.bind.annotation.*;\n@RestController\nclass App {\n@GetMapping(\"/items\")\nvoid a() {}\nString sample=\"\"\"\n@GetMapping(\"/items\")\nvoid b() {}\n\"\"\";\n}",
		"app.rs":   "use axum::{Router,routing::get};\nfn main(){\nlet app = Router::new().route(\"/items\",get(a));\nlet sample = \"let other = Router::new().route(\\\"/items\\\",get(b));\";\n}",
	}
	rs, _, e := Routes(context.Background(), sources, "tree")
	if e != nil || len(rs) != 4 {
		t.Fatalf("%+v %v", rs, e)
	}
}
func TestIndependentLexicalRouterScopes(t *testing.T) {
	source := "const express=require('express');\nfunction a(){\nconst app=express();\napp.get('/items',a);\n}\nfunction b(){\nconst app=express();\napp.get('/items',b);\n}"
	rs, _, e := Routes(context.Background(), map[string]string{"app.js": source}, "tree")
	if e != nil || len(rs) != 2 || rs[0].Scope == rs[1].Scope {
		t.Fatalf("%+v %v", rs, e)
	}
}
func TestUnboundFactoryNameDoesNotProveRoutes(t *testing.T) {
	source := "const lib=require('express');\nfunction express(){ return custom; }\nconst app=express();\napp.get('/items',a);\napp.get('/items',b);\n"
	rs, ds, e := Routes(context.Background(), map[string]string{"app.js": source}, "tree")
	if e != nil || len(rs) != 0 || len(ds) == 0 {
		t.Fatalf("%+v %+v %v", rs, ds, e)
	}
}
func TestSpringLiteralRequestMapping(t *testing.T) {
	source := "import org.springframework.web.bind.annotation.*;\n@RestController\n@RequestMapping(\"/api\")\nclass Routes {\n@RequestMapping(path=\"/items\",method=RequestMethod.GET)\nvoid a(){}\n@GetMapping(\"/items\")\nvoid b(){}\n}"
	rs, ds, e := Routes(context.Background(), map[string]string{"Routes.java": source, "pom.xml": "<project/>"}, "tree")
	if e != nil || len(rs) != 2 || len(ds) != 0 || rs[0].Path != "/api/items" {
		t.Fatalf("%+v %+v %v", rs, ds, e)
	}
}

func TestIndependentAxumRouterConstructions(t *testing.T) {
	source := "use axum::{Router,routing::get};\nfn public_app(){\nlet app = Router::new().route(\"/health\",get(public_health));\n}\nfn admin_app(){\nlet app = Router::new().route(\"/health\",get(admin_health));\n}"
	rs, ds, e := Routes(context.Background(), map[string]string{"app.rs": source}, "tree")
	if e != nil || len(rs) != 2 || len(ds) != 0 || rs[0].Scope == rs[1].Scope {
		t.Fatalf("independent routers: %+v %+v %v", rs, ds, e)
	}
}
