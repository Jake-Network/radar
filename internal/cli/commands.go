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
	bases, with, only         []string
	again                     bool
	replay, id                string
	fields, direction, source string
	into                      []string
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
	fs.StringVar(&o.plan, "plan", "", "structured plan `PATH`")
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

// helpCopy is the everyday help: a short line for the command list, worked
// examples and the safety notes a person needs before running the command.
var helpCopy = map[string]struct {
	brief    string
	examples [][2]string
	notes    []string
}{
	"gate": {brief: "Combine branches and tell whether they work together",
		examples: [][2]string{
			{"radar gate", "check every worktree branch with commits beyond the base"},
			{"radar gate agent-a agent-b", "combine only the named branches"},
			{"radar gate --run", "also run the related tests on the combined tree"},
			{"radar gate --run --suite full", "run the whole supported test inventory (within --max-commands)"},
			{"radar gate --with ../payments", "check another repository in the same run"},
		},
		notes: []string{
			"Without --run, Radar reads committed Git objects only and runs no repository code.",
			"With --run, selected tests run with your permissions in a private copy of the combined\nbranches (not an OS sandbox). Your checkout and branches are never changed.",
			"Exit codes: 0 pass, 1 fail (with --run or --policy also not verified), 2 error.",
		}},
	"workspace": {brief: "Group repositories so gate checks them together",
		examples: [][2]string{
			{"radar workspace add ../payments", "register a sibling repository with this one"},
			{"radar workspace show", "list the repositories radar gate will check"},
		}},
	"check": {brief: "Analyze changed files, dependency impact and contracts",
		examples: [][2]string{
			{"radar check --base main", "analyze the working tree against main"},
			{"radar check --base main --suggest-tests", "also recommend tests (nothing runs)"},
		}},
	"discover": {brief: "Propose contract relationships, with evidence",
		examples: [][2]string{{"radar discover", "list proposed producer/consumer relationships; accepted bindings are never changed"}}},
	"setup": {brief: "Install project-local agent skills and MCP config",
		examples: [][2]string{
			{"radar setup --agent claude --dry-run", "show what would be written"},
			{"radar setup --agent both", "configure Codex and Claude Code for this project"},
		}},
	"doctor": {brief: "Check tools, languages and repository state"},
	"mcp":    {brief: "Serve Radar tools to coding agents over MCP (stdio)"},
}

func (c command) usageText(w io.Writer) {
	p := paletteOf(w)
	fmt.Fprintf(w, "%s %s [--root DIR] [--json]\n\n%s\n", p.bold("Usage:"), c.usage, c.summary)
	copy := helpCopy[c.name]
	if len(copy.examples) > 0 {
		fmt.Fprintln(w, "\n"+p.bold("Examples:"))
		width := 0
		for _, e := range copy.examples {
			width = max(width, len(e[0]))
		}
		for _, e := range copy.examples {
			fmt.Fprintf(w, "  %s%s  %s\n", p.command(e[0]), strings.Repeat(" ", width-len(e[0])), p.dim(e[1]))
		}
	}
	if len(copy.notes) > 0 {
		fmt.Fprintln(w)
		for _, n := range copy.notes {
			fmt.Fprintln(w, n)
		}
	}
	if c.flags == nil {
		return
	}
	fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
	c.flags(fs, &options{})
	type row struct{ left, usage string }
	rows, width := []row{}, 0
	fs.VisitAll(func(f *flag.Flag) {
		if slices.Contains(c.advancedFlags, f.Name) {
			return
		}
		name, usage := flag.UnquoteUsage(f)
		left := "--" + f.Name
		if name != "" {
			left += " " + name
		}
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" && !strings.Contains(usage, "default") {
			usage += " (default " + f.DefValue + ")"
		}
		rows = append(rows, row{left, usage})
		width = max(width, len(left))
	})
	width = min(width, 24)
	fmt.Fprintln(w, "\n"+p.bold("Flags:"))
	for _, r := range rows {
		if len(r.left) > width {
			fmt.Fprintf(w, "  %s\n  %s  %s\n", p.cyan(r.left), strings.Repeat(" ", width), r.usage)
			continue
		}
		fmt.Fprintf(w, "  %s%s  %s\n", p.cyan(r.left), strings.Repeat(" ", width-len(r.left)), r.usage)
	}
	if len(c.advancedFlags) > 0 {
		fmt.Fprintln(w, "\nMore flags: radar help --all")
	}
}

// advancedUsage lists flags and subcommands kept out of the everyday help.
var advancedUsage = [][2]string{
	{"radar gate --base REPO:REF", "base for one workspace repository (repeatable)"},
	{"radar gate --only REPO", "check only these workspace repositories (repeatable)"},
	{"radar gate --replay RUN", "rebuild a recorded workspace run's exact commits (run ID or last)"},
	{"radar workspace remove REPO", "unregister a repository from this repository's workspace"},
	{"radar workspace connect PRODUCER CONSUMER_REPO --fields a,b --direction request|response [--id ID] [--source PATH] [--into repo:PATH]", "declare a cross-repository contract; commit both written files before gate"},
}

// helpGroups orders the everyday commands by what a person is doing.
var helpGroups = []struct {
	title    string
	commands []string
}{
	{"Everyday", []string{"gate", "workspace", "check", "discover", "doctor"}},
	{"Agent setup", []string{"setup", "mcp"}},
}

const tagline = "Know whether your agents' branches work together before you merge them."

func printHelp(w io.Writer, all bool) {
	p := paletteOf(w)
	if p.on {
		printLogo(w, p, "radar "+Version, tagline)
	} else {
		fmt.Fprintln(w, "Radar — "+strings.ToLower(tagline[:1])+tagline[1:])
	}
	fmt.Fprintln(w, "\n"+p.bold("Usage:")+" radar <command> [flags] [--root DIR] [--json] [--color auto|always|never]")

	section := func(title string) { fmt.Fprintln(w, "\n"+p.bold(title)) }
	example := func(cmd, what string) {
		fmt.Fprintf(w, "  %s%s  %s\n", p.command(cmd), strings.Repeat(" ", max(0, 33-len(cmd))), p.dim(what))
	}
	section("Start here")
	example("radar", "interactive menu (in a terminal)")
	example("radar gate", "check every worktree branch together; runs no code")
	example("radar gate --run", "…and run the related tests on the combined tree")
	example("radar workspace add ../other-repo", "check several repositories together")

	width := 0
	for _, c := range commands {
		width = max(width, len(c.name))
	}
	line := func(c command) {
		text := c.summary
		if b := helpCopy[c.name].brief; b != "" && !all {
			text = b
		}
		fmt.Fprintf(w, "  %s%s  %s\n", p.cyan(c.name), strings.Repeat(" ", width-len(c.name)), text)
	}
	for _, g := range helpGroups {
		section(g.title)
		for _, name := range g.commands {
			if c, ok := lookup(name); ok {
				line(c)
			}
		}
	}
	advanced := 0
	for _, c := range commands {
		if c.advanced {
			advanced++
		}
	}
	if all {
		section("Advanced (merge-check flags, plans, evidence, contracts, graph)")
		for _, c := range commands {
			if c.advanced {
				line(c)
			}
		}
		section("Advanced workspace flags and subcommands")
		for _, u := range advancedUsage {
			fmt.Fprintf(w, "  %s  %s\n", p.cyan(fmt.Sprintf("%-28s", u[0])), u[1])
		}
	} else {
		section("More")
		fmt.Fprintf(w, "  %d advanced commands (plans, evidence, contracts, graph queries): %s\n", advanced, p.command("radar help --all"))
	}
	fmt.Fprintf(w, "  Flags and examples for one command: %s\n", p.command("radar help COMMAND"))

	fmt.Fprintln(w)
	fmt.Fprintf(w, "%s  0 ok · 1 a check failed or required evidence is missing · 2 error\n", p.dim("Exit codes"))
	fmt.Fprintf(w, "%s     local only: no telemetry, no LLM calls; never modifies your branches.\n", p.dim("Privacy"))
	fmt.Fprintln(w, "            Repository code runs only with --run / --allow-execution.")
}
