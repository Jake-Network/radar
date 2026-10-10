package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The single-repository gate is a public contract: a repository that is not in
// a workspace must keep the exact human output, JSON and exit code it had
// before workspaces existed. Regenerate only for a deliberate change:
// RADAR_UPDATE_GOLDEN=1 go test ./internal/cli -run TestSingleRepoGateGolden

// fixedGit runs Git with fixed identities and dates so commit SHAs, and every
// identifier derived from them, are identical on every run.
func fixedGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Radar test", "GIT_AUTHOR_EMAIL=radar@example.invalid",
		"GIT_COMMITTER_NAME=Radar test", "GIT_COMMITTER_EMAIL=radar@example.invalid",
		"GIT_AUTHOR_DATE=2001-02-03T04:05:06Z", "GIT_COMMITTER_DATE=2001-02-03T04:05:06Z")
	b, e := c.CombinedOutput()
	if e != nil {
		t.Fatalf("git %v: %v %s", args, e, b)
	}
	return strings.TrimSpace(string(b))
}

// goldenRepo builds the checkout fixture with deterministic commits. It
// returns the main checkout and the directory holding agent worktrees.
func goldenRepo(t *testing.T, branches map[string]map[string]string) (string, string) {
	t.Helper()
	if _, e := exec.LookPath("git"); e != nil {
		t.Skip(e)
	}
	base := realTempDir(t)
	root := filepath.Join(base, "repo")
	worktrees := filepath.Join(base, "worktrees")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	fixedGit(t, root, "init", "-q")
	fixedGit(t, root, "checkout", "-q", "-b", "main")
	for p, content := range map[string]string{"backend.py": "def price():\n    return 1\n", "frontend.ts": "export const QUANTITY = 1;\n", "test_checkout.py": checkoutTest} {
		put(t, root, p, content)
	}
	fixedGit(t, root, "add", ".")
	fixedGit(t, root, "commit", "-qm", "baseline")
	names := []string{}
	for name := range branches {
		names = append(names, name)
	}
	// Worktree creation order defines single-repository selection order.
	sort.Strings(names)
	for _, name := range names {
		dir := filepath.Join(worktrees, name)
		fixedGit(t, root, "worktree", "add", "-q", "-b", name, dir, "main")
		if len(branches[name]) == 0 {
			continue
		}
		for p, content := range branches[name] {
			put(t, dir, p, content)
		}
		fixedGit(t, dir, "add", ".")
		fixedGit(t, dir, "commit", "-qm", name)
	}
	return root, base
}

// The private candidate lives under the host's temporary directory (/tmp on
// Linux, /private/var/folders/... on macOS), so its whole path is volatile.
var goldenVolatile = []*regexp.Regexp{
	regexp.MustCompile(`(?:/[^/\s"]+)*/radar-integration-[0-9]+`),
}

func normalizeGolden(s, base string) string {
	s = strings.ReplaceAll(s, base, "$TMP")
	for _, re := range goldenVolatile {
		s = re.ReplaceAllString(s, "$$VOLATILE")
	}
	return s
}

func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "single-repo", name+".golden")
	if os.Getenv("RADAR_UPDATE_GOLDEN") == "1" {
		if e := os.MkdirAll(filepath.Dir(path), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(path, []byte(got), 0644); e != nil {
			t.Fatal(e)
		}
		return
	}
	want, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if string(want) != got {
		t.Fatalf("%s changed for an unregistered repository.\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

func TestSingleRepoGateGolden(t *testing.T) {
	cases := []struct {
		name     string
		branches map[string]map[string]string
		args     []string
	}{
		{"auto-pass", map[string]map[string]string{
			"agent-backend":  {"backend.py": "def price():\n    return 2\n"},
			"agent-frontend": {"frontend.ts": "export const QUANTITY = 2;\n"},
			"agent-idle":     nil,
		}, []string{"gate"}},
		{"named-conflict", map[string]map[string]string{
			"agent-a": {"backend.py": "def price():\n    return 3\n"},
			"agent-b": {"backend.py": "def price():\n    return 4\n"},
		}, []string{"gate", "agent-b", "agent-a"}},
		{"no-branches", map[string]map[string]string{}, []string{"gate"}},
		{"bad-ref", map[string]map[string]string{}, []string{"gate", "orders:agent/api"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, base := goldenRepo(t, c.branches)
			code, out, errs := run(t, root, c.args...)
			checkGolden(t, c.name+".txt", normalizeGolden("exit "+strconv.Itoa(code)+"\n--- stdout\n"+out+"--- stderr\n"+errs, base))
			var jout, jerr bytes.Buffer
			jcode := Run(context.Background(), append(append([]string{}, c.args...), "--root", root, "--json"), &jout, &jerr)
			checkGolden(t, c.name+".json", normalizeGolden("exit "+strconv.Itoa(jcode)+"\n"+jout.String()+jerr.String(), base))
			a := &app{ctx: context.Background(), root: root}
			arguments := map[string]any{}
			if len(c.args) > 1 {
				arguments["branches"] = strings.Join(c.args[1:], ",")
			}
			result := a.callTool("radar_gate", arguments)
			var text strings.Builder
			for _, part := range result["content"].([]map[string]any) {
				text.WriteString(part["text"].(string) + "\n")
			}
			checkGolden(t, c.name+".mcp", normalizeGolden("isError "+strconv.FormatBool(result["isError"].(bool))+"\n"+text.String(), base))
		})
	}
}
