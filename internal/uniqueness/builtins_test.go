package uniqueness

import (
	"github.com/Jake-Network/radar/internal/model"
	"testing"
)

func TestProtobufFieldCollisionUsesMessageScopeAndBase(t *testing.T) {
	good := map[string]string{"x.proto": "syntax = \"proto3\";\nmessage M {string a = 1;}\nmessage N {string b = 1;}"}
	bad := map[string]string{"x.proto": "syntax = \"proto3\";\nmessage M {\nstring a = 1;\nstring b = 1;\n}\nmessage N {string c = 1;}"}
	r := evaluateFixture(t, good, bad, []string{"x.proto"})
	if !r.Applicable || r.Status != model.StatusFailed || len(r.Findings) != 1 || len(r.Findings[0].Locations) != 2 || r.Findings[0].Locations[0].Line != 3 || r.Findings[0].Locations[1].Line != 4 {
		t.Fatalf("%+v", r)
	}
	r = evaluateFixture(t, bad, bad, []string{"x.proto"})
	if r.Status == model.StatusFailed || r.Findings[0].Code != "unique_protobuf_field_number_on_base" {
		t.Fatalf("%+v", r)
	}
	r = evaluateFixture(t, good, good, []string{"x.proto"})
	if r.Status != model.StatusPassed || len(r.Findings) != 0 {
		t.Fatalf("%+v", r)
	}
	r = evaluateFixture(t, good, map[string]string{"x.proto": `message M {map<string,string> values = 1;}`}, []string{"x.proto"})
	if r.Status != model.StatusUnknown {
		t.Fatalf("%+v", r)
	}
}
func TestTypedCandidatesAreAdvisoryAndIgnoreStrings(t *testing.T) {
	base := map[string]string{"a.py": "from sqlalchemy.orm import DeclarativeBase\nclass A:\n __tablename__ = 'users'\n"}
	candidate := map[string]string{"a.py": base["a.py"], "b.py": "from sqlalchemy.orm import DeclarativeBase\nclass B:\n __tablename__ = 'users'\n"}
	r := evaluateFixture(t, base, candidate, []string{"b.py"})
	if r.Applicable || len(r.Findings) != 1 || r.Findings[0].Evidence != model.Inferred || r.Findings[0].Severity != model.SeverityWarning {
		t.Fatalf("%+v", r)
	}
	candidate["b.py"] = "from sqlalchemy.orm import DeclarativeBase\ntext = '''\nclass B:\n __tablename__ = 'users'\n'''\n"
	r = evaluateFixture(t, base, candidate, []string{"b.py"})
	if r.Applicable || len(r.Findings) != 0 {
		t.Fatalf("%+v", r)
	}
}
func TestTaskAndURLCandidatesDoNotClaimRuntimeCollision(t *testing.T) {
	src := "from celery import Celery\nfrom django.urls import path\napp = Celery('one')\n@app.task(name='send')\ndef first(): pass\n@app.task(name='send')\ndef second(): pass\nurlpatterns = [path('a/', first, name='detail'), path('b/', second, name='detail')]\n"
	r := evaluateFixture(t, nil, map[string]string{"app.py": src}, []string{"app.py"})
	if r.Applicable || len(r.Findings) != 2 {
		t.Fatalf("%+v", r)
	}
	for _, f := range r.Findings {
		if f.Evidence != model.Inferred || f.Severity != model.SeverityWarning {
			t.Fatal(f)
		}
	}
}
func TestPrismaMapCandidateIsLiteralAndScopedToModels(t *testing.T) {
	src := `model A {id Int @id @@map("users")} model B {id Int @id @@map("users")}`
	r := evaluateFixture(t, nil, map[string]string{"schema.prisma": src}, []string{"schema.prisma"})
	if r.Applicable || len(r.Findings) != 1 {
		t.Fatalf("%+v", r)
	}
	r = evaluateFixture(t, nil, map[string]string{"schema.prisma": `// model A { @@map("users") }
model B {id Int @id @@map("users")}`}, []string{"schema.prisma"})
	if len(r.Findings) != 0 {
		t.Fatalf("%+v", r)
	}
}
func TestTableCandidateFrameworkSyntaxVariants(t *testing.T) {
	cases := map[string]string{
		"entity.ts":   "import {Entity} from 'typeorm';\n@Entity('users')\nexport class A {}\n@Entity({name: 'users'})\nexport class B {}\n",
		"Entity.java": "import jakarta.persistence.Table;\n@Table(name=\"users\") class A {}\n@Table(name=\"users\") class B {}\n",
		"models.py":   "from django.db import models\nclass A(models.Model):\n class Meta:\n  db_table = 'users'\nclass B(models.Model):\n class Meta:\n  db_table = 'users'\n",
	}
	for file, src := range cases {
		t.Run(file, func(t *testing.T) {
			r := evaluateFixture(t, nil, map[string]string{file: src}, []string{file})
			if r.Applicable || len(r.Findings) != 1 || r.Findings[0].Evidence != model.Inferred {
				t.Fatalf("%+v", r)
			}
		})
	}
}
func TestUnrelatedTaskCallsAndGlobalTableVariablesAreNotCandidates(t *testing.T) {
	src := "from celery import Celery\nfrom sqlalchemy.orm import DeclarativeBase\n__tablename__ = 'users'\nobj.task(name='x')\nobj.task(name='x')\nother = '__tablename__ = users'\n"
	r := evaluateFixture(t, nil, map[string]string{"a.py": src}, []string{"a.py"})
	if len(r.Findings) != 0 || r.Applicable {
		t.Fatalf("%+v", r)
	}
}
