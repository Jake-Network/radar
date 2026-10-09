package contracts

import (
	"context"
	gitrepo "github.com/radar-engine/radar/internal/git"
	"github.com/radar-engine/radar/internal/model"
	"strings"
)

func Impact(ctx context.Context, root, base, head string) (Report, error) {
	return analyze(ctx, root, base, []string{head}, false)
}
func Scan(ctx context.Context, root, base string, branches []string) (Report, error) {
	return analyze(ctx, root, base, branches, true)
}
func analyze(ctx context.Context, root, base string, heads []string, cross bool) (Report, error) {
	r := Report{Base: base, Heads: heads, Checkpoint: "authoritative", Findings: []model.Finding{}, Diagnostics: []model.Diagnostic{}}
	baseSHA, err := gitrepo.Resolve(ctx, root, base)
	if err != nil {
		return r, err
	}
	base = baseSHA
	heads = append([]string(nil), heads...)
	r.Base = base

	for i, head := range heads {
		if head == "WORKTREE" {
			r.Checkpoint = "informational"
		} else {
			sha, err := gitrepo.Resolve(ctx, root, head)
			if err != nil {
				return r, err
			}
			heads[i] = sha
		}
	}
	r.Heads = heads
	manifest, err := LoadManifest(ctx, root, base)
	if err != nil {
		r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Message: err.Error(), Severity: "warning"})
		return r, nil
	}
	seen := map[string]bool{}
	for _, binding := range manifest.Bindings {
		before, err := ReadSchema(ctx, root, base, binding.Schema)
		if err != nil {
			r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: binding.Schema, Message: err.Error(), Severity: "warning"})
			continue
		}
		for _, producerHead := range heads {
			after, readErr := ReadSchema(ctx, root, producerHead, binding.Schema)
			var changes []Change
			var err error
			if readErr != nil {
				deleted := false
				if producerHead != "WORKTREE" {
					files, listErr := gitrepo.Files(ctx, root, producerHead)
					if listErr == nil {
						deleted = true
						for _, path := range files {
							if path == binding.Schema {
								deleted = false
								break
							}
						}
					}
				}
				if !deleted {
					r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: binding.Schema, Message: producerHead + ": " + readErr.Error(), Severity: "warning"})
					continue
				}
				if _, err := Compare(before, before, binding.Pointer); err != nil {
					r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: binding.Schema, Message: err.Error(), Severity: "warning"})
					continue
				}
				changes = []Change{{Kind: "contract_removed", Explanation: "declared contract schema file was deleted"}}
			} else {
				changes, err = Compare(before, after, binding.Pointer)
			}
			if err != nil {
				r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: binding.Schema, Message: producerHead + ": " + err.Error(), Severity: "warning"})
				continue
			}
			consumers := []string{producerHead}
			if cross {
				consumers = heads
			}
			for _, change := range changes {
				classification := classify(change, binding.Direction)
				if classification == "compatible" {
					continue
				}
				for _, consumerHead := range consumers {
					consumerBinding := binding
					if m, err := LoadManifest(ctx, root, consumerHead); err == nil {
						found := false
						for _, b := range m.Bindings {
							if b.ID == binding.ID {
								consumerBinding = b
								found = true
								break
							}
						}
						if !found {
							continue
						}
					} else {
						r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Message: consumerHead + ": " + err.Error(), Severity: "warning"})
						continue
					}
					if consumerBinding.Schema != binding.Schema || consumerBinding.Pointer != binding.Pointer {
						r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: ".radar/contracts.json", Message: consumerHead + ": binding " + binding.ID + " moved; association with the changed schema is not verified", Severity: "warning"})
						continue
					}
					used := change.Kind == "required_added" || change.Field == ""
					for _, field := range consumerBinding.Fields {
						if field == change.Field || strings.HasPrefix(field, change.Field+".") {
							used = true
						}
					}
					if !used {
						continue
					}
					if consumerBinding.Consumer != "" {
						if _, err := gitrepo.ReadFile(ctx, root, consumerHead, consumerBinding.Consumer); err != nil {
							r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: consumerBinding.Consumer, Message: err.Error(), Severity: "warning"})
							continue
						}
					}
					id := model.StableID(root, "contract", binding.ID, change.Kind, change.Field, producerHead, consumerHead)
					if seen[id] {
						continue
					}
					seen[id] = true
					f := model.Finding{ID: id, Code: "contract_" + change.Kind, Severity: "error", Evidence: model.VerifiedStatic, Contract: binding.ID, Producer: binding.Producer, Consumer: consumerBinding.Consumer, Branches: []string{producerHead, consumerHead}, Explanation: change.Explanation + "; consumer dependency is explicitly declared in .radar/contracts.json (runtime usage is not proven)", Remediation: "Coordinate producer and consumer migration; retain the old field until declared consumers are updated.", Verification: "Re-run radar scan at committed checkpoints and run producer/consumer integration tests.", Locations: []model.Provenance{{Repository: root, Revision: producerHead, Path: binding.Schema, Method: "json_schema_comparison", Evidence: model.VerifiedStatic}, {Repository: root, Revision: consumerHead, Path: ".radar/contracts.json", Method: "explicit_consumer_binding", Evidence: model.VerifiedStatic}, {Repository: root, Revision: consumerHead, Path: consumerBinding.Consumer, Method: "declared_consumer_location", Evidence: model.Inferred}}}
					if classification == "risk" {
						f.Severity = "warning"
						f.Evidence = model.Inferred
						f.Explanation += "; supported schema evidence does not confirm directional incompatibility"
					}
					if r.Checkpoint == "informational" {
						f.Severity = "warning"
						f.Explanation = "Informational working-tree observation; no authoritative failure: " + f.Explanation
					}
					r.Findings = append(r.Findings, f)
				}
			}
		}
	}
	return r, nil
}
