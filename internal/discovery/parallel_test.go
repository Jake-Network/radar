package discovery

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCompareParallelTreesDeterministicAndCanceled(t *testing.T) {
	root := fixture(t)
	content, err := os.ReadFile(filepath.Join(root, "api.py"))
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "api.py", strings.Replace(string(content), "    email: str\n", "", 1))
	first, err := Compare(context.Background(), root, "HEAD", "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Findings) != 1 {
		t.Fatal("missing removed-field finding")
	}
	for i := 0; i < 3; i++ {
		again, err := Compare(context.Background(), root, "HEAD", "WORKTREE")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, again) {
			t.Fatal("parallel scheduling changed comparison")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { _, err := Compare(ctx, root, "HEAD", "WORKTREE"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled comparison succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled comparison did not join readers")
	}
}
