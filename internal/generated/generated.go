// Package generated compares explicitly declared source/output relationships.
// Static observations are warnings: output correctness requires regeneration.
package generated

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
)

const ManifestPath = ".radar/generated.json"
const CheckID = "generated_drift"

type Rule struct {
	Name    string   `json:"name,omitempty"`
	Sources []string `json:"sources"`
	Outputs []string `json:"outputs"`
	Command []string `json:"command,omitempty"`
	CWD     string   `json:"cwd,omitempty"`
}
type Manifest struct {
	Version    int      `json:"version,omitempty"`
	Generators []Rule   `json:"generators,omitempty"`
	Sources    []string `json:"sources,omitempty"`
	Outputs    []string `json:"outputs,omitempty"`
	Command    []string `json:"command,omitempty"`
	CWD        string   `json:"cwd,omitempty"`
}
type Report struct {
	Applicable  bool
	Status      model.Status
	Evidence    model.Evidence
	Explanation string
	Findings    []model.Finding
	Diagnostics []model.Diagnostic
	Rules       []Rule
}

func Decode(data []byte) ([]Rule, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&m); e != nil {
		return nil, e
	}
	var trailing any
	if e := dec.Decode(&trailing); e != io.EOF {
		return nil, fmt.Errorf("generated manifest must contain exactly one object")
	}
	if m.Version != 0 && m.Version != 1 {
		return nil, fmt.Errorf("unsupported generated manifest version %d", m.Version)
	}
	rules := m.Generators
	if len(m.Sources) > 0 || len(m.Outputs) > 0 || len(m.Command) > 0 || m.CWD != "" {
		if len(rules) > 0 {
			return nil, fmt.Errorf("cannot mix single generator and generators list")
		}
		rules = []Rule{{Sources: m.Sources, Outputs: m.Outputs, Command: m.Command, CWD: m.CWD}}
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("generated manifest requires a generator")
	}
	if len(rules) > 256 {
		return nil, fmt.Errorf("generator count exceeds 256-rule budget")
	}
	names := map[string]bool{}
	for i := range rules {
		r := &rules[i]
		if r.Name == "" {
			r.Name = fmt.Sprintf("generator-%d", i+1)
		}
		if names[r.Name] {
			return nil, fmt.Errorf("duplicate generator name")
		}
		names[r.Name] = true
		if len(r.Sources) > 64 || len(r.Outputs) > 64 || len(r.Command) > 256 {
			return nil, fmt.Errorf("generator sources, outputs or argv exceed bounds")
		}
		if len(r.Sources) == 0 || len(r.Outputs) == 0 {
			return nil, fmt.Errorf("generator requires sources and outputs")
		}
		for _, g := range append(append([]string{}, r.Sources...), r.Outputs...) {
			if len(g) > 1024 || len(strings.Split(g, "/")) > 64 {
				return nil, fmt.Errorf("generator glob exceeds path budget")
			}
			if _, e := pathutil.RepoRelative(g); e != nil {
				return nil, e
			}
			if g == "." || strings.HasPrefix(g, ".git/") || g == ".git" {
				return nil, fmt.Errorf("unsafe generator glob")
			}
			for _, part := range strings.Split(g, "/") {
				if part == "**" {
					continue
				}
				if _, e := path.Match(part, ""); e != nil {
					return nil, fmt.Errorf("invalid generator glob: %w", e)
				}
			}
		}
		if r.CWD == "" {
			r.CWD = "."
		}
		if _, e := pathutil.RepoRelative(r.CWD); e != nil {
			return nil, e
		}
		if len(r.Command) > 0 {
			if r.Command[0] == "" || strings.HasPrefix(r.Command[0], "/") || strings.Contains(r.Command[0], "\\") {
				return nil, fmt.Errorf("generator requires PATH or repository-relative executable")
			}
			if _, e := pathutil.RepoRelative(r.Command[0]); e != nil {
				return nil, e
			}
			for _, arg := range r.Command {
				if strings.ContainsRune(arg, 0) {
					return nil, fmt.Errorf("NUL in generator command")
				}
			}
		}
	}
	return rules, nil
}
func Load(ctx context.Context, root, ref string) ([]Rule, error) {
	b, e := gitrepo.ReadFile(ctx, root, ref, ManifestPath)
	if e != nil {
		return nil, e
	}
	return Decode(b)
}

// Match expands ** by path component without touching the working tree.
func Match(pattern, file string) bool {
	if len(pattern) > 1024 || len(file) > 16384 {
		return false
	}
	p, f := strings.Split(pattern, "/"), strings.Split(file, "/")
	if len(p) > 64 || len(f) > 256 {
		return false
	}
	type state struct{ i, j int }
	memo := map[state]bool{}
	seen := map[state]bool{}
	var walk func(int, int) bool
	walk = func(i, j int) bool {
		key := state{i, j}
		if seen[key] {
			return memo[key]
		}
		seen[key] = true
		ok := false
		switch {
		case i == len(p):
			ok = j == len(f)
		case p[i] == "**":
			ok = walk(i+1, j) || j < len(f) && walk(i, j+1)
		case j < len(f):
			match, _ := path.Match(p[i], f[j])
			ok = match && walk(i+1, j+1)
		}
		memo[key] = ok
		return ok
	}
	return walk(0, 0)
}
func Matches(patterns []string, file string) bool {
	for _, g := range patterns {
		if Match(g, file) {
			return true
		}
	}
	return false
}

// Analyze warns when a declared source changes without any matching output
// changing in the same combined candidate. An output edit is not proof of sync.
func Analyze(ctx context.Context, root, base, candidate string, changed []string) (Report, error) {
	r := Report{Status: model.StatusUnknown, Evidence: model.Unknown}
	before, be := Load(ctx, root, base)
	after, ae := Load(ctx, root, candidate)
	if errors.Is(be, os.ErrNotExist) && errors.Is(ae, os.ErrNotExist) {
		return r, nil
	}
	r.Applicable = true
	if be != nil && !errors.Is(be, os.ErrNotExist) || ae != nil {
		r.Status = model.StatusIncomplete
		r.Explanation = "Declared generator configuration is missing or unusable."
		r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: ManifestPath, Severity: model.SeverityWarning, Message: fmt.Sprintf("base: %v; candidate: %v", be, ae)})
		return r, nil
	}
	if be == nil {
		for _, old := range before {
			found := false
			for _, now := range after {
				if patternsCover(now.Sources, old.Sources) && patternsCover(now.Outputs, old.Outputs) {
					found = true
					break
				}
			}
			if !found {
				r.Status = model.StatusIncomplete
				r.Explanation = "A base generator declaration was removed or changed; prior output obligations are unverified."
				return r, nil
			}
		}
	}
	r.Rules = after
	r.Status = model.StatusUnknown
	r.Explanation = "Declared generated outputs have not been regenerated; static source/output edits cannot prove synchronization."
	for _, rule := range after {
		var source, output []string
		for _, f := range changed {
			if Matches(rule.Sources, f) {
				source = append(source, f)
			}
			if Matches(rule.Outputs, f) {
				output = append(output, f)
			}
		}
		older := false
		if len(source) > 0 && len(output) > 0 {
			var e error
			older, e = outputsPrecedeSources(ctx, root, base, candidate, source, output)
			if e != nil {
				return r, e
			}
		}
		if len(source) > 0 && (len(output) == 0 || older) {
			explanation := "Generator " + rule.Name + " changed sources without changing declared outputs: " + strings.Join(source, ", ")
			if older {
				explanation = "Generator " + rule.Name + " has an output edit that precedes the latest source edit in commit ancestry; regeneration is unverified."
			}
			f := model.Finding{ID: model.StableID("generated", rule.Name, base, candidate), Code: "generated_sources_changed", Severity: model.SeverityWarning, Evidence: model.Inferred, Explanation: explanation, Remediation: "Regenerate declared outputs with the configured argv and review the diff; radar gate --run can observe regeneration."}
			r.Findings = append(r.Findings, f)
		}
	}
	return r, nil
}

func outputsPrecedeSources(ctx context.Context, root, base, candidate string, sources, outputs []string) (bool, error) {
	latest := func(files []string) (string, error) {
		args := []string{"log", "--topo-order", "-1", "--format=%H", base + ".." + candidate, "--"}
		args = append(args, files...)
		v, e := gitrepo.RunPrivate(ctx, root, args...)
		return strings.TrimSpace(v), e
	}
	s, e := latest(sources)
	if e != nil {
		return false, e
	}
	o, e := latest(outputs)
	if e != nil {
		return false, e
	}
	if s == "" || o == "" || s == o {
		return false, nil
	}
	_, e = gitrepo.RunPrivate(ctx, root, "merge-base", "--is-ancestor", o, s)
	if e == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(e, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, e
}

func patternsCover(current, prior []string) bool {
	for _, p := range prior {
		found := false
		for _, c := range current {
			found = found || p == c
		}
		if !found {
			return false
		}
	}
	return true
}
