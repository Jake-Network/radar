package runproof

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryIsRepositoryBoundAndTamperChecked(t *testing.T) {
	state := t.TempDir()
	a, err := Save(state, "repo", map[string]string{"verdict": "fail"})
	if err != nil {
		t.Fatal(err)
	}
	s := RunSummary{ID: a.ID, Verdict: "fail", InputKey: "sources", CreatedAt: "2026-11-01T00:00:00Z"}
	if err = Remember(state, "repo", s); err != nil {
		t.Fatal(err)
	}
	got, err := Recent(state, "repo", 5)
	if err != nil || len(got) != 1 || got[0] != s {
		t.Fatalf("%+v %v", got, err)
	}
	if other, err := Recent(state, "other", 5); err != nil || len(other) != 0 {
		t.Fatalf("%v %v", other, err)
	}
	if err = Remember(state, "other", s); err == nil {
		t.Fatal("cross repo history accepted")
	}
	dir, _ := recentDir(state, "repo")
	if err = os.WriteFile(filepath.Join(dir, summaryName(s)), []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Recent(state, "repo", 5); err == nil {
		t.Fatal("tampered summary accepted")
	}
}
func TestRecentOrdersTimestampsWithAndWithoutFractionalSeconds(t *testing.T) {
	state := t.TempDir()
	for i, stamp := range []string{"2026-11-01T00:00:00Z", "2026-11-01T00:00:00.1Z"} {
		r, err := Save(state, "repo", map[string]int{"observation": i})
		if err != nil {
			t.Fatal(err)
		}
		if err = Remember(state, "repo", RunSummary{ID: r.ID, Verdict: "blocked", InputKey: r.ID, CreatedAt: stamp}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Recent(state, "repo", 5)
	if err != nil || len(got) != 2 || got[0].CreatedAt != "2026-11-01T00:00:00.1Z" {
		t.Fatalf("%+v %v", got, err)
	}
}
