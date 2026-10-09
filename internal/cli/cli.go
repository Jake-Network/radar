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
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/Jake-Network/radar/internal/checkpoint"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/project"
	"github.com/Jake-Network/radar/internal/storage"
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
	store       *storage.Store
}

// Run executes one command. Exit codes: 0 success or informational report,
// 1 a supported check failed, 2 invocation or analysis error.
func Run(ctx context.Context, args []string, out, errout io.Writer) int {
	rootArg := "."
	machine := false
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
		default:
			rest = append(rest, args[i])
		}
	}
	a := &app{ctx: ctx, out: out, errout: errout, machine: machine}
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
		return a.fail(fmt.Errorf("unknown command %q; run radar help", rest[0]))
	}
	fs := flag.NewFlagSet("radar "+c.name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	o := options{}
	if c.flags != nil {
		c.flags(fs, &o)
	}
	parseArgs := rest[1:]
	if c.name == "gate" {
		var e error
		parseArgs, e = gateFlagArgs(fs, parseArgs)
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
	if a.machine {
		a.emit(map[string]any{"error": e.Error(), "status": model.StatusError})
	} else {
		fmt.Fprintln(a.errout, "radar:", e)
	}
	return 2
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
