package testselection

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/model"
)

func TestOversizedInputsBlockOnlyPossibleTestsAndManifests(t *testing.T) {
	for _, tt := range []struct {
		file  string
		block bool
	}{
		{"static/vendor.js", false},
		{"source.py", false},
		{"foo.test.js", true},
		{"test_big.py", true},
		{"big_test.go", true},
		{"__tests__/big.ts", true},
		{"pyproject.toml", true},
		{"src/lib.rs", true}, // Inline Rust tests have no path convention.
		{"src/test/java/Big.java", true},
		{"tests/big.cpp", true},
	} {
		t.Run(tt.file, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, filepath.FromSlash(tt.file))
			if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, make([]byte, MaxFileBytes+1), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "test_ok.py"), []byte("def test_ok(): pass\n"), 0600); err != nil {
				t.Fatal(err)
			}
			inv, err := Discover(context.Background(), root, "WORKTREE")
			if err != nil {
				t.Fatal(err)
			}
			if len(inv.Diagnostics) != 1 || inv.Diagnostics[0].Severity != model.SeverityWarning {
				t.Fatalf("missing warning: %+v", inv)
			}
			proposal, err := Select(inv, model.Snapshot{}, []string{"test_ok.py"}, Options{ToolAvailable: func(string) bool { return true }})
			if err != nil {
				t.Fatal(err)
			}
			selection, err := Plan(proposal, []string{"test_ok.py"}, ModeTargeted, 16)
			if err != nil {
				t.Fatal(err)
			}
			blocked := strings.Contains(strings.Join(selection.Blocking, "\n"), "inventory incomplete:")
			if blocked != tt.block {
				t.Fatalf("inventory blocked=%v want %v: %+v", blocked, tt.block, selection)
			}
		})
	}
}
