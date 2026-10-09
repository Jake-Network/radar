// Package planning validates declared intent against evidence, without inventing a design.
package planning

import (
	"fmt"
	"sort"
)

func Tasks(p Plan) (Schedule, error) {
	out := Schedule{Order: []string{}, ParallelGroups: [][]string{}, PlanDigest: Digest(p), Instructions: []TaskPacket{}}
	for _, t := range p.Tasks {
		packet := TaskPacket{ContractDeltas: []ContractDelta{}, GraphDeltas: p.GraphDeltas, PlanDigest: out.PlanDigest, FeatureID: p.FeatureID, Decisions: p.Decisions, Approval: p.Approval, Task: t, Requirements: []Requirement{}, Acceptance: []Criterion{}, Constraints: p.Constraints, BaseRevision: p.BaseRevision, Evidence: p.Evidence}
		for _, d := range p.ContractDeltas {
			for _, id := range t.Contracts {
				if d.Contract == id {
					packet.ContractDeltas = append(packet.ContractDeltas, d)
					break
				}
			}
		}
		for _, id := range t.Requirements {
			for _, r := range p.Requirements {
				if r.ID == id {
					packet.Requirements = append(packet.Requirements, r)
				}
			}
		}
		for _, id := range t.Acceptance {
			for _, c := range p.Acceptance {
				if c.ID == id {
					packet.Acceptance = append(packet.Acceptance, c)
				}
			}
		}
		out.Instructions = append(out.Instructions, packet)
	}
	tasks := map[string]Task{}
	done := map[string]bool{}
	for _, t := range p.Tasks {
		if t.ID == "" {
			return out, fmt.Errorf("task ID is required")
		}
		if _, ok := tasks[t.ID]; ok {
			return out, fmt.Errorf("duplicate task %q", t.ID)
		}
		tasks[t.ID] = t
	}
	for _, t := range p.Tasks {
		for _, d := range t.DependsOn {
			if _, ok := tasks[d]; !ok {
				return out, fmt.Errorf("task %q has missing dependency %q", t.ID, d)
			}
		}
	}
	for len(done) < len(tasks) {
		ready := []string{}
		for id, t := range tasks {
			if done[id] {
				continue
			}
			ok := true
			for _, d := range t.DependsOn {
				if !done[d] {
					ok = false
				}
			}
			if ok {
				ready = append(ready, id)
			}
		}
		sort.Strings(ready)
		if len(ready) == 0 {
			return out, fmt.Errorf("task dependency cycle")
		}
		// Split a ready frontier by declared contract ownership; sharing a contract prevents parallel eligibility.
		remaining := ready
		for len(remaining) > 0 {
			group := []string{}
			next := []string{}
			owners := map[string]bool{}
			for _, id := range remaining {
				conflict := false
				for _, c := range resources(tasks[id]) {
					if owners[c] {
						conflict = true
					}
				}
				if conflict {
					next = append(next, id)
					continue
				}
				group = append(group, id)
				for _, c := range resources(tasks[id]) {
					owners[c] = true
				}
			}
			out.ParallelGroups = append(out.ParallelGroups, group)
			remaining = next
		}
		for _, id := range ready {
			done[id] = true
			out.Order = append(out.Order, id)
		}
	}
	return out, nil
}

// Declared shared components conservatively serialize otherwise ready tasks.
// This is scheduling caution, not a confirmed semantic incompatibility.
func resources(t Task) []string {
	out := []string{}
	for _, c := range t.Contracts {
		out = append(out, "contract:"+c)
	}
	for _, c := range t.Components {
		out = append(out, "component:"+c)
	}
	return out
}
