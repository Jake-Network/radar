// Package cli binds terminal commands to domain services. It owns presentation,
// explicit execution opt-in and local artifacts, never architecture analysis.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/Jake-Network/radar/internal/checkpoint"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/project"
	"github.com/Jake-Network/radar/internal/storage"
	"github.com/Jake-Network/radar/internal/termui"
	"github.com/Jake-Network/radar/internal/workspace"
)

// Version is set at release time with -ldflags "-X .../internal/cli.Version=X.Y.Z";
// `go install module@vX.Y.Z` builds report the module version instead.
var Version = "0.4.0-dev"

// releaseVersion matches tagged module versions, not VCS pseudo-versions
// (v0.0.0-20261009072112-e5ed75a0bfa9) or dirty builds.
var releaseVersion = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?$`)
var pseudoVersion = regexp.MustCompile(`\d{14}-[0-9a-f]{12}$`)

func init() {
	if info, ok := debug.ReadBuildInfo(); ok && strings.HasSuffix(Version, "-dev") && releaseVersion.MatchString(info.Main.Version) && !pseudoVersion.MatchString(info.Main.Version) {
		Version = strings.TrimPrefix(info.Main.Version, "v")
	}
}

type app struct {
	ctx         context.Context
	root        string // analyzed repository
	stateRoot   string // directory holding .radar state (shared by linked worktrees)
	out, errout io.Writer
	machine     bool
	// live is where transient --run progress goes: stderr on a terminal.
	live  io.Writer
	store *storage.Store
}

// Run executes one command. Exit codes: 0 success or informational report,
// 1 a supported check failed, 2 invocation or analysis error.
func Run(ctx context.Context, args []string, out, errout io.Writer) int {
	rootArg := "."
	machine := false
	colorArg := string(termui.Auto)
	rest := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		switch {
		case args[i] == "--json":
			machine = true
		case args[i] == "--root":
			i++
			if i >= len(args) {
				fmt.Fprintln(errout, "--root requires a path")
				return 2
			}
			rootArg = args[i]
		case strings.HasPrefix(args[i], "--root="):
			rootArg = strings.TrimPrefix(args[i], "--root=")
		case args[i] == "--color":
			i++
			if i >= len(args) || strings.HasPrefix(args[i], "-") {
				fmt.Fprintln(errout, "--color requires auto, always or never")
				return 2
			}
			colorArg = args[i]
		case strings.HasPrefix(args[i], "--color="):
			colorArg = strings.TrimPrefix(args[i], "--color=")
		default:
			rest = append(rest, args[i])
		}
	}
	mode, e := termui.ParseMode(colorArg)
	if e != nil {
		fmt.Fprintln(errout, "radar:", e)
		return 2
	}
	// Machine output and the MCP server's stdio stay byte-for-byte plain.
	if machine || (len(rest) > 0 && rest[0] == "mcp") {
		mode = termui.Never
	}
	liveOK := mode != termui.Never && termui.Live(errout, os.Getenv)
	out, errout = restyle(out, mode), restyle(errout, mode)
	var live io.Writer
	if liveOK {
		live = errout
	}
	a := &app{ctx: ctx, out: out, errout: errout, machine: machine, live: live}
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "--help" || rest[0] == "-h" {
		all := false
		if len(rest) > 1 {
			if c, ok := lookup(rest[1]); ok {
				c.usageText(out)
				return 0
			}
			all = rest[1] == "--all" || rest[1] == "all"
		}
		printHelp(out, all)
		return 0
	}
	if rest[0] == "version" || rest[0] == "--version" {
		a.report(map[string]string{"version": Version}, func(w io.Writer) { fmt.Fprintln(w, "radar", Version) })
		return 0
	}
	c, ok := lookup(rest[0])
	if !ok {
		if guess := suggestCommand(rest[0]); guess != "" {
			return a.fail(fmt.Errorf("unknown command %q; did you mean %q? Run radar help for every command", rest[0], guess))
		}
		return a.fail(fmt.Errorf("unknown command %q; run radar help", rest[0]))
	}
	fs := flag.NewFlagSet("radar "+c.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	o := options{}
	if c.flags != nil {
		c.flags(fs, &o)
	}
	parseArgs := rest[1:]
	if c.name == "gate" || c.name == "workspace" {
		var e error
		parseArgs, e = interspersedFlagArgs(c.name, fs, parseArgs)
		if e != nil {
			return a.fail(e)
		}
	}
	if e := fs.Parse(parseArgs); e != nil {
		if errors.Is(e, flag.ErrHelp) {
			c.usageText(out)
			return 0
		}
		return a.fail(fmt.Errorf("%w (see radar help %s)", e, c.name))
	}
	o.args = fs.Args()
	root, e := project.Root(rootArg)
	if e != nil {
		return a.fail(e)
	}
	a.root = root
	a.stateRoot = project.StateRoot(ctx, root)
	if c.state {
		if _, e = project.Read(a.stateRoot); e != nil {
			return a.fail(e)
		}
		db, e := project.SafePath(a.stateRoot, ".radar/state.db")
		if e != nil {
			return a.fail(e)
		}
		a.store, e = storage.Open(ctx, db)
		if e != nil {
			return a.fail(e)
		}
		defer a.store.Close()
	}
	return c.run(a, o)
}

// emit writes machine-readable JSON.
func (a *app) emit(value any) {
	e := json.NewEncoder(a.out)
	e.SetIndent("", "  ")
	if err := e.Encode(value); err != nil {
		fmt.Fprintln(a.errout, "radar: write output:", err)
	}
}

// report writes JSON with --json, otherwise the human rendering.
func (a *app) report(value any, human func(io.Writer)) {
	if a.machine || human == nil {
		a.emit(value)
		return
	}
	human(a.out)
}

func (a *app) fail(e error) int {
	var next *workspace.Error
	if a.machine && errors.As(e, &next) {
		a.emit(map[string]any{"error": next.Message, "next": next.Next, "status": model.StatusError})
	} else if a.machine {
		a.emit(map[string]any{"error": e.Error(), "status": model.StatusError})
	} else {
		p := paletteOf(a.errout)
		fmt.Fprintln(a.errout, p.paint("1;31", "radar:"), e)
	}
	return 2
}

// restyle decides styling for one output stream. A writer the caller
// already styled (the interactive menu dispatching a command) keeps its
// decision unless styling is now disabled.
func restyle(w io.Writer, mode termui.Mode) io.Writer {
	if s, ok := w.(styledWriter); ok {
		if mode == termui.Never {
			return s.Writer
		}
		return s
	}
	return styled(w, termui.Color(w, mode, os.Getenv))
}

// suggestCommand returns the command a mistyped name most likely meant, or
// "" when nothing is close (edit distance at most 2, or a unique prefix).
func suggestCommand(name string) string {
	best, bestDistance := "", 3
	prefixed := []string{}
	for _, c := range commands {
		if len(name) >= 2 && strings.HasPrefix(c.name, name) {
			prefixed = append(prefixed, c.name)
		}
		if d := editDistance(name, c.name); d < bestDistance {
			best, bestDistance = c.name, d
		}
	}
	if best == "" && len(prefixed) == 1 {
		return prefixed[0]
	}
	return best
}

// editDistance is the Damerau-Levenshtein (optimal string alignment) distance,
// so a swapped pair of letters ("gaet") counts as one edit.
func editDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	d := make([][]int, len(x)+1)
	for i := range d {
		d[i] = make([]int, len(y)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(x); i++ {
		for j := 1; j <= len(y); j++ {
			cost := 1
			if x[i-1] == y[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && x[i-1] == y[j-2] && x[i-2] == y[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(x)][len(y)]
}
func (a *app) repository() error {
	info, e := gitrepo.Inspect(a.ctx, a.root)
	if e != nil {
		return e
	}
	if filepath.Clean(info.Root) != a.root {
		return errors.New("checkpoint analysis requires --root at the Git repository top level; selected directory belongs to a parent repository")
	}
	return nil
}
func (a *app) snapshot(ref string) (model.Snapshot, error) {
	if strings.HasPrefix(ref, "WORKTREE:") {
		s, e := freshSnapshot(a.ctx, a.root)
		if e != nil {
			return s, e
		}
		if s.Revision != ref {
			return s, fmt.Errorf("stale working-tree checkpoint; reindex and review the changed design baseline")
		}
		return s, nil
	}
	if ref == "" || ref == "WORKTREE" {
		return freshSnapshot(a.ctx, a.root)
	}
	if e := a.repository(); e != nil {
		return model.Snapshot{}, e
	}
	return checkpoint.Index(a.ctx, a.root, ref)
}

// storedOrFresh returns the latest persisted snapshot, indexing the working
// tree when none exists yet.
func (a *app) storedOrFresh(ref string) (model.Snapshot, error) {
	if ref != "" {
		return a.snapshot(ref)
	}
	if s, e := a.store.Snapshot(a.ctx, a.root, ""); e == nil {
		return s, nil
	}
	s, e := freshSnapshot(a.ctx, a.root)
	if e != nil {
		return s, e
	}
	return s, a.store.SaveSnapshot(a.ctx, s)
}
