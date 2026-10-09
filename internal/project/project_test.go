package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBoundary(t *testing.T) {
	root := t.TempDir()
	if _, e := SafePath(root, "../escape"); e == nil {
		t.Fatal("escaped")
	}
	outside := t.TempDir()
	if e := os.Symlink(outside, filepath.Join(root, ".radar")); e != nil {
		t.Skip(e)
	}
	if e := Init(root); e == nil {
		t.Fatal("symlink accepted")
	}
}
func TestInit(t *testing.T) {
	root := t.TempDir()
	if e := Init(root); e != nil {
		t.Fatal(e)
	}
	if e := Init(root); e != nil {
		t.Fatal(e)
	}
	c, e := Read(root)
	if e != nil || !c.NoTelemetry {
		t.Fatal(e)
	}
}
