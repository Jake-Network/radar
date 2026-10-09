package discovery

import (
	"context"
	"github.com/Jake-Network/radar/internal/contracts"
	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/testselection"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func exampleRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	source := filepath.Join("..", "..", "examples", "fastapi-typescript")
	err := filepath.WalkDir(source, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(source, p)
		if e != nil {
			return e
		}
		target := filepath.Join(root, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		return os.WriteFile(target, b, 0600)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.invalid"}, {"add", "."}, {"commit", "-qm", "baseline"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("%s %v", b, e)
		}
	}
	return root
}

func TestRepresentativeDiscoveryQuality(t *testing.T) {
	root := exampleRepository(t)
	ctx := context.Background()
	start := time.Now()
	r, err := Discover(ctx, root, "HEAD")
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	falseCandidates := 0
	for _, c := range r.Candidates {
		if c.Consumer == "" {
			continue
		}
		if c.Endpoint == "/api/v1/users/current" && (c.Consumer == "frontend/client.ts" || c.Consumer == "frontend/axios-client.ts") && c.Evidence == model.Inferred {
			found[c.Consumer] = true
		} else {
			falseCandidates++
		}
		for _, loc := range c.Locations {
			if loc.Revision != r.Revision || loc.Line <= 0 {
				t.Fatalf("missing exact provenance: %+v", loc)
			}
		}
	}
	if len(found) != 2 || falseCandidates != 0 {
		t.Fatalf("ground truth %d/2, false %d: %+v", len(found), falseCandidates, r)
	}
	unresolved := 0
	for _, d := range r.Diagnostics {
		if strings.Contains(strings.ToLower(d.Message), "dynamic") || strings.Contains(strings.ToLower(d.Message), "literal") {
			unresolved++
		}
	}
	t.Logf("ground_truth_found=%d/2 false_candidates=%d unresolved_diagnostics=%d discovery_ms=%.3f", len(found), falseCandidates, unresolved, float64(elapsed.Microseconds())/1000)
	if r.Status == model.StatusPassed || len(r.ProposedManifest.Bindings) != 0 {
		t.Fatal("Python source discovery became accepted certainty")
	}
}

func TestReviewedExampleBindingAndRecommendations(t *testing.T) {
	root := exampleRepository(t)
	ctx := context.Background()
	os.MkdirAll(filepath.Join(root, ".radar"), 0700)
	b, err := os.ReadFile(filepath.Join(root, "accepted-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ".radar/contracts.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "review accepted bindings"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("%s %v", b, e)
		}
	}
	baseCmd := exec.Command("git", "rev-parse", "HEAD")
	baseCmd.Dir = root
	baseline, err := baseCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	modelFile := filepath.Join(root, "backend/models.py")
	b, _ = os.ReadFile(modelFile)
	os.WriteFile(modelFile, []byte(strings.Replace(string(b), "    email: str", "    replacement: str", 1)), 0600)
	cmp, err := Compare(ctx, root, "HEAD", "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	if len(cmp.Findings) == 0 || cmp.Findings[0].Evidence != model.Inferred {
		t.Fatalf("removed consumed field absent %+v", cmp)
	}
	b, _ = os.ReadFile(filepath.Join(root, "schema.json"))
	os.WriteFile(filepath.Join(root, "schema.json"), []byte(strings.Replace(string(b), `"email":{"type":"string"}`, `"replacement":{"type":"string"}`, 1)), 0600)
	for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "remove consumed field"}} {
		c := exec.Command("git", args...)
		c.Dir = root
		if b, e := c.CombinedOutput(); e != nil {
			t.Fatalf("%s %v", b, e)
		}
	}
	declared, err := contracts.Impact(ctx, root, strings.TrimSpace(string(baseline)), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if gate.NoBreaking(declared.Findings, declared.Unestablished()).Status != model.StatusFailed {
		t.Fatalf("accepted removed field did not fail %+v", declared)
	}
	inv, err := testselection.Discover(ctx, root, "WORKTREE")
	if err != nil {
		t.Fatal(err)
	}
	p, err := testselection.Select(inv, model.Snapshot{}, []string{"schema.json"}, testselection.Options{ToolAvailable: func(string) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	py, node := false, false
	for _, c := range p.Commands {
		py = py || c.Framework == "unittest"
		node = node || c.Framework == "node-test"
	}
	if !py || !node {
		t.Fatalf("contract snapshot test recommendations missing %+v", p)
	}
}
