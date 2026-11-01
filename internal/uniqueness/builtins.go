package uniqueness

import (
	"context"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Jake-Network/radar/internal/contracts"
	"github.com/Jake-Network/radar/internal/migrations"
	"github.com/Jake-Network/radar/internal/model"
)

func evaluateBuiltins(ctx context.Context, trees []migrations.Tree, changed []string, read migrations.Reader) (Result, error) {
	if len(trees) < 2 {
		return Result{}, fmt.Errorf("uniqueness comparison needs base and candidate")
	}
	proto, typed := false, false
	for _, p := range changed {
		switch path.Ext(p) {
		case ".proto":
			proto = true
		case ".py", ".java", ".ts", ".js", ".prisma":
			typed = true
		}
	}
	if !proto && !typed {
		return Result{}, nil
	}
	states := make([]map[string][]model.Provenance, len(trees))
	advisories := make([]map[string][]model.Provenance, len(trees))
	uncertain := make([]bool, len(trees))
	for i, t := range trees {
		states[i] = map[string][]model.Provenance{}
		advisories[i] = map[string][]model.Provenance{}
		if t.Unavailable != "" {
			uncertain[i] = true
			continue
		}
		total, count, observations := 0, 0, 0
		for _, f := range t.Files {
			if err := ctx.Err(); err != nil {
				return Result{}, err
			}
			ext := path.Ext(f.Path)
			isProto := proto && ext == ".proto"
			isTyped := typed && (ext == ".py" || ext == ".java" || ext == ".ts" || ext == ".js" || ext == ".prisma")
			if !isProto && !isTyped || strings.Contains("/"+f.Path, "/node_modules/") || strings.Contains("/"+f.Path, "/.venv/") || strings.Contains("/"+f.Path, "/vendor/") {
				continue
			}
			count++
			if count > 10000 {
				uncertain[i] = true
				break
			}
			b, err := read(f.OID)
			total += len(b)
			if err != nil || len(b) > 1<<20 || total > 64<<20 {
				uncertain[i] = true
				continue
			}
			loc := model.Provenance{Path: f.Path, Revision: t.Revision, Repository: model.RepositoryID, Evidence: model.VerifiedStatic, Method: "protobuf_field_number"}
			if isProto {
				numbers, err := contracts.ProtobufFieldNumbers(b)
				if err != nil {
					uncertain[i] = true
					continue
				}
				for _, n := range numbers {
					observations++
					if observations > maxKeyObservations {
						uncertain[i] = true
						count = 10000
						break
					}
					p := loc
					p.Line = n.Line
					key := f.Path + "#" + n.Message + ":" + n.Number
					states[i][key] = append(states[i][key], p)
				}
			} else {
				candidates, err := typedCandidates(ctx, f.Path, b)
				if err != nil {
					continue
				}
				for _, c := range candidates {
					observations++
					if observations > maxKeyObservations {
						uncertain[i] = true
						count = 10000
						break
					}
					p := loc
					p.Line = c.line
					p.Evidence = model.Inferred
					p.Method = "syntax_unique_candidate"
					advisories[i][c.kind+":"+c.name] = append(advisories[i][c.kind+":"+c.name], p)
				}
			}
		}
	}
	last := len(trees) - 1
	out := Result{Applicable: proto, Status: model.StatusPassed, Evidence: model.VerifiedStatic, Explanation: "Compared literal protobuf field tags within each message; imported type resolution and runtime compatibility are not checked."}
	if proto && (uncertain[0] || uncertain[last]) {
		out.Status = model.StatusUnknown
		out.Evidence = model.Unknown
		out.Diagnostics = append(out.Diagnostics, model.Diagnostic{Severity: model.SeverityWarning, Message: "Protobuf field uniqueness is incomplete: malformed, unsupported or unavailable source."})
	}
	keys := make([]string, 0, len(states[last]))
	for key := range states[last] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		locs := states[last][key]
		if len(locs) < 2 {
			continue
		}
		f := model.NewFinding("unique_protobuf_field_number", fmt.Sprintf("Protobuf message field tag %s is declared more than once.", key), model.VerifiedStatic)
		f.Locations = locs
		if uncertain[0] {
			continue
		}
		if subset(locs, states[0][key]) {
			f.Code += "_on_base"
			f.Severity = model.SeverityWarning
			f.Explanation += " These duplicates already exist on base."
		} else {
			f.Severity = model.SeverityError
			out.Status = model.StatusFailed
			out.Evidence = model.VerifiedStatic
			f.Remediation = "Assign distinct field numbers in this message, preserving existing wire identifiers."
		}
		out.Findings = append(out.Findings, f)
	}
	keys = nil
	for key := range advisories[last] {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		locs := advisories[last][key]
		if len(locs) < 2 || subset(locs, advisories[0][key]) {
			continue
		}
		f := model.NewFinding("unique_name_candidate", fmt.Sprintf("Repeated %s declarations may share a registration scope; scope and permitted reuse are not established, so this is an advisory only.", key), model.Inferred)
		f.Severity = model.SeverityWarning
		f.Locations = locs
		f.Remediation = "Check the framework registry, database schema or URL namespace. If these names must be unique, declare the intended scope in .radar/unique.json."
		out.Findings = append(out.Findings, f)
	}
	return out, nil
}
