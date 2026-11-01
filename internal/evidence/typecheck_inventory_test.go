package evidence

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGoTypecheckFilesDoesNotInventCoverageForAnotherCommand(t *testing.T) {
	for _, argv := range [][]string{
		{"go", "version"},
		{"go", "vet", "-buildtag", "./a"},
		{"go", "vet", "-tags=custom", "./..."},
		{"python3", "unknown-wrapper.py", "go", "vet", "-buildtag", "./..."},
	} {
		if _, err := GoTypecheckFiles(context.Background(), t.TempDir(), ".", argv, time.Second); err == nil {
			t.Fatalf("invented inventory for %v", argv)
		}
	}
}

func TestRuffCheckedFilesObservesItsOwnExclusions(t *testing.T) {
	if _, err := exec.LookPath("ruff"); err != nil {
		t.Skip("ruff unavailable")
	}
	root := t.TempDir()
	for file, data := range map[string]string{
		"ruff.toml":   "exclude = ['excluded.py']\n",
		"selected.py": "value = 1\n",
		"excluded.py": "ignored = 1\n",
	} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := RuffCheckedFiles(context.Background(), root, ".", []string{"ruff", "check", "--output-format", "concise", "--select", "F821", "."}, 10*time.Second)
	if err != nil || !files["selected.py"] || files["excluded.py"] {
		t.Fatalf("files=%v err=%v", files, err)
	}
}
