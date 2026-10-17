package git

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBlobStreamDrainsPartialCallbacks(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}} {
		if out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "blob"), []byte(strings.Repeat("x", 128<<10)), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command("git", "-C", root, "hash-object", "-w", "blob").Output()
	if err != nil {
		t.Fatal(err)
	}
	oid := strings.TrimSpace(string(output))
	reader, err := OpenBlobReader(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	sentinel := errors.New("callback failed")
	err = reader.Stream(oid, func(size int64, source io.Reader) error {
		if size != 128<<10 {
			t.Fatal(size)
		}
		one := make([]byte, 1)
		if _, err := io.ReadFull(source, one); err != nil {
			t.Fatal(err)
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	if _, err := reader.Read(oid, 1); !errors.Is(err, ErrBlobTooLarge) {
		t.Fatal(err)
	}
	contents, err := reader.Read(oid, 128<<10)
	if err != nil || string(contents) != strings.Repeat("x", 128<<10) {
		t.Fatalf("stream framing lost: %d %v", len(contents), err)
	}
}
