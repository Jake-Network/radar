package generated

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/Jake-Network/radar/internal/evidence"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/model"
	"github.com/Jake-Network/radar/internal/pathutil"
)

// Execution binds argv and the observed output diff to one generator run.
// It contains no raw output. Caller binds it to the isolated candidate tree.
type Execution struct {
	CandidateCommit string                        `json:"candidate_commit,omitempty"`
	CandidateTree   string                        `json:"candidate_tree,omitempty"`
	Name            string                        `json:"name"`
	Command         []string                      `json:"command,omitempty"`
	CWD             string                        `json:"cwd"`
	Status          model.Status                  `json:"status"`
	Observation     evidence.CandidateObservation `json:"observation"`
	ChangedOutputs  []string                      `json:"changed_outputs,omitempty"`
	ChangedSources  []string                      `json:"changed_sources,omitempty"`
	Explanation     string                        `json:"explanation"`
}

// Run requires explicit execution opt-in and a disposable private candidate.
// Success means the bounded command succeeded and all declared output bytes
// remained identical; it does not imply that any tests were observed.
func Run(ctx context.Context, root string, rule Rule, timeout time.Duration) (Execution, error) {
	r := Execution{Name: rule.Name, Command: rule.Command, CWD: rule.CWD, Status: model.StatusIncomplete}
	if len(rule.Command) == 0 {
		r.Explanation = "No regeneration argv is declared."
		return r, nil
	}
	before, e := fingerprint(root)
	if e != nil {
		return r, e
	}
	sources, outputs := 0, 0
	for f := range before {
		if Matches(rule.Sources, f) {
			sources++
		}
		if Matches(rule.Outputs, f) {
			outputs++
		}
	}
	if sources == 0 || outputs == 0 {
		r.Explanation = "Source or output globs matched no files; synchronization cannot be established."
		return r, nil
	}
	r.Observation, e = evidence.ObserveToolCandidate(ctx, root, rule.CWD, rule.Command, timeout)
	if e != nil {
		return r, e
	}
	r.Status = r.Observation.Status
	if r.Status != model.StatusPassed {
		r.Explanation = "The declared generator did not complete successfully within execution bounds."
		return r, nil
	}
	after, e := fingerprint(root)
	if e != nil {
		return r, e
	}
	files := map[string]bool{}
	for f := range before {
		files[f] = true
	}
	for f := range after {
		files[f] = true
	}
	for f := range files {
		if before[f] == after[f] {
			continue
		}
		if Matches(rule.Outputs, f) {
			r.ChangedOutputs = append(r.ChangedOutputs, f)
		} else {
			r.ChangedSources = append(r.ChangedSources, f)
		}
	}
	sort.Strings(r.ChangedOutputs)
	sort.Strings(r.ChangedSources)
	switch {
	case len(r.ChangedSources) > 0:
		r.Status = model.StatusIncomplete
		r.Explanation = "Generator changed files outside its declared outputs; the regeneration observation is invalid."
	case len(r.ChangedOutputs) > 0:
		r.Status = model.StatusFailed
		r.Explanation = "Regeneration changed declared output bytes; committed generated outputs have drifted."
	default:
		r.Status = model.StatusPassed
		r.Explanation = "The declared generator completed and declared output bytes remained identical."
	}
	return r, nil
}
func fingerprint(root string) (map[string]string, error) {
	out := map[string]string{}
	total := int64(0)
	e := filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		rel = filepath.ToSlash(rel)
		if rel == ".git" {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}
		if len(out) >= 100000 {
			return fmt.Errorf("generator snapshot file-count limit exceeded")
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("generator snapshot refuses nonregular file %s", rel)
		}
		if info.Size() > gitrepo.MaxFileBytes {
			return fmt.Errorf("generator file exceeds size limit: %s", rel)
		}
		total += info.Size()
		if total > 512<<20 {
			return fmt.Errorf("generator snapshot byte limit exceeded")
		}
		f, e := os.Open(p)
		if e != nil {
			return e
		}
		h := sha256.New()
		n, e := io.Copy(h, io.LimitReader(f, gitrepo.MaxFileBytes+1))
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		if n > gitrepo.MaxFileBytes {
			return fmt.Errorf("generator file exceeds size limit: %s", rel)
		}
		out[rel] = hex.EncodeToString(h.Sum(nil)) + fmt.Sprintf(":%o", info.Mode().Perm())
		return nil
	})
	return out, e
}

// RunSpec observes a contract generator and returns only its declared spec.
// Other source writes invalidate it; output exists even when never committed.
func RunSpec(ctx context.Context, root, name, schema string, argv []string, timeout time.Duration) (Execution, []byte, error) {
	r := Execution{Name: name, Command: argv, CWD: ".", Status: model.StatusIncomplete}
	before, e := fingerprint(root)
	if e != nil {
		return r, nil, e
	}
	r.Observation, e = evidence.ObserveToolCandidate(ctx, root, ".", argv, timeout)
	if e != nil {
		return r, nil, e
	}
	r.Status = r.Observation.Status
	if r.Status != model.StatusPassed {
		r.Explanation = "Contract generator did not complete within execution bounds."
		return r, nil, fmt.Errorf("contract generator %s: %s", name, r.Status)
	}
	after, e := fingerprint(root)
	if e != nil {
		return r, nil, e
	}
	files := map[string]bool{}
	for f := range before {
		files[f] = true
	}
	for f := range after {
		files[f] = true
	}
	for f := range files {
		if before[f] != after[f] {
			if f == schema {
				r.ChangedOutputs = append(r.ChangedOutputs, f)
			} else {
				r.ChangedSources = append(r.ChangedSources, f)
			}
		}
	}
	sort.Strings(r.ChangedSources)
	if len(r.ChangedSources) > 0 {
		r.Status = model.StatusIncomplete
		r.Explanation = "Contract generation changed source outside its declared schema."
		return r, nil, fmt.Errorf("contract generator %s changed non-schema source", name)
	}
	p, e := pathutil.ResolveInside(root, schema)
	if e != nil {
		return r, nil, e
	}
	f, e := os.Open(p)
	if e != nil {
		return r, nil, e
	}
	b, e := io.ReadAll(io.LimitReader(f, gitrepo.MaxFileBytes+1))
	ce := f.Close()
	if e != nil {
		return r, nil, e
	}
	if ce != nil {
		return r, nil, ce
	}
	if len(b) > gitrepo.MaxFileBytes {
		return r, nil, fmt.Errorf("generated schema exceeds size limit")
	}
	r.Explanation = "Contract schema generated from the isolated committed checkpoint."
	return r, b, nil
}
