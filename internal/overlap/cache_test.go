package overlap

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
)

func TestAnalysisCacheDoesNotFreezeNamedBaseOrHead(t *testing.T) {
	for _, moving := range []string{"base", "head"} {
		t.Run(moving, func(t *testing.T) {
			ctx := WithAnalysisCache(context.Background())
			root := t.TempDir()
			git := func(dir string, args ...string) string {
				t.Helper()
				out, err := gitrepo.RunPrivate(ctx, dir, args...)
				if err != nil {
					t.Fatal(args, err, out)
				}
				return out
			}
			put := func(dir, path, content string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, path), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			git(root, "init", "-q")
			git(root, "checkout", "-qb", "main")
			git(root, "config", "core.autocrlf", "false")
			put(root, "backend.py", "def charge(): return 1\n")
			put(root, "client.py", "VALUE=1\n")
			git(root, "add", ".")
			git(root, "commit", "-qm", "base")
			base := git(root, "rev-parse", "HEAD")
			api, consumer := filepath.Join(t.TempDir(), "api"), filepath.Join(t.TempDir(), "consumer")
			git(root, "worktree", "add", "-qb", "api", api, "main")
			git(root, "worktree", "add", "-qb", "consumer", consumer, "main")
			put(api, "backend.py", "def bill(): return 1\n")
			git(api, "add", ".")
			git(api, "commit", "-qm", "remove charge")
			put(consumer, "client.py", "from backend import charge\nVALUE=charge()\n")
			git(consumer, "add", ".")
			git(consumer, "commit", "-qm", "consume charge")
			inputs := []Input{{Branch: "api", Head: "api"}, {Branch: "consumer", Head: "consumer"}}
			if moving == "base" {
				base = "main"
				for i := range inputs {
					inputs[i].Head = git(root, "rev-parse", inputs[i].Head)
				}
			}
			before, err := AnalyzeCurrent(ctx, root, base, "api", inputs)
			if err != nil || len(before.Findings) != 1 || before.Findings[0].Code != "symbol_dangling_reference" {
				t.Fatal("fixture lacks initial drift", before, err)
			}
			if moving == "head" {
				put(consumer, "client.py", "VALUE=1\n")
				git(consumer, "add", ".")
				git(consumer, "commit", "-qm", "remove consumer")
			} else {
				git(root, "merge", "--ff-only", "api")
			}
			after, err := AnalyzeCurrent(ctx, root, base, "api", inputs)
			if err != nil || len(after.Findings) != 0 {
				t.Fatal("moving named input reused stale analysis", after, err)
			}
		})
	}
}

func TestTextualStandaloneComparisonDoesNotReplaceFullSemanticAnalysis(t *testing.T) {
	ctx := WithAnalysisCache(context.Background())
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := gitrepo.RunPrivate(ctx, root, args...)
		if err != nil {
			t.Fatal(args, err, out)
		}
		return out
	}
	write := func(name, source string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	git("config", "core.autocrlf", "false")
	write("backend.py", "def charge(): return 1\n")
	write("client.py", "from backend import charge\nVALUE=charge()\n")
	git("add", ".")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	write("backend.py", "def bill(): return 1\n")
	git("add", ".")
	git("commit", "-qm", "remove consumed name")
	inputs := []Input{{Branch: "api", Head: git("rev-parse", "HEAD")}}
	textual, err := analyzeComparison(ctx, root, base, inputs, nil, true)
	if err != nil || len(textual.Findings) != 0 || len(textual.Conflicts) != 0 {
		t.Fatal("unexpected textual comparison", textual, err)
	}
	if _, ok := cachedAnalysis(ctx, root, base, inputs); ok {
		t.Fatal("partial textual analysis entered full semantic cache")
	}
	full, err := analyze(ctx, root, base, inputs, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range full.Checks {
		if check.ID == "no_symbol_conflicts" {
			return
		}
	}
	t.Fatal("full analysis did not perform semantic inspection after textual comparison", full)
}

func TestTextualStandaloneIdentityCannotBypassSnapshotValidation(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := gitrepo.RunPrivate(ctx, root, args...)
		if err != nil {
			t.Fatal(args, err, out)
		}
		return out
	}
	git("init", "-q")
	git("commit", "--allow-empty", "-qm", "base")
	base := git("rev-parse", "HEAD")
	fake := integration.DirtySnapshot{Worktree: root, Head: base, Digest: "uncaptured"}
	inputs := []Input{{Branch: "own", Head: base, Snapshot: &fake}, {Branch: "peer", Head: base}}
	if report, err := AnalyzeCurrent(ctx, root, base, "own", inputs); err == nil {
		t.Fatal("base identity accepted unverified snapshot before full pair validation", report)
	}
}

func TestTextualStandaloneMapUpgradesBeforeLaterSemanticPair(t *testing.T) {
	ctx := WithAnalysisCache(context.Background())
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := gitrepo.RunPrivate(ctx, root, args...)
		if err != nil {
			t.Fatal(args, err, out)
		}
		return out
	}
	write := func(name, value string) {
		t.Helper()
		full := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	git("config", "core.autocrlf", "false")
	write("shared.txt", "base\n")
	write("schema.json", `{"type":"object","properties":{"total":{"type":"integer"}}}`)
	write("api.py", "def summary(): return {'total': 1}\n")
	write("consumer.ts", "export const use = (x:any) => x.total;\n")
	write(".radar/contracts.json", `{"version":1,"bindings":[{"id":"api","schema":"schema.json","producer":"api.py","consumer":"consumer.ts","fields":["total"],"direction":"response"}]}`)
	git("add", ".")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	git("checkout", "-qb", "a")
	write("shared.txt", "a\n")
	write("schema.json", `{"type":"object","properties":{}}`)
	git("add", ".")
	git("commit", "-qm", "own contract failure")
	a := Input{Branch: "a", Head: git("rev-parse", "HEAD")}
	git("checkout", "-qb", "b", base)
	write("shared.txt", "b\n")
	git("add", ".")
	git("commit", "-qm", "conflicting text")
	b := Input{Branch: "b", Head: git("rev-parse", "HEAD")}
	git("checkout", "-qb", "c", base)
	write("note.md", "unrelated\n")
	git("add", ".")
	git("commit", "-qm", "unrelated")
	c := Input{Branch: "c", Head: git("rev-parse", "HEAD")}
	standalone, err := analyze(context.Background(), root, base, []Input{a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ownFailure := false
	for _, finding := range standalone.Findings {
		ownFailure = ownFailure || strings.HasPrefix(finding.Code, "contract_")
	}
	if !ownFailure {
		t.Fatal("fixture lacks standalone contract failure")
	}
	report, err := AnalyzeCurrent(ctx, root, base, "a", []Input{a, b, c})
	if err != nil || report.Pairs != 2 {
		t.Fatal(report, err)
	}
	for _, finding := range report.Findings {
		if strings.HasPrefix(finding.Code, "contract_") {
			t.Fatal("textual-only standalone map hid existing semantic failure", finding)
		}
	}
}
