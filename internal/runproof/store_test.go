package runproof

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestImmutableRepositoryBoundProof(t *testing.T) {
	state := t.TempDir()
	r, err := Save(state, "git:repo", map[string]any{"candidate_tree": "tree", "gate": map[string]string{"verdict": "pass"}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(state, "git:repo", r.ID)
	if err != nil || string(loaded.Report) != string(r.Report) {
		t.Fatal(loaded, err)
	}
	if _, err = Load(state, "git:other", r.ID); err == nil {
		t.Fatal("different repository accepted")
	}
	if _, err = Load(state, "git:repo", "../escape"); err == nil {
		t.Fatal("unsafe ID accepted")
	}
	path, _ := recordPath(state, "git:repo", r.ID)
	var changed Record
	b, _ := os.ReadFile(path)
	json.Unmarshal(b, &changed)
	changed.Report = json.RawMessage(`{"gate":{"verdict":"fail"}}`)
	b, _ = json.Marshal(changed)
	os.WriteFile(path, b, 0600)
	if _, err = Load(state, "git:repo", r.ID); err == nil {
		t.Fatal("tampered proof accepted")
	}
	if _, err = Save(state, "git:repo", map[string]any{"candidate_tree": "tree", "gate": map[string]string{"verdict": "pass"}}); err == nil {
		t.Fatal("corrupt existing record overwritten")
	}
}

func TestConcurrentProofPublicationAndSymlinkRejection(t *testing.T) {
	state := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := Save(state, "git:repo", map[string]string{"tree": "bound"})
			if err != nil {
				t.Error(err)
				return
			}
			if _, err = Load(state, "git:repo", r.ID); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	unsafe := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(unsafe, "gate-proofs")); err != nil {
		t.Skip(err)
	}
	if _, err := Save(unsafe, "git:repo", map[string]string{"tree": "bound"}); err == nil {
		t.Fatal("symlink state accepted")
	}
}
