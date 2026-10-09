package discovery

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("git: %s %v", b, e)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	write(t, root, "api.py", "from pydantic import BaseModel\nfrom fastapi import FastAPI\napp = FastAPI()\nclass User(BaseModel):\n    id: int\n    email: str\n\n@app.get(\"/users\", response_model=User)\ndef users():\n    return User(id=1, email=\"x\")\n")
	write(t, root, "types.ts", "export interface User { id: number; email: string; }\n")
	write(t, root, "client.ts", "import type { User } from './types';\nconst user: User = await (await fetch('/users')).json();\nconsole.log(user.email);\n")
	run("add", ".")
	run("commit", "-qm", "base")
	return root
}
func write(t *testing.T, root, p, s string) {
	t.Helper()
	if e := os.WriteFile(filepath.Join(root, p), []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
}
func TestTypedFastAPIAndRemovedField(t *testing.T) {
	root := fixture(t)
	ctx := context.Background()
	r, e := Discover(ctx, root, "WORKTREE")
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Candidates) != 1 || r.Candidates[0].Consumer != "client.ts" || len(r.Candidates[0].Locations) < 4 {
		t.Fatalf("%+v", r)
	}
	if len(r.ProposedManifest.Bindings) != 0 {
		t.Fatal("Python models cannot be promoted to JSON manifest")
	}
	p := filepath.Join(root, "api.py")
	b, _ := os.ReadFile(p)
	write(t, root, "api.py", strings.Replace(string(b), "    email: str\n", "", 1))
	c, e := Compare(ctx, root, "HEAD", "WORKTREE")
	if e != nil {
		t.Fatal(e)
	}
	if len(c.Findings) != 1 || c.Findings[0].Consumer != "client.ts" || c.Findings[0].Evidence != "inferred" {
		t.Fatalf("%+v", c)
	}
}
func TestUnrelatedAndAmbiguous(t *testing.T) {
	root := fixture(t)
	write(t, root, "unrelated.json", `{"$schema":"https://json-schema.org/draft/2020-12/schema","title":"Other","properties":{"other":{"type":"string"}}}`)
	r, e := Compare(context.Background(), root, "HEAD", "WORKTREE")
	if e != nil || len(r.Findings) != 0 {
		t.Fatalf("%+v %v", r, e)
	}
	b, _ := os.ReadFile(filepath.Join(root, "api.py"))
	write(t, root, "other.py", string(b))
	r2, e := Discover(context.Background(), root, "WORKTREE")
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range r2.Candidates {
		if c.Consumer != "" && c.Evidence != "unknown" {
			t.Fatalf("ambiguous link promoted: %+v", c)
		}
	}
}
func TestOpenAPIAndJSONSchema(t *testing.T) {
	root := fixture(t)
	write(t, root, "api.py", "# no Python producer\n")
	write(t, root, "openapi.json", `{"openapi":"3.1.0","components":{"schemas":{"User":{"properties":{"id":{"type":"integer"},"email":{"type":"string"}}}}},"paths":{"/users":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/User"}}}}}}}}}`)
	r, e := Discover(context.Background(), root, "WORKTREE")
	if e != nil {
		t.Fatal(e)
	}
	if len(r.ProposedManifest.Bindings) != 1 || len(r.Candidates) != 1 {
		t.Fatalf("%+v", r)
	}
	if r.Status == "passed" {
		t.Fatal("discovery must never imply complete verification")
	}
}
func TestUnresolvedImportDoesNotInventConsumer(t *testing.T) {
	root := fixture(t)
	write(t, root, "client.ts", "import type { User } from './missing';\nconst user: User = await (await fetch('/users')).json();\nconsole.log(user.email);\n")
	r, e := Discover(context.Background(), root, "WORKTREE")
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range r.Candidates {
		if c.Consumer != "" {
			t.Fatalf("invented consumer %+v", c)
		}
	}
}

func TestNoLexicalFalseFieldsOrEndpoints(t *testing.T) {
	root := fixture(t)
	b, _ := os.ReadFile(filepath.Join(root, "api.py"))
	write(t, root, "api.py", strings.Replace(string(b), "    email: str\n", "    email: str\n    def helper(self):\n        bogus: str = 'not a response field'\n", 1))
	r, e := Discover(context.Background(), root, "WORKTREE")
	if e != nil {
		t.Fatal(e)
	}
	for _, s := range r.Schemas {
		if contains(s.Fields, "bogus") {
			t.Fatalf("method local became field: %+v", s)
		}
	}
	write(t, root, "api.py", "from pydantic import BaseModel\nclass User(BaseModel):\n    email: str\n\ntext = '''\n@app.get('/users', response_model=User)\ndef fake(): pass\n'''\n")
	r, e = Discover(context.Background(), root, "WORKTREE")
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Candidates) != 0 {
		t.Fatalf("string became endpoint: %+v", r)
	}
	write(t, root, "api.py", "from pydantic import BaseModel\nclass User(BaseModel):\n    email: str\n\n@cache.get('/users', response_model=User)\ndef fake(): pass\n")
	r, e = Discover(context.Background(), root, "WORKTREE")
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Candidates) != 0 {
		t.Fatalf("arbitrary decorator became endpoint: %+v", r)
	}
}
func TestCommentIsNotFieldUse(t *testing.T) {
	root := fixture(t)
	write(t, root, "client.ts", "import type { User } from './types';\nconst user: User = await (await fetch('/users')).json();\n// user.email\nconsole.log('user.email');\nconsole.log(user.id);\n")
	r, e := Discover(context.Background(), root, "WORKTREE")
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Candidates) != 1 || contains(r.Candidates[0].Fields, "email") {
		t.Fatalf("comment became use: %+v", r)
	}
}
func TestProposedPointerEscaping(t *testing.T) {
	root := fixture(t)
	write(t, root, "api.py", "# removed\n")
	write(t, root, "openapi.json", `{"openapi":"3.1.0","components":{"schemas":{"User/v~1":{"properties":{"email":{"type":"string"}}}}},"paths":{"/users":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/User~1v~01"}}}}}}}}}`)
	r, e := Discover(context.Background(), root, "WORKTREE")
	if e != nil {
		t.Fatal(e)
	}
	if len(r.ProposedManifest.Bindings) != 1 || r.ProposedManifest.Bindings[0].Pointer != "/components/schemas/User~1v~01" {
		t.Fatalf("%+v", r)
	}
}

func TestInitializerAndImportMustBeActualSyntax(t *testing.T) {
	for _, source := range []string{
		"import type { User } from './types';\nconst user: User = \"fetch('/users').json()\";\nconsole.log(user.email);\n",
		"import type { User } from './types';\nconst user: User = unrelated(/* fetch('/users').json() */);\nconsole.log(user.email);\n",
		"import type { User } from './types';\nconst user: User = await other(fetch('/users')).json();\nconsole.log(user.email);\n",
		"import type { Other } from './types'; // import type { User } from './types';\nconst user: User = await (await fetch('/users')).json();\nconsole.log(user.email);\n",
	} {
		t.Run(source, func(t *testing.T) {
			root := fixture(t)
			write(t, root, "client.ts", source)
			r, e := Discover(context.Background(), root, "WORKTREE")
			if e != nil {
				t.Fatal(e)
			}
			for _, c := range r.Candidates {
				if c.Consumer != "" {
					t.Fatalf("lexical text invented consumer: %+v", c)
				}
			}
		})
	}
}
