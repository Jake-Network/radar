package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func objectCopyFixture(t *testing.T) (string, string) {
	t.Helper()
	// Match private candidate preparation: resolve trusted temporary-directory
	// aliases before testing the object store's stricter canonical-path guard.
	source, dest := realTempDir(t), realTempDir(t)
	ctx := context.Background()
	for _, root := range []string{source, dest} {
		if _, err := RunPrivate(ctx, root, "init", "--quiet"); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "a.txt"), []byte("independent object bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "a.txt"}, {"commit", "-qm", "fixture"}} {
		if _, err := RunPrivate(ctx, source, args...); err != nil {
			t.Fatal(err)
		}
	}
	return source, dest
}
func TestCopyObjectStoreIndependentLooseAndPacked(t *testing.T) {
	for _, packed := range []bool{false, true} {
		t.Run(map[bool]string{false: "loose", true: "packed"}[packed], func(t *testing.T) {
			source, dest := objectCopyFixture(t)
			ctx := context.Background()
			if packed {
				if _, err := RunPrivate(ctx, source, "-c", "pack.writeReverseIndex=true", "repack", "-ad"); err != nil {
					t.Fatal(err)
				}
			}
			sha, err := Resolve(ctx, source, "HEAD")
			if err != nil {
				t.Fatal(err)
			}
			before := Identity(ctx, source)
			copied, err := CopyObjectStore(ctx, source, dest)
			if err != nil || !copied {
				t.Fatalf("copy %v %v", copied, err)
			}
			if _, err := RunPrivate(ctx, dest, "checkout", "--quiet", "--detach", sha); err != nil {
				t.Fatal(err)
			}
			if Identity(ctx, dest) != before || Identity(ctx, source) != before {
				t.Fatal("identity changed")
			}
			if _, err := os.Lstat(filepath.Join(dest, ".git", "objects", "info", "alternates")); !os.IsNotExist(err) {
				t.Fatal("created alternates")
			}
			filepath.WalkDir(filepath.Join(source, ".git", "objects"), func(p string, d os.DirEntry, e error) error {
				if e != nil {
					t.Fatal(e)
				}
				if d.IsDir() {
					return nil
				}
				rel, _ := filepath.Rel(filepath.Join(source, ".git", "objects"), p)
				target := filepath.Join(dest, ".git", "objects", rel)
				srcInfo, _ := os.Stat(p)
				dstInfo, err := os.Stat(target)
				if err == nil && os.SameFile(srcInfo, dstInfo) {
					t.Fatal("source object aliased", rel)
				}
				return nil
			})
			if _, err := RunPrivate(ctx, dest, "fsck", "--full", "--no-reflogs"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestCopyObjectStoreRejectsUnsafeStoresAndOccupiedDestination(t *testing.T) {
	cases := []string{"alternates", "symlink", "missing-index", "occupied"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			source, dest := objectCopyFixture(t)
			objects := filepath.Join(source, ".git", "objects")
			switch name {
			case "alternates":
				os.WriteFile(filepath.Join(objects, "info", "alternates"), []byte("/outside\n"), 0600)
			case "symlink":
				outside := t.TempDir()
				if err := os.Symlink(outside, filepath.Join(objects, "ab")); err != nil {
					t.Skip(err)
				}
			case "missing-index":
				if _, err := RunPrivate(context.Background(), source, "repack", "-ad"); err != nil {
					t.Fatal(err)
				}
				paths, _ := filepath.Glob(filepath.Join(objects, "pack", "*.idx"))
				if len(paths) == 0 {
					t.Fatal("no index")
				}
				os.Remove(paths[0])
			case "occupied":
				os.WriteFile(filepath.Join(dest, ".git", "objects", "keep-me"), []byte("untouched"), 0600)
			}
			copied, err := CopyObjectStore(context.Background(), source, dest)
			if copied {
				t.Fatal("unsafe copy succeeded")
			}
			if name == "occupied" {
				if err == nil {
					t.Fatal("occupied destination accepted")
				}
				b, _ := os.ReadFile(filepath.Join(dest, ".git", "objects", "keep-me"))
				if string(b) != "untouched" {
					t.Fatal("destination changed")
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCopyObjectStoreRejectsSymlinkDestination(t *testing.T) {
	source, dest := objectCopyFixture(t)
	objects := filepath.Join(dest, ".git", "objects")
	outside := filepath.Join(t.TempDir(), "objects")
	if err := os.Rename(objects, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, objects); err != nil {
		t.Skip(err)
	}
	if copied, err := CopyObjectStore(context.Background(), source, dest); copied || err == nil {
		t.Fatalf("symlink destination accepted: copied=%v err=%v", copied, err)
	}
}
