package buildidentity

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCurrentExecutableIdentityIsAvailable(t *testing.T) {
	name, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	id, err := readID(f)
	if err != nil || id == "" {
		t.Fatalf("running Go test binary has no build ID: %q %v", id, err)
	}
	if Identity() != Identity() || !strings.HasPrefix(Identity(), "go-build:") {
		t.Fatal("unstable build identity")
	}
}
func TestUnversionedDevelopmentBuildChangesIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("builds two small Go fixtures")
	}
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Skip("Go compiler unavailable")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	binary := filepath.Join(dir, "fixture")
	var ids []string
	for _, value := range []string{"first extractor", "changed extractor"} {
		if err := os.WriteFile(src, []byte("package main\nfunc main(){println("+strconv.Quote(value)+")}\n"), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(goPath, "build", "-o", binary, src)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("fixture build: %v %s", err, out)
		}
		f, err := os.Open(binary)
		if err != nil {
			t.Fatal(err)
		}
		id, err := readID(f)
		f.Close()
		if err != nil || id == "" {
			t.Fatalf("missing fixture build ID: %v", err)
		}
		ids = append(ids, id)
	}
	if ids[0] == ids[1] {
		t.Fatal("extractor source changes retained same Go build ID")
	}
}

func TestRawBuildIDAndReadBudget(t *testing.T) {
	data := []byte("MZ binary \xff Go build ID: \"abc/action/content\"\n \xff")
	id, err := readID(bytes.NewReader(data))
	if err != nil || id != "abc/action/content" {
		t.Fatalf("%q %v", id, err)
	}
	for _, bad := range [][]byte{[]byte("unversioned"), []byte("\xff Go build ID: \"unterminated")} {
		if id, _ := readID(bytes.NewReader(bad)); id != "" {
			t.Fatal("accepted malformed build ID")
		}
	}
	reader := &boundedReader{ReaderAt: bytes.NewReader(make([]byte, 100)), remaining: 8}
	if _, err := reader.ReadAt(make([]byte, 9), 0); err == nil {
		t.Fatal("metadata budget ignored")
	}
}
