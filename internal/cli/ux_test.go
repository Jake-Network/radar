package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/testselection"
	"github.com/Jake-Network/radar/internal/watch"
)

func doctorProjectOf(t *testing.T, root string) doctorProject {
	t.Helper()
	code, out, errs := run(t, root, "doctor", "--json")
	if code != 0 {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	var r struct {
		Project *doctorProject `json:"project"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.Project == nil {
		t.Fatalf("%v: %s", err, out)
	}
	return *r.Project
}

func TestDoctorFirstRunDetection(t *testing.T) {
	t.Run("agent branches, dirty worktree and runners", func(t *testing.T) {
		root, agent := agentRepo(t, map[string]string{
			"backend.py": "def price():\n    return 1\n", "test_backend.py": "import unittest\nfrom backend import price\nclass T(unittest.TestCase):\n    def test(self): self.assertEqual(price(), 1)\n",
			"web/package.json": `{"name":"web","devDependencies":{"jest":"1"}}`, "web/app.ts": "export const x = 1;\n", "web/app.test.ts": "import {x} from './app';\ntest('x', () => expect(x).toBe(1));\n",
		})
		agent("agent-a", map[string]string{"backend.py": "def price():\n    return 2\n"})
		dirty := agent("agent-b", map[string]string{"web/app.ts": "export const x = 2;\n"})
		agent("agent-idle", nil)
		put(t, dirty, "notes.txt", "draft\n")
		d := doctorProjectOf(t, root)
		if d.Base != "main" || strings.Join(d.Branches, ",") != "agent-a,agent-b" || len(d.Skipped) != 1 || d.Worktrees != 4 {
			t.Fatalf("%+v", d)
		}
		if len(d.Dirty) != 1 || d.Dirty[0].Branch != "agent-b" || d.Dirty[0].Untracked != 1 {
			t.Fatalf("dirty=%+v", d.Dirty)
		}
		statuses := map[string]string{}
		for _, r := range d.Tests {
			statuses[r.Framework] = r.Status
		}
		if statuses["jest"] != "not_copied" || statuses["unittest"] == "" {
			t.Fatalf("runners=%+v", d.Tests)
		}
		if !strings.HasPrefix(d.Next, "radar gate") || d.Agents["claude"] || d.Contracts {
			t.Fatalf("%+v", d)
		}
		code, out, _ := run(t, root, "doctor")
		for _, want := range []string{"Base        main", "agent-a, agent-b", "skipped: agent-idle", "agent-b: 0 staged, 0 unstaged, 1 untracked — not checked until committed", "? jest", "node_modules is not copied", "radar setup claude", "Next: radar gate"} {
			if code != 0 || !strings.Contains(out, want) {
				t.Fatalf("doctor lacks %q:\n%s", want, out)
			}
		}
	})
	t.Run("no worktrees yet", func(t *testing.T) {
		root, _ := agentRepo(t, map[string]string{"a.py": "A = 1\n"})
		d := doctorProjectOf(t, root)
		if len(d.Branches) != 0 || !strings.Contains(d.Next, "git worktree add") || len(d.Tests) != 0 {
			t.Fatalf("%+v", d)
		}
		_, out, _ := run(t, root, "doctor")
		if !strings.Contains(out, "no worktree branch has commits beyond main") || !strings.Contains(out, "no tests found") || !strings.Contains(out, "no uncommitted changes") {
			t.Fatal(out)
		}
	})
	t.Run("uncommitted work only", func(t *testing.T) {
		root, agent := agentRepo(t, map[string]string{"a.py": "A = 1\n"})
		dir := agent("agent-a", nil)
		put(t, dir, "a.py", "A = 2\n")
		if d := doctorProjectOf(t, root); !strings.HasPrefix(d.Next, "commit the agents' work") {
			t.Fatalf("%+v", d)
		}
		_, out, _ := run(t, root, "doctor")
		if strings.Contains(out, "no uncommitted changes") || !strings.Contains(out, "agent-a: 0 staged, 1 unstaged, 0 untracked") {
			t.Fatal(out)
		}
	})
	t.Run("unavailable dirty linked worktree", func(t *testing.T) {
		root, agent := agentRepo(t, map[string]string{"a.py": "A = 1\n"})
		dir := agent("agent-a", map[string]string{"a.py": "A = 2\n"})
		put(t, dir, "a.py", "A = 3\n")
		// Git records the physical worktree path (e.g. /private/var on macOS).
		physicalDir, err := filepath.EvalSymlinks(dir)
		if err != nil {
			t.Fatal(err)
		}
		// InspectWorktrees exposes native, cleaned paths on each platform.
		physicalDir = filepath.Clean(physicalDir)
		if err := os.Rename(dir, dir+"-offline"); err != nil {
			t.Fatal(err)
		}
		d := doctorProjectOf(t, root)
		if d.Worktrees != 2 || len(d.Dirty) != 0 || !strings.Contains(strings.Join(d.Problems, "\n"), "worktree inspection unavailable for "+physicalDir+":") || !strings.HasPrefix(d.Next, "restore access to the worktrees") {
			t.Fatalf("%+v", d)
		}
		code, out, _ := run(t, root, "doctor")
		if code != 0 || strings.Contains(out, "no uncommitted changes") || !strings.Contains(out, "inspection incomplete") || !strings.Contains(out, "worktree inspection unavailable for "+physicalDir+":") {
			t.Fatalf("%d %s", code, out)
		}
	})
	t.Run("not a repository", func(t *testing.T) {
		code, out, _ := run(t, t.TempDir(), "doctor")
		if code != 0 || !strings.Contains(out, "not a Git repository") || !strings.Contains(out, "Next:") {
			t.Fatalf("%d %s", code, out)
		}
		_, js, _ := run(t, t.TempDir(), "doctor", "--json")
		if strings.Contains(js, `"project"`) {
			t.Fatal("no project facts outside a repository", js)
		}
	})
}

func TestGateGapsExplainEachBlocker(t *testing.T) {
	jest := "./node_modules/.bin/jest --runInBand --runTestsByPath ./a.test.ts"
	r := integration.Report{Selection: &testselection.Selection{Mode: "balanced", MaxCommands: 2,
		Blocking: []string{
			"required command runner unavailable or unverified: " + jest,
			"environment_unavailable: " + jest,
			"required command omitted by budget: python3 -m unittest a",
			"required command omitted by budget: python3 -m unittest b",
			"time_budget_exhausted: go test ./...",
			"inventory incomplete: big.py: test/configuration exceeds 1 MiB discovery limit",
			"inventoried test unsupported: tests/odd.c",
			"no runnable verification command was selected for changed code",
			"something new",
		},
		Omitted: []testselection.Omission{{Command: strings.Fields(jest), Reason: "environment_unavailable", Explanation: "runner ./node_modules/.bin/jest is absent from the private candidate (untracked dependency directories such as node_modules are not copied); Radar does not install dependencies."}},
	}}
	gaps := gateGaps(r)
	kinds := []string{}
	for _, g := range gaps {
		kinds = append(kinds, g.kind)
	}
	if strings.Join(kinds, ",") != "runner,budget,timeout,inventory,unsupported,no_tests,other" {
		t.Fatalf("kinds=%v", kinds)
	}
	if len(gaps[0].items) != 1 || !strings.Contains(gaps[0].items[0], "not copied") || !strings.Contains(gaps[0].fix, "plan test_run with link") {
		t.Fatalf("runner gap must merge planning and execution entries and explain node_modules: %+v", gaps[0])
	}
	if !strings.Contains(gaps[1].title, "2 required test commands") || !strings.Contains(gaps[1].fix, "--max-commands 4") {
		t.Fatalf("%+v", gaps[1])
	}
	var out bytes.Buffer
	renderGaps(&out, gaps, palette{})
	for _, want := range []string{"? not verified: runner unavailable: 1 test command could not start", "fix: Raise the budget", "fix: Raise --timeout", "inventory incomplete: test discovery skipped 1 file", "no supported test command", "no test related to the changed code", "Inspect selection.blocking"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q:\n%s", want, out.String())
		}
	}
}

func TestGateGapsBoundLongLists(t *testing.T) {
	s := &testselection.Selection{Mode: "targeted", MaxCommands: 16}
	for i := range 40 {
		s.Blocking = append(s.Blocking, "required command omitted by budget: python3 -m pytest ./"+strings.Repeat("very/long/path/", 6)+"test_"+string(rune('a'+i%26))+".py")
	}
	var out bytes.Buffer
	renderGaps(&out, gateGaps(integration.Report{Selection: s}), palette{})
	text := out.String()
	if strings.Count(text, "python3 -m pytest") != 3 || !strings.Contains(text, "… 23 more (see --json)") || strings.Contains(text, "--suite targeted") {
		t.Fatalf("%s", text)
	}
}

func TestBlockedNextKeepsTheSelection(t *testing.T) {
	o := options{verify: true, suite: "targeted", maxCommands: 2, timeout: 10 * time.Minute, args: []string{"agent-a", "agent-b"}}
	again := rerunWith(o, "main", []string{"agent-a", "agent-b"})
	budget := integration.Report{Selection: &testselection.Selection{MaxCommands: 2, Blocking: []string{"required command omitted by budget: x", "required command omitted by budget: y"}}}
	if got := blockedNext(budget, gateGaps(budget), again, o); got != "raise the command budget: radar gate --run --suite targeted --max-commands 4 agent-a agent-b" {
		t.Fatal(got)
	}
	uncovered := integration.Report{Selection: &testselection.Selection{Uncovered: []string{"settings.yaml"}}}
	if got := blockedNext(uncovered, gateGaps(uncovered), again, o); got != "add or name tests that cover the uncovered files, commit, then rerun: radar gate --run --suite targeted --max-commands 2 agent-a agent-b" {
		t.Fatal(got)
	}
	mixed := integration.Report{Selection: &testselection.Selection{Uncovered: []string{"x"}, Blocking: []string{"required command omitted by budget: y"}}}
	if got := blockedNext(mixed, gateGaps(mixed), again, o); !strings.HasPrefix(got, "resolve the items marked ? above") {
		t.Fatal(got)
	}
	timeout := integration.Report{Selection: &testselection.Selection{Blocking: []string{"time_budget_exhausted: x"}}}
	if got := blockedNext(timeout, gateGaps(timeout), again, o); !strings.Contains(got, "--timeout 30m0s") {
		t.Fatal(got)
	}
	o.timeout = 30 * time.Minute
	if got := blockedNext(timeout, gateGaps(timeout), rerunWith(o, "main", []string{"agent-a", "agent-b"}), o); strings.HasPrefix(got, "raise the time budget") {
		t.Fatalf("already at the maximum: %s", got)
	}
	capped := integration.Report{Selection: &testselection.Selection{MaxCommands: 256, Blocking: []string{"required command omitted by budget: x"}}}
	if got := blockedNext(capped, gateGaps(capped), again, o); strings.HasPrefix(got, "raise the command budget") {
		t.Fatalf("already at the maximum: %s", got)
	}
	if args := gateArgs(options{maxCommands: 16, timeout: 10 * time.Minute}, "main", nil); args != "" {
		t.Fatalf("defaults stay implicit: %q", args)
	}
	if args := gateArgs(options{maxCommands: 3, timeout: 2 * time.Minute}, "main", nil); args != "--max-commands 3 --timeout 2m0s" {
		t.Fatalf("%q", args)
	}
}

func TestGateErrorsSayWhatToDoNext(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"a.py": "A = 1\n"})
	for _, tc := range []struct {
		args       []string
		message    string
		nextSubstr string
	}{
		{[]string{"gate", "agent-missing"}, `branch "agent-missing" not found`, "git branch --list"},
		{[]string{"gate", "orders:agent/api"}, `branch "orders:agent/api" not found`, "workspace"},
	} {
		code, _, errs := run(t, root, tc.args...)
		if code != 2 || !strings.Contains(errs, tc.message) || !strings.Contains(errs, "Next: ") || !strings.Contains(errs, tc.nextSubstr) {
			t.Fatalf("%v: %d %s", tc.args, code, errs)
		}
		code, out, _ := run(t, root, append(tc.args, "--json")...)
		var e map[string]string
		if code != 2 || json.Unmarshal([]byte(out), &e) != nil || !strings.Contains(e["error"], tc.message) || !strings.Contains(e["next"], tc.nextSubstr) || e["status"] != "error" {
			t.Fatalf("%v: %d %s", tc.args, code, out)
		}
	}
	dir := agent("agent-link", nil)
	if err := os.Symlink("a.py", filepath.Join(dir, "link.py")); err != nil {
		t.Skip(err)
	}
	gitTest(t, dir, "add", "link.py")
	gitTest(t, dir, "commit", "-qm", "link")
	code, _, errs := run(t, root, "gate")
	if code != 2 || !strings.Contains(errs, "preview refuses symlink or submodule: link.py") || !strings.Contains(errs, "unsupported layout") || !strings.Contains(errs, "Next: remove or replace link.py") {
		t.Fatalf("%d %s", code, errs)
	}
}

func TestSetupAcceptsTheAgentName(t *testing.T) {
	root, _ := agentRepo(t, map[string]string{"a.py": "A = 1\n"})
	code, out, errs := run(t, root, "setup", "claude", "--dry-run")
	if code != 0 || !strings.Contains(out, ".claude/skills/radar/SKILL.md") || strings.Contains(out, ".codex") {
		t.Fatalf("%d %s %s", code, out, errs)
	}
	if code, out, _ = run(t, root, "setup", "--dry-run", "--agent", "codex"); code != 0 || !strings.Contains(out, ".codex/config.toml") {
		t.Fatalf("flag form: %d %s", code, out)
	}
	if code, out, _ = run(t, root, "setup", "--dry-run"); code != 0 || !strings.Contains(out, ".codex/config.toml") {
		t.Fatalf("default stays codex: %d %s", code, out)
	}
	for _, args := range [][]string{{"setup", "claude", "--agent", "codex", "--dry-run"}, {"setup", "cursor", "--dry-run"}, {"setup", "claude", "codex", "--dry-run"}} {
		if code, _, errs := run(t, root, args...); code != 2 || errs == "" {
			t.Fatalf("%v: %d %s", args, code, errs)
		}
	}
	if _, err := os.Stat(filepath.Join(root, ".claude")); !os.IsNotExist(err) {
		t.Fatal("dry run wrote files")
	}
}

func TestHelpForAgentsNamesTheContract(t *testing.T) {
	code, out, _ := run(t, t.TempDir(), "help", "agents")
	for _, want := range []string{"radar gate --json", "radar_gate", "Never add --run on your own", "only passed satisfies", "not proof of blame", "gate excludes them; watch --include-dirty previews", "Gate and watch default to zero diagnostic reruns", "--run --attribution-runs 8", "no unsaved buffers or ignored files", "Preview evidence does not\n                                  qualify committed inputs", "PASS (static)\" means no test ran"} {
		if code != 0 || !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestWatchRendersGateVocabularyOnce(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"a.py": "A = 1\n"})
	agent("agent-a", map[string]string{"a.py": "A = 2\n"})
	var out bytes.Buffer
	a := &app{ctx: context.Background(), root: root, out: &out}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	a.renderWatch(watch.Event{Type: watch.Completed, Time: now, State: watch.State{Key: "0123456789abcdef"}, Result: &watch.Result{Value: map[string]string{"state": "NOT VERIFIED", "reason": "No selected branches with commits beyond the base; waiting for changes."}}}, options{})
	if got := out.String(); !strings.Contains(got, "03:04:05 completed @ 0123456789ab") || !strings.Contains(got, "NOT VERIFIED — No selected branches") || strings.Contains(got, "map[") {
		t.Fatalf("%q", got)
	}
	for _, tc := range []struct {
		verdict gate.Verdict
		ran     bool
		want    string
	}{
		{gate.Pass, false, "Next: static checks only; restart with radar watch --run"},
		{gate.Fail, true, "Next: repair on the branches above and commit; watch rechecks automatically."},
		{gate.Blocked, true, "Next: resolve the items marked ? above; watch rechecks"},
	} {
		out.Reset()
		r := integration.Report{Gate: gate.Result{Verdict: tc.verdict, Policy: gate.Policy{Name: "supported-integration", Require: []string{"textual_merge"}}, Required: []gate.Check{{ID: "textual_merge", Status: model.StatusPassed}}}}
		a.renderWatch(watch.Event{Type: watch.Completed, Time: now, State: watch.State{Key: "k"}, Result: &watch.Result{Value: r}}, options{verify: tc.ran})
		got := out.String()
		if strings.Count(got, "Radar watch: ") != 1 || strings.Contains(got, "Radar gate:") || !strings.Contains(got, tc.want) || !strings.Contains(got, "--jsonl") {
			t.Fatalf("%s:\n%s", tc.verdict, got)
		}
		if tc.verdict == gate.Pass && strings.Contains(got, "Repair") {
			t.Fatalf("a pass must not ask for repair:\n%s", got)
		}
	}
}

func TestReproduceOnlyForFailedCombinedTests(t *testing.T) {
	argv := []string{"python3", "-m", "unittest", "discover", "-s", ".", "-p", "test_x.py"}
	g := gateReport{Branches: []gateBranch{{Ref: "agent-a", Commit: strings.Repeat("a", 40)}, {Ref: "agent b", Commit: strings.Repeat("b", 40)}}}
	g.Base = strings.Repeat("c", 40)
	g.Inputs = []string{strings.Repeat("a", 40), strings.Repeat("b", 40)}
	explanation := fmt.Sprintf("Combined verification command %q in %q returned failed (exit 1).", argv, "svc")
	for _, tc := range []struct {
		status model.Status
		code   string
		want   string
	}{
		{model.StatusFailed, "integration_execution_failed", `d=$(mktemp -d) && git worktree add --detach "$d" cccccccccccc && cd "$d" && git merge --no-ff --no-edit --no-verify aaaaaaaaaaaa && git merge --no-ff --no-edit --no-verify bbbbbbbbbbbb && cd svc && python3 -m unittest discover -s . -p test_x.py`},
		{model.StatusError, "integration_execution_error", ""},
		{model.StatusTimeout, "integration_execution_timeout", ""},
	} {
		g.Executions = []integration.ExecutionEvidence{{Command: argv, CWD: "svc", Status: tc.status}}
		if got := g.reproduce(model.Finding{Code: tc.code, Explanation: explanation}); got != tc.want {
			t.Fatalf("%s: %q", tc.status, got)
		}
	}
}

func TestReproduceKeepsLongCommandsWhole(t *testing.T) {
	argv := []string{"python3", "-m", "pytest"}
	for i := range 30 {
		argv = append(argv, fmt.Sprintf("./tests/integration/payments/test_case_%02d.py", i))
	}
	g := gateReport{Branches: []gateBranch{{Ref: "a", Commit: strings.Repeat("a", 40)}}}
	g.Base = strings.Repeat("c", 40)
	g.Inputs = []string{strings.Repeat("a", 40)}
	g.Executions = []integration.ExecutionEvidence{{Command: argv, CWD: ".", Status: model.StatusFailed}}
	got := g.reproduce(model.Finding{Code: "integration_execution_failed", Explanation: fmt.Sprintf("%q in %q", argv, ".")})
	if strings.Contains(got, "…") || !strings.HasSuffix(got, "test_case_29.py") {
		t.Fatal(got)
	}
}

func TestReproduceSkipsPlanDeclaredCommands(t *testing.T) {
	argv := []string{"make", "check"}
	g := gateReport{Branches: []gateBranch{{Ref: "a", Commit: strings.Repeat("a", 40)}}}
	g.Base = strings.Repeat("c", 40)
	g.Inputs = []string{strings.Repeat("a", 40)}
	g.Selection = &testselection.Selection{Commands: []testselection.Command{{ID: "plan-1", Framework: "plan-declared", Command: argv}}}
	g.Executions = []integration.ExecutionEvidence{{Command: argv, CWD: ".", Status: model.StatusFailed, SelectionID: "plan-1"}}
	if got := g.reproduce(model.Finding{Code: "integration_execution_failed", Explanation: fmt.Sprintf("%q in %q", argv, ".")}); got != "" {
		t.Fatal(got)
	}
}

func TestReproduceRefusesCommitsTheUserDoesNotHave(t *testing.T) {
	argv := []string{"go", "test", "-json", "."}
	finding := model.Finding{Code: "integration_execution_failed", Explanation: fmt.Sprintf("%q in %q", argv, ".")}
	base := gateReport{Branches: []gateBranch{{Ref: "a", Commit: strings.Repeat("a", 40)}}}
	base.Base = strings.Repeat("c", 40)
	base.Executions = []integration.ExecutionEvidence{{Command: argv, CWD: ".", Status: model.StatusFailed}}
	snapshot := base
	snapshot.Inputs = []string{strings.Repeat("a", 40)}
	snapshot.snapshot = true
	if got := snapshot.reproduce(finding); got != "" {
		t.Fatalf("watch --include-dirty inputs are private snapshots: %s", got)
	}
	extra := base
	extra.Inputs = []string{strings.Repeat("a", 40), strings.Repeat("d", 40)}
	if got := extra.reproduce(finding); got != "" {
		t.Fatalf("an input with no branch would be left out: %s", got)
	}
	moved := base
	moved.Inputs = []string{strings.Repeat("e", 40)}
	if got := moved.reproduce(finding); got != "" {
		t.Fatalf("the branch commit is not what was tested: %s", got)
	}
}

func TestSymlinkOnTheBaseBlamesNoBranch(t *testing.T) {
	root := gitRepo(t)
	gitTest(t, root, "checkout", "-q", "-b", "main")
	put(t, root, "a.txt", "a\n")
	if err := os.Symlink("a.txt", filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base with link")
	gitTest(t, root, "checkout", "-q", "-b", "agent-a")
	put(t, root, "b.txt", "b\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "b")
	gitTest(t, root, "checkout", "-q", "main")
	code, _, errs := run(t, root, "gate", "agent-a")
	if code != 2 || !strings.Contains(errs, "Next: link is on the base") || strings.Contains(errs, "branch that adds it") {
		t.Fatalf("%d %s", code, errs)
	}
}

func TestBlockedHeadlineSkipsSatisfiedChecks(t *testing.T) {
	g := gateReport{}
	g.Gate = gate.Result{Verdict: gate.Blocked, Required: []gate.Check{{ID: "integration_execution", Status: model.StatusPassed}, {ID: "plan_review", Status: model.StatusUnknown}}}
	if got := gateHeadline(g, true); strings.Contains(got, "tests") || !strings.Contains(got, "plan_review not verified") {
		t.Fatal(got)
	}
}

func TestDoctorRunnerStatusClaimsOnlyWhatPathShows(t *testing.T) {
	for _, tc := range []struct {
		r    testselection.Runner
		want string
	}{
		{testselection.Runner{Framework: "unittest", Program: "git"}, "available"},
		{testselection.Runner{Framework: "pytest", Program: "git"}, "unverified"},
		{testselection.Runner{Framework: "maven", Program: "./mvnw"}, "unverified"},
		{testselection.Runner{Framework: "jest", Program: "./node_modules/.bin/jest"}, "not_copied"},
		{testselection.Runner{Framework: "go", Program: "radar-no-such-program"}, "missing"},
		{testselection.Runner{Framework: "c-unknown"}, "unsupported"},
	} {
		if got := runnerStatus(tc.r); got.Status != tc.want {
			t.Fatalf("%+v: %s (%s)", tc.r, got.Status, got.Note)
		}
	}
}

func TestSymlinkMadeByABranchIsNotBlamedOnTheBase(t *testing.T) {
	root := gitRepo(t)
	gitTest(t, root, "checkout", "-q", "-b", "main")
	put(t, root, "link", "regular\n")
	put(t, root, "target", "t\n")
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-qm", "base")
	gitTest(t, root, "checkout", "-q", "-b", "agent-a")
	if err := os.Remove(filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(root, "link")); err != nil {
		t.Skip(err)
	}
	gitTest(t, root, "add", "-A")
	gitTest(t, root, "commit", "-qm", "symlink")
	gitTest(t, root, "checkout", "-q", "main")
	code, _, errs := run(t, root, "gate", "agent-a")
	if code != 2 || !strings.Contains(errs, "on the branch that adds it") || strings.Contains(errs, "is on the base") {
		t.Fatalf("%d %s", code, errs)
	}
}

func TestNotCopiedRunnerDoesNotSuggestARerun(t *testing.T) {
	jest := "./node_modules/.bin/jest --runInBand --runTestsByPath ./a.test.ts"
	r := integration.Report{Selection: &testselection.Selection{Blocking: []string{"required command runner unavailable or unverified: " + jest, "environment_unavailable: " + jest}}}
	got := blockedNext(r, gateGaps(r), rerunWith(options{verify: true}, "main", nil), options{})
	if strings.Contains(got, "rerun:") || !strings.Contains(got, "run them yourself") {
		t.Fatal(got)
	}
	g := gateGaps(r)
	r.Gate.Required = []gate.Check{{ID: "test_selection", Status: model.StatusIncomplete}}
	if label := gateLabel(r.Gate.Required[0], r); !strings.Contains(label, "1 blocking gap ") || len(g) != 1 {
		t.Fatalf("one merged group is one gap: %s", label)
	}
}

func TestDoctorNamesTheWorkspace(t *testing.T) {
	root, agent := agentRepo(t, map[string]string{"a.py": "A = 1\n"})
	agent("agent-a", map[string]string{"a.py": "A = 2\n"})
	other, _ := agentRepo(t, map[string]string{"b.py": "B = 1\n"})
	if code, _, errs := run(t, root, "workspace", "add", other); code != 0 {
		t.Fatal(errs)
	}
	d := doctorProjectOf(t, root)
	if d.Workspace == "" || !strings.Contains(d.Next, "gate checks every repository in it") {
		t.Fatalf("%+v", d)
	}
}

func TestGateBranchUnknownChangesDoNotRenderZeroFiles(t *testing.T) {
	var out bytes.Buffer
	renderGate(&out, gateReport{
		BaseRef:  "main",
		Branches: []gateBranch{{Ref: "agent-a", Commit: "123456789012", changedUnknown: true}, {Ref: "agent-b", Commit: "234567890123", Changed: []string{"app.py"}}},
		Report:   integration.Report{Base: "345678901234", Gate: gate.Result{Verdict: gate.Blocked}},
	}, false)
	text := out.String()
	if !strings.Contains(text, "agent-a  123456789012  changed files unavailable") || !strings.Contains(text, "agent-b  234567890123  1 file changed") || strings.Contains(text, "0 files changed") {
		t.Fatal(text)
	}
}

func TestDoctorRecognizesSharedSkillSetup(t *testing.T) {
	root, _ := agentRepo(t, map[string]string{"app.py": "VALUE = 1\n"})
	invoke(t, root, 0, "setup", "both", "--hook=false")
	report := invoke(t, root, 0, "doctor")
	project := report["project"].(map[string]any)
	installed := project["agent_skills_installed"].(map[string]any)
	if installed["claude"] != true || installed["codex"] != true {
		t.Fatalf("shared skills not detected: %v", installed)
	}
}
