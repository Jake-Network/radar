package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Jake-Network/radar/internal/composition"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/project"
	"github.com/Jake-Network/radar/internal/termui"
)

// maxListedBranches bounds the branch picker on repositories with many refs.
const maxListedBranches = 30

// Interactive is bare `radar` on a terminal: it shows the repository's
// branches and dispatches each menu choice to the same command line Run
// executes, so it adds no analysis or execution path of its own. Tests run
// only after the person confirms each --run.
func Interactive(ctx context.Context, dir string, in io.Reader, out, errout io.Writer) int {
	p := palette{on: termui.Color(out, termui.Auto, os.Getenv)}
	out = styled(out, p.on)
	// Gate requires the repository top level as --root, so a menu opened in a
	// subdirectory works on the repository that contains it.
	root, err := project.Root(dir)
	if err == nil {
		root, err = gitrepo.TopLevel(ctx, root)
	}
	if err == nil {
		root, err = project.Root(root)
	}
	if err != nil {
		fmt.Fprintf(out, "%s not inside a Git repository. cd into a project and run radar again.\n", p.yellow("Radar:"))
		fmt.Fprintln(out)
		printHelp(out, false)
		return 0
	}
	s := &session{ctx: ctx, root: root, in: bufio.NewScanner(in), out: out, errout: errout, p: p}
	printLogo(out, p, "radar "+Version, tagline)
	for {
		s.refresh()
		s.menu()
		choice, ok := s.prompt(p.paint("1;32", "› "))
		if !ok {
			return 0
		}
		switch strings.ToLower(choice) {
		case "1":
			s.dispatch("gate")
		case "2":
			if picked := s.pick(); len(picked) > 0 {
				s.selected = picked
				s.dispatch(append([]string{"gate"}, picked...)...)
			}
		case "3":
			if s.confirmRun(s.selected) {
				s.dispatch(append([]string{"gate", "--run"}, s.selected...)...)
			}
		case "4":
			s.dispatch("doctor")
		case "h":
			printHelp(out, true)
		case "q", "quit", "exit":
			return 0
		case "":
		default:
			fmt.Fprintf(out, "Unknown choice %q. Type a number, h for all commands or q to quit.\n", choice)
		}
	}
}

type session struct {
	ctx         context.Context
	root        string
	in          *bufio.Scanner
	out, errout io.Writer
	p           palette
	base        string
	worktree    []string // worktree branches with commits beyond base (gate's default)
	others      []string // other local branches with commits beyond base
	selected    []string // branches chosen in the picker; empty means gate's default
	workspace   string   // workspace name when gate checks several repositories
	scope       []target // what `radar gate` with the current selection checks
}

// target is one repository a gate run checks and the branches it combines
// there. A repository with no branches takes part at its base only, and
// --run executes nothing in it.
type target struct {
	repo     string
	current  bool
	branches []string
	problem  string
}

// targets resolves what `radar gate ARGS` checks with gate's own scope rules:
// every repository of this repository's workspace, or this repository alone.
func (s *session) targets(args []string) (string, []target, error) {
	a := &app{ctx: s.ctx, root: s.root, out: io.Discard, errout: io.Discard}
	inv, err := a.workspaceInvocation(options{args: args})
	if err != nil {
		return "", nil, err
	}
	if inv == nil {
		if s.base == "" {
			return "", nil, errors.New("no main, master or trunk branch found")
		}
		baseSHA, err := gitrepo.Resolve(s.ctx, s.root, s.base)
		if err != nil {
			return "", nil, err
		}
		refs, _, err := a.gateBranches(args, s.base, baseSHA)
		if err != nil {
			return "", nil, err
		}
		return "", []target{{repo: filepath.Base(s.root), current: true, branches: refs}}, nil
	}
	repos, err := composition.Collect(s.ctx, composition.Request{Scope: inv.scope, Named: inv.named, Bases: inv.bases})
	if err != nil {
		return "", nil, err
	}
	out := []target{}
	for _, r := range repos {
		t := target{repo: r.ID, current: r.ID == inv.scope.Current, problem: r.Error}
		for _, b := range r.Branches {
			t.branches = append(t.branches, b.Ref)
		}
		out = append(out, t)
	}
	return inv.scope.Workspace, out, nil
}

func sameTargets(a, b []target) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].repo != b[i].repo || a[i].problem != b[i].problem || strings.Join(a[i].branches, "\x00") != strings.Join(b[i].branches, "\x00") {
			return false
		}
	}
	return true
}

// printTargets lists each repository with the branches gate combines there.
func (s *session) printTargets(targets []target, run bool) {
	width := 0
	for _, t := range targets {
		width = max(width, len(t.repo))
	}
	for _, t := range targets {
		name := t.repo + strings.Repeat(" ", width-len(t.repo))
		detail := s.p.cyan(strings.Join(t.branches, " + "))
		switch {
		case t.problem != "":
			detail = s.p.mark("!") + " " + t.problem
		case len(t.branches) == 0 && run:
			detail = s.p.dim("base only — no branches here, so no tests run")
		case len(t.branches) == 0:
			detail = s.p.dim("base only")
		}
		current := ""
		if t.current && len(targets) > 1 {
			current = s.p.dim(" (this repository)")
		}
		fmt.Fprintf(s.out, "    %s  %s%s\n", s.p.bold(name), detail, current)
	}
}

// refresh rereads branches so commits made while the menu is open are seen.
func (s *session) refresh() {
	s.base, s.worktree, s.others = gitrepo.DefaultBranch(s.ctx, s.root), nil, nil
	s.workspace, s.scope, _ = s.targets(s.selected)
	if s.base == "" {
		return
	}
	baseSHA, err := gitrepo.Resolve(s.ctx, s.root, s.base)
	if err != nil {
		return
	}
	a := &app{ctx: s.ctx, root: s.root}
	s.worktree, _, _ = a.gateBranches(nil, s.base, baseSHA)
	inWorktree := map[string]bool{s.base: true}
	for _, b := range s.worktree {
		inWorktree[b] = true
	}
	local, _ := gitrepo.LocalBranches(s.ctx, s.root)
	for _, b := range local {
		if inWorktree[b] {
			continue
		}
		sha, e := gitrepo.Resolve(s.ctx, s.root, b)
		if e != nil {
			continue
		}
		if mb, e := gitrepo.MergeBase(s.ctx, s.root, baseSHA, sha); e == nil && mb != sha {
			s.others = append(s.others, b)
		}
	}
}

func (s *session) menu() {
	w, p := s.out, s.p
	base := s.base
	if base == "" {
		base = "(no main/master/trunk branch)"
	}
	fmt.Fprintf(w, "\n%s %s\n\n", p.bold(filepath.Base(s.root)), p.dim("(base "+base+")"))
	fmt.Fprintf(w, "  Worktree branches beyond %s: %s\n", base, s.branchList(s.worktree))
	if len(s.others) > 0 {
		fmt.Fprintf(w, "  Other branches beyond %s:    %s\n", base, s.branchList(s.others))
	}
	if len(s.selected) > 0 {
		fmt.Fprintf(w, "  Selected:  %s\n", p.cyan(strings.Join(s.selected, ", ")))
	}
	if s.workspace != "" {
		fmt.Fprintf(w, "\n  Workspace %q — radar gate checks every repository in it:\n", s.workspace)
		s.printTargets(s.scope, false)
	}
	target := "worktree branches"
	if len(s.selected) > 0 {
		target = "selected branches"
	}
	item := func(key, text, note string) {
		if note != "" {
			note = "  " + p.dim(note)
		}
		fmt.Fprintf(w, "  %s %s%s\n", p.command("["+key+"]"), text, note)
	}
	fmt.Fprintln(w)
	item("1", "Check worktree branches together", "static · runs no code")
	item("2", "Pick branches to check together", "")
	item("3", "Run tests on the combined "+target, "runs repository code · asks first")
	item("4", "Environment check (doctor)", "")
	fmt.Fprintf(w, "  %s All commands    %s Quit\n", p.command("[h]"), p.command("[q]"))
}

func (s *session) branchList(branches []string) string {
	text := listOrNone(branches)
	if len(branches) == 0 {
		return s.p.dim(text)
	}
	return s.p.cyan(text)
}

func listOrNone(branches []string) string {
	if len(branches) == 0 {
		return "none"
	}
	if len(branches) > 6 {
		return strings.Join(branches[:6], ", ") + fmt.Sprintf(", … (+%d)", len(branches)-6)
	}
	return strings.Join(branches, ", ")
}

func (s *session) prompt(label string) (string, bool) {
	fmt.Fprint(s.out, label)
	if !s.in.Scan() {
		fmt.Fprintln(s.out)
		return "", false
	}
	return strings.TrimSpace(s.in.Text()), true
}

// pick numbers every branch beyond the base and returns the chosen ones.
func (s *session) pick() []string {
	all := append(append([]string{}, s.worktree...), s.others...)
	if len(all) == 0 {
		fmt.Fprintf(s.out, "No branch has commits beyond %s yet.\n", s.base)
		return nil
	}
	if len(all) > maxListedBranches {
		all = all[:maxListedBranches]
	}
	for i, b := range all {
		fmt.Fprintf(s.out, "  %2d) %s\n", i+1, b)
	}
	line, ok := s.prompt("Branches (numbers, e.g. 1 3; Enter to cancel): ")
	if !ok || line == "" {
		return nil
	}
	picked, seen := []string{}, map[int]bool{}
	for _, f := range strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == ',' }) {
		n, err := strconv.Atoi(f)
		if err != nil || n < 1 || n > len(all) {
			fmt.Fprintf(s.out, "%q is not a listed number.\n", f)
			return nil
		}
		if !seen[n] {
			seen[n] = true
			picked = append(picked, all[n-1])
		}
	}
	return picked
}

// confirmRun shows exactly which repositories and branches `radar gate
// --run ARGS` will test, resolved with gate's own scope rules, and asks.
// Nothing runs when the scope cannot be resolved or changes before the
// command starts.
func (s *session) confirmRun(args []string) bool {
	_, shown, err := s.targets(args)
	if err != nil {
		fmt.Fprintf(s.out, "%s Cannot tell what radar gate --run would test: %v\nNothing ran.\n", s.p.mark("!"), err)
		return false
	}
	fmt.Fprintf(s.out, "%s radar gate --run will run tests in:\n", s.p.mark("!"))
	s.printTargets(shown, true)
	fmt.Fprintln(s.out, "  Tests run with your permissions in a private copy of the combined branches")
	fmt.Fprintln(s.out, "  (not an OS sandbox). Your checkouts and branches are not changed.")
	answer, ok := s.prompt(s.p.bold("Run tests? [y/N] "))
	if !ok || !(strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")) {
		return false
	}
	if _, now, err := s.targets(args); err != nil || !sameTargets(shown, now) {
		fmt.Fprintf(s.out, "%s The repositories or branches changed after you confirmed; nothing ran. Review the list and choose again.\n", s.p.mark("!"))
		return false
	}
	return true
}

// dispatch echoes the equivalent command so the CLI is learnable, then runs
// it. An interrupt cancels that command and returns to the menu; at the
// prompt it exits radar as usual.
func (s *session) dispatch(args ...string) {
	fmt.Fprintf(s.out, "\n%s\n\n", s.p.command("$ radar "+strings.Join(args, " ")))
	ctx, stop := signal.NotifyContext(s.ctx, os.Interrupt)
	code := Run(ctx, append(args, "--root", s.root), s.out, s.errout)
	stop()
	exit := fmt.Sprintf("(exit %d)", code)
	if code == 0 {
		exit = s.p.green(exit)
	} else {
		exit = s.p.red(exit)
	}
	fmt.Fprintf(s.out, "\n%s\n", exit)
}
