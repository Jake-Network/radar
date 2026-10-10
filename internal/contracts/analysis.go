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
	a := &analyzer{ctx: ctx, root: root, baseSHA: baseSHA, heads: heads, cross: cross, approved: approved, r: &r, manifests: map[string]manifestAt{}, baseBindings: map[string]Binding{}, changedConsumers: map[string]bool{}, seen: map[string]bool{}}
	manifest, err := LoadManifest(ctx, root, baseSHA)
	if err != nil {
		a.warn("", err.Error())
		// A repository without a manifest keeps manifest-free analysis; a
		// manifest that exists but cannot be read leaves obligations unknown.
		if !errors.Is(err, os.ErrNotExist) {
			a.unverified("base contract manifest is unusable: " + err.Error())
		}
		r.Status = model.StatusIncomplete
		return r, nil
	}
	r.Bindings = len(manifest.Bindings)
	a.manifest = manifest
	for _, b := range manifest.Bindings {
		a.baseBindings[b.ID] = b
	}
	for _, binding := range manifest.Bindings {
		// Both passes always run: a vanished declaration is itself unverified
		// coverage, whether or not the schema changed.
		complete := a.recordObligations(binding)
		if a.compareSchemas(binding) && complete {
			r.Analyzed++
		}
	}
	r.Status = reportStatus(r)
	return r, nil
}

// analyzer holds the state of one obligation analysis across bindings.
type analyzer struct {
	ctx      context.Context
	root     string
	baseSHA  string
	heads    []string
	cross    bool
	approved []Retirement
	manifest Manifest
	r        *Report
	// manifests caches head manifests by ref.
	manifests    map[string]manifestAt
	baseBindings map[string]Binding
	// changedConsumers caches consumerChanged by head and binding.
	changedConsumers map[string]bool
	// seen de-duplicates findings and unanalyzed warnings.
	seen map[string]bool
}

func (a *analyzer) warn(path, message string) {
	r := a.r
	r.Diagnostics = append(r.Diagnostics, model.Diagnostic{Path: path, Message: message, Severity: model.SeverityWarning})
}

func (a *analyzer) unverified(message string) {
	r := a.r
	for _, m := range r.Unverified {
		if m == message {
			return
		}
	}
	r.Unverified = append(r.Unverified, message)
}

func (a *analyzer) manifestFor(ref string) (Manifest, error) {
	ctx, root, manifests := a.ctx, a.root, a.manifests
	if m, ok := manifests[ref]; ok {
		return m.manifest, m.err
	}
	m, err := LoadManifest(ctx, root, ref)
	manifests[ref] = manifestAt{m, err}
	return m, err
}

func (a *analyzer) exists(ref, path string) bool {
	ctx, root := a.ctx, a.root
	_, err := gitrepo.ReadFile(ctx, root, ref, path)
	return err == nil
}

// declaration finds binding b in the head manifest. The base manifest defines
// the obligations under review. A head may add obligations or explicitly
// retire them; it cannot silently drop them.
func (a *analyzer) declaration(head string, b Binding) (Binding, string, error) {
	m, err := a.manifestFor(head)
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
func (a *analyzer) retiredFor(head, id, field string) *Retirement {
	manifest, approved := a.manifest, a.approved
	var candidates []Retirement
	if m, err := a.manifestFor(head); err == nil {
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

func (a *analyzer) obligation(b Binding, head, kind, explanation string) {
	r := a.r
	r.Obligations = append(r.Obligations, ObligationChange{Binding: b.ID, Head: head, Kind: kind, Explanation: explanation})
}

// consumerChanged reports whether a branch changed a binding's declaration
// or its consumer file. An untouched branch adds nothing to a merge beyond
// what the producer branch's own check already reports.
func (a *analyzer) consumerChanged(head string, b Binding) bool {
	ctx, root, baseSHA, baseBindings, changedConsumers := a.ctx, a.root, a.baseSHA, a.baseBindings, a.changedConsumers
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

// recordObligations records how each head changed the binding's declaration.
// It reports false when a head leaves the obligation unverifiable.
func (a *analyzer) recordObligations(binding Binding) bool {
	ctx, root, heads := a.ctx, a.root, a.heads
	complete := true
	// Record how each head changed this obligation, whether or not the
	// schema changed: a vanished declaration is itself unverified coverage.
	for _, head := range heads {
		hb, state, err := a.declaration(head, binding)
		switch state {
		case "invalid":
			a.unverified(head + ": contract manifest is unusable, so binding " + binding.ID + " is unverified: " + err.Error())
			a.obligation(binding, head, "invalid", err.Error())
			complete = false
		case "moved":
			doc, err := ReadSchema(ctx, root, head, hb.Schema)
			if err == nil {
				_, err = Analyzable(doc, hb.Pointer)
			}
			if err != nil {
				a.unverified(head + ": binding " + binding.ID + " now references an unanalyzable schema: " + err.Error())
				a.obligation(binding, head, "invalid", "moved declaration is not analyzable: "+err.Error())
				complete = false
			} else {
				a.obligation(binding, head, "moved", "schema or pointer changed; the base location remains the reviewed obligation")
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
				a.obligation(binding, head, "narrowed", "consumer fields no longer declared: "+strings.Join(dropped, ", "))
			}
		case "removed":
			switch {
			case a.retiredFor(head, binding.ID, "") != nil:
				a.obligation(binding, head, "retired", a.retiredFor(head, binding.ID, "").Reason)
			case binding.Consumer != "" && !a.exists(head, binding.Consumer):
				a.obligation(binding, head, "consumer_removed", "binding removed together with its declared consumer "+binding.Consumer)
			default:
				a.obligation(binding, head, "removed", "binding removed without a retirement record while its consumer remains")
				a.unverified(head + ": binding " + binding.ID + " was removed without an explicit retirement record in " + ManifestPath)
			}
		}
	}
	return complete
}

// compareSchemas classifies each head's schema changes for the binding and
// reports findings against the consumer declarations they affect. It reports
// false when any change could not be fully analyzed.
func (a *analyzer) compareSchemas(binding Binding) bool {
	ctx, root, baseSHA, heads, cross, seen, r := a.ctx, a.root, a.baseSHA, a.heads, a.cross, a.seen, a.r
	complete := true
	before, err := ReadSchema(ctx, root, baseSHA, binding.Schema)
	if err == nil {
		_, err = Analyzable(before, binding.Pointer)
	}
	if err != nil {
		a.warn(binding.Schema, "binding "+binding.ID+" at base: "+err.Error())
		return false
	}
	for _, producerHead := range heads {
		changes, err := headChanges(ctx, root, binding, before, producerHead)
		if err != nil {
			a.warn(binding.Schema, producerHead+": binding "+binding.ID+": "+err.Error())
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
				consumerBinding, state, _ := a.declaration(consumerHead, binding)
				if state == "invalid" {
					continue
				}
				if cross && consumerHead != producerHead && state == "present" && !a.consumerChanged(consumerHead, consumerBinding) {
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
						a.warn(binding.Schema, producerHead+": binding "+binding.ID+": "+change.Explanation)
					}
					complete = false
					continue
				}
				if usedHead {
					if consumerBinding.Consumer != "" && !a.exists(consumerHead, consumerBinding.Consumer) {
						a.warn(consumerBinding.Consumer, consumerHead+": declared consumer of "+binding.ID+" is missing; affected usage cannot be verified")
						a.unverified(consumerHead + ": declared consumer " + consumerBinding.Consumer + " of binding " + binding.ID + " is missing")
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
				if binding.Consumer != "" && !a.exists(consumerHead, binding.Consumer) {
					continue
				}
				id := model.StableID(root, "contract-obligation", binding.ID, change.Kind, change.Field, producerHead, consumerHead)
				if seen[id] {
					continue
				}
				seen[id] = true
				f := finding(id, binding, binding, change, classification, producerHead, consumerHead, r.Checkpoint, root)
				if ret := a.retiredFor(consumerHead, binding.ID, change.Field); ret != nil {
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
	return complete
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
