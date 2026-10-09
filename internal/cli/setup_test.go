package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetupCLI(t *testing.T) {
	root := realTempDir(t)
	gitTest(t, root, "init", "-q")
	sub := filepath.Join(root, "nested")
	if e := os.Mkdir(sub, 0700); e != nil {
		t.Fatal(e)
	}
	r := invoke(t, sub, 0, "setup", "--agent", "both", "--dry-run")
	if r["root"] != root || r["dry_run"] != true {
		t.Fatal(r)
	}
	if _, e := os.Stat(filepath.Join(root, ".radar")); !os.IsNotExist(e) {
		t.Fatal("dry run wrote state", e)
	}
	invoke(t, sub, 0, "setup", "--agent", "both")
	r = invoke(t, root, 0, "setup", "--agent", "both")
	for _, change := range r["changes"].([]any) {
		if change.(map[string]any)["action"] != "unchanged" {
			t.Fatal(change)
		}
	}
	invoke(t, root, 2, "setup", "--agent", "bad")
	put(t, root, ".mcp.json", `{"mcpServers":{"radar":{"command":"custom"}}}`)
	invoke(t, root, 2, "setup", "--agent", "claude")
}
