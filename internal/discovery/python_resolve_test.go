package discovery

import (
	"context"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func TestPythonImportedModelsAndNestedRouters(t *testing.T) {
	sources := map[string]string{
		"backend/models.py": `from pydantic import BaseModel as Model
class Profile(Model):
    email: str
class Base(Model):
    id: int
class User(Base):
    name: str
    profile: Profile
`,
		"backend/routes.py": `from fastapi import APIRouter as Router
from .models import User as Response
router = Router(prefix="/users")
@router.get(
    "/me",
    response_model=Response,
)
def read():
    return {}
`,
		"backend/main.py": `import fastapi as f
from .routes import router as users
root = f.APIRouter(prefix="/v1")
root.include_router(users, prefix="/account")
app = f.FastAPI()
app.include_router(root, prefix="/api")
`,
	}
	schemas, endpoints, ds, err := resolvePython(context.Background(), sources, "abc123")
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %+v", ds)
	}
	if len(endpoints) != 1 {
		t.Fatalf("endpoints: %+v", endpoints)
	}
	ep := endpoints[0]
	if ep.url != "/api/v1/account/users/me" || ep.method != "GET" || ep.schema != model.ContractID("backend/models.py", "/models/User") {
		t.Fatalf("endpoint: %+v", ep)
	}
	if ep.location.Line != 4 || ep.location.Path != "backend/routes.py" || ep.location.Revision != "abc123" || len(ep.relationships) < 4 {
		t.Fatalf("missing provenance: %+v", ep)
	}
	for _, schema := range schemas {
		if schema.Name == "User" {
			if strings.Join(schema.Fields, ",") != "id,name,profile,profile.email" {
				t.Fatalf("fields: %+v", schema)
			}
			return
		}
	}
	t.Fatal("User schema absent")
}

func TestPythonResolutionConservative(t *testing.T) {
	tests := []struct {
		name    string
		sources map[string]string
		want    string
	}{
		{"unregistered router", map[string]string{"app.py": `from fastapi import APIRouter
from pydantic import BaseModel
class User(BaseModel):
    name: str
router=APIRouter()
@router.get("/user", response_model=User)
def read(): pass
`}, "no statically resolved"},
		{"dynamic registration prefix", map[string]string{"app.py": `from fastapi import FastAPI, APIRouter
from pydantic import BaseModel
class User(BaseModel):
    name: str
app=FastAPI()
router=APIRouter()
app.include_router(router, prefix=PREFIX)
@router.get("/user", response_model=User)
def read(): pass
`}, "dynamic include_router"},
		{"ambiguous imports", map[string]string{"app.py": `from fastapi import FastAPI
from a import User
from b import User
app=FastAPI()
@app.get("/user", response_model=User)
def read(): pass
`, "a.py": `from pydantic import BaseModel
class User(BaseModel):
    name: str
`, "b.py": `from pydantic import BaseModel
class User(BaseModel):
    email: str
`}, "multiply defined"},
		{"computed response model", map[string]string{"app.py": `from fastapi import FastAPI
app=FastAPI()
@app.get("/user", response_model=make_model())
def read(): pass
`}, "response_model is unresolved"},
		{"shadowed constructor", map[string]string{"app.py": `from fastapi import FastAPI
from pydantic import BaseModel
FastAPI = OtherApp
class User(BaseModel):
    name: str
app=FastAPI()
@app.get("/user", response_model=User)
def read(): pass
`}, "multiply defined"},
		{"unrelated same name", map[string]string{"app.py": `from fastapi import FastAPI
app=FastAPI()
@app.get("/user", response_model=User)
def read(): pass
`, "other/models.py": `from pydantic import BaseModel
class User(BaseModel):
    name: str
`}, "response_model User is unresolved"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, eps, ds, err := resolvePython(context.Background(), test.sources, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			if len(eps) != 0 {
				t.Fatalf("fabricated endpoints: %+v", eps)
			}
			found := false
			for _, d := range ds {
				if strings.Contains(d.Message, test.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("expected %q in %+v", test.want, ds)
			}
		})
	}
}

func TestPythonDirectAppAndAbsoluteImports(t *testing.T) {
	sources := map[string]string{"backend/models.py": `import pydantic as p
class User(p.BaseModel):
    name: str
`, "backend/main.py": `from fastapi import FastAPI as App
from backend.models import User as Response
app=App()
@app.post("/users", response_model=Response)
def read(): pass
`}
	_, eps, ds, err := resolvePython(context.Background(), sources, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 1 || eps[0].method != "POST" || len(ds) != 0 {
		t.Fatalf("endpoints %+v diagnostics %+v", eps, ds)
	}
}

func TestPythonRecursiveModelsStayBounded(t *testing.T) {
	sources := map[string]string{"models.py": `from pydantic import BaseModel
class User(BaseModel):
    friend: User
`}
	schemas, _, ds, err := resolvePython(context.Background(), sources, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 1 || strings.Join(schemas[0].Fields, ",") != "friend" || len(ds) == 0 {
		t.Fatalf("schemas %+v diagnostics %+v", schemas, ds)
	}
}

func TestPythonLocalLibraryShadowAndFunctionShadow(t *testing.T) {
	for _, sources := range []map[string]string{
		{"pydantic.py": "class BaseModel:\n    pass\n", "app.py": `from pydantic import BaseModel
from fastapi import FastAPI
class User(BaseModel):
    name: str
app=FastAPI()
@app.get("/user", response_model=User)
def read(): pass
`},
		{"app.py": `from pydantic import BaseModel
from fastapi import FastAPI
class User(BaseModel):
    name: str
app=FastAPI()
def app(): pass
@app.get("/user", response_model=User)
def read(): pass
`},
	} {
		_, eps, ds, err := resolvePython(context.Background(), sources, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		if len(eps) != 0 || len(ds) == 0 {
			t.Fatalf("shadowed source should remain unresolved: %+v %+v", eps, ds)
		}
	}
}

func TestPythonModelRuntimeEffectsRemainExplicit(t *testing.T) {
	_, _, ds, err := resolvePython(context.Background(), map[string]string{"app.py": `from pydantic import BaseModel, Field, field_validator
class User(BaseModel):
    name: str = Field(alias="displayName")
    children: list[User]
    @field_validator("name")
    def normalize(cls, value): return value
`}, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"runtime field configuration", "unresolved nested shape", "decorated runtime behavior"} {
		found := false
		for _, d := range ds {
			if strings.Contains(d.Message, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q in %+v", want, ds)
		}
	}
}

func TestPythonConditionalAndWildcardBindingsRemainUnresolved(t *testing.T) {
	for _, shadow := range []string{"if DEBUG:\n    app = OtherApp()\n", "if DEBUG:\n    from custom import FastAPI\n", "from custom import *\n", "app, other = factory()\n", "del app\n"} {
		source := `from fastapi import FastAPI
from pydantic import BaseModel
class User(BaseModel):
    name: str
app=FastAPI()
` + shadow + `@app.get("/user", response_model=User)
def read(): pass
`
		_, eps, ds, err := resolvePython(context.Background(), map[string]string{"app.py": source}, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		if len(eps) != 0 || len(ds) == 0 {
			t.Fatalf("shadow %q: %+v %+v", shadow, eps, ds)
		}
	}
}

func TestPythonRouterRegistrationSnapshotsRespectSourceOrder(t *testing.T) {
	for _, registration := range []string{
		"app.include_router(router)\n@router.get(\"/users\", response_model=User)\ndef users(): pass\n",
		"@router.get(\"/users\", response_model=User)\ndef users(): pass\nparent=APIRouter()\napp.include_router(parent)\nparent.include_router(router)\n",
	} {
		source := `from fastapi import FastAPI, APIRouter
from pydantic import BaseModel
class User(BaseModel):
    name: str
app=FastAPI()
router=APIRouter()
` + registration
		_, eps, ds, err := resolvePython(context.Background(), map[string]string{"app.py": source}, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		if len(eps) != 0 {
			t.Fatalf("route added after snapshot fabricated: %+v", eps)
		}
		found := false
		for _, d := range ds {
			if strings.Contains(d.Message, "after") {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing chronology diagnostic: %+v", ds)
		}
	}
}

func TestPythonPrivateClassVarsAndRuntimeConfiguration(t *testing.T) {
	schemas, eps, ds, err := resolvePython(context.Background(), map[string]string{"app.py": `from fastapi import FastAPI
from pydantic import BaseModel
from typing import ClassVar, ClassVar as CV
class User(BaseModel):
    name: str
    _secret: str
    registry: ClassVar[str]
    alias_registry: CV[str]
    model_config = {"populate_by_name": True}
app=FastAPI()
@app.get("/user", response_model=User, response_model_exclude={"name"}, **opts)
def read(): pass
`}, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 1 || strings.Join(schemas[0].Fields, ",") != "name" || len(eps) != 1 {
		t.Fatalf("syntax fields: %+v endpoints %+v", schemas, eps)
	}
	for _, want := range []string{"private model attribute", "ClassVar model attribute", "runtime model_config", "response_model_exclude", "dynamic argument expansion"} {
		found := false
		for _, d := range ds {
			if strings.Contains(d.Message, want) {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %q in %+v", want, ds)
		}
	}
}

func TestPythonPrefixSplatAfterLiteralRemainsUnresolved(t *testing.T) {
	_, eps, ds, err := resolvePython(context.Background(), map[string]string{"app.py": `from fastapi import FastAPI, APIRouter
from pydantic import BaseModel
class User(BaseModel):
    name: str
app=FastAPI()
router=APIRouter()
@router.get("/user", response_model=User)
def read(): pass
app.include_router(router, prefix="/api", **options)
`}, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 0 || len(ds) == 0 {
		t.Fatalf("dynamic kwargs fabricated registration: %+v %+v", eps, ds)
	}
}
