package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestIDs(t *testing.T) {
	for _, id := range []string{"orders", "Orders_api", "a", "pay-ments2"} {
		if err := ValidID(id); err != nil {
			t.Errorf("%q rejected: %v", id, err)
		}
	}
	for _, id := range []string{"", "1orders", "-x", "_x", "or ders", "or:ders", "ör", strings.Repeat("a", 65)} {
		if ValidID(id) == nil {
			t.Errorf("%q accepted", id)
		}
	}
	for folder, want := range map[string]string{"orders": "orders", "orders.api": "orders-api", "2024 site": "repo-2024-site", "...": "repo", "my repo!": "my-repo", "_x_": "x"} {
		if got := DefaultID(folder); got != want || ValidID(got) != nil {
			t.Errorf("DefaultID(%q) = %q, want %q", folder, got, want)
		}
	}
}

func TestParseTargets(t *testing.T) {
	known := map[string]bool{"agent/api": true, "a,b": true, "payments:agent/client": true, "orders:x,y": true}
	valid := func(t Target) bool { return known[t.String()] }
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"agent/api"}, []string{"agent/api"}},
		{[]string{"a,b"}, []string{"a,b"}},    // a ref containing a comma
		{[]string{"c,d"}, []string{"c", "d"}}, // historical comma list
		{[]string{"payments:agent/client"}, []string{"payments:agent/client"}},
		{[]string{"orders:a,payments:b"}, []string{"orders:a", "payments:b"}},
		{[]string{"orders:x,y"}, []string{"orders:x,y"}}, // repo ref with a comma
		{[]string{"agent/api", "payments:main"}, []string{"agent/api", "payments:main"}},
		{[]string{"orders:feature:odd"}, []string{"orders:feature:odd"}}, // split at the first ':' only
	}
	for _, c := range cases {
		got := []string{}
		for _, t := range ParseTargets(c.args, valid) {
			got = append(got, t.String())
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: got %v want %v", c.args, got, c.want)
		}
	}
	if tg := ParseTarget("orders:feature:odd"); tg.Repo != "orders" || tg.Ref != "feature:odd" {
		t.Fatal(tg)
	}
}

func loc(name string) Location {
	root := filepath.Join(string(filepath.Separator), "src", name)
	return Location{Root: root, CommonDir: filepath.Join(root, ".git"), MainPath: root}
}

func worktreeOf(main Location, dir string) Location {
	return Location{Root: filepath.Join(string(filepath.Separator), "wt", dir), CommonDir: main.CommonDir, MainPath: main.MainPath}
}

func allUsable(Repo) string { return "" }

func registryWith(repos ...Location) Registry {
	r := Registry{Version: RegistryVersion}
	w := Workspace{Name: "shop"}
	for _, l := range repos {
		w.Repos = append(w.Repos, Repo{ID: l.DefaultRepoID(), Path: l.MainPath, CommonDir: l.CommonDir})
	}
	r.Workspaces = append(r.Workspaces, w)
	return r
}

func TestResolveScope(t *testing.T) {
	orders, payments, web := loc("orders"), loc("payments"), loc("web")
	reg := registryWith(orders, payments)
	ids := func(s *Scope) []string {
		out := []string{}
		for _, r := range s.Repos {
			out = append(out, r.ID)
		}
		return out
	}

	// Every member and worktree resolves the same workspace.
	for _, current := range []Location{orders, worktreeOf(orders, "agent-api"), payments} {
		s, err := ResolveScope(ScopeInput{Registry: reg, Current: current, Usable: allUsable})
		if err != nil || s == nil || s.Workspace != "shop" || s.Key != "shop" || !reflect.DeepEqual(ids(s), []string{"orders", "payments"}) || s.Source != SourceRegistry {
			t.Fatalf("from %s: %+v %v", current.Root, s, err)
		}
	}

	// Unregistered without --with: single-repository mode.
	if s, err := ResolveScope(ScopeInput{Registry: reg, Current: web, Usable: allUsable}); s != nil || err != nil {
		t.Fatal("unregistered repo must keep single-repository mode", s, err)
	}
	if _, err := ResolveScope(ScopeInput{Registry: reg, Current: web, Only: []string{"web"}, Usable: allUsable}); err == nil || !strings.Contains(err.Error(), "Next: radar workspace add") {
		t.Fatal("--only outside a workspace", err)
	}

	// --with alone: current repo plus the named ones.
	s, err := ResolveScope(ScopeInput{Registry: Registry{}, Current: web, With: []Location{payments}, WithArgs: []string{"../payments"}, Usable: allUsable})
	if err != nil || !s.OneOff || s.Workspace != "" || s.Source != SourceWith || s.Current != "web" || !reflect.DeepEqual(ids(s), []string{"payments", "web"}) || !strings.HasPrefix(s.Key, "_with-web-") {
		t.Fatal("one-off scope", s, err)
	}
	again, _ := ResolveScope(ScopeInput{Registry: Registry{}, Current: worktreeOf(web, "agent"), With: []Location{payments}, WithArgs: []string{"../payments"}, Usable: allUsable})
	if again.Key != s.Key {
		t.Fatal("worktrees of one repository must share the one-off key", again.Key, s.Key)
	}

	// --with on a workspace adds for this run only; a member is not duplicated.
	s, err = ResolveScope(ScopeInput{Registry: reg, Current: orders, With: []Location{web, worktreeOf(payments, "x")}, WithArgs: []string{"../web", "../x"}, Usable: allUsable})
	if err != nil || !s.OneOff || s.Workspace != "shop" || !reflect.DeepEqual(ids(s), []string{"orders", "payments", "web"}) {
		t.Fatal("--with on workspace", s, err)
	}
	clash := Location{Root: "/elsewhere/orders", CommonDir: "/elsewhere/orders/.git", MainPath: "/elsewhere/orders"}
	if _, err = ResolveScope(ScopeInput{Registry: reg, Current: payments, With: []Location{clash}, WithArgs: []string{"/elsewhere/orders"}, Usable: allUsable}); err == nil || !strings.Contains(err.Error(), "--id orders_2") {
		t.Fatal("--with ID collision", err)
	}

	// --only restricts and is partial; unknown IDs list the registered ones.
	s, err = ResolveScope(ScopeInput{Registry: reg, Current: orders, Only: []string{"payments"}, Usable: allUsable})
	if err != nil || !s.Partial() || !reflect.DeepEqual(ids(s), []string{"payments"}) || len(s.All) != 2 {
		t.Fatal("--only", s, err)
	}
	if _, err = ResolveScope(ScopeInput{Registry: reg, Current: orders, Only: []string{"paymnts"}, Usable: allUsable}); err == nil || !strings.Contains(err.Error(), "registered repos: orders, payments") {
		t.Fatal("unknown --only", err)
	}

	// A missing registered path is reported, not dropped; the current
	// checkout replaces its own unusable registered path.
	gone := func(r Repo) string {
		if r.ID == "payments" || r.ID == "orders" {
			return "path " + r.Path + " is not available"
		}
		return ""
	}
	s, _ = ResolveScope(ScopeInput{Registry: reg, Current: worktreeOf(orders, "a"), Usable: gone})
	if s.Repos[0].Missing != "" || s.Repos[0].Path != worktreeOf(orders, "a").Root || s.Repos[1].Missing == "" {
		t.Fatal("missing paths", s.Repos)
	}
}

func TestAssignTargetsAndBases(t *testing.T) {
	orders, payments := loc("orders"), loc("payments")
	s, _ := ResolveScope(ScopeInput{Registry: registryWith(orders, payments), Current: orders, Usable: allUsable})
	got, err := s.AssignTargets(ParseTargets([]string{"agent/b", "payments:agent/b", "agent/a"}, func(Target) bool { return true }))
	if err != nil || !reflect.DeepEqual(got, map[string][]string{"orders": {"agent/b", "agent/a"}, "payments": {"agent/b"}}) {
		t.Fatal("same branch name in two repos, argument order kept", got, err)
	}
	if _, err = s.AssignTargets([]Target{ParseTarget("paymnts:agent/x")}); err == nil || !strings.Contains(err.Error(), "registered repos: orders, payments") {
		t.Fatal("unknown repo", err)
	}
	bases, err := s.AssignBases([]string{"develop", "payments:release/1"})
	if err != nil || bases["orders"] != "develop" || bases["payments"] != "release/1" {
		t.Fatal("bases", bases, err)
	}
	if _, err = s.AssignBases([]string{"main", "orders:develop"}); err == nil {
		t.Fatal("two bases for one repo")
	}
	if q := s.Qualify(ParseTargets([]string{"agent/a", "payments:b"}, func(Target) bool { return true })); !reflect.DeepEqual(q, []string{"orders:agent/a", "payments:b"}) {
		t.Fatal("qualify", q)
	}
	only, _ := ResolveScope(ScopeInput{Registry: registryWith(orders, payments), Current: orders, Only: []string{"payments"}, Usable: allUsable})
	if _, err = only.AssignTargets([]Target{ParseTarget("agent/a")}); err == nil || !strings.Contains(err.Error(), "--only excludes") {
		t.Fatal("unqualified target for an excluded current repo", err)
	}
}

func TestAddAndRemove(t *testing.T) {
	orders, payments, web := loc("orders"), loc("payments"), loc("web")
	r := Registry{Version: RegistryVersion}

	res, err := r.Add(orders, payments, "", "", allUsable)
	if err != nil || !res.Created || res.Workspace != "orders" || len(r.Workspaces[0].Repos) != 2 {
		t.Fatal("create", res, err)
	}
	// Same repo again, or one of its worktrees: unchanged.
	for _, again := range []Location{payments, worktreeOf(payments, "agent"), worktreeOf(orders, "agent")} {
		before := fmt.Sprint(r)
		if res, err = r.Add(orders, again, "", "", allUsable); err != nil || !res.Unchanged || fmt.Sprint(r) != before {
			t.Fatal("re-add", again.Root, res, err)
		}
	}
	// Default ID collision requires --id.
	other := Location{Root: "/x/payments", CommonDir: "/x/payments/.git", MainPath: "/x/payments"}
	if _, err = r.Add(orders, other, "", "", allUsable); err == nil || !strings.Contains(err.Error(), "--id payments_2") {
		t.Fatal("ID collision", err)
	}
	if _, err = r.Add(orders, web, "9web", "", allUsable); err == nil || !strings.Contains(err.Error(), "invalid ID") {
		t.Fatal("invalid ID", err)
	}
	// A moved repository: same ID, new location.
	moved := Location{Root: "/moved/payments", CommonDir: "/moved/payments/.git", MainPath: "/moved/payments"}
	if res, err = r.Add(orders, moved, "payments", "", allUsable); err != nil || res.Updated == nil || res.Updated.Old != payments.MainPath || res.Updated.New != moved.MainPath {
		t.Fatal("moved with --id", res, err)
	}
	back := func(Repo) string { return "path is not available" }
	if res, err = r.Add(orders, payments, "", "", back); err != nil || res.Updated == nil {
		t.Fatal("moved back, old path gone", res, err)
	}
	// A repo of one workspace cannot join another.
	r.Workspaces = append(r.Workspaces, Workspace{Name: "blog", Repos: []Repo{{ID: "web", Path: web.MainPath, CommonDir: web.CommonDir}}})
	if _, err = r.Add(orders, web, "", "", allUsable); err == nil || !strings.Contains(err.Error(), "workspace remove web") {
		t.Fatal("other workspace", err)
	}
	if err = r.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, _, err = r.Remove(orders, "nope"); err == nil || !strings.Contains(err.Error(), "registered repos: orders, payments") {
		t.Fatal("remove unknown", err)
	}
	if name, repo, err := r.Remove(orders, "payments"); err != nil || name != "orders" || repo.ID != "payments" || len(r.Workspaces[0].Repos) != 1 {
		t.Fatal("remove", err)
	}
	if _, _, err = r.Remove(orders, "orders"); err != nil || r.Named("orders") >= 0 {
		t.Fatal("empty workspace is deleted", r, err)
	}
}

func TestRegistryConcurrentUpdates(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- Update(dir, func(r *Registry) error {
				name := fmt.Sprintf("w%02d", i)
				root := filepath.Join(string(filepath.Separator), "src", name)
				r.Workspaces = append(r.Workspaces, Workspace{Name: name, Repos: []Repo{{ID: name, Path: root, CommonDir: filepath.Join(root, ".git")}}})
				return nil
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	r, err := Load(dir)
	if err != nil || len(r.Workspaces) != 16 {
		t.Fatal("lost or corrupted concurrent updates", len(r.Workspaces), err)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*")); len(matches) > 0 {
		t.Fatal("temporary files left", matches)
	}
	if _, err = os.Stat(filepath.Join(dir, RegistryFile+".lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("lock left behind", err)
	}
	// A failing update leaves the registry untouched.
	if err = Update(dir, func(r *Registry) error { r.Workspaces = nil; return errors.New("stop") }); err == nil {
		t.Fatal("error swallowed")
	}
	if r, _ = Load(dir); len(r.Workspaces) != 16 {
		t.Fatal("failed update was written")
	}
}

func TestStaleLockTakeoverKeepsTheNewLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), RegistryFile+".lock")
	old := time.Now().Add(-2 * staleLock)
	os.WriteFile(path, []byte("1 crashed\n"), 0o600)
	os.Chtimes(path, old, old)
	// Every waiter that saw the stale lock tries to break it; after the
	// first takes the lock, the rest must leave that fresh lock alone.
	var wg sync.WaitGroup
	var mu sync.Mutex
	holders, maxHolders := 0, 0
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := lock(path)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			holders++
			maxHolders = max(maxHolders, holders)
			mu.Unlock()
			time.Sleep(5 * time.Millisecond)
			mu.Lock()
			holders--
			mu.Unlock()
			unlock()
		}()
	}
	wg.Wait()
	if maxHolders != 1 {
		t.Fatal("lock held by", maxHolders, "writers at once")
	}
	// A writer whose lock was taken over does not remove its successor's.
	unlock, err := lock(path)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte("2 successor\n"), 0o600)
	unlock()
	if b, _ := os.ReadFile(path); string(b) != "2 successor\n" {
		t.Fatal("removed another holder's lock", string(b))
	}
	if _, err = os.Stat(path + ".break"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("break lock left behind", err)
	}
}

func TestLoadRejectsInvalidRegistry(t *testing.T) {
	dir := t.TempDir()
	if r, err := Load(dir); err != nil || len(r.Workspaces) != 0 {
		t.Fatal("missing registry is empty", err)
	}
	for _, body := range []string{"{", `{"version":2,"workspaces":[]}`, `{"version":1,"workspaces":[{"name":"a","repos":[{"id":"x","path":"rel","common_dir":"/x/.git"}]}]}`} {
		os.WriteFile(filepath.Join(dir, RegistryFile), []byte(body), 0o600)
		if _, err := Load(dir); err == nil {
			t.Fatal("accepted", body)
		}
	}
}

func TestShellPathStaysOneArgument(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip(err)
	}
	for _, p := range []string{"/src/orders", "/src/my repo", "/src/a\nrm -rf x", "/src/a\nb", "/src/it's", "~/x", "/src/$(id)", "/src/a\tb", "-x", ""} {
		out, err := exec.Command("sh", "-c", "set -- "+ShellPath(p)+"; printf '%s|%s' \"$#\" \"$1\"").CombinedOutput()
		if err != nil || string(out) != "1|"+p {
			t.Errorf("%q quoted as %s: %q %v", p, ShellPath(p), out, err)
		}
	}
	if ShellPath("/src/orders") != "/src/orders" {
		t.Error("safe path quoted")
	}
}
