package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Jake-Network/radar/internal/workspace"
)

// shop is two repositories, orders and payments, with agent worktrees:
// orders has agent/api and agent/rounding, payments has agent/client.
type shop struct {
	t                      *testing.T
	base, orders, payments string
}

const (
	ordersTest   = "import unittest\nfrom api import total\nclass T(unittest.TestCase):\n    def test_total(self):\n        self.assertGreaterEqual(total(), 1)\n"
	utilTest     = "import unittest\nfrom util import X\nclass T(unittest.TestCase):\n    def test_x(self):\n        self.assertGreaterEqual(X, 1)\n"
	paymentsTest = "import unittest\nfrom client import TOTAL\nclass T(unittest.TestCase):\n    def test_total(self):\n        self.assertGreaterEqual(TOTAL, 1)\n"
)

func newShop(t *testing.T) *shop {
	t.Helper()
	if _, e := exec.LookPath("git"); e != nil {
		t.Skip(e)
	}
	t.Setenv("RADAR_CONFIG_DIR", t.TempDir())
	t.Setenv("RADAR_STATE_DIR", t.TempDir())
	s := &shop{t: t, base: realTempDir(t)}
	s.orders = s.repo("orders", map[string]string{"util.py": "X = 1\n", "api.py": "from util import X\n\ndef total():\n    return 1 * X\n", "test_api.py": ordersTest, "test_util.py": utilTest})
	s.payments = s.repo("payments", map[string]string{"client.py": "TOTAL = 1\n", "test_client.py": paymentsTest})
	s.agent(s.orders, "agent/api", map[string]string{"api.py": "from util import X\n\ndef total():\n    return 2 * X\n"})
	s.agent(s.orders, "agent/rounding", map[string]string{"util.py": "X = 2\n"})
	s.agent(s.payments, "agent/client", map[string]string{"client.py": "TOTAL = 2\n"})
	return s
}

func (s *shop) repo(name string, files map[string]string) string {
	root := filepath.Join(s.base, name)
	os.MkdirAll(root, 0o700)
	gitTest(s.t, root, "init", "-q")
	gitTest(s.t, root, "config", "user.email", "radar@example.invalid")
	gitTest(s.t, root, "config", "user.name", "Radar test")
	gitTest(s.t, root, "checkout", "-q", "-b", "main")
	for p, c := range files {
		put(s.t, root, p, c)
	}
	gitTest(s.t, root, "add", ".")
	gitTest(s.t, root, "commit", "-qm", "baseline")
	return root
}

// wt is the worktree directory of a branch.
func (s *shop) wt(root, branch string) string {
	return filepath.Join(s.base, "wt", filepath.Base(root)+"-"+strings.ReplaceAll(branch, "/", "-"))
}

func (s *shop) agent(root, branch string, changes map[string]string) string {
	dir := s.wt(root, branch)
	gitTest(s.t, root, "worktree", "add", "-q", "-b", branch, dir, "main")
	if len(changes) > 0 {
		s.commit(dir, changes)
	}
	return dir
}

func (s *shop) commit(dir string, changes map[string]string) string {
	for p, c := range changes {
		put(s.t, dir, p, c)
	}
	gitTest(s.t, dir, "add", ".")
	gitTest(s.t, dir, "commit", "-qm", "change")
	return gitTest(s.t, dir, "rev-parse", "HEAD")
}

func (s *shop) register() {
	s.t.Helper()
	invoke(s.t, s.orders, 0, "workspace", "add", s.payments, "--name", "shop")
}

// gateJSON runs radar gate --json in dir and returns exit code and report.
func gateJSON(t *testing.T, dir string, args ...string) (int, map[string]any) {
	t.Helper()
	var out, errs bytes.Buffer
	code := Run(context.Background(), append([]string{"--root", dir, "--json", "gate"}, args...), &out, &errs)
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("gate %v: %v\n%s\n%s", args, err, out.String(), errs.String())
	}
	return code, v
}

func repoOf(t *testing.T, report map[string]any, id string) map[string]any {
	t.Helper()
	for _, raw := range report["repos"].([]any) {
		if r := raw.(map[string]any); r["id"] == id {
			return r
		}
	}
	t.Fatalf("repo %s missing: %v", id, report["repos"])
	return nil
}

func refs(r map[string]any) []string {
	out := []string{}
	for _, b := range r["branches"].([]any) {
		out = append(out, b.(map[string]any)["ref"].(string))
	}
	return out
}

func commitOf(r map[string]any, ref string) string {
	for _, b := range r["branches"].([]any) {
		if b := b.(map[string]any); b["ref"] == ref {
			return b["commit"].(string)
		}
	}
	return ""
}

func registryText(t *testing.T) string {
	b, _ := os.ReadFile(filepath.Join(os.Getenv("RADAR_CONFIG_DIR"), workspace.RegistryFile))
	return string(b)
}

func TestWorkspaceAddRegistersOnceFromAnyCheckout(t *testing.T) {
	s := newShop(t)
	code, out, _ := run(t, s.orders, "workspace", "add", s.payments)
	if code != 0 || !strings.Contains(out, `Created workspace "orders": orders (`) || !strings.Contains(out, "Next: radar gate") {
		t.Fatal(code, out)
	}
	before := registryText(t)
	// The same repository, and its agent worktree, are recognized as payments.
	for _, path := range []string{s.payments, s.wt(s.payments, "agent/client"), s.wt(s.orders, "agent/api")} {
		r := invoke(t, s.orders, 0, "workspace", "add", path)
		result := r["result"].(map[string]any)
		if result["unchanged"] != true {
			t.Fatal("re-add must change nothing", path, r)
		}
	}
	if r := invoke(t, s.orders, 0, "workspace", "add", s.wt(s.payments, "agent/client")); r["result"].(map[string]any)["repo"].(map[string]any)["id"] != "payments" {
		t.Fatal("worktree must map to its repository's ID", r)
	}
	if registryText(t) != before {
		t.Fatal("registry changed by a re-add")
	}
}

func TestWorkspaceAddErrors(t *testing.T) {
	s := newShop(t)
	s.register()
	// Default ID collision: another repository folder also named payments.
	other := s.repo(filepath.Join("elsewhere", "payments"), map[string]string{"a.py": "A = 1\n"})
	code, _, errs := run(t, s.orders, "workspace", "add", other)
	if code != 2 || !strings.Contains(errs, "already used") || !strings.Contains(errs, "Next: radar workspace add "+other+" --id payments_2") {
		t.Fatal("ID collision", code, errs)
	}
	if code, _, errs = run(t, s.orders, "workspace", "add", other, "--id", "9pay"); code != 2 || !strings.Contains(errs, "invalid ID") {
		t.Fatal("invalid ID", code, errs)
	}
	invoke(t, s.orders, 0, "workspace", "add", other, "--id", "payments_eu")
	// A repository in another workspace cannot join this one.
	blog := s.repo("blog", map[string]string{"a.py": "A = 1\n"})
	web := s.repo("web", map[string]string{"a.py": "A = 1\n"})
	invoke(t, blog, 0, "workspace", "add", web)
	code, _, errs = run(t, s.orders, "workspace", "add", web)
	if code != 2 || !strings.Contains(errs, `workspace "blog"`) || !strings.Contains(errs, "Next: radar --root "+web+" workspace remove web") {
		t.Fatal("other workspace", code, errs)
	}
	r := invoke(t, s.orders, 2, "workspace", "add", web)
	if !strings.Contains(r["next"].(string), "workspace remove web") {
		t.Fatal("JSON errors carry the next action", r)
	}
}

func TestWorkspaceMovedRepositoryAndRemove(t *testing.T) {
	s := newShop(t)
	s.register()
	moved := filepath.Join(s.base, "moved", "payments")
	os.MkdirAll(filepath.Dir(moved), 0o700)
	if err := os.Rename(s.payments, moved); err != nil {
		t.Fatal(err)
	}
	gitTest(t, moved, "worktree", "repair")
	// The moved repository is an error; orders is still checked.
	code, out, _ := run(t, s.orders, "gate")
	if code != 2 || !strings.Contains(out, "Radar gate: ERROR") || !strings.Contains(out, "✓ orders") || !strings.Contains(out, "next: radar workspace add <NEW PATH> --id payments") {
		t.Fatal("missing path", code, out)
	}
	code, out, _ = run(t, s.orders, "workspace", "add", moved)
	if code != 0 || !strings.Contains(out, "Updated payments") || !strings.Contains(out, s.payments+" → "+moved) {
		t.Fatal("moved path update", code, out)
	}
	if code, out, _ = run(t, s.orders, "gate"); code != 0 || !strings.Contains(out, "✓ payments") {
		t.Fatal("gate after update", code, out)
	}
	code, out, _ = run(t, s.orders, "workspace", "remove", "payments")
	if code != 0 || !strings.Contains(out, "Removed payments") {
		t.Fatal("remove", code, out)
	}
	if code, _, errs := run(t, s.orders, "workspace", "remove", "paymnts"); code != 2 || !strings.Contains(errs, "registered repos: orders") {
		t.Fatal("remove unknown", code, errs)
	}
	if show := invoke(t, s.orders, 0, "workspace", "show"); len(show["repos"].([]any)) != 1 {
		t.Fatal("show after remove", show)
	}
}

func TestWorkspaceConcurrentAdds(t *testing.T) {
	s := newShop(t)
	web := s.repo("web", map[string]string{"a.py": "A = 1\n"})
	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i, path := range []string{s.payments, web} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out, errs bytes.Buffer
			codes[i] = Run(context.Background(), []string{"--root", s.orders, "workspace", "add", path}, &out, &errs)
		}()
	}
	wg.Wait()
	if codes[0] != 0 || codes[1] != 0 {
		t.Fatal(codes)
	}
	registry, err := workspace.Load(os.Getenv("RADAR_CONFIG_DIR"))
	if err != nil || len(registry.Workspaces) != 1 || len(registry.Workspaces[0].Repos) != 3 {
		t.Fatalf("concurrent adds lost an update: %+v %v", registry, err)
	}
}

func TestWorkspaceGateSameScopeFromEveryCheckout(t *testing.T) {
	s := newShop(t)
	s.register()
	var digest string
	for _, dir := range []string{s.orders, s.wt(s.orders, "agent/api"), s.payments} {
		code, r := gateJSON(t, dir)
		if code != 0 || r["workspace"] != "shop" || r["scope_source"] != "registry" || r["repo_count"] != 2.0 || r["branch_count"] != 3.0 {
			t.Fatal(dir, code, r["scope_summary"], r["verdict"])
		}
		if got := refs(repoOf(t, r, "orders")); strings.Join(got, " ") != "agent/api agent/rounding" {
			t.Fatal("orders auto selection", got)
		}
		if digest == "" {
			digest = r["digest"].(string)
		} else if r["digest"] != digest {
			t.Fatal("digest differs between checkouts", dir, r["digest"], digest)
		}
		if r["cross_repo"].(map[string]any)["status"] != "not_checked" || !strings.HasPrefix(r["next"].(string), "radar gate --again") {
			t.Fatal(r["cross_repo"], r["next"])
		}
	}
	code, out, _ := run(t, s.orders, "gate")
	for _, want := range []string{`Radar gate: PASS (static) — workspace "shop" · 2 repos · 3 branches · per-repo checks only · 0 cross-repo links checked`, "(auto: worktree)", "✓ orders    2 branches combined", "✓ payments  1 branch combined", "! cross-repo links: not checked — this version checks each repo separately", "Next: radar gate --again --run"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestWorkspaceWithAndOnly(t *testing.T) {
	s := newShop(t)
	// Not registered: --with builds a one-off scope that includes this repo.
	code, r := gateJSON(t, s.orders, "--with", "../payments")
	if code != 0 || r["workspace"] != "" || r["one_off"] != true || r["repo_count"] != 2.0 || !strings.Contains(r["scope_summary"].(string), "one-off scope (--with)") {
		t.Fatal("--with", code, r["scope_summary"])
	}
	repoOf(t, r, "orders")
	// --again finds the one-off run from another worktree of orders.
	if code, again := gateJSON(t, s.wt(s.orders, "agent/api"), "--again"); code != 0 || again["repo_count"] != 2.0 || again["digest"] != r["digest"] {
		t.Fatal("--again of a one-off scope", code, again["error"])
	}
	s.register()
	code, out, _ := run(t, s.orders, "gate", "--only", "payments")
	if code != 0 || !strings.Contains(out, `workspace "shop" · 1 repo · 1 branch · per-repo checks only · 0 cross-repo links checked · partial workspace (1/2 repos)`) || strings.Contains(out, "✓ orders") {
		t.Fatal("--only", code, out)
	}
	if code, _, errs := run(t, s.orders, "gate", "--only", "paymnts"); code != 2 || !strings.Contains(errs, "registered repos: orders, payments") {
		t.Fatal("unknown --only", errs)
	}
}

func TestWorkspaceSelection(t *testing.T) {
	s := newShop(t)
	s.register()
	// A target in this repo's syntax selects only it; payments stays at base.
	_, r := gateJSON(t, s.orders, "agent/api")
	o, p := repoOf(t, r, "orders"), repoOf(t, r, "payments")
	if strings.Join(refs(o), " ") != "agent/api" || o["selection_source"] != "named" || len(refs(p)) != 0 || p["selection_source"] != "base only" {
		t.Fatal("named selection", refs(o), p["selection_source"])
	}
	_, r = gateJSON(t, s.orders, "payments:agent/client")
	if len(refs(repoOf(t, r, "orders"))) != 0 || strings.Join(refs(repoOf(t, r, "payments")), " ") != "agent/client" {
		t.Fatal("repo:ref selection")
	}
	code, out, errs := run(t, s.orders, "gate", "paymnts:agent/client")
	if code != 2 || !strings.Contains(errs, `unknown repo "paymnts"`) || !strings.Contains(errs, "registered repos: orders, payments") {
		t.Fatal("unknown repo", code, out, errs)
	}
	// The same branch name in two repos stays distinct.
	sharedOrders := s.agent(s.orders, "agent/shared", map[string]string{"util.py": "X = 3\n"})
	sharedPayments := s.agent(s.payments, "agent/shared", map[string]string{"client.py": "TOTAL = 3\n"})
	_, r = gateJSON(t, s.orders, "agent/shared", "payments:agent/shared")
	if commitOf(repoOf(t, r, "orders"), "agent/shared") != gitTest(t, sharedOrders, "rev-parse", "HEAD") || commitOf(repoOf(t, r, "payments"), "agent/shared") != gitTest(t, sharedPayments, "rev-parse", "HEAD") {
		t.Fatal("same branch name in two repos")
	}
	// Commas: a ref containing one, and a list of repo:ref targets.
	gitTest(t, s.orders, "branch", "fix,comma", "agent/api")
	if _, r = gateJSON(t, s.orders, "fix,comma"); strings.Join(refs(repoOf(t, r, "orders")), " ") != "fix,comma" {
		t.Fatal("comma ref", refs(repoOf(t, r, "orders")))
	}
	_, r = gateJSON(t, s.orders, "orders:agent/api,payments:agent/client")
	if strings.Join(refs(repoOf(t, r, "orders")), " ") != "agent/api" || strings.Join(refs(repoOf(t, r, "payments")), " ") != "agent/client" {
		t.Fatal("comma list of targets")
	}
}

func TestWorkspaceDetachedDirtyAndBase(t *testing.T) {
	s := newShop(t)
	s.register()
	detached := filepath.Join(s.base, "wt", "payments-detached")
	gitTest(t, s.payments, "worktree", "add", "-q", "--detach", detached, "main")
	sha := s.commit(detached, map[string]string{"client.py": "TOTAL = 5\n"})
	put(t, s.wt(s.orders, "agent/rounding"), "notes.txt", "wip\n")
	idle := s.agent(s.orders, "agent/idle", nil)
	put(t, idle, "idle.txt", "wip\n")
	code, out, _ := run(t, s.orders, "gate")
	if code != 0 || !strings.Contains(out, "! payments  detached worktree "+detached) || !strings.Contains(out, "radar gate payments:"+sha) {
		t.Fatal("detached worktree guidance", out)
	}
	if !strings.Contains(out, "· orders:agent/rounding  committed changes only (1 uncommitted excluded)") || strings.Contains(out, "idle.txt") || strings.Contains(out, "orders:agent/idle  committed") {
		t.Fatal("dirty line only for selected worktrees", out)
	}
	_, r := gateJSON(t, s.orders)
	dirty := repoOf(t, r, "orders")["dirty"].([]any)
	if len(dirty) != 2 {
		t.Fatal("all dirty worktrees stay in JSON", dirty)
	}
	// Base detection fails only for payments; orders is still checked.
	gitTest(t, s.payments, "branch", "-m", "main", "develop")
	code, out, _ = run(t, s.orders, "gate")
	if code != 2 || !strings.Contains(out, "✓ orders") || !strings.Contains(out, "next: radar gate --base payments:<REF>") {
		t.Fatal("base failure", code, out)
	}
	code, r = gateJSON(t, s.orders, "--base", "payments:develop")
	if code != 0 || repoOf(t, r, "payments")["base_ref"] != "develop" || repoOf(t, r, "payments")["base_source"] != "named" {
		t.Fatal("--base payments:develop", code, r["verdict"])
	}
}

func TestWorkspaceConflictAndSourcesUntouched(t *testing.T) {
	s := newShop(t)
	s.register()
	s.agent(s.orders, "agent/clash", map[string]string{"api.py": "def total():\n    return 7\n"})
	snapshot := func() string {
		out := ""
		for _, dir := range []string{s.orders, s.payments, s.wt(s.orders, "agent/api"), s.wt(s.payments, "agent/client")} {
			out += gitTest(t, dir, "for-each-ref", "--format=%(refname) %(objectname)") + gitTest(t, dir, "rev-parse", "HEAD") + gitTest(t, dir, "status", "--porcelain=v1", "--untracked-files=all") + gitTest(t, dir, "ls-files", "-s")
		}
		return out
	}
	before := snapshot()
	code, out, _ := run(t, s.orders, "gate")
	if code != 1 || !strings.Contains(out, "Radar gate: FAIL") || !strings.Contains(out, "✗ orders    branches conflict: api.py") || !strings.Contains(out, "✓ payments") {
		t.Fatal("conflict in orders must not hide payments", code, out)
	}
	if !strings.Contains(out, "Repair leads (where to look, not proof of cause)") || !strings.Contains(out, "orders:agent/api  api.py") || !strings.Contains(out, "Next: repair and commit on the branches above, then: radar gate --again") {
		t.Fatal("repair leads", out)
	}
	if snapshot() != before {
		t.Fatal("gate changed a source repository")
	}
	// An unusable payments does not hide the conflict in orders.
	if err := os.Rename(s.payments, s.payments+"-moved"); err != nil {
		t.Fatal(err)
	}
	code, r := gateJSON(t, s.orders)
	if code != 1 || r["verdict"] != "fail" || repoOf(t, r, "payments")["verdict"] != "error" || r["next"] != "repair and commit on the branches above and resolve the errors above, then: radar gate --again" {
		t.Fatal("conflict with an unusable repo", code, r["verdict"], r["next"])
	}
}

func TestWorkspaceSymlinkRepoWarnsAndErrors(t *testing.T) {
	s := newShop(t)
	if err := os.Symlink("client.py", filepath.Join(s.payments, "alias.py")); err != nil {
		t.Skip(err)
	}
	gitTest(t, s.payments, "add", ".")
	gitTest(t, s.payments, "commit", "-qm", "symlink")
	code, out, _ := run(t, s.orders, "workspace", "add", s.payments)
	if code != 0 || !strings.Contains(out, "! payments: trees with submodules or symlinks cannot be combined") {
		t.Fatal("add warning", out)
	}
	if code, out, _ = run(t, s.orders, "workspace", "show"); !strings.Contains(out, "submodules or symlinks") {
		t.Fatal("show warning", out)
	}
	code, out, _ = run(t, s.orders, "gate")
	if code != 2 || !strings.Contains(out, "register repos bundled as submodules separately with radar workspace add") || !strings.Contains(out, "✓ orders") {
		t.Fatal("gate error", code, out)
	}
}

func TestWorkspaceAgainAndReplay(t *testing.T) {
	s := newShop(t)
	s.register()
	if code, _, errs := run(t, s.orders, "gate", "--again"); code != 2 || !strings.Contains(errs, "Next: radar gate") {
		t.Fatal("--again without a previous run", errs)
	}
	_, first := gateJSON(t, s.orders)
	// Auto mode: a new branch joins and is called out.
	s.agent(s.payments, "agent/extra", map[string]string{"extra.py": "E = 1\n"})
	code, out, _ := run(t, s.orders, "gate", "--again")
	if code != 0 || !strings.Contains(out, "! again: branches changed since run "+first["run_id"].(string)+": + payments:agent/extra") {
		t.Fatal("--again participation change", out)
	}
	// Named mode: the same refs resolve to their latest commits.
	_, named := gateJSON(t, s.orders, "agent/api")
	old := commitOf(repoOf(t, named, "orders"), "agent/api")
	head := s.commit(s.wt(s.orders, "agent/api"), map[string]string{"api.py": "from util import X\n\ndef total():\n    return 3 * X\n"})
	_, again := gateJSON(t, s.payments, "--again")
	if commitOf(repoOf(t, again, "orders"), "agent/api") != head || len(refs(repoOf(t, again, "payments"))) != 0 || again["again"].(map[string]any)["moved"].([]any)[0] != "orders:agent/api" {
		t.Fatal("--again in named mode", again["again"])
	}
	// Replay rebuilds the recorded commits, not the branch's new head.
	code, replay := gateJSON(t, s.orders, "--replay", named["run_id"].(string))
	if code != 0 || commitOf(repoOf(t, replay, "orders"), "agent/api") != old || replay["digest"] != named["digest"] || replay["scope_source"] != "replay" || !strings.Contains(replay["scope_summary"].(string), "replay of "+named["run_id"].(string)) {
		t.Fatal("replay", code, replay["verdict"], replay["digest"], named["digest"])
	}
	// Replay takes precedence over the registry: payments was removed since.
	invoke(t, s.orders, 0, "workspace", "remove", "payments")
	if code, replay = gateJSON(t, s.orders, "--replay", first["run_id"].(string)); code != 0 || replay["repo_count"] != 2.0 || replay["digest"] != first["digest"] {
		t.Fatal("replay must use the recorded repositories", code, replay["repo_count"])
	}
	invoke(t, s.orders, 0, "workspace", "add", s.payments)
	if code, _, errs := run(t, s.orders, "gate", "--replay", "last", "agent/api"); code != 2 || !strings.Contains(errs, "Next: radar gate --replay last, or radar gate --again") {
		t.Fatal("--replay with a selection", errs)
	}
	if code, _, errs := run(t, s.orders, "gate", "--again", "--only", "orders"); code != 2 || !strings.Contains(errs, "--again repeats") {
		t.Fatal("--again with a selection", errs)
	}
}

func TestWorkspaceReplayWithoutObjects(t *testing.T) {
	s := newShop(t)
	s.register()
	gitTest(t, s.payments, "branch", "tmp", "main")
	tmp := filepath.Join(s.base, "wt", "payments-tmp")
	gitTest(t, s.payments, "worktree", "add", "-q", tmp, "tmp")
	s.commit(tmp, map[string]string{"gone.py": "G = 1\n"})
	_, r := gateJSON(t, s.orders, "payments:tmp")
	gitTest(t, s.payments, "worktree", "remove", "--force", tmp)
	gitTest(t, s.payments, "branch", "-D", "tmp")
	gitTest(t, s.payments, "reflog", "expire", "--expire=now", "--all")
	gitTest(t, s.payments, "gc", "-q", "--prune=now")
	code, out, _ := run(t, s.orders, "gate", "--replay", r["run_id"].(string))
	if code != 2 || !strings.Contains(out, "not reproducible: commit") || !strings.Contains(out, "✓ orders") {
		t.Fatal("replay without objects", code, out)
	}
}

func TestWorkspaceReplayUsesTheCheckoutWithTheCommits(t *testing.T) {
	s := newShop(t)
	s.register()
	_, r := gateJSON(t, s.orders)
	// payments is re-registered as a fresh clone without agent/client; the
	// recorded checkout still has the recorded commits.
	clone := filepath.Join(s.base, "payments-clone")
	gitTest(t, s.base, "clone", "-q", "--no-local", "--single-branch", "--branch", "main", s.payments, clone)
	invoke(t, s.orders, 0, "workspace", "remove", "payments")
	invoke(t, s.orders, 0, "workspace", "add", clone, "--id", "payments")
	code, replay := gateJSON(t, s.orders, "--replay", r["run_id"].(string))
	if code != 0 || replay["digest"] != r["digest"] || repoOf(t, replay, "payments")["path"] != s.payments {
		t.Fatal("replay picked a checkout without the recorded commits", code, repoOf(t, replay, "payments")["path"], repoOf(t, replay, "payments")["error"])
	}
}

func TestWorkspaceConcurrentGatesKeepBothRecords(t *testing.T) {
	s := newShop(t)
	s.register()
	var wg sync.WaitGroup
	reports := make([]map[string]any, 2)
	for i, dir := range []string{s.orders, s.payments} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out, errs bytes.Buffer
			Run(context.Background(), []string{"--root", dir, "--json", "gate"}, &out, &errs)
			json.Unmarshal(out.Bytes(), &reports[i])
		}()
	}
	wg.Wait()
	a, b := reports[0]["run_id"], reports[1]["run_id"]
	if a == nil || b == nil || a == b {
		t.Fatal("concurrent runs", a, b)
	}
	for _, id := range []any{a, b} {
		if code, _ := gateJSON(t, s.orders, "--replay", id.(string)); code != 0 {
			t.Fatal("record lost", id)
		}
	}
}

func TestWorkspaceRunExecutesPerRepo(t *testing.T) {
	if _, e := exec.LookPath("python3"); e != nil {
		t.Skip(e)
	}
	s := newShop(t)
	s.register()
	code, r := gateJSON(t, s.orders, "--run")
	if code != 0 || r["verdict"] != "pass" || r["cross_repo_execution"].(map[string]any)["status"] != "not_checked" {
		t.Fatal("--run", code, r["verdict"], r["next"])
	}
	for _, id := range []string{"orders", "payments"} {
		report := repoOf(t, r, id)["report"].(map[string]any)
		if len(report["executions"].([]any)) == 0 {
			t.Fatal(id, "executed nothing")
		}
	}
	// payments takes part at its base: nothing is combined there, so
	// nothing is run or required there.
	code, r = gateJSON(t, s.orders, "--run", "orders:agent/api")
	payments := repoOf(t, r, "payments")
	executions, _ := payments["report"].(map[string]any)["executions"].([]any)
	if code != 0 || r["verdict"] != "pass" || payments["verdict"] != "pass" || len(executions) != 0 {
		t.Fatal("--run with a base-only repo", code, r["verdict"], payments["verdict"], r["next"])
	}
	code, out, _ := run(t, s.orders, "gate", "--run")
	if code != 0 || !strings.Contains(out, "! cross-repo execution: not checked — this version checks each repo separately") || !strings.Contains(out, "per-repo checks only") {
		t.Fatal(out)
	}
	s.commit(s.wt(s.payments, "agent/client"), map[string]string{"client.py": "TOTAL = 0\n"})
	code, r = gateJSON(t, s.orders, "--again", "--run")
	if code != 1 || repoOf(t, r, "payments")["verdict"] != "fail" || repoOf(t, r, "orders")["verdict"] != "pass" || r["next"] != "repair and commit on the branches above, then: radar gate --again --run" {
		t.Fatal("failing payments tests", code, r["next"])
	}
}

func TestBrokenRegistryKeepsTheSingleRepositoryGate(t *testing.T) {
	s := newShop(t)
	os.WriteFile(filepath.Join(os.Getenv("RADAR_CONFIG_DIR"), workspace.RegistryFile), []byte(`{"version":2,"workspaces":[]}`), 0o600)
	code, out, errs := run(t, s.orders, "gate", "agent/api")
	if code != 0 || !strings.Contains(out, "Radar gate: PASS") || !strings.Contains(errs, "warning: workspace registry not read") || !strings.Contains(errs, "without its workspace") {
		t.Fatal("broken registry stopped the single-repository gate", code, out, errs)
	}
	// Workspace options need the registry.
	if code, _, errs := run(t, s.orders, "gate", "--only", "orders"); code != 2 || !strings.Contains(errs, "fix or remove the registry file") {
		t.Fatal("--only with a broken registry", code, errs)
	}
}

func TestWorkspacePolicyAndPlan(t *testing.T) {
	s := newShop(t)
	s.register()
	put(t, s.orders, "policy.json", `{"version":1,"name":"merge-only","require":["textual_merge"]}`)
	code, r := gateJSON(t, s.orders, "--policy", "policy.json")
	if code != 0 {
		t.Fatal(code, r)
	}
	for _, id := range []string{"orders", "payments"} {
		if repoOf(t, r, id)["report"].(map[string]any)["gate"].(map[string]any)["policy"].(map[string]any)["name"] != "merge-only" {
			t.Fatal("policy applies to every repo", id)
		}
	}
	if code, _, errs := run(t, s.orders, "gate", "--plan", "plan.json"); code != 2 || !strings.Contains(errs, "Next: radar gate --only orders --plan plan.json") {
		t.Fatal("--plan in a workspace", errs)
	}
}

func TestHelpShowsOnlyImplementedWorkspaceSurface(t *testing.T) {
	root := t.TempDir()
	_, help, _ := run(t, root, "help")
	_, gateHelp, _ := run(t, root, "gate", "--help")
	_, wsHelp, _ := run(t, root, "help", "workspace")
	_, all, _ := run(t, root, "help", "--all")
	if !strings.Contains(help, "  workspace ") || !strings.Contains(help, "radar workspace add") {
		t.Fatal("help lists workspace", help)
	}
	for _, want := range []string{"[REPO:]BRANCH", "-with PATH", "-again", "-run"} {
		if !strings.Contains(gateHelp, want) {
			t.Fatalf("gate --help lacks %q:\n%s", want, gateHelp)
		}
	}
	for _, text := range []string{help, gateHelp, wsHelp} {
		for _, hidden := range []string{"-only", "-replay", "remove"} {
			if strings.Contains(text, hidden) {
				t.Fatalf("everyday help shows advanced %q:\n%s", hidden, text)
			}
		}
	}
	for _, want := range []string{"radar gate --only REPO", "radar gate --replay RUN", "radar gate --base REPO:REF", "radar workspace remove REPO"} {
		if !strings.Contains(all, want) {
			t.Fatalf("help --all lacks %q", want)
		}
	}
	// Later stages are not exposed anywhere.
	for _, text := range []string{help, gateHelp, wsHelp, all} {
		for _, later := range []string{"connect", "--workspace", "scenario", "consumes.json", "workspace.json"} {
			if strings.Contains(text, later) {
				t.Fatalf("help mentions unimplemented %q:\n%s", later, text)
			}
		}
	}
}
