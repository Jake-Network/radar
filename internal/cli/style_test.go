package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/Jake-Network/radar/internal/gate"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func TestCapturedOutputIsPlainByDefault(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	for _, args := range [][]string{{"gate"}, {"help"}, {"help", "gate"}, {"doctor"}, {"gaet"}} {
		_, out, errs := run(t, root, args...)
		if strings.Contains(out+errs, "\x1b[") {
			t.Fatalf("%v: escape sequences in captured output:\n%q", args, out+errs)
		}
	}
}

func TestColorAlwaysStylesOnlyHumanOutput(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-frontend", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})

	code, out, _ := run(t, root, "gate", "--color=always", "--run")
	if code != 1 || !strings.Contains(out, "\x1b[") {
		t.Fatalf("code=%d, expected styled output:\n%s", code, out)
	}
	plain := ansi.ReplaceAllString(out, "")
	for _, want := range []string{"Radar gate:", "FAIL", "1 of 1 test command failed on the combined tree", "✗ Tests", "? Test selection", "Uncovered changes"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("styled output lost %q:\n%s", want, plain)
		}
	}
	// Machine output never carries styling, whatever --color says.
	if _, out, _ := run(t, root, "gate", "--color", "always", "--json"); strings.Contains(out, "\x1b[") {
		t.Fatalf("--json carried escape sequences: %q", out)
	}
	t.Setenv("FORCE_COLOR", "1")
	if _, out, _ := run(t, root, "gate", "--json"); strings.Contains(out, "\x1b[") {
		t.Fatalf("FORCE_COLOR styled --json: %q", out)
	}
	if _, out, _ := run(t, root, "gate", "--color=never"); strings.Contains(out, "\x1b[") {
		t.Fatalf("--color=never styled output: %q", out)
	}
}

func TestStaticPassIsNotStyledAsFullSuccess(t *testing.T) {
	p := palette{on: true}
	static := p.verdict(gate.Pass, "PASS (static)")
	full := p.verdict(gate.Pass, "PASS (selected policy; bounded verification)")
	if strings.Contains(static, "42m") || !strings.Contains(full, "42m") {
		t.Fatalf("static=%q full=%q", static, full)
	}
	for _, s := range []model.Status{model.StatusUnknown, model.StatusIncomplete, model.StatusBlocked} {
		if strings.Contains(p.status(s, "x"), "32m") {
			t.Fatalf("%s styled as success", s)
		}
	}
	if strings.Contains(p.mark("?"), "32m") || strings.Contains(p.mark("!"), "32m") {
		t.Fatal("unknown marks styled as success")
	}
}

func TestInvalidColorModeIsAnInvocationError(t *testing.T) {
	root, _ := checkoutRepo(t)
	if code, _, errs := run(t, root, "help", "--color=sometimes"); code != 2 || !strings.Contains(errs, "--color must be auto, always or never") {
		t.Fatalf("code=%d %s", code, errs)
	}
	if code, _, errs := run(t, root, "help", "--color"); code != 2 || !strings.Contains(errs, "--color requires") {
		t.Fatalf("code=%d %s", code, errs)
	}
}

func TestHelpShowsLogoOnlyWhenStyled(t *testing.T) {
	root, _ := checkoutRepo(t)
	_, plain, _ := run(t, root, "help")
	_, styled, _ := run(t, root, "help", "--color=always")
	if strings.Contains(plain, "◉") || !strings.Contains(styled, "◉") {
		t.Fatalf("plain:\n%s\nstyled:\n%s", plain, styled)
	}
	for _, want := range []string{"Start here", "Everyday", "Agent setup", "radar help --all", "Exit codes", "--run / --allow-execution"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("help lacks %q:\n%s", want, plain)
		}
	}
	_, gateHelp, _ := run(t, root, "help", "gate")
	for _, want := range []string{"Examples:", "radar gate --run", "runs no repository code", "not an OS sandbox", "--suite MODE", "--max-commands N"} {
		if !strings.Contains(gateHelp, want) {
			t.Fatalf("gate help lacks %q:\n%s", want, gateHelp)
		}
	}
}

func TestUnknownCommandSuggestsTheClosest(t *testing.T) {
	root, _ := checkoutRepo(t)
	for typo, want := range map[string]string{"gaet": "gate", "doctr": "doctor", "workspce": "workspace", "merge": "merge-check"} {
		code, _, errs := run(t, root, typo)
		if code != 2 || !strings.Contains(errs, `did you mean "`+want+`"`) {
			t.Fatalf("%s: code=%d %s", typo, code, errs)
		}
	}
	if _, _, errs := run(t, root, "zzzzzz"); strings.Contains(errs, "did you mean") {
		t.Fatalf("unrelated input got a suggestion: %s", errs)
	}
	if editDistance("gaet", "gate") != 1 || editDistance("", "abc") != 3 || editDistance("same", "same") != 0 {
		t.Fatal("edit distance")
	}
}

func TestReadableExplanationShowsCopyableCommand(t *testing.T) {
	ev := integration.ExecutionEvidence{Command: []string{"python3", "-m", "unittest", "it's"}, CWD: "."}
	text := `Combined verification command ["python3" "-m" "unittest" "it's"] in "." returned failed (exit 1).`
	got := readableExplanation(text, []integration.ExecutionEvidence{ev}, palette{})
	if got != `Combined verification command python3 -m unittest 'it'"'"'s' (in .) returned failed (exit 1).` {
		t.Fatal(got)
	}
	long := []string{"pytest"}
	for i := 0; i < 40; i++ {
		long = append(long, "tests/test_module_with_a_long_name.py")
	}
	if d := displayCommand(long); len(d) > 120 || !strings.Contains(d, "(+") {
		t.Fatalf("long command not elided: %s", d)
	}
}

func TestLiveProgressDrawsAndErases(t *testing.T) {
	var buf bytes.Buffer
	a := &app{}
	if p := a.startProgress("x"); p != nil {
		t.Fatal("progress without a live terminal")
	}
	var nilProgress *liveProgress
	nilProgress.step(integration.Step{Stage: integration.StageTest})
	nilProgress.done()
	if nilProgress.callback() != nil {
		t.Fatal("nil progress returned a callback")
	}

	a.live = &buf
	p := a.startProgress("combining")
	p.callback()(integration.Step{Repo: "payments", Stage: integration.StageTest, Index: 2, Total: 3, Command: []string{"go", "test", "./..."}})
	p.draw()
	p.done()
	out := buf.String()
	if !strings.Contains(out, "payments  running test command 2/3  go test ./...") || !strings.HasSuffix(out, "\r\x1b[2K") {
		t.Fatalf("%q", out)
	}
}
