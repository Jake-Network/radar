package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTreeCachePinsCommitsWithoutFreezingMutableRefs(t *testing.T) {
	root, empty := objectCopyFixture(t)
	ctx := WithTreeCache(context.Background())
	head, err := Resolve(ctx, root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	initial, err := Entries(ctx, root, head)
	if err != nil || len(initial) != 1 {
		t.Fatal(initial, err)
	}
	initial[0].Path = "caller mutation"
	again, err := Entries(ctx, root, head)
	if err != nil || len(again) != 1 || again[0].Path != "a.txt" {
		t.Fatal("cached inventory aliased caller", again, err)
	}
	if _, err := Entries(ctx, empty, head); err == nil {
		t.Fatal("cached commit from another root hid missing local objects")
	}
	if _, err := Entries(ctx, root, "HEAD"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), []byte("new commit"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "b.txt"}, {"commit", "-qm", "move HEAD"}} {
		if _, err := RunPrivate(ctx, root, args...); err != nil {
			t.Fatal(err)
		}
	}
	moved, err := Entries(ctx, root, "HEAD")
	if err != nil || len(moved) != 2 {
		t.Fatal("mutable HEAD was cached", moved, err)
	}
	pinned, err := Entries(ctx, root, head)
	if err != nil || len(pinned) != 1 || pinned[0].Path != "a.txt" {
		t.Fatal("immutable input moved", pinned, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := Entries(canceled, root, head); err == nil {
		t.Fatal("cached inventory bypassed cancellation")
	}
}
