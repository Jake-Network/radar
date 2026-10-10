package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func interact(t *testing.T, root, input string) string {
	t.Helper()
	var out, errs bytes.Buffer
	if code := Interactive(context.Background(), root, strings.NewReader(input), &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s%s", code, out.String(), errs.String())
	}
	return out.String() + errs.String()
}

func TestInteractiveListsBranchesAndDispatchesStaticGate(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-frontend", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})
	gitTest(t, root, "branch", "feature-off-tree", "agent-backend")

	out := interact(t, root, "1\nq\n")
	for _, want := range []string{"Worktree branches beyond main: agent-backend, agent-frontend", "Other branches beyond main:    feature-off-tree", "$ radar gate\n", "Radar gate: PASS (static)", "(exit 0)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "selected tests pass") || strings.Contains(out, "ran 1 of") {
		t.Fatal("static choice must not execute tests:\n" + out)
	}
}

func TestInteractiveRunRequiresConfirmationAndUsesPickedBranches(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	agent("agent-frontend", map[string]string{"frontend.ts": "export const QUANTITY = 2;\n"})

	declined := interact(t, root, "3\nn\nq\n")
	if !strings.Contains(declined, "Run tests? [y/N]") || strings.Contains(declined, "$ radar gate --run") {
		t.Fatal("declined run must not dispatch:\n" + declined)
	}
	eof := interact(t, root, "3\n")
	if strings.Contains(eof, "$ radar gate --run") {
		t.Fatal("end of input at the confirmation must not run tests:\n" + eof)
	}

	out := interact(t, root, "2\n1 2\n3\ny\nq\n")
	for _, want := range []string{"$ radar gate agent-backend agent-frontend\n", "Selected:  agent-backend, agent-frontend", "$ radar gate --run agent-backend agent-frontend\n", "Radar gate: FAIL", "(exit 1)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestInteractivePickerRejectsUnlistedNumbers(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	out := interact(t, root, "2\n7\nq\n")
	if !strings.Contains(out, `"7" is not a listed number`) || strings.Contains(out, "$ radar gate") {
		t.Fatal(out)
	}
}

func TestInteractiveOutsideRepositoryShowsHelp(t *testing.T) {
	out := interact(t, t.TempDir(), "")
	if !strings.Contains(out, "not inside a Git repository") || !strings.Contains(out, "Usage: radar <command>") {
		t.Fatal(out)
	}
}

func TestInteractiveFromSubdirectoryUsesRepositoryRoot(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	sub := filepath.Join(root, "src")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	out := interact(t, sub, "1\nq\n")
	for _, want := range []string{"Worktree branches beyond main: agent-backend", "Radar gate: PASS (static)", "(exit 0)"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestInteractiveRunShowsEveryWorkspaceRepositoryBeforeConsent(t *testing.T) {
	s := newShop(t)
	s.register()

	out := interact(t, s.orders, "3\nn\nq\n")
	for _, want := range []string{`Workspace "shop" — radar gate checks every repository in it:`, "radar gate --run will run tests in:", "orders    agent/api + agent/rounding (this repository)", "payments  agent/client", "Run tests? [y/N]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "$ radar gate --run") {
		t.Fatal("declined run dispatched:\n" + out)
	}
	// Branches picked in this repository leave the others at their base,
	// where --run executes nothing; the confirmation says so.
	out = interact(t, s.orders, "2\n1\n3\nn\nq\n")
	if !strings.Contains(out, "orders    agent/api (this repository)") || !strings.Contains(out, "payments  base only — no branches here, so no tests run") {
		t.Fatal(out)
	}
}

// steppedReader returns one line per read and runs a hook before the line
// at the same index, so a test can change the repository mid-dialog.
type steppedReader struct {
	lines []string
	hooks map[int]func()
	next  int
}

func (r *steppedReader) Read(b []byte) (int, error) {
	if r.next >= len(r.lines) {
		return 0, io.EOF
	}
	if h := r.hooks[r.next]; h != nil {
		h()
	}
	n := copy(b, r.lines[r.next])
	r.next++
	return n, nil
}

func TestInteractiveRunAbortsWhenScopeChangesAfterConsent(t *testing.T) {
	root, agent := checkoutRepo(t)
	agent("agent-backend", map[string]string{"backend.py": "def price():\n    return 2\n"})
	in := &steppedReader{lines: []string{"3\n", "y\n", "q\n"}, hooks: map[int]func(){1: func() {
		agent("agent-late", map[string]string{"frontend.ts": "export const QUANTITY = 3;\n"})
	}}}
	var out, errs bytes.Buffer
	if code := Interactive(context.Background(), root, in, &out, &errs); code != 0 {
		t.Fatal(code, errs.String())
	}
	text := out.String()
	if !strings.Contains(text, "changed after you confirmed; nothing ran") || strings.Contains(text, "$ radar gate --run") {
		t.Fatal(text)
	}
}
