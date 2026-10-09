package contracts

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
)

// Impact compares declared obligations at base with head. approved lists
// retirements reviewed outside the manifest (an approved plan's removal delta);
// they apply in addition to "retired" entries the head manifest introduces.
func Impact(ctx context.Context, root, base, head string, approved ...Retirement) (Report, error) {
	return analyze(ctx, root, base, []string{head}, false, approved)
}
func Scan(ctx context.Context, root, base string, branches []string) (Report, error) {
	return analyze(ctx, root, base, branches, true, nil)
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

func analyze(ctx context.Context, root, base string, heads []string, cross bool, approved []Retirement) (Report, error) {
	r := Report{Base: base, Heads: heads, Checkpoint: "authoritative", Findings: []model.Finding{}, Diagnostics: []model.Diagnostic{}, Obligations: []ObligationChange{}, Unverified: []string{}}
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
	unverified := func(message string) {
		for _, m := range r.Unverified {
			if m == message {
				return
			}
		}
		r.Unverified = append(r.Unverified, message)
	}
	manifest, err := LoadManifest(ctx, root, baseSHA)
	if err != nil {
		warn("", err.Error())
		// A repository without a manifest keeps manifest-free analysis; a
		// manifest that exists but cannot be read leaves obligations unknown.
		if !errors.Is(err, os.ErrNotExist) {
			unverified("base contract manifest is unusable: " + err.Error())
		}
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
	exists := func(ref, path string) bool {
		_, err := gitrepo.ReadFile(ctx, root, ref, path)
		return err == nil
	}
	// The base manifest defines the obligations under review. A head may add
	// obligations or explicitly retire them; it cannot silently drop them.
	declaration := func(head string, b Binding) (Binding, string, error) {
		m, err := manifestFor(head)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return Binding{}, "removed", nil
			}
			return Binding{}, "invalid", err
		}
		for _, hb := range m.Bindings {
			if hb.ID == b.ID {
				if hb.Schema != b.Schema || hb.Pointer != b.Pointer {
					return hb, "moved", nil
				}
				return hb, "present", nil
			}
		}
		return Binding{}, "removed", nil
	}
	// retiredFor finds a retirement introduced at head (not already present at
	// base) that covers the changed field of a binding.
	retiredFor := func(head, id, field string) *Retirement {
		var candidates []Retirement
		if m, err := manifestFor(head); err == nil {
			for _, ret := range m.Retired {
				if !containsRetirement(manifest.Retired, ret) {
					candidates = append(candidates, ret)
				}
			}
		}
		candidates = append(candidates, approved...)
		for i, ret := range candidates {
			if ret.ID != id {
				continue
			}
			if len(ret.Fields) == 0 {
				return &candidates[i]
			}
			for _, f := range ret.Fields {
				if field != "" && FieldOverlaps(f, field) {
					return &candidates[i]
				}
			}
		}
		return nil
	}
	obligation := func(b Binding, head, kind, explanation string) {
		r.Obligations = append(r.Obligations, ObligationChange{Binding: b.ID, Head: head, Kind: kind, Explanation: explanation})
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
		complete := true
		// Record how each head changed this obligation, whether or not the
		// schema changed: a vanished declaration is itself unverified coverage.
		for _, head := range heads {
			hb, state, err := declaration(head, binding)
			switch state {
			case "invalid":
				unverified(head + ": contract manifest is unusable, so binding " + binding.ID + " is unverified: " + err.Error())
				obligation(binding, head, "invalid", err.Error())
				complete = false
			case "moved":
				doc, err := ReadSchema(ctx, root, head, hb.Schema)
				if err == nil {
					_, err = Analyzable(doc, hb.Pointer)
				}
				if err != nil {
					unverified(head + ": binding " + binding.ID + " now references an unanalyzable schema: " + err.Error())
					obligation(binding, head, "invalid", "moved declaration is not analyzable: "+err.Error())
					complete = false
				} else {
					obligation(binding, head, "moved", "schema or pointer changed; the base location remains the reviewed obligation")
				}
			case "present":
				var dropped []string
				for _, f := range binding.Fields {
					kept := false
					for _, g := range hb.Fields {
						kept = kept || g == f
					}
					if !kept {
						dropped = append(dropped, f)
					}
				}
				if len(dropped) > 0 {
					obligation(binding, head, "narrowed", "consumer fields no longer declared: "+strings.Join(dropped, ", "))
				}
			case "removed":
				switch {
				case retiredFor(head, binding.ID, "") != nil:
					obligation(binding, head, "retired", retiredFor(head, binding.ID, "").Reason)
				case binding.Consumer != "" && !exists(head, binding.Consumer):
					obligation(binding, head, "consumer_removed", "binding removed together with its declared consumer "+binding.Consumer)
				default:
					obligation(binding, head, "removed", "binding removed without a retirement record while its consumer remains")
					unverified(head + ": binding " + binding.ID + " was removed without an explicit retirement record in " + ManifestPath)
				}
			}
		}
		before, err := ReadSchema(ctx, root, baseSHA, binding.Schema)
		if err == nil {
			_, err = Analyzable(before, binding.Pointer)
		}
		if err != nil {
			warn(binding.Schema, "binding "+binding.ID+" at base: "+err.Error())
			continue
		}
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
					consumerBinding, state, _ := declaration(consumerHead, binding)
					if state == "invalid" {
						continue
					}
					if cross && consumerHead != producerHead && state == "present" && !consumerChanged(consumerHead, consumerBinding) {
						continue
					}
					usedBase := change.Kind == "required_added" || overlapsAny(binding.Fields, change.Field)
					usedHead := state == "present" && (change.Kind == "required_added" || overlapsAny(consumerBinding.Fields, change.Field))
					if !usedBase && !usedHead {
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
					if usedHead {
						if consumerBinding.Consumer != "" && !exists(consumerHead, consumerBinding.Consumer) {
							warn(consumerBinding.Consumer, consumerHead+": declared consumer of "+binding.ID+" is missing; affected usage cannot be verified")
							unverified(consumerHead + ": declared consumer " + consumerBinding.Consumer + " of binding " + binding.ID + " is missing")
							complete = false
							continue
						}
						id := model.StableID(root, "contract", binding.ID, change.Kind, change.Field, producerHead, consumerHead)
						if seen[id] {
							continue
						}
						seen[id] = true
						r.Findings = append(r.Findings, finding(id, binding, consumerBinding, change, classification, producerHead, consumerHead, r.Checkpoint, root))
						continue
					}
					// The base declaration still depends on this change, but the
					// head declaration no longer covers it (binding removed, moved,
					// narrowed or the manifest deleted).
					if binding.Consumer != "" && !exists(consumerHead, binding.Consumer) {
						continue
					}
					id := model.StableID(root, "contract-obligation", binding.ID, change.Kind, change.Field, producerHead, consumerHead)
					if seen[id] {
						continue
					}
					seen[id] = true
					f := finding(id, binding, binding, change, classification, producerHead, consumerHead, r.Checkpoint, root)
					if ret := retiredFor(consumerHead, binding.ID, change.Field); ret != nil {
						f.Code = "contract_obligation_retired"
						f.Severity = model.SeverityWarning
						f.Explanation = change.Explanation + "; the base declaration depended on it and " + ManifestPath + " retires that obligation: " + ret.Reason
						f.Remediation = "Confirm the retirement was reviewed and the consumer no longer depends on this field."
					} else {
						f.Code = "contract_obligation_removed"
						f.Explanation = change.Explanation + "; the base declaration of " + binding.ID + " depends on it, and the change also removes or narrows that declaration (" + state + ") without a retirement record"
						f.Remediation = "Restore the binding, or add a reviewed entry to \"retired\" in " + ManifestPath + " with the binding id, affected fields and a reason."
					}
					r.Findings = append(r.Findings, f)
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

func overlapsAny(fields []string, changed string) bool {
	for _, field := range fields {
		if FieldOverlaps(field, changed) {
			return true
		}
	}
	return false
}

func containsRetirement(list []Retirement, r Retirement) bool {
	for _, x := range list {
		if reflect.DeepEqual(x, r) {
			return true
		}
	}
	return false
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
	if r.Analyzed < r.Bindings || len(r.Diagnostics) > 0 || len(r.Unverified) > 0 {
		return model.StatusIncomplete
	}
	return model.StatusPassed
}
