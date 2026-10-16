package landing

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/Jake-Network/radar/internal/gate"
	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/model"
)

type Applied struct {
	Ref         string             `json:"ref"`
	Commit      string             `json:"commit"`
	Tree        string             `json:"tree"`
	FastForward []string           `json:"fast_forward"`
	Report      integration.Report `json:"report"`
	Outcome     ApplyOutcome       `json:"outcome"`
}

// ApplyOutcome distinguishes immutable object import from publishing a ref.
// An imported object is not a verification claim and may remain after refusal.
type ApplyOutcome struct {
	Published       bool   `json:"published"`
	ObjectsImported bool   `json:"objects_imported"`
	Reason          string `json:"reason,omitempty"`
}

// Apply is an explicit local mutation: it publishes only a new private ref and
// immutable objects. It never updates a user branch, index or worktree. The
// complete N-way candidate is reanalyzed; pairwise results cannot authorize it.
func Apply(ctx context.Context, root string, r Report) (Applied, error) {
	return ApplyWithOptions(ctx, root, r, Options{})
}

// ApplyWithOptions repeats explicit authorization for external merge drivers.
func ApplyWithOptions(ctx context.Context, root string, r Report, o Options) (a Applied, err error) {
	a = Applied{FastForward: []string{}}
	defer func() {
		if err != nil {
			a.Outcome.Reason = err.Error()
		}
		if !a.Outcome.Published {
			a.Ref = ""
			a.FastForward = []string{}
		}
	}()
	if len(r.RestackFirst) > 0 {
		return a, errors.New("landing inputs require restack against base; no objects imported or integration ref published")
	}
	analysis, e := analysisOptions(o)
	if e != nil {
		return a, e
	}
	if len(r.Order) != len(r.Branches) || len(r.Order) == 0 {
		return a, errors.New("invalid landing order")
	}
	base, e := gitrepo.Resolve(ctx, root, r.Base)
	if e != nil {
		return a, e
	}
	if base != r.Base {
		return a, errors.New("base must be pinned")
	}
	names := map[string]string{}
	for _, b := range r.Branches {
		current, e := gitrepo.Resolve(ctx, root, b.Name)
		if e != nil {
			return a, e
		}
		if current != b.Commit {
			return a, errors.New("landing input changed; recompute plan")
		}
		names[b.Name] = b.Commit
	}
	var inputs []string
	for _, name := range r.Order {
		sha, ok := names[name]
		if !ok {
			return a, errors.New("invalid or duplicate landing order")
		}
		delete(names, name)
		inputs = append(inputs, sha)
	}
	if r.Driver != o.Driver {
		return a, errors.New("landing driver authorization differs from plan")
	}
	c, _, e := build(ctx, root, r.Base, inputs, o)
	defer c.Close()
	if e != nil {
		return a, e
	}
	a.Report, e = integration.Analyze(ctx, c, analysis)
	if e != nil {
		return a, e
	}
	if len(c.Conflicts) > 0 {
		return a, errors.New("landing candidate has textual conflicts")
	}
	// A selected policy may require a check supplied by Finalize (for example
	// configuration_stability). Defer that verdict until the final boundary.
	if o.Finalize == nil && a.Report.Gate.Verdict != gate.Pass {
		return a, errors.New("landing candidate has failing or unverified required static checks")
	}
	a.Commit = c.Commit
	a.Tree = c.Tree
	ref := "refs/radar/integration/" + model.StableID(r.Base, strings.Join(inputs, "\n"), c.Tree)
	// Repeating the same candidate need not import another pack. All exclusions
	// are immutable commits already resolved from the source, never mutable refs.
	if _, exists := gitrepo.Resolve(ctx, root, c.Commit); exists != nil {
		copyCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		producer := gitrepo.PrivateCommand(copyCtx, c.Dir, "pack-objects", "--stdout", "--revs", "--incremental")
		producer.Stdin = strings.NewReader(packRevisions(c.Commit, r.Base, inputs))
		pipe, e := producer.StdoutPipe()
		if e != nil {
			return a, e
		}
		defer pipe.Close()
		producer.Stderr = io.Discard
		consumer := gitrepo.PrivateCommand(copyCtx, root, "index-pack", "--stdin")
		consumer.Stdin = pipe
		consumer.Stdout = io.Discard
		consumer.Stderr = io.Discard
		if e = consumer.Start(); e != nil {
			return a, e
		}
		if e = producer.Start(); e != nil {
			pipe.Close()
			consumer.Wait()
			return a, e
		}
		pe := producer.Wait()
		ce := consumer.Wait()
		if pe != nil || ce != nil {
			return a, errors.New("unable to import private integration objects")
		}
		a.Outcome.ObjectsImported = true
	}
	// Configuration belongs to the invocation, not the Git candidate. Check its
	// final boundary after object import and before making any integration ref
	// visible. A failed finalizer may leave immutable objects, never a new ref.
	if e = ctx.Err(); e != nil {
		return a, e
	}
	if o.Finalize != nil {
		if e = o.Finalize(ctx, &a.Report); e != nil {
			return a, e
		}
	}
	if a.Report.Gate.Verdict != gate.Pass {
		return a, errors.New("landing configuration or selected requirements are unverified; no integration ref published")
	}
	for _, b := range r.Branches {
		current, resolveErr := gitrepo.Resolve(ctx, root, b.Name)
		if resolveErr != nil || current != b.Commit {
			return a, errors.New("landing input changed before publication; recompute plan")
		}
	}
	yes, e := gitrepo.IsAncestor(ctx, root, "HEAD", a.Commit)
	if e != nil {
		return a, e
	}
	// Create-only compare-and-swap: concurrent invocations cannot replace a ref.
	if existing, err := gitrepo.Resolve(ctx, root, ref); err == nil {
		if existing != a.Commit {
			return a, errors.New("integration ref already has another commit")
		}
	} else {
		if _, e = gitrepo.RunPrivate(ctx, root, "update-ref", ref, a.Commit, strings.Repeat("0", len(a.Commit))); e != nil {
			return a, e
		}
	}
	a.Ref = ref
	a.Outcome.Published = true
	if yes {
		a.FastForward = []string{"git", "merge", "--ff-only", a.Ref}
	}
	return a, nil
}

func packRevisions(commit, base string, inputs []string) string {
	lines := []string{commit, "^" + base}
	seen := map[string]bool{base: true}
	for _, sha := range inputs {
		if !seen[sha] {
			seen[sha] = true
			lines = append(lines, "^"+sha)
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
