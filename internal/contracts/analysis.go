package contracts

import (
	"bytes"
	"context"
	"reflect"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
)

func Impact(ctx context.Context, root, base, head string) (Report, error) {
	return analyze(ctx, root, base, []string{head}, false)
}
func Scan(ctx context.Context, root, base string, branches []string) (Report, error) {
	return analyze(ctx, root, base, branches, true)
}

// FieldOverlaps reports whether a declared consumer field depends on a changed
// schema field: the same field, a nested field of it, or an enclosing object.
func FieldOverlaps(declared, changed string) bool {
	d := strings.ReplaceAll(declared, "[]", "")
	c := strings.ReplaceAll(changed, "[]", "")
	return c == "" || d == c || strings.HasPrefix(d, c+".") || strings.HasPrefix(c, d+".")
}

type manifestAt struct {
	manifest Manifest
	err      error
}

func analyze(ctx context.Context, root, base string, heads []string, cross bool) (Report, error) {
	r := Report{Base: base, Heads: heads, Checkpoint: "authoritative", Findings: []model.Finding{}, Diagnostics: []model.Diagnostic{}}
	baseSHA, err := gitrepo.Resolve(ctx, root, base)
	if err != nil {
		return r, err
	}
	r.Base = baseSHA
	heads = append([]string(nil), heads...)
	for i, head := range heads {
		if head == "WORKTREE" {
			r.Checkpoint = "informational"
			continue
		}
		sha, err := gitrepo.Resolve(ctx, root, head)
		if err != nil {
			return r, err
		}
		heads[i] = sha
	}
	r.Heads = heads
	warn := func(path, message string) {
		r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: path, Message: message, Severity: model.SeverityWarning})
	}
	manifest, err := LoadManifest(ctx, root, baseSHA)
	if err != nil {
		warn("", err.Error())
		r.Status = model.StatusIncomplete
		return r, nil
	}
	r.Bindings = len(manifest.Bindings)
	manifests := map[string]manifestAt{}
	manifestFor := func(ref string) (Manifest, error) {
		if m, ok := manifests[ref]; ok {
			return m.manifest, m.err
		}
		m, err := LoadManifest(ctx, root, ref)
		manifests[ref] = manifestAt{m, err}
		return m, err
	}
	seen := map[string]bool{}
	baseBindings := map[string]Binding{}
	for _, b := range manifest.Bindings {
		baseBindings[b.ID] = b
	}
	// consumerChanged reports whether a branch changed a binding's declaration
	// or its consumer file. An untouched branch adds nothing to a merge beyond
	// what the producer branch's own check already reports.
	changedConsumers := map[string]bool{}
	consumerChanged := func(head string, b Binding) bool {
		key := head + "\x00" + b.ID
		if changed, ok := changedConsumers[key]; ok {
			return changed
		}
		changed := !reflect.DeepEqual(baseBindings[b.ID], b)
		if !changed && b.Consumer != "" {
			before, beforeErr := gitrepo.ReadFile(ctx, root, baseSHA, b.Consumer)
			after, afterErr := gitrepo.ReadFile(ctx, root, head, b.Consumer)
			changed = (beforeErr == nil) != (afterErr == nil) || !bytes.Equal(before, after)
		}
		changedConsumers[key] = changed
		return changed
	}
	for _, binding := range manifest.Bindings {
		before, err := ReadSchema(ctx, root, baseSHA, binding.Schema)
		if err == nil {
			_, err = Analyzable(before, binding.Pointer)
		}
		if err != nil {
			warn(binding.Schema, "binding "+binding.ID+" at base: "+err.Error())
			continue
		}
		complete := true
		for _, producerHead := range heads {
			changes, err := headChanges(ctx, root, binding, before, producerHead)
			if err != nil {
				warn(binding.Schema, producerHead+": binding "+binding.ID+": "+err.Error())
				complete = false
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
					m, err := manifestFor(consumerHead)
					if err != nil {
						warn(ManifestPath, consumerHead+": "+err.Error())
						continue
					}
					consumerBinding, found := Binding{}, false
					for _, b := range m.Bindings {
						if b.ID == binding.ID {
							consumerBinding, found = b, true
							break
						}
					}
					if !found {
						continue
					}
					if cross && consumerHead != producerHead && !consumerChanged(consumerHead, consumerBinding) {
						continue
					}
					if consumerBinding.Schema != binding.Schema || consumerBinding.Pointer != binding.Pointer {
						warn(ManifestPath, consumerHead+": binding "+binding.ID+" moved; association with the changed schema is not verified")
						continue
					}
					used := change.Kind == "required_added"
					for _, field := range consumerBinding.Fields {
						if FieldOverlaps(field, change.Field) {
							used = true
						}
					}
					if !used {
						continue
					}
					if classification == "unanalyzed" {
						key := "unanalyzed\x00" + binding.ID + "\x00" + producerHead + "\x00" + change.Field
						if !seen[key] {
							seen[key] = true
							warn(binding.Schema, producerHead+": binding "+binding.ID+": "+change.Explanation)
						}
						complete = false
						continue
					}
					if consumerBinding.Consumer != "" {
						if _, err := gitrepo.ReadFile(ctx, root, consumerHead, consumerBinding.Consumer); err != nil {
							warn(consumerBinding.Consumer, err.Error())
							continue
						}
					}
					id := model.StableID(root, "contract", binding.ID, change.Kind, change.Field, producerHead, consumerHead)
					if seen[id] {
						continue
					}
					seen[id] = true
					r.Findings = append(r.Findings, finding(id, binding, consumerBinding, change, classification, producerHead, consumerHead, r.Checkpoint, root))
				}
			}
		}
		if complete {
			r.Analyzed++
		}
	}
	r.Status = reportStatus(r)
	return r, nil
}

// headChanges compares a binding's base schema with its state at head,
// recognizing a committed schema-file deletion as a contract removal.
func headChanges(ctx context.Context, root string, binding Binding, before map[string]any, head string) ([]Change, error) {
	after, readErr := ReadSchema(ctx, root, head, binding.Schema)
	if readErr == nil {
		return Compare(before, after, binding.Pointer)
	}
	if head != "WORKTREE" {
		if files, err := gitrepo.Files(ctx, root, head); err == nil {
			for _, p := range files {
				if p == binding.Schema {
					return nil, readErr
				}
			}
			return []Change{{Kind: "contract_removed", Explanation: "declared contract schema file was deleted"}}, nil
		}
	}
	return nil, readErr
}

func finding(id string, binding, consumer Binding, change Change, classification, producerHead, consumerHead, checkpoint, root string) model.Finding {
	f := model.Finding{
		ID:           id,
		Code:         "contract_" + change.Kind,
		Severity:     model.SeverityError,
		Evidence:     model.VerifiedStatic,
		Contract:     binding.ID,
		Producer:     binding.Producer,
		Consumer:     consumer.Consumer,
		Branches:     []string{producerHead, consumerHead},
		Explanation:  change.Explanation + "; consumer dependency is explicitly declared in .radar/contracts.json (runtime usage is not proven)",
		Remediation:  "Coordinate producer and consumer migration; retain the old field until declared consumers are updated.",
		Verification: "Re-run radar scan at committed checkpoints and run producer/consumer integration tests.",
		Locations: []model.Provenance{
			{Repository: root, Revision: producerHead, Path: binding.Schema, Method: "json_schema_comparison", Evidence: model.VerifiedStatic},
			{Repository: root, Revision: consumerHead, Path: ManifestPath, Method: "explicit_consumer_binding", Evidence: model.VerifiedStatic},
			{Repository: root, Revision: consumerHead, Path: consumer.Consumer, Method: "declared_consumer_location", Evidence: model.Inferred},
		},
	}
	if classification == "risk" {
		f.Severity = model.SeverityWarning
		f.Evidence = model.Inferred
		f.Explanation += "; supported schema evidence does not confirm directional incompatibility"
	}
	if checkpoint == "informational" {
		f.Severity = model.SeverityWarning
		f.Explanation = "Informational working-tree observation; no authoritative failure: " + f.Explanation
	}
	return f
}

func reportStatus(r Report) model.Status {
	for _, f := range r.Findings {
		if f.Severity == model.SeverityError && r.Checkpoint == "authoritative" {
			return model.StatusFailed
		}
	}
	if len(r.Findings) > 0 {
		return model.StatusWarning
	}
	if r.Analyzed < r.Bindings || len(r.Diagnostics) > 0 {
		return model.StatusIncomplete
	}
	return model.StatusPassed
}
