package integration

import (
	"context"
	"fmt"
	"time"

	"github.com/Jake-Network/radar/internal/contracts"
	"github.com/Jake-Network/radar/internal/generated"
)

// generatedContractDocuments executes only manifest-declared generators after
// authorization. Every checkpoint and binding receives a fresh private copy.
func generatedContractDocuments(ctx context.Context, temp string, r *Report, o Options) (map[string]map[string]contracts.DocumentOverride, error) {
	docs := map[string]map[string]contracts.DocumentOverride{}
	if !o.Verify || !o.AllowExecution {
		return docs, nil
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	max := o.MaxCommands
	if max <= 0 {
		max = 16
	}
	used := 0
	for _, ref := range []string{r.Base, r.CandidateCommit} {
		manifest, e := contracts.LoadManifest(ctx, temp, ref)
		if e != nil {
			continue
		}
		docs[ref] = map[string]contracts.DocumentOverride{}
		for _, binding := range manifest.Bindings {
			if len(binding.Generate) == 0 {
				continue
			}
			if _, done := docs[ref][binding.Schema]; done {
				return docs, fmt.Errorf("multiple generators declare the same contract schema at %s", ref)
			}
			if used >= max || o.budget != nil && !o.budget.take(1) {
				docs[ref][binding.Schema] = contracts.DocumentOverride{Err: fmt.Errorf("contract generation command budget exhausted")}
				continue
			}
			used++
			runCtx := ctx
			if o.budget != nil {
				runCtx = o.budget.context()
			}
			c, e := BuildCandidate(runCtx, temp, ref, nil)
			if e != nil {
				return docs, e
			}
			run, data, e := generated.RunSpec(runCtx, c.Dir, "contract:"+binding.ID, binding.Schema, binding.Generate, timeout)
			run.CandidateCommit = ref
			run.CandidateTree = c.Tree
			r.GeneratedExecutions = append(r.GeneratedExecutions, run)
			c.Close()
			d := contracts.DocumentOverride{Err: e}
			if e == nil {
				d.Document, d.Err = contracts.DecodeDocumentAt(binding.Schema, data)
			}
			docs[ref][binding.Schema] = d
		}
	}
	return docs, nil
}
