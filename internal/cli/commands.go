package cli

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/project"
)

type options struct {
	plan, ref, format, kind, name, from, edge, base, head, branches, output, reviewer, evidence, agent, policy, suite, cwd string
	reverse, allow, projected, strict, summary, dryRun, hook, verify, suggestTests                                         bool
	depth, limit                                                                                                           int
	timeout                                                                                                                time.Duration
	maxCommands                                                                                                            int
	args                                                                                                                   []string
	// Workspace gate and workspace command options.
	bases, with, only []string
	again             bool
	replay, id        string
}

// listFlag collects a repeatable flag's values in order.
type listFlag struct{ values *[]string }

func (f listFlag) String() string {
	if f.values == nil {
		return ""
	}
	return strings.Join(*f.values, ",")
}
func (f listFlag) Set(v string) error { *f.values = append(*f.values, v); return nil }

// command describes one subcommand: only its own flags are accepted.
type command struct {
	name, usage, summary string
	state                bool // requires initialized .radar state
	advanced             bool // listed only by `radar help --all`
	// advancedFlags are accepted but listed only by `radar help --all`.
	advancedFlags []string
	flags         func(*flag.FlagSet, *options)
	run           func(*app, options) int
}

func planFlag(fs *flag.FlagSet, o *options) {
	fs.StringVar(&o.plan, "plan", "", "structured plan path")
}
func refFlag(fs *flag.FlagSet, o *options, usage string) {
	fs.StringVar(&o.ref, "ref", "", usage)
}

const refUsage = "immutable Git checkpoint (default: current working tree)"

// commands is populated in init because mcp re-enters Run, which reads it.
var commands []command

func init() {
	commands = []command{
		gateCommand(),
		workspaceCommand(),
		checkCommand(),
		discoveryCommand(),
		setupCommand(),
		{name: "doctor", usage: "radar doctor", summary: "Report capabilities, tools and repository state.", run: (*app).doctor},
		{name: "mcp", usage: "radar mcp [--root DIR]", summary: "Serve Radar tools to coding agents over the Model Context Protocol (stdio).", run: (*app).mcp},
		mergeCheckCommand(),
		{name: "init", usage: "radar init", summary: "Create .radar state for this repository (linked worktrees share the main worktree's state).",
			run: func(a *app, _ options) int {
				if e := project.Init(a.root); e != nil {
					return a.fail(e)
				}
				a.report(map[string]any{"status": "initialized", "root": a.root, "telemetry": false}, func(w io.Writer) {
					fmt.Fprintf(w, "Initialized Radar state in %s/.radar (no telemetry).\n", a.root)
				})
				return 0
			}},
		{name: "index", usage: "radar index [--ref REF] [--summary]", summary: "Index source structure, import dependencies and explicit contracts.", state: true,
			flags: func(fs *flag.FlagSet, o *options) {
				refFlag(fs, o, refUsage)
				fs.BoolVar(&o.summary, "summary", false, "with --json, print counts and diagnostics instead of the full snapshot")
			}, run: (*app).index},
		{name: "graph", usage: "radar graph [--ref REF] [--plan PATH [--projected]] [--kind K] [--name S] [--from ID [--edge KIND] [--reverse] [--depth N]] [--format text|json|mermaid]", summary: "Query the indexed graph or a plan's intent overlay.", state: true,
			flags: func(fs *flag.FlagSet, o *options) {
				refFlag(fs, o, refUsage)
				planFlag(fs, o)
				fs.BoolVar(&o.projected, "projected", false, "apply explicit proposed graph deltas (requires --plan)")
				fs.StringVar(&o.format, "format", "", "text, json or mermaid (default text, or json with --json)")
				fs.StringVar(&o.kind, "kind", "", "node kind")
				fs.StringVar(&o.name, "name", "", "name substring")
				fs.StringVar(&o.from, "from", "", "start traversal at this entity ID")
				fs.StringVar(&o.edge, "edge", "", "edge kind to follow")
				fs.BoolVar(&o.reverse, "reverse", false, "traverse edges backwards")
				fs.IntVar(&o.depth, "depth", 0, "maximum traversal depth (0 = unlimited)")
			}, run: (*app).graph},
		{name: "resolve", usage: "radar resolve QUERY [--kind K] [--ref REF] [--limit N]", summary: "Find entity IDs for a name, path or path#Qualified.Name.", state: true,
			flags: func(fs *flag.FlagSet, o *options) {
				refFlag(fs, o, "resolve against this checkpoint (default: latest index)")
				fs.StringVar(&o.kind, "kind", "", "restrict to a node kind")
				fs.IntVar(&o.limit, "limit", 20, "maximum results")
			}, run: (*app).resolve},
		{name: "plan", usage: "radar plan [--ref REF] [--output PATH] DESCRIPTION", summary: "Write a grounded, incomplete design bundle for an agent to fill.", state: true,
			flags: func(fs *flag.FlagSet, o *options) {
				refFlag(fs, o, refUsage)
				fs.StringVar(&o.output, "output", "", "new repository-relative plan path")
			}, run: (*app).plan},
		{name: "preflight", usage: "radar preflight --plan PATH [--ref REF]", summary: "Check a plan against the indexed baseline; prints next steps.", state: true,
			flags: func(fs *flag.FlagSet, o *options) { planFlag(fs, o); refFlag(fs, o, refUsage) }, run: planCommand("preflight")},
		{name: "tasks", usage: "radar tasks --plan PATH", summary: "Emit the task DAG, parallel groups and per-task instruction packets.", state: true,
			flags: planFlag, run: planCommand("tasks")},
		{name: "approve", usage: "radar approve --plan PATH --reviewer NAME [--output PATH]", summary: "Record a local, digest-bound review declaration after a human review.", state: true,
			flags: func(fs *flag.FlagSet, o *options) {
				planFlag(fs, o)
				fs.StringVar(&o.reviewer, "reviewer", "", "identity of the person who reviewed the plan")
				fs.StringVar(&o.output, "output", "", "new repository-relative path for the reviewed plan")
			}, run: planCommand("approve")},
		{name: "verify", usage: "radar verify --plan PATH [--ref REF] [--evidence ID,ID]", summary: "Verify an implementation against the plan's rules and test evidence.", state: true,
			flags: func(fs *flag.FlagSet, o *options) {
				planFlag(fs, o)
				refFlag(fs, o, "implementation commit (default: working tree, informational)")
				fs.StringVar(&o.evidence, "evidence", "", "comma-separated persisted evidence IDs")
			}, run: planCommand("verify")},
		{name: "test", usage: "radar test --plan PATH --ref REF --allow-execution [--timeout 2m] -- COMMAND ARGS", summary: "Run a plan-declared test command in a private committed snapshot.", state: true,
			flags: func(fs *flag.FlagSet, o *options) {
				planFlag(fs, o)
				refFlag(fs, o, "commit to test")
				fs.BoolVar(&o.allow, "allow-execution", false, "explicit opt-in to run declared repository test code")
				fs.DurationVar(&o.timeout, "timeout", 2*time.Minute, "test timeout, maximum 30 minutes")
			}, run: (*app).test},
		{name: "impact", usage: "radar impact --base REF --head REF|WORKTREE [--strict]", summary: "Compare declared contracts between two checkpoints.", state: true,
			flags: func(fs *flag.FlagSet, o *options) {
				fs.StringVar(&o.base, "base", "", "base Git ref")
				fs.StringVar(&o.head, "head", "", "head ref or WORKTREE")
				fs.BoolVar(&o.strict, "strict", false, "exit 1 when a binding could not be analyzed")
			}, run: contractCommand("impact")},
		{name: "scan", usage: "radar scan --base REF --branches REF,REF [--strict]", summary: "Find contract conflicts across concurrent branches.", state: true,
			flags: func(fs *flag.FlagSet, o *options) {
				fs.StringVar(&o.base, "base", "", "base Git ref")
				fs.StringVar(&o.branches, "branches", "", "comma-separated refs")
				fs.BoolVar(&o.strict, "strict", false, "exit 1 when a binding could not be analyzed")
			}, run: contractCommand("scan")},
		{name: "affected", usage: "radar affected --base REF [--head REF|WORKTREE] [--plan PATH] [--depth N]", summary: "List files, contracts and plan tasks affected by changed files through import dependencies.", state: true,
			flags: func(fs *flag.FlagSet, o *options) {
				fs.StringVar(&o.base, "base", "", "base Git ref")
				fs.StringVar(&o.head, "head", "WORKTREE", "head ref or WORKTREE")
				planFlag(fs, o)
				fs.IntVar(&o.depth, "depth", 0, "maximum dependency depth (0 = unlimited)")
			}, run: (*app).affected},
		{name: "contracts", usage: "radar contracts [--ref REF]", summary: "Lint .radar/contracts.json: schemas, pointers, declared fields and stale consumers.", state: true,
			flags: func(fs *flag.FlagSet, o *options) { refFlag(fs, o, "checkpoint to lint (default: working tree)") }, run: (*app).contracts},
		{name: "explain", usage: "radar explain FINDING_ID", summary: "Show a persisted finding with its evidence.", state: true, run: (*app).explain},
	}
	// The plan/evidence/graph toolkit stays out of the default help.
	core := map[string]bool{"gate": true, "workspace": true, "check": true, "discover": true, "setup": true, "doctor": true, "mcp": true}
	for i := range commands {
		commands[i].advanced = !core[commands[i].name]
	}
}

func lookup(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

func (c command) usageText(w io.Writer) {
	fmt.Fprintf(w, "Usage: %s [--root DIR] [--json]\n\n%s\n", c.usage, c.summary)
	if c.flags == nil {
		return
	}
	fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
	c.flags(fs, &options{})
	fmt.Fprintln(w, "\nFlags:")
	visible := flag.NewFlagSet(c.name, flag.ContinueOnError)
	fs.VisitAll(func(f *flag.Flag) {
		if !slices.Contains(c.advancedFlags, f.Name) {
			visible.Var(f.Value, f.Name, f.Usage)
		}
	})
	visible.SetOutput(w)
	visible.PrintDefaults()
}

// advancedUsage lists flags and subcommands kept out of the everyday help.
var advancedUsage = [][2]string{
	{"radar gate --base REPO:REF", "base for one workspace repository (repeatable)"},
	{"radar gate --only REPO", "check only these workspace repositories (repeatable)"},
	{"radar gate --replay RUN", "rebuild a recorded workspace run's exact commits (run ID or last)"},
	{"radar workspace remove REPO", "unregister a repository from this repository's workspace"},
}

func printHelp(w io.Writer, all bool) {
	fmt.Fprintln(w, "Radar — know whether your agents' branches work together before you merge them.")
	fmt.Fprintln(w, "\nUsage: radar <command> [flags] [--root DIR] [--json]")
	width := 0
	for _, c := range commands {
		width = max(width, len(c.name))
	}
	list := func(advanced bool) (n int) {
		for _, c := range commands {
			if c.advanced == advanced {
				fmt.Fprintf(w, "  %-*s  %s\n", width, c.name, c.summary)
				n++
			}
		}
		return n
	}
	fmt.Fprintln(w, "\nCommands:")
	list(false)
	if all {
		fmt.Fprintln(w, "\nAdvanced (merge-check flags, plans, evidence, contracts, graph):")
		list(true)
		fmt.Fprintln(w, "\nAdvanced workspace flags and subcommands:")
		for _, u := range advancedUsage {
			fmt.Fprintf(w, "  %-28s  %s\n", u[0], u[1])
		}
	} else {
		n := 0
		for _, c := range commands {
			if c.advanced {
				n++
			}
		}
		fmt.Fprintf(w, "\n%d advanced commands (plans, evidence, contracts, graph queries): radar help --all\n", n)
	}
	fmt.Fprintln(w, "\nStart here:  radar gate            # combine every worktree branch, check, suggest tests")
	fmt.Fprintln(w, "             radar gate --run      # ...and run those tests on the combined tree")
	fmt.Fprintln(w, "             radar workspace add ../other-repo   # check several repositories together")
	fmt.Fprintln(w, "\nRun `radar help COMMAND` for flags. Exit codes: 0 ok, 1 a check failed or required evidence is missing, 2 error.")
	fmt.Fprintln(w, "No telemetry, no LLM calls, never modifies your branches. Repository code runs only with --run / --allow-execution.")
}
