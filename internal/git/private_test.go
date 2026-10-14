package git

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrivateCommitDoesNotStartAutomaticMaintenance(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"config", "maintenance.auto", "true"},
		{"config", "maintenance.autoDetach", "true"},
		{"config", "gc.auto", "1"},
		{"config", "gc.autoDetach", "true"},
	} {
		if _, err := RunPrivate(ctx, root, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "input.txt"), []byte("candidate input\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunPrivate(ctx, root, "add", "input.txt"); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	cmd := PrivateCommand(ctx, root, "commit", "--quiet", "-m", "private candidate")
	cmd.Env = append(cmd.Env, "GIT_TRACE2_EVENT="+trace)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %s: %v", out, err)
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event struct {
			Event string   `json:"event"`
			Argv  []string `json:"argv"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Event == "child_start" {
			for _, arg := range event.Argv {
				if arg == "maintenance" || arg == "gc" {
					t.Fatalf("private commit started an unowned maintenance process: %v", event.Argv)
				}
			}
		}
	}
}
