package indexer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func parallelFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for i := 0; i < 64; i++ {
		content := fmt.Sprintf("from module_%02d import run\ndef run(): pass\n", (i+1)%64)
		if i == 32 {
			content = "def broken(:\n"
		}
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("module_%02d.py", i)), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
func TestParallelParsingPreservesOrderedSnapshot(t *testing.T) {
	root := parallelFixture(t)
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	serial, err := Index(context.Background(), root, "revision")
	if err != nil {
		t.Fatal(err)
	}
	runtime.GOMAXPROCS(4)
	parallel, err := Index(context.Background(), root, "revision")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(serial, parallel) {
		t.Fatal("parallel parsing changed snapshot")
	}
}

type cancelReadProvider struct {
	Provider
	cancel context.CancelFunc
}

func (p cancelReadProvider) Read(ctx context.Context, e Entry, limit int64) ([]byte, error) {
	content, err := p.Provider.Read(ctx, e, limit)
	p.cancel()
	return content, err
}
func TestParallelParsingCancelsAndJoinsWorkers(t *testing.T) {
	root := parallelFixture(t)
	provider, err := NewWorktree(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := Build(ctx, root, "revision", cancelReadProvider{provider, cancel}); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled build did not join parser workers")
	}
}
