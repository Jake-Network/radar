package integration

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/evidence"
	"github.com/Jake-Network/radar/internal/indexer"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
	"github.com/Jake-Network/radar/internal/runners"
	"github.com/Jake-Network/radar/internal/typecheck"
)

// typecheckProfiles loads immutable execution declarations and replaces an
// automatically selected tool only within the matching profile. File selectors
// trigger a declaration; the tool's own inventory still establishes coverage.
func typecheckProfiles(ctx context.Context, provider indexer.Provider, plan *typecheck.Plan, changed []string) (runners.Config, error) {
	profiles, err := runners.Load(ctx, provider)
	if err != nil {
		return profiles, err
	}
	type key struct{ root, tool string }
	declared := map[key]*typecheck.Target{}
	checks := map[key]runners.Typecheck{}
	for _, profile := range profiles.Profiles {
		for _, check := range profile.Typecheck {
			k := key{profile.Root, check.Tool}
			checks[k] = check
			declared[k] = &typecheck.Target{
				Tool: check.Tool, Root: profile.Root, CWD: profile.Root,
				Command: append([]string(nil), check.Command...), Custom: true,
			}
		}
	}
	selected := map[key]bool{}
	var automatic []typecheck.Target
	for _, target := range plan.Targets {
		profile := profiles.Match(target.CWD)
		if profile == nil || declared[key{profile.Root, target.Tool}] == nil {
			automatic = append(automatic, target)
			continue
		}
		k := key{profile.Root, target.Tool}
		selected[k] = true
		custom := declared[k]
		custom.Changed = appendUnique(custom.Changed, target.Changed...)
		custom.Via = appendUnique(custom.Via, target.Via...)
		custom.Packages = appendUnique(custom.Packages, target.Packages...)
		custom.Paths = appendUnique(custom.Paths, target.Paths...)
		custom.Deleted = appendUnique(custom.Deleted, target.Deleted...)
	}
	for _, file := range changed {
		profile := profiles.Match(path.Dir(file))
		if profile == nil {
			continue
		}
		rel := strings.TrimPrefix(file, profile.Root+"/")
		if profile.Root == "." {
			rel = file
		}
		for _, check := range profile.Typecheck {
			matches := len(check.Files) == 0 && typecheckFileForTool(check.Tool, file)
			for _, pattern := range check.Files {
				matches = matches || runners.MatchFile(pattern, rel)
			}
			if !matches {
				continue
			}
			k := key{profile.Root, check.Tool}
			selected[k] = true
			if typecheckFileForTool(check.Tool, file) {
				declared[k].Changed = appendUnique(declared[k].Changed, file)
			}
		}
	}
	// A declared Python command can check a project without an automatic
	// configuration marker. Preserve deleted paths for its import probe.
	if len(selected) > 0 {
		entries, err := provider.Entries(ctx)
		if err != nil {
			return profiles, err
		}
		present := map[string]bool{}
		var pythonPaths []string
		for _, e := range entries {
			present[e.Path] = true
			if typecheckFileForTool("mypy", e.Path) && e.Mode == indexer.ModeRegular {
				pythonPaths = append(pythonPaths, e.Path)
			}
		}
		for k := range selected {
			t := declared[k]
			if k.tool == "mypy" {
				t.Paths = appendUnique(t.Paths, pythonPaths...)
				for _, file := range changed {
					if !present[file] && typecheckFileForTool("mypy", file) {
						t.Paths = appendUnique(t.Paths, file)
						t.Deleted = appendUnique(t.Deleted, file)
					}
				}
			}
		}
	}
	plan.Targets = automatic
	covered := map[string]bool{}
	for k := range selected {
		t := declared[k]
		sort.Strings(t.Changed)
		sort.Strings(t.Via)
		t.ID = model.StableID("declared-typecheck", k.root, k.tool, strings.Join(checks[k].Command, "\x00"))
		plan.Targets = append(plan.Targets, *t)
		for _, file := range t.Changed {
			covered[file] = true
		}
	}
	var unconfigured []string
	for _, file := range plan.Unconfigured {
		if !covered[file] {
			unconfigured = append(unconfigured, file)
		}
	}
	plan.Unconfigured = unconfigured
	var gaps []typecheck.Gap
	for _, gap := range plan.Gaps {
		if !covered[gap.Path] {
			gaps = append(gaps, gap)
		}
	}
	plan.Gaps = gaps
	sort.Slice(plan.Targets, func(i, j int) bool {
		a, b := plan.Targets[i], plan.Targets[j]
		return a.Root < b.Root || a.Root == b.Root && a.Tool < b.Tool
	})
	return profiles, nil
}

func appendUnique(into []string, values ...string) []string {
	for _, value := range values {
		found := false
		for _, existing := range into {
			found = found || existing == value
		}
		if !found {
			into = append(into, value)
		}
	}
	return into
}

func typecheckFileForTool(tool, file string) bool {
	ext := strings.ToLower(path.Ext(file))
	switch tool {
	case "go":
		return ext == ".go"
	case "mypy", "ruff":
		return ext == ".py" || ext == ".pyi"
	case "tsc":
		return ext == ".ts" || ext == ".tsx" || ext == ".mts" || ext == ".cts"
	case "cargo":
		return ext == ".rs"
	case "java":
		return ext == ".java" || ext == ".kt" || ext == ".kts"
	case "cpp":
		switch ext {
		case ".c", ".cc", ".cpp", ".cxx", ".c++", ".h", ".hh", ".hpp", ".hxx", ".h++", ".ipp", ".tpp", ".inl":
			return true
		}
	}
	return false
}

// A preparation belongs to a single private candidate. Base comparisons use
// another instance so neither dependency artifacts nor prepared state cross
// the candidate/base boundary.
type typecheckPreparation struct {
	profiles  runners.Config
	prepared  map[string]evidence.RunnerPreparation
	artifacts []string
	last      *evidence.RunnerPreparation
}

func newTypecheckPreparation(profiles runners.Config) *typecheckPreparation {
	return &typecheckPreparation{profiles: profiles, prepared: map[string]evidence.RunnerPreparation{}}
}

func (p *typecheckPreparation) prepare(ctx context.Context, dir string, cp evidence.CandidateCheckpoint, t typecheck.Target, r *Report, o Options) (typecheck.Target, string, string) {
	p.last = nil
	logical := t.LogicalCommand
	if len(logical) == 0 {
		logical = append([]string(nil), t.Command...)
	}
	profile := p.profiles.Match(t.CWD)
	if profile != nil {
		prep, seen := p.prepared[profile.Root]
		if !seen {
			state, stateErr := runtimeState()
			cacheDir, cacheErr := pathutil.StatePath(state, "runner-cache")
			if stateErr != nil || cacheErr != nil {
				return t, "runner_setup_unavailable", "the declared runner cache location could not be resolved"
			}
			timeout := typecheckRemaining(ctx, o.Timeout)
			if timeout <= 0 {
				return t, "time_budget_exhausted", "the verification timeout expired before declared setup"
			}
			var err error
			prep, err = evidence.PrepareRunnerBudget(ctx, dir, cp.Repository, *profile, runners.Cache{Dir: cacheDir, MaxBytes: p.profiles.MaxCacheBytes}, timeout, func() bool { return o.budget == nil || o.budget.take(1) })
			if err != nil {
				prep.Status = model.StatusIncomplete
				prep.Error = evidence.PreparationDiagnostic(dir, err)
			}
			p.artifacts = appendUnique(p.artifacts, prep.Artifacts...)
			sourceCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			inspection, inspectErr := evidence.InspectCandidateSource(sourceCtx, dir, cp, p.artifacts)
			cancel()
			if inspectErr != nil || !inspection.Matches {
				prep.Status = model.StatusIncomplete
				prep.Error = "Declared runner setup changed source or source integrity is unknown"
			}
			p.prepared[profile.Root] = prep
			r.RunnerPreparations = append(r.RunnerPreparations, prep)
		}
		p.last = &prep
		if prep.Status != model.StatusPassed {
			return t, "runner_setup_unavailable", "declared setup did not complete against unchanged source"
		}
	}
	if t.Tool == typecheck.ToolTSC && !t.Custom {
		var err error
		logical, err = evidence.ResolveTypeScriptCommand(dir, t.CWD, logical)
		if err != nil {
			return t, "environment_unavailable", "a candidate-local TypeScript compiler is unavailable: " + evidence.PreparationDiagnostic(dir, err)
		}
	}
	if t.Tool == "cpp" && t.Custom {
		var ok bool
		logical, ok = typecheck.CanonicalCPPCommand(logical)
		if !ok {
			return t, "environment_unavailable", "the declared C++ command is not a supported direct GCC or Clang syntax check"
		}
	}
	t.LogicalCommand = append([]string(nil), logical...)
	t.Artifacts = append([]string(nil), p.artifacts...)
	t.Command = append([]string(nil), logical...)
	if profile != nil {
		t.Command = profile.RewriteTypecheck(logical)
	}
	if len(t.Command) == 0 {
		return t, "environment_unavailable", "the type-check command is empty"
	}
	return t, "", ""
}

func typecheckRemaining(ctx context.Context, timeout time.Duration) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		timeout = min(timeout, time.Until(deadline))
	}
	return timeout
}

func uncheckedDeclaredTypecheck(t typecheck.Target) string {
	if t.Custom && t.Tool != "mypy" && t.Tool != "tsc" && t.Tool != "go" && t.Tool != "cpp" && t.Tool != "ruff" {
		return fmt.Sprintf("the declared %s command has no verified source inventory; its files selectors only trigger execution", t.Tool)
	}
	return ""
}

// A declaration's trigger globs cannot establish what an arbitrary checker
// command actually read. Keep tools without an input inventory incomplete,
// even when the command exits successfully or no surviving changed file exists.
func recordDeclaredTypecheckCoverage(r *Report, dir string, t typecheck.Target, ev *TypecheckExecution) {
	reason := uncheckedDeclaredTypecheck(t)
	if reason == "" {
		return
	}
	seen := map[string]bool{}
	for _, g := range r.TypecheckGaps {
		seen[g.Path] = true
	}
	for _, file := range t.Changed {
		if seen[file] || !typecheckFileForTool(t.Tool, file) {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(file))); err != nil || !info.Mode().IsRegular() {
			continue
		}
		seen[file] = true
		r.TypecheckGaps = append(r.TypecheckGaps, typecheck.Gap{Path: file, Reason: reason})
	}
	if ev.Status == model.StatusPassed {
		ev.Status = model.StatusIncomplete
		ev.ExecutionError = reason
	}
}
