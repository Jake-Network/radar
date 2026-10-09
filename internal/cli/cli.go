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
	"strings"
	"time"

	"github.com/radar-engine/radar/internal/checkpoint"
	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/model"
	"github.com/radar-engine/radar/internal/project"
	"github.com/radar-engine/radar/internal/storage"
)

const Version = "0.1.0"

type options struct {
	plan, ref, format, kind, name, from, edge, base, head, branches, output, reviewer, evidence string
	reverse, allow, projected                                                                   bool
	timeout                                                                                     time.Duration
	args                                                                                        []string
}
type app struct {
	ctx         context.Context
	root        string
	out, errout io.Writer
	machine     bool
	store       *storage.Store
}

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
	if len(rest) == 0 || rest[0] == "help" || rest[0] == "--help" {
		fmt.Fprint(out, help)
		return 0
	}
	if rest[0] == "version" || rest[0] == "--version" {
		a.emit(map[string]string{"version": Version})
		return 0
	}
	root, e := project.Root(rootArg)
	if e != nil {
		return a.fail(e)
	}
	a.root = root
	fs := flag.NewFlagSet(rest[0], flag.ContinueOnError)
	fs.SetOutput(errout)
	o := options{}
	fs.StringVar(&o.plan, "plan", "", "structured plan path")
	fs.StringVar(&o.ref, "ref", "", "immutable Git checkpoint (default current working tree)")
	fs.StringVar(&o.format, "format", "json", "json or mermaid")
	fs.StringVar(&o.kind, "kind", "", "node kind")
	fs.StringVar(&o.name, "name", "", "name substring")
	fs.StringVar(&o.from, "from", "", "reachable entity ID")
	fs.StringVar(&o.edge, "edge", "", "edge kind")
	fs.BoolVar(&o.projected, "projected", false, "apply explicit proposed graph deltas for --plan graph view")
	fs.BoolVar(&o.reverse, "reverse", false, "reverse graph traversal")
	fs.StringVar(&o.base, "base", "", "base Git ref")
	fs.StringVar(&o.head, "head", "", "head ref or WORKTREE")
	fs.StringVar(&o.branches, "branches", "", "comma-separated refs")
	fs.StringVar(&o.output, "output", "", "new repository-relative artifact path")
	fs.StringVar(&o.reviewer, "reviewer", "", "explicit local review declaration identity")
	fs.StringVar(&o.evidence, "evidence", "", "comma-separated persisted evidence IDs")
	fs.BoolVar(&o.allow, "allow-execution", false, "explicit opt-in to run declared repository test code")
	fs.DurationVar(&o.timeout, "timeout", 2*time.Minute, "test timeout, maximum 30 minutes")
	if e = fs.Parse(rest[1:]); e != nil {
		return a.fail(e)
	}
	o.args = fs.Args()
	command := rest[0]
	if command == "doctor" {
		return a.doctor()
	}
	switch command {
	case "init", "index", "graph", "plan", "preflight", "tasks", "approve", "impact", "scan", "explain", "verify", "test":
	default:
		return a.fail(fmt.Errorf("unknown command %q; run radar help", command))
	}
	if command == "init" {
		if e = project.Init(root); e != nil {
			return a.fail(e)
		}
		a.emit(map[string]any{"status": "initialized", "root": root, "telemetry": false})
		return 0
	}
	if _, e = project.Read(root); e != nil {
		return a.fail(e)
	}
	db, e := project.SafePath(root, ".radar/state.db")
	if e != nil {
		return a.fail(e)
	}
	a.store, e = storage.Open(ctx, db)
	if e != nil {
		return a.fail(e)
	}
	defer a.store.Close()
	switch command {
	case "index":
		return a.index(o)
	case "graph":
		return a.graph(o)
	case "plan":
		return a.plan(o)
	case "preflight", "tasks", "approve", "verify":
		return a.planCommand(command, o)
	case "impact", "scan":
		return a.contractCommand(command, o)
	case "explain":
		return a.explain(o)
	case "test":
		return a.test(o)
	}
	return 0
}
func (a *app) emit(value any) error {
	e := json.NewEncoder(a.out)
	e.SetIndent("", "  ")
	return e.Encode(value)
}
func (a *app) fail(e error) int {
	if a.machine {
		a.emit(map[string]any{"error": e.Error(), "status": "error"})
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

const help = `Radar — plan against code, coordinate contracts, verify evidence.

Usage: radar <command> [flags] [--root DIR] [--json]
Commands: init doctor index graph plan preflight tasks approve impact scan explain test verify
index/plan/preflight/verify: --ref REF selects immutable committed evidence.
plan [--output path.json] DESCRIPTION creates a grounded incomplete design bundle.
preflight/tasks/approve/verify/test: --plan PATH
approve: --reviewer NAME [--output NEWPATH] records an explicit local review declaration.
test: --ref REF --allow-execution [--timeout 2m] -- COMMAND ARGS
verify: [--ref REF] [--evidence ID,ID]
graph: [--plan PATH --projected] [--ref REF] --format json|mermaid --kind KIND --name NAME --from ID --edge KIND --reverse
impact: --base REF --head REF|WORKTREE; scan: --base REF --branches REF,REF
explain FINDING_ID shows persisted evidence.
No telemetry, cloud inference or destructive Git operations. Test execution requires explicit opt-in.
`
