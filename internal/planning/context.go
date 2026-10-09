// Package planning validates declared intent against evidence, without inventing a design.
package planning

import (
	"github.com/Jake-Network/radar/internal/model"
	"sort"
	"strings"
	"unicode"
)

func Generate(intent string, s model.Snapshot) Plan {
	p := Plan{
		SchemaVersion:  SchemaVersion,
		FeatureID:      model.StableID("feature", intent),
		Intent:         intent,
		BaseRevision:   s.Revision,
		Requirements:   []Requirement{{ID: "request", Intent: intent}},
		Decisions:      []Decision{},
		Constraints:    []Constraint{},
		ContractDeltas: []ContractDelta{},
		Tasks:          []Task{},
		Acceptance:     []Criterion{},
		GraphDeltas:    []Delta{},
		Assumptions: []Assumption{{
			ID:     "runtime-behavior",
			Text:   "Authorization, scalability, and runtime behavior require investigation; structural indexing does not verify them.",
			Status: AssumptionOpen,
		}},
		Evidence:   []model.Provenance{},
		Incomplete: []string{"architecture decisions", "task decomposition", "acceptance and verification criteria", "design review"},
	}
	p.Context = GroundedContext(intent, s)
	for _, match := range p.Context.Matches {
		p.Evidence = append(p.Evidence, match.Evidence)
	}

	return p
}

// GroundedContext ranks lexical evidence, not semantic relevance or runtime behavior.
func GroundedContext(intent string, s model.Snapshot) *Context {
	c := &Context{Relationships: []model.Edge{}, Query: intent, Matches: []ContextMatch{}, Unknown: []string{"Structural name matching does not verify authorization, runtime calls, persistence behavior, or scalability."}, PlanningDepth: "minimal", Reason: "No declared high-risk scope matched; an agent must confirm scope."}
	query := tokens(intent)
	for _, n := range s.Nodes {
		words := tokens(n.Name + " " + n.Provenance.Path + " " + n.Properties["binding_id"])
		score := 0
		for word := range query {
			if words[word] {
				score++
			}
		}
		if score > 0 {
			c.Matches = append(c.Matches, ContextMatch{Entity: n.ID, Score: score, Explanation: "Lexical match in indexed identifier/path or explicit contract name; semantic relevance unverified.", Evidence: n.Provenance})
		}
	}
	sort.Slice(c.Matches, func(i, j int) bool {
		if c.Matches[i].Score != c.Matches[j].Score {
			return c.Matches[i].Score > c.Matches[j].Score
		}
		return c.Matches[i].Entity < c.Matches[j].Entity
	})
	if len(c.Matches) > 100 {
		c.Matches = c.Matches[:100]
	}
	matched := map[string]bool{}
	for _, m := range c.Matches {
		matched[m.Entity] = true
	}
	// Expand one evidence-backed hop; these links describe static structure, not runtime behavior.
	files := map[string]bool{}
	for _, m := range c.Matches {
		if m.Evidence.Path != "" {
			files[model.FileID(m.Evidence.Path)] = true
		}
	}
	related := map[string]bool{}
	for _, edge := range s.Edges {
		if edge.Kind != "DEPENDS_ON" && edge.Kind != "CONSUMES" && edge.Kind != "EXPOSES" {
			continue
		}
		if (files[edge.From] || matched[edge.From]) && !matched[edge.To] {
			related[edge.To] = true
		}
		if (files[edge.To] || matched[edge.To]) && !matched[edge.From] {
			related[edge.From] = true
		}
	}
	orderedNodes := append([]model.Node(nil), s.Nodes...)
	sort.Slice(orderedNodes, func(i, j int) bool { return orderedNodes[i].ID < orderedNodes[j].ID })
	for _, n := range orderedNodes {
		if related[n.ID] && len(c.Matches) < 100 {
			c.Matches = append(c.Matches, ContextMatch{Entity: n.ID, Score: 0, Explanation: "One-hop import or declared contract neighbor; inspect relationship provenance; runtime relevance unverified.", Evidence: n.Provenance})
			matched[n.ID] = true
		}
	}
	for _, edge := range s.Edges {
		if matched[edge.From] || matched[edge.To] {
			c.Relationships = append(c.Relationships, edge)
		}
	}
	sort.Slice(c.Relationships, func(i, j int) bool { return c.Relationships[i].ID < c.Relationships[j].ID })
	if len(c.Relationships) > 200 {
		c.Relationships = c.Relationships[:200]
		c.Unknown = append(c.Unknown, "Relevant graph edges truncated at 200; inspect the full graph for remaining relationships.")
	}

	if len(c.Matches) == 0 {
		c.Unknown = append(c.Unknown, "No indexed identifier or path matches request tokens. Investigate actual source before designing.")
	}
	for _, word := range []string{"security", "authorization", "authentication", "tenant", "organization", "database", "migration", "schema", "contract", "permission"} {
		if query[word] {
			c.PlanningDepth = "extended"
			c.Reason = "Request contains a security, data, or contract scope token; inferred planning caution, not a verified architectural property."
			break
		}
	}
	for _, d := range s.Diagnostics {
		c.Unknown = append(c.Unknown, d.Message)
	}
	return c
}
func tokens(s string) map[string]bool {
	var b strings.Builder
	var previous rune
	for _, r := range s {
		if unicode.IsUpper(r) && unicode.IsLower(previous) {
			b.WriteRune(' ')
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(' ')
		}
		previous = r
	}
	result := map[string]bool{}
	for _, word := range strings.Fields(b.String()) {
		if len(word) > 2 {
			result[word] = true
		}
	}
	return result
}
