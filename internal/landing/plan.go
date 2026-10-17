// Package landing plans integration without writing user branches or executing
// repository code. Pairwise compatibility is bounded evidence, not proof of an
// N-way combination or runtime compatibility.
package landing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
)

type Options struct {
	Base           string
	Branches       []string
	MaxPairs       int
	Driver         string
	AllowExecution bool
	// Analysis carries the same selected policy/plan as gate. Landing remains
	// static: execution flags in Analysis are rejected independently of driver
	// authorization above.
	Analysis integration.Options
	// Finalize checks invocation-bound configuration immediately before a ref
	// is published. It may add checks or reject the observation, never authorize
	// repository execution. CLI callers use their shared gate evaluator here.
	Finalize func(context.Context, *integration.Report) error
}
type Branch struct {
	Name    string   `json:"name"`
	Commit  string   `json:"commit"`
	Changed []string `json:"changed"`
}
type Pair struct {
	Left          string              `json:"left"`
	Right         string              `json:"right"`
	Status        model.Status        `json:"status"`
	Checks        []integration.Check `json:"checks"`
	Findings      []model.Finding     `json:"findings"`
	Conflicts     []string            `json:"conflicts"`
	ResolvedBy    string              `json:"resolved_by,omitempty"`
	ResolvedFiles []string            `json:"resolved_files,omitempty"`
	Explanation   string              `json:"explanation"`
}
type RestackBranch struct {
	Name      string   `json:"name"`
	Commit    string   `json:"commit"`
	Behind    *int     `json:"behind,omitempty"`
	Conflicts []string `json:"conflicts"`
}

type Report struct {
	RestackFirst     []RestackBranch `json:"restack_first,omitempty"`
	Base             string          `json:"base"`
	Driver           string          `json:"driver,omitempty"`
	Branches         []Branch        `json:"branches"`
	Pairs            []Pair          `json:"pairs"`
	Order            []string        `json:"order"`
	Independent      []string        `json:"independent"`
	Chains           [][]string      `json:"chains"`
	Complete         bool            `json:"complete"`
	EstimatedRebases int             `json:"estimated_rebases"`
	OrderMethod      string          `json:"order_method"`
	Limitations      []string        `json:"limitations"`
}

func Plan(ctx context.Context, root string, o Options) (Report, error) {
	r := Report{Pairs: []Pair{}, Order: []string{}, Branches: []Branch{}, Independent: []string{}, Chains: [][]string{}, Complete: true, Limitations: []string{"Pair checks cannot prove N-way compatibility. Unchecked pairs are unknown.", "Rebase estimate counts later branches touching an earlier conflicting pair; it does not predict actual manual resolutions."}}
	if len(o.Branches) < 1 || len(o.Branches) > 64 {
		return r, errors.New("landing requires between 1 and 64 branches")
	}
	if o.MaxPairs < 0 || o.MaxPairs > 2016 {
		return r, errors.New("max pairs must be between 0 and 2016")
	}
	if o.MaxPairs == 0 {
		o.MaxPairs = 256
	}
	if _, e := validateDriver(root, o); e != nil {
		return r, e
	}
	r.Driver = o.Driver
	var err error
	r.Base, err = gitrepo.Resolve(ctx, root, o.Base)
	if err != nil {
		return r, err
	}
	names := append([]string{}, o.Branches...)
	sort.Strings(names)
	seen := map[string]bool{}
	for _, name := range names {
		sha, e := gitrepo.Resolve(ctx, root, name)
		if e != nil {
			return r, e
		}
		if seen[sha] {
			return r, fmt.Errorf("duplicate landing commit: %s", name)
		}
		seen[sha] = true
		changed, e := gitrepo.ChangedFiles(ctx, root, r.Base, sha)
		if e != nil {
			return r, e
		}
		r.Branches = append(r.Branches, Branch{name, sha, changed})
	}
	// Probe each pinned tip against base using the same authorized driver as
	// the final candidate. Static probes share one disposable object store.
	// A descendant may resolve its ancestor's conflict, so probe every tip.
	probe := newStandaloneProbe(ctx, root, r.Base, o)
	defer probe.Close()
	eligible := []Branch{}
	probeUnknown := map[string]bool{}
	for _, b := range r.Branches {
		paths, e := probe.conflicts(ctx, b.Commit)
		if e != nil {
			if ctx.Err() != nil {
				return r, ctx.Err()
			}
			probeUnknown[b.Name] = true
			r.Complete = false
			r.Limitations = append(r.Limitations, fmt.Sprintf("Standalone landing probe unavailable for %s: %s", b.Name, e))
			eligible = append(eligible, b)
			continue
		}
		if len(paths) == 0 {
			eligible = append(eligible, b)
			continue
		}
		restack := RestackBranch{Name: b.Name, Commit: b.Commit, Conflicts: paths}
		if boundary, e := gitrepo.ShallowBoundary(ctx, root); e == nil && len(boundary) == 0 {
			if count, e := gitrepo.RunPrivate(ctx, root, "rev-list", "--count", b.Commit+".."+r.Base); e == nil {
				if n, e := strconv.Atoi(strings.TrimSpace(count)); e == nil {
					restack.Behind = &n
				}
			}
		}
		r.RestackFirst = append(r.RestackFirst, restack)
		r.Complete = false
	}
	if len(r.RestackFirst) > 0 {
		r.Limitations = append(r.Limitations, "Restack inputs conflict with base alone and are excluded from pairs, order and the analyzed candidate. Missing behind counts are unknown.")
	}
	n := len(eligible)
	ancestors := make([][]bool, n)
	conflicts := make([][]bool, n)
	unknown := make([]bool, n)
	for i, b := range eligible {
		unknown[i] = probeUnknown[b.Name]
	}
	for i := range ancestors {
		ancestors[i] = make([]bool, n)
		conflicts[i] = make([]bool, n)
	}
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			yes, e := gitrepo.IsAncestor(ctx, root, eligible[i].Commit, eligible[j].Commit)
			if e != nil {
				return r, e
			}
			ancestors[j][i] = yes
		}
	}
	runs := 0
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			a, b := eligible[i], eligible[j]
			p := Pair{Left: a.Name, Right: b.Name, Status: model.StatusUnknown, Checks: []integration.Check{}, Findings: []model.Finding{}, Conflicts: []string{}}
			if (n > 12 && !overlap(a.Changed, b.Changed)) || runs >= o.MaxPairs {
				p.Explanation = "Pair omitted by the analysis bound; compatibility is unknown."
				r.Complete = false
				unknown[i] = true
				unknown[j] = true
				r.Pairs = append(r.Pairs, p)
				continue
			}
			runs++
			candidate, resolved, e := build(ctx, root, r.Base, []string{a.Commit, b.Commit}, o)
			var preview integration.Report
			if e == nil {
				preview, e = integration.Analyze(ctx, candidate, integration.Options{})
			}
			candidate.Close()
			if len(resolved) > 0 {
				p.ResolvedBy = o.Driver
				p.ResolvedFiles = resolved
			}
			if e != nil {
				if ctx.Err() != nil {
					return r, ctx.Err()
				}
				p.Explanation = "Pair analysis unavailable: " + e.Error()
				r.Complete = false
				unknown[i] = true
				unknown[j] = true
			} else {
				p.Status = model.StatusPassed
				p.Conflicts = preview.Conflicts
				p.Findings = preview.Findings
				p.Explanation = "Supported static pair checks completed; no runtime evidence."
				required := map[string]bool{}
				for _, check := range preview.Gate.Required {
					required[check.ID] = true
				}
				for _, check := range preview.Checks {
					if check.ID == "integration_execution" {
						continue
					}
					p.Checks = append(p.Checks, check)
					if required[check.ID] && check.Status == model.StatusFailed {
						p.Status = model.StatusFailed
					}
					if (required[check.ID] || check.ID == "symbol_resolution") && check.Status != model.StatusPassed && check.Status != model.StatusFailed && p.Status != model.StatusFailed {
						p.Status = model.StatusUnknown
					}
				}
				if p.Status == model.StatusUnknown {
					r.Complete = false
					unknown[i] = true
					unknown[j] = true
				}
				conflicts[i][j] = p.Status == model.StatusFailed
				conflicts[j][i] = conflicts[i][j]
			}
			r.Pairs = append(r.Pairs, p)
		}
	}
	for i := 0; i < n; i++ {
		ind := !unknown[i]
		for j := 0; j < n; j++ {
			if conflicts[i][j] || ancestors[i][j] || ancestors[j][i] {
				ind = false
			}
		}
		if ind {
			r.Independent = append(r.Independent, eligible[i].Name)
		}
	}
	// Nearest selected ancestors form stacks. Emit complete root-to-leaf
	// chains rather than treating ancestors as unrelated competing branches.
	parents := make([]int, n)
	hasChild := make([]bool, n)
	for i := 0; i < n; i++ {
		parents[i] = -1
		for j := 0; j < n; j++ {
			if ancestors[i][j] && (parents[i] < 0 || ancestors[j][parents[i]]) {
				parents[i] = j
			}
		}
		if parents[i] >= 0 {
			hasChild[parents[i]] = true
		}
	}
	for i := 0; i < n; i++ {
		if hasChild[i] || parents[i] < 0 {
			continue
		}
		var chain []string
		for j := i; j >= 0; j = parents[j] {
			chain = append(chain, eligible[j].Name)
		}
		for a, b := 0, len(chain)-1; a < b; a, b = a+1, b-1 {
			chain[a], chain[b] = chain[b], chain[a]
		}
		r.Chains = append(r.Chains, chain)
	}
	order, cost := orderBranches(n, ancestors, conflicts)
	r.EstimatedRebases = cost
	r.OrderMethod = "exact subset optimization of pair-based rebase estimate with ancestry constraints"
	if n > 12 {
		r.OrderMethod = "deterministic greedy pair-based rebase estimate with ancestry constraints"
	}
	for _, i := range order {
		r.Order = append(r.Order, eligible[i].Name)
	}
	return r, nil
}
func overlap(a, b []string) bool {
	m := map[string]bool{}
	for _, s := range a {
		m[s] = true
	}
	for _, s := range b {
		if m[s] {
			return true
		}
	}
	return false
}
func orderBranches(n int, anc, conf [][]bool) ([]int, int) {
	if n <= 12 {
		size := 1 << n
		cost := make([]int, size)
		prev := make([]int, size)
		pick := make([]int, size)
		for s := 1; s < size; s++ {
			cost[s] = n + 1
		}
		for s := 0; s < size; s++ {
			for i := 0; i < n; i++ {
				if s&(1<<i) != 0 {
					continue
				}
				ok := true
				extra := 0
				for j := 0; j < n; j++ {
					if anc[i][j] && s&(1<<j) == 0 {
						ok = false
					}
					if conf[i][j] && s&(1<<j) != 0 {
						extra = 1
					}
				}
				next := s | 1<<i
				if ok && cost[s]+extra < cost[next] {
					cost[next] = cost[s] + extra
					prev[next] = s
					pick[next] = i
				}
			}
		}
		var out []int
		for s := size - 1; s > 0; s = prev[s] {
			out = append(out, pick[s])
		}
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
			out[i], out[j] = out[j], out[i]
		}
		return out, cost[size-1]
	}
	done := make([]bool, n)
	var out []int
	cost := 0
	for len(out) < n {
		best := -1
		score := n + 1
		for i := 0; i < n; i++ {
			if done[i] {
				continue
			}
			ok := true
			s := 0
			for j := 0; j < n; j++ {
				if anc[i][j] && !done[j] {
					ok = false
				}
				if conf[i][j] && !done[j] {
					s++
				}
			}
			if ok && s < score {
				best = i
				score = s
			}
		}
		if best < 0 {
			break
		}
		for j := 0; j < n; j++ {
			if done[j] && conf[best][j] {
				cost++
				break
			}
		}
		done[best] = true
		out = append(out, best)
	}
	return out, cost
}
