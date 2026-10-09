package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func worktreeGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, b)
	}
}

func worktreeRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	worktreeGit(t, root, "init", "-q")
	worktreeGit(t, root, "config", "user.name", "Radar")
	worktreeGit(t, root, "config", "user.email", "radar@example.invalid")
	if err := os.WriteFile(filepath.Join(root, "tracked"), []byte("baseline\n"), 0600); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, root, "add", "tracked")
	worktreeGit(t, root, "commit", "-qm", "baseline")
	return root
}

func TestInspectWorktreesSkipsBareRepository(t *testing.T) {
	root := worktreeRepo(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	worktreeGit(t, root, "clone", "--bare", "--quiet", root, bare)
	linked := filepath.Join(t.TempDir(), "linked")
	worktreeGit(t, bare, "worktree", "add", "--detach", linked, "HEAD")
	got, err := InspectWorktrees(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != linked || !got[0].Detached || got[0].InspectionError != "" {
		t.Fatalf("bare entry must be omitted: %+v", got)
	}
}

func TestInspectWorktreesHonorsGlobalIgnoresWithoutExecutingConfig(t *testing.T) {
	root := worktreeRepo(t)
	global := t.TempDir()
	ignore := filepath.Join(global, "global ignore")
	if err := os.WriteFile(ignore, []byte(".serena/\n.DS_Store\n"), 0600); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(global, "executed")
	monitor := filepath.Join(global, "monitor")
	if err := os.WriteFile(monitor, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(global, "config")
	// git config creates portable quoting for spaces and Windows paths.
	t.Setenv("GIT_CONFIG_GLOBAL", config)
	worktreeGit(t, root, "config", "--global", "core.excludesFile", ignore)
	linked := filepath.Join(t.TempDir(), "linked")
	worktreeGit(t, root, "worktree", "add", "--detach", linked, "HEAD")
	for _, path := range []string{root, linked} {
		if err := os.Mkdir(filepath.Join(path, ".serena"), 0700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{".serena/cache", ".DS_Store", "actual.py"} {
			if err := os.WriteFile(filepath.Join(path, name), []byte("data"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A malicious global and repository-local fsmonitor must remain disabled.
	worktreeGit(t, root, "config", "--global", "core.fsmonitor", monitor)
	worktreeGit(t, root, "config", "core.fsmonitor", monitor)
	got, err := InspectWorktrees(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("worktrees: %+v", got)
	}
	for _, w := range got {
		if w.InspectionError != "" || !reflect.DeepEqual(w.Untracked, []string{"actual.py"}) {
			t.Fatalf("global ignore not honored: %+v", w)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("fsmonitor executed")
	}
	// Local core.excludesFile overrides global configuration, matching Git.
	localIgnore := filepath.Join(global, "local-ignore")
	if err := os.WriteFile(localIgnore, []byte("actual.py\n"), 0600); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, root, "config", "core.excludesFile", localIgnore)
	got, err = InspectWorktrees(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range got {
		if !reflect.DeepEqual(w.Untracked, []string{".DS_Store", ".serena/cache"}) {
			t.Fatalf("local ignore precedence: %+v", w)
		}
	}
}

func TestInspectWorktreesRetainsListingOrderAndDirtyCategories(t *testing.T) {
	root := worktreeRepo(t)
	paths := []string{root}
	for i := 0; i < 5; i++ {
		linked := filepath.Join(t.TempDir(), "linked")
		worktreeGit(t, root, "worktree", "add", "--detach", linked, "HEAD")
		paths = append(paths, linked)
	}
	for _, path := range paths {
		if err := os.WriteFile(filepath.Join(path, "tracked"), []byte("staged\n"), 0600); err != nil {
			t.Fatal(err)
		}
		worktreeGit(t, path, "add", "tracked")
		if err := os.WriteFile(filepath.Join(path, "tracked"), []byte("unstaged\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "untracked"), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	listing, err := runListing(context.Background(), root, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		t.Fatal(err)
	}
	var expected []string
	for _, field := range strings.Split(string(listing), "\x00") {
		if path, ok := strings.CutPrefix(field, "worktree "); ok {
			expected = append(expected, path)
		}
	}
	for range 3 {
		got, err := InspectWorktrees(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		var actual []string
		for _, w := range got {
			actual = append(actual, w.Path)
			if w.InspectionError != "" || !reflect.DeepEqual(w.Staged, []string{"tracked"}) || !reflect.DeepEqual(w.Unstaged, []string{"tracked"}) || !reflect.DeepEqual(w.Untracked, []string{"untracked"}) {
				t.Fatalf("dirty categories: %+v", w)
			}
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("order changed: got %v, want %v", actual, expected)
		}
	}
}

func TestInspectWorktreesReportsLegacyListingUnavailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("legacy command shim requires POSIX executable scripts")
	}
	root := worktreeRepo(t)
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("legacy command shim requires POSIX shell")
	}
	bin := t.TempDir()
	shim := "#!/bin/sh\ncase \" $* \" in\n  *' worktree list --porcelain -z '*) echo \"error: unknown switch z\" >&2; exit 129;;\nesac\nexec \"$RADAR_WORKTREE_REAL_GIT\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(shim), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RADAR_WORKTREE_REAL_GIT", gitPath)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	got, err := InspectWorktrees(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "unknown switch z") || len(got) != 0 {
		t.Fatalf("unavailable inspection must be distinguished from clean worktrees: %+v %v", got, err)
	}
}

func TestInspectWorktreesHonorsGlobalIncludesPerWorktree(t *testing.T) {
	root := worktreeRepo(t)
	linked := filepath.Join(t.TempDir(), "linked")
	worktreeGit(t, root, "worktree", "add", "--detach", linked, "HEAD")
	gitdirBytes, err := exec.Command("git", "-C", linked, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	gitdir := filepath.ToSlash(strings.TrimSpace(string(gitdirBytes)))
	settings := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(settings, "global-config"))
	include := filepath.Join(settings, "included config")
	conditional := filepath.Join(settings, "conditional-config")
	genericIgnore := filepath.Join(settings, "generic-ignore")
	conditionalIgnore := filepath.Join(settings, "conditional-ignore")
	for path, data := range map[string]string{genericIgnore: ".serena/\n", conditionalIgnore: ".DS_Store\n"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	worktreeGit(t, root, "config", "--file", include, "core.excludesFile", genericIgnore)
	worktreeGit(t, root, "config", "--file", conditional, "core.excludesFile", conditionalIgnore)
	worktreeGit(t, root, "config", "--global", "include.path", include)
	worktreeGit(t, root, "config", "--global", "includeIf.gitdir:"+gitdir+".path", conditional)
	marker := filepath.Join(settings, "executed")
	monitor := filepath.Join(settings, "monitor")
	if err := os.WriteFile(monitor, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, root, "config", "--file", include, "core.fsmonitor", monitor)
	for _, path := range []string{root, linked} {
		if err := os.Mkdir(filepath.Join(path, ".serena"), 0700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{".serena/cache", ".DS_Store", "actual.py"} {
			if err := os.WriteFile(filepath.Join(path, name), []byte("data"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	got, err := InspectWorktrees(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("worktrees: %+v", got)
	}
	for _, w := range got {
		expected := []string{".DS_Store", "actual.py"}
		if w.Path == linked {
			expected = []string{".serena/cache", "actual.py"}
		}
		if w.InspectionError != "" || !reflect.DeepEqual(w.Untracked, expected) {
			t.Fatalf("includeIf must match each actual worktree gitdir: %+v, want %v", w, expected)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("included fsmonitor executed")
	}
}
