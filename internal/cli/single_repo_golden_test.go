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
	fixedGit(t, root, "config", "core.autocrlf", "false")
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

// Only the fixture's checkout/worktree roots and private candidate directory
// names are volatile. Do not rewrite arbitrary backslashes or JSON escapes.
var goldenCandidatePath = regexp.MustCompile(`(?:[A-Za-z]:(?:\\\\|\\|/)(?:[^\\/:"\r\n\t]+(?:\\\\|\\|/))*|/(?:[^/\s"\\]+/)*)radar-integration-[0-9]+`)

func replaceGoldenPath(s string, re *regexp.Regexp, replacement func(string) string) string {
	var out strings.Builder
	end := 0
	for _, loc := range re.FindAllStringIndex(s, -1) {
		// A match must be a complete path, not a prefix of another directory
		// or a fragment embedded in a longer path/identifier.
		if loc[0] > 0 && !goldenPathDelimiter(s[loc[0]-1]) || loc[1] < len(s) && !goldenPathDelimiter(s[loc[1]]) {
			continue
		}
		out.WriteString(s[end:loc[0]])
		out.WriteString(replacement(s[loc[0]:loc[1]]))
		end = loc[1]
	}
	out.WriteString(s[end:])
	return out.String()
}

func goldenPathDelimiter(c byte) bool {
	return strings.ContainsRune(" \t\r\n\"'`=,;()[]{}", rune(c))
}

func normalizeGolden(s, base string) string {
	for _, prefix := range []string{strings.Trim(strconv.Quote(base), `"`), base, strings.ReplaceAll(base, `\`, "/")} {
		sep := `(?:\\\\|\\|/)`
		re := regexp.MustCompile(regexp.QuoteMeta(prefix) + sep + `(?:repo|worktrees` + sep + `[A-Za-z0-9._-]+)`)
		s = replaceGoldenPath(s, re, func(p string) string {
			suffix := strings.TrimPrefix(p, prefix)
			return "$TMP" + strings.ReplaceAll(strings.ReplaceAll(suffix, `\\`, "/"), `\`, "/")
		})
	}
	return replaceGoldenPath(s, goldenCandidatePath, func(string) string { return "$VOLATILE" })
}

func TestNormalizeGoldenPaths(t *testing.T) {
	cases := []struct{ name, base, input, want string }{
		{"posix fixture", "/tmp/fixture", `{"path":"/tmp/fixture/repo","peer":"/tmp/fixture/worktrees/agent-a"}`, `{"path":"$TMP/repo","peer":"$TMP/worktrees/agent-a"}`},
		{"windows text", `C:\Temp\fixture`, `C:\Temp\fixture\repo C:\Temp\fixture\worktrees\agent-a`, `$TMP/repo $TMP/worktrees/agent-a`},
		{"windows JSON", `C:\Temp\fixture`, `{"path":"C:\\Temp\\fixture\\repo","peer":"C:\\Temp\\fixture\\worktrees\\agent-a"}`, `{"path":"$TMP/repo","peer":"$TMP/worktrees/agent-a"}`},
		{"windows slash", `C:\Temp\fixture`, `{"path":"C:/Temp/fixture/repo"}`, `{"path":"$TMP/repo"}`},
		{"posix candidate", "/tmp/fixture", `{"repository":"/private/var/folders/abc/radar-integration-123"}`, `{"repository":"$VOLATILE"}`},
		{"windows candidate", `C:\Temp\fixture`, `C:\Temp\radar-integration-123`, `$VOLATILE`},
		{"windows JSON candidate", `C:\Temp\fixture`, `{"repository":"C:\\Users\\First Last\\Temp\\radar-integration-123"}`, `{"repository":"$VOLATILE"}`},
		{"windows slash candidate", `C:\Temp\fixture`, `{"repository":"C:/Temp/radar-integration-123"}`, `{"repository":"$VOLATILE"}`},
		{"unrelated backslashes and escapes", `C:\Temp\fixture`, `{"command":["cmd.exe","/c","echo C:\\other\\repo"],"escape":"line\nnext\t\"quoted\"","pattern":"\\d+"}`, `{"command":["cmd.exe","/c","echo C:\\other\\repo"],"escape":"line\nnext\t\"quoted\"","pattern":"\\d+"}`},
		{"fixture path boundaries", `C:\Temp\fixture`, `C:\Temp\fixture-extra\repo C:\Temp\fixture\repository C:\Temp\fixture\repo\other C:\Temp\fixture\unrelated`, `C:\Temp\fixture-extra\repo C:\Temp\fixture\repository C:\Temp\fixture\repo\other C:\Temp\fixture\unrelated`},
		{"candidate boundaries", "/tmp/fixture", `/tmp/radar-integration-123-extra /tmp/radar-integration-123/file radar-integration-123 relative/radar-integration-123`, `/tmp/radar-integration-123-extra /tmp/radar-integration-123/file radar-integration-123 relative/radar-integration-123`},
		{"candidate multiple paths", "/tmp/fixture", `/tmp/radar-integration-123 /private/var/folders/abc/radar-integration-456`, `$VOLATILE $VOLATILE`},
		{"other filename suffixes", "/tmp/fixture", `/tmp/fixture/repo+other /tmp/radar-integration-123中文 /tmp/radar-integration-123+other`, `/tmp/fixture/repo+other /tmp/radar-integration-123中文 /tmp/radar-integration-123+other`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := normalizeGolden(c.input, c.base); got != c.want {
				t.Fatalf("normalizeGolden() = %q; want %q", got, c.want)
			}
		})
	}
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
		// Both agents add the same Alembic root, then one revision each on it.
		{"migration-heads", map[string]map[string]string{
			"agent-orders":  {"db/env.py": "print('env')\n", "db/versions/r0_init.py": "revision = 'r0'\ndown_revision = None\n", "db/versions/a1_orders.py": "revision = 'a1'\ndown_revision = 'r0'\n"},
			"agent-refunds": {"db/env.py": "print('env')\n", "db/versions/r0_init.py": "revision = 'r0'\ndown_revision = None\n", "db/versions/b1_refunds.py": "revision = 'b1'\ndown_revision = 'r0'\n"},
		}, []string{"gate"}},
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
