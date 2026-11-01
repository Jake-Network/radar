package integration

import (
	"context"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
)

func TestProtobufUniqueTagsFailOnlyTheCombinedCandidate(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q", "-b", "main")
	source := "syntax = \"proto3\";\nmessage M {\nstring first = 1;\nstring b = 2;\nstring c = 3;\nstring d = 4;\nstring e = 5;\nstring f = 6;\nstring g = 7;\nstring last = 8;\n}\n"
	put(t, root, "schema.proto", source)
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	for name, replacement := range map[string][2]string{"first": {"first = 1", "first = 9"}, "last": {"last = 8", "last = 9"}} {
		gitTest(t, root, "checkout", "-qb", name, "main")
		put(t, root, "schema.proto", strings.Replace(source, replacement[0], replacement[1], 1))
		gitTest(t, root, "commit", "-qam", name)
	}
	gitTest(t, root, "checkout", "-q", "main")
	before := gitTest(t, root, "status", "--porcelain")
	for _, refs := range [][]string{{"first"}, {"last"}, {"first", "last"}} {
		r, err := Preview(context.Background(), root, Options{Base: "main", Branches: refs})
		if err != nil {
			t.Fatal(err)
		}
		if len(refs) == 1 {
			if r.Gate.Verdict != gate.Pass {
				t.Fatalf("standalone %+v", r)
			}
		} else {
			if r.Gate.Verdict != gate.Fail {
				t.Fatalf("combined %+v", r)
			}
			found := false
			for _, f := range r.Findings {
				if f.Code == "unique_protobuf_field_number" && f.Evidence == model.VerifiedStatic && len(f.Locations) == 2 {
					found = true
				}
			}
			if !found {
				t.Fatalf("findings %+v", r.Findings)
			}
		}
	}
	if gitTest(t, root, "status", "--porcelain") != before {
		t.Fatal("analysis changed worktree")
	}
}
func TestAdvisoryDuplicateTableDoesNotCreatePassingUniqueCheck(t *testing.T) {
	root := t.TempDir()
	gitTest(t, root, "init", "-q", "-b", "main")
	put(t, root, "a.py", "from sqlalchemy.orm import DeclarativeBase\nclass A:\n __tablename__ = 'users'\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	gitTest(t, root, "checkout", "-qb", "second")
	put(t, root, "b.py", "from sqlalchemy.orm import DeclarativeBase\nclass B:\n __tablename__ = 'users'\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "second")
	r, err := Preview(context.Background(), root, Options{Base: "main", Branches: []string{"second"}})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range r.Findings {
		if f.Code == "unique_name_candidate" && f.Evidence == model.Inferred && f.Severity == model.SeverityWarning {
			found = true
		}
	}
	if !found {
		t.Fatalf("advisory missing %+v", r.Findings)
	}
	for _, c := range r.Checks {
		if c.ID == "unique_keys" {
			t.Fatalf("advisory became a check %+v", c)
		}
	}
}
