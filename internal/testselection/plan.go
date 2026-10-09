package testselection

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/model"
)

// Selection modes. None of them implies complete behavioral coverage.
const (
	// ModeTargeted runs plan-declared commands and tests with a direct static
	// relationship to the change (changed tests, import or contract impact,
	// Go package companions, cross-component integration suites).
	ModeTargeted = "targeted"
	// ModeBalanced adds conservative package-root fallbacks for changes whose
	// direct test relationship was not established. "recommended" is an alias.
	ModeBalanced = "balanced"
	// ModeFull runs one discovered whole-suite command per framework and
	// package root, plus plan-declared commands.
	ModeFull = "full"

	TierRequired = "required"
	TierOptional = "optional"

	// DefaultMaxCommands bounds one verification run after grouping.
	DefaultMaxCommands = 16
	// maxGroupedFiles keeps grouped argument vectors well below ARG_MAX.
	maxGroupedFiles = 200
)

// NormalizeMode maps accepted spellings to a mode, or reports an error.
func NormalizeMode(mode string) (string, error) {
	switch mode {
	case "", "recommended", ModeBalanced:
		return ModeBalanced, nil
	case ModeTargeted, ModeFull:
		return mode, nil
	}
	return "", fmt.Errorf("unknown selection mode %q; supported: targeted, balanced (recommended), full", mode)
}

// Omission records a candidate command that was not selected or not executed.
type Omission struct {
	ID          string   `json:"id"`
	Command     []string `json:"command"`
	CWD         string   `json:"cwd"`
	TestFiles   []string `json:"test_files"`
	Tier        string   `json:"tier"`
	Reason      string   `json:"reason"`
	Explanation string   `json:"explanation"`
}

// Selection is a bounded, ordered execution plan derived from a proposal.
// Blocking lists reasons the selection cannot satisfy required verification;
// a gate must not pass on a selection with blocking reasons.
type Selection struct {
	Mode        string     `json:"mode"`
	MaxCommands int        `json:"max_commands"`
	Commands    []Command  `json:"commands"`
	Omitted     []Omission `json:"omitted"`
	Uncovered   []string   `json:"uncovered_changes"`
	Blocking    []string   `json:"blocking"`
	Candidates  int        `json:"candidate_commands"`
	TestFiles   int        `json:"selected_test_files"`
	Inventory   int        `json:"inventory_test_files"`
	Explanation string     `json:"explanation"`
}

// tierFor classifies a selection reason. Only a conservative package fallback
// is optional: every other reason is a direct static or declared relationship.
func tierFor(c Command) string {
	for _, r := range c.EvidenceReasons {
		if r.Code != "package_fallback" {
			return TierRequired
		}
	}
	return TierOptional
}

// Plan derives a bounded execution plan. It never drops a required command
// silently: commands beyond the budget are listed as omitted and blocking.
func Plan(p Proposal, changed []string, mode string, maxCommands int) (Selection, error) {
	mode, err := NormalizeMode(mode)
	if err != nil {
		return Selection{}, err
	}
	if maxCommands <= 0 {
		maxCommands = DefaultMaxCommands
	}
	s := Selection{Mode: mode, MaxCommands: maxCommands, Commands: []Command{}, Omitted: []Omission{}, Uncovered: []string{}, Blocking: []string{}, Inventory: len(p.Inventory.Tests)}
	var candidates []Command
	if mode == ModeFull {
		candidates = fullSuite(p)
	} else {
		for _, c := range p.Commands {
			c.Tier = tierFor(c)
			if mode == ModeTargeted && c.Tier == TierOptional {
				s.Omitted = append(s.Omitted, omission(c, "mode_excluded", "Package-root fallback without an established direct relationship; excluded by targeted mode."))
				continue
			}
			candidates = append(candidates, c)
		}
		candidates = group(candidates)
		s.Uncovered = uncovered(p, changed)
	}
	s.Candidates = len(candidates)
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Tier != b.Tier {
			return a.Tier == TierRequired
		}
		if a.Priority != b.Priority {
			return a.Priority > b.Priority
		}
		return a.ID < b.ID
	})
	for _, c := range candidates {
		if len(s.Commands) >= maxCommands {
			o := omission(c, "budget_exceeded", fmt.Sprintf("Command budget of %d reached after grouping.", maxCommands))
			s.Omitted = append(s.Omitted, o)
			if c.Tier == TierRequired {
				s.Blocking = append(s.Blocking, "required command omitted by budget: "+strings.Join(c.Command, " "))
			}
			continue
		}
		s.Commands = append(s.Commands, c)
		s.TestFiles += len(c.TestFiles)
	}
	for _, c := range s.Commands {
		if c.Tier == TierRequired && !c.ToolAvailable {
			s.Blocking = append(s.Blocking, "required command runner unavailable or unverified: "+strings.Join(c.Command, " "))
		}
	}
	codeChanged := mode == ModeFull
	for _, f := range changed {
		codeChanged = codeChanged || relevant(f)
	}
	if len(s.Commands) == 0 && codeChanged {
		s.Blocking = append(s.Blocking, "no runnable verification command was selected for changed code")
	}
	switch {
	case len(s.Blocking) > 0:
		s.Explanation = "Selection cannot satisfy required verification; inspect blocking and omitted entries."
	case len(s.Uncovered) > 0:
		s.Explanation = "Selected commands have a direct relationship to the change, but some changed files have no established test relationship; they are listed as uncovered."
	case len(s.Commands) == 0:
		s.Explanation = "No changed code or test configuration; no tests were selected."
	default:
		s.Explanation = "Every changed source file has a selected direct or fallback test relationship; this is static evidence, not complete behavioral coverage."
	}
	return s, nil
}

func omission(c Command, reason, explanation string) Omission {
	return Omission{ID: c.ID, Command: c.Command, CWD: c.CWD, TestFiles: c.TestFiles, Tier: c.Tier, Reason: reason, Explanation: explanation}
}

// groupable frameworks accept several test paths in one invocation with the
// same configuration discovery from the same working directory, so one
// invocation observes the same files as the separate ones. Plan-declared
// commands are exact reviewed commands and are never merged.
var groupable = map[string]bool{"pytest": true, "go": true, "jest": true, "vitest": true, "node-test": true}

// group merges same-framework, same-directory, same-tier commands whose
// argument vectors differ only by trailing path arguments.
func group(commands []Command) []Command {
	type key struct{ framework, cwd, tier, prefix string }
	buckets := map[key][]Command{}
	var order []key
	var out []Command
	for _, c := range commands {
		prefix, ok := pathPrefix(c)
		if !ok {
			out = append(out, c)
			continue
		}
		k := key{c.Framework, c.CWD, c.Tier, strings.Join(prefix, "\x00")}
		if _, seen := buckets[k]; !seen {
			order = append(order, k)
		}
		buckets[k] = append(buckets[k], c)
	}
	for _, k := range order {
		members := buckets[k]
		if len(members) == 1 {
			out = append(out, members[0])
			continue
		}
		prefix := strings.Split(k.prefix, "\x00")
		var paths []string
		for _, c := range members {
			paths = append(paths, c.Command[len(prefix):]...)
		}
		paths = unique(paths)
		// "./..." already covers every package below the module root.
		if k.framework == "go" && has(paths, "./...") {
			paths = []string{"./..."}
		}
		for start := 0; start < len(paths); start += maxGroupedFiles {
			end := start + maxGroupedFiles
			if end > len(paths) {
				end = len(paths)
			}
			chunk := paths[start:end]
			merged := Command{Command: append(append([]string(nil), prefix...), chunk...), CWD: k.cwd, Framework: k.framework, Tier: k.tier, TestFiles: []string{}, Affected: []string{}, ToolAvailable: true}
			for _, c := range members {
				if !coversAny(c.Command[len(prefix):], chunk) {
					continue
				}
				merged.TestFiles = append(merged.TestFiles, c.TestFiles...)
				merged.Affected = append(merged.Affected, c.Affected...)
				merged.EvidenceReasons = append(merged.EvidenceReasons, c.EvidenceReasons...)
				merged.ToolAvailable = merged.ToolAvailable && c.ToolAvailable
				if c.Priority > merged.Priority {
					merged.Priority = c.Priority
				}
				merged.GroupedFrom = append(merged.GroupedFrom, c.ID)
			}
			merged.TestFiles = unique(merged.TestFiles)
			merged.Affected = unique(merged.Affected)
			merged.GroupedFrom = unique(merged.GroupedFrom)
			merged.ID = commandID(merged)
			out = append(out, merged)
		}
	}
	return out
}

func coversAny(args, chunk []string) bool {
	for _, a := range args {
		if has(chunk, a) || (a != "./..." && has(chunk, "./...")) {
			return true
		}
	}
	return false
}

// pathPrefix returns the fixed runner prefix of a recognized per-path
// command, whose remaining arguments are all repository-relative paths.
func pathPrefix(c Command) ([]string, bool) {
	if !groupable[c.Framework] {
		return nil, false
	}
	var n int
	switch c.Framework {
	case "pytest":
		n = 3 // python3 -m pytest
	case "go":
		n = 3 // go test -json
	case "jest":
		n = 3 // jest --runInBand --runTestsByPath
	case "vitest":
		n = 2 // vitest run
	case "node-test":
		n = 3 // node --test --test-reporter=tap
	}
	if len(c.Command) <= n {
		return nil, false
	}
	for _, a := range c.Command[n:] {
		if !strings.HasPrefix(a, "./") {
			return nil, false
		}
	}
	return c.Command[:n], true
}

// fullSuite proposes one whole-suite command per framework and package root,
// letting the runner's own configuration choose the tests.
func fullSuite(p Proposal) []Command {
	var out []Command
	for _, c := range p.Commands {
		if c.Framework == "plan-declared" {
			c.Tier = TierRequired
			out = append(out, c)
		}
	}
	type key struct{ framework, root string }
	seen := map[key][]string{}
	var order []key
	tools := map[string]bool{}
	for _, c := range p.Commands {
		tools[c.Framework+"\x00"+c.CWD] = c.ToolAvailable
	}
	for _, t := range p.Inventory.Tests {
		k := key{t.Framework, t.PackageRoot}
		if _, ok := seen[k]; !ok {
			order = append(order, k)
		}
		seen[k] = append(seen[k], t.Path)
	}
	for _, k := range order {
		var argv []string
		cwd := k.root
		switch k.framework {
		case "pytest":
			argv = []string{"python3", "-m", "pytest"}
		case "unittest":
			argv = []string{"python3", "-m", "unittest", "discover"}
		case "go":
			argv = []string{"go", "test", "-json", "./..."}
		case "jest":
			argv = []string{"./node_modules/.bin/jest", "--runInBand"}
		case "vitest":
			argv = []string{"./node_modules/.bin/vitest", "run"}
		case "cargo":
			argv, cwd = []string{"cargo", "test", "--manifest-path", path.Join(k.root, "Cargo.toml")}, "."
		default:
			continue
		}
		available, known := tools[k.framework+"\x00"+cwd]
		if !known {
			available = k.framework != "jest" && k.framework != "vitest"
		}
		c := Command{Command: argv, CWD: cwd, Framework: k.framework, Tier: TierRequired, TestFiles: unique(seen[k]), Affected: []string{}, Priority: 50, ToolAvailable: available, EvidenceReasons: []Reason{{Code: "full_suite", Explanation: "Whole discovered suite for this framework and package root; the runner's configuration selects tests.", Evidence: model.Inferred}}}
		c.ID = commandID(c)
		out = append(out, c)
	}
	return out
}

// uncovered lists changed non-documentation files, including data and
// schema files, that no required command relates to.
func uncovered(p Proposal, changed []string) []string {
	tests := map[string]bool{}
	for _, t := range p.Inventory.Tests {
		tests[t.Path] = true
	}
	related := map[string]bool{}
	for _, c := range p.Commands {
		// A reviewed plan command relates to the whole change but proves no
		// static relationship, so it does not mark files as covered.
		if tierFor(c) != TierRequired || c.Framework == "plan-declared" {
			continue
		}
		for _, r := range c.EvidenceReasons {
			for _, f := range r.RelatedFiles {
				related[f] = true
			}
		}
	}
	out := []string{}
	for _, f := range changed {
		if tests[f] || isManifest(f) || documentation(f) || related[f] {
			continue
		}
		out = append(out, f)
	}
	return unique(out)
}

func commandID(c Command) string {
	return model.StableID("verification-command", c.CWD, strings.Join(c.Command, "\x00"))
}
