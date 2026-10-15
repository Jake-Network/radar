package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorSharedResources(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	secret := "postgres://user:very-secret@localhost/db"
	for _, root := range []string{a, b} {
		if err := os.WriteFile(filepath.Join(root, ".env"), []byte("DATABASE_URL="+secret+"\nPORT=8080\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	result := inspectResources([]string{a, b})
	if len(result) != 2 {
		t.Fatalf("want two warnings: %+v", result)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "very-secret") || strings.Contains(string(data), "8080") {
		t.Fatal("resource values leaked")
	}
	os.WriteFile(filepath.Join(b, ".env"), []byte("DATABASE_URL=postgres://other/db\nPORT=8081\n"), 0600)
	if got := inspectResources([]string{a, b}); len(got) != 0 {
		t.Fatalf("distinct resources: %+v", got)
	}
}
func TestDoctorResourcesUnknownAndSymlink(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	os.WriteFile(filepath.Join(a, ".env"), []byte("DATABASE_URL=${DATABASE_URL}\n"), 0600)
	if err := os.Symlink(filepath.Join(a, ".env"), filepath.Join(b, ".env")); err != nil {
		t.Skip(err)
	}
	got := inspectResources([]string{a, b})
	if len(got) != 1 || got[0].Status != "unknown" || len(got[0].Worktrees) != 1 {
		t.Fatalf("%+v", got)
	}
}
func TestDoctorComposeResources(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, root := range []string{a, b} {
		os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("name: local\nservices:\n  db:\n    ports:\n      - \"5432:5432\"\n"), 0600)
	}
	if got := inspectResources([]string{a, b}); len(got) != 2 {
		t.Fatalf("%+v", got)
	}
}
func TestDoctorEditableStatic(t *testing.T) {
	root, env := t.TempDir(), t.TempDir()
	site := filepath.Join(env, "lib", "python3.12", "site-packages")
	os.MkdirAll(site, 0700)
	file := filepath.Join(site, "editable.pth")
	os.WriteFile(file, []byte(root+"\n"), 0600)
	if got := editableAdvisories(root, env); len(got) != 0 {
		t.Fatalf("candidate-local: %+v", got)
	}
	os.WriteFile(file, []byte(t.TempDir()+"\n"), 0600)
	if got := editableAdvisories(root, env); len(got) != 1 || got[0].Kind != "external_editable" {
		t.Fatalf("%+v", got)
	}
	os.WriteFile(file, []byte("import arbitrary_side_effect\n"), 0600)
	if got := editableAdvisories(root, env); len(got) != 1 || got[0].Status != "unknown" {
		t.Fatalf("%+v", got)
	}
}

func TestDoctorResourcesRefuseLargeAndLinkedFiles(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, ".env"), []byte(strings.Repeat("X", (1<<20)+1)), 0600)
	if b, ok := resourceFile(root, ".env"); ok || b != nil {
		t.Fatal("oversize resource configuration read")
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("sensitive"), 0600)
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skip(err)
	}
	if _, ok := resourceFile(root, filepath.Join("linked", "secret")); ok {
		t.Fatal("followed symlink parent")
	}
	if _, ok := resourceFile(root, "../secret"); ok {
		t.Fatal("allowed parent traversal")
	}
}
