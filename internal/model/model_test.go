package model

import "testing"

func TestIdentities(t *testing.T) {
	cases := map[string]string{
		FileID("a/b.py"): "a/b.py",
		EntityID("function", "a/b.py", "C.run", 0):  "a/b.py",
		EntityID("function", "a/b.py", "C.run", 2):  "a/b.py",
		SchemaFieldID("api.yaml", "/properties/id"): "api.yaml",
		ModuleID("python", "os"):                    "",
		RepositoryID:                                "",
		"plan:f:task:t":                             "",
	}
	for id, want := range cases {
		if got := PathFromID(id); got != want {
			t.Errorf("PathFromID(%q) = %q, want %q", id, got, want)
		}
	}
	if EntityID("type", "x.go", "S", 1) != "type:x.go#S~1" {
		t.Fatal("occurrence suffix")
	}
}
