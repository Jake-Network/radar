package integration

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/pathutil"
)

// UnsupportedEntryError reports a tree entry the private candidate cannot
// reproduce faithfully: a symlink, a submodule or an unsafe path.
type UnsupportedEntryError struct {
	Path   string
	Unsafe error
}

func (e *UnsupportedEntryError) Error() string {
	if e.Unsafe != nil {
		return "unsafe tree path: " + e.Unsafe.Error()
	}
	return "preview refuses symlink or submodule: " + e.Path
}
func (e *UnsupportedEntryError) Unwrap() error { return e.Unsafe }

func validateTree(ctx context.Context, root, sha string) error {
	entries, e := gitrepo.Entries(ctx, root, sha)
	if e != nil {
		return e
	}
	return validateTreeEntries(entries)
}

func validateTreeEntries(entries []gitrepo.TreeEntry) error {
	for _, v := range entries {
		if _, e := pathutil.RepoRelative(v.Path); e != nil {
			return &UnsupportedEntryError{Path: v.Path, Unsafe: e}
		}
		if v.Mode != "100644" && v.Mode != "100755" {
			return &UnsupportedEntryError{Path: v.Path}
		}
	}
	return nil
}

// Candidate is the combined commit built from pinned inputs in a private Git
// repository. Commit and Tree are empty when Conflicts is not. Close removes
// the private repository; the source repository is never written.
type Candidate struct {
	// Source is the repository the objects were copied from.
	Source string
	// Dir is the private repository holding the candidate checkout.
	Dir       string
	Base      string
	Inputs    []string
	Commit    string
	Tree      string
	Conflicts []string
	shallow   bool
}

// Close removes the private repository. It is safe to call more than once.
func (c *Candidate) Close() error {
	if c == nil || c.Dir == "" {
		return nil
	}
	dir := c.Dir
	c.Dir = ""
	return os.RemoveAll(dir)
}

// BuildCandidate pins base and branches to commit SHAs and merges the
// branches, in order, onto the base in a private repository. Without branches
// the candidate is the base itself. Textual conflicts are reported in
// Conflicts rather than as an error. The caller must Close the candidate.
func BuildCandidate(ctx context.Context, root, base string, branches []string) (*Candidate, error) {
	c, err := prepareCandidate(ctx, root, base, branches)
	if err != nil {
		return c, err
	}
	return mergeCandidate(ctx, c)
}

// prepareCandidate owns one disposable object store and a pristine base
// checkout. Dirty previews can materialize several immutable input commits in
// its private index without copying or checking out the source tree again.
func prepareCandidate(ctx context.Context, root, base string, branches []string) (*Candidate, error) {
	c := &Candidate{Source: root, Inputs: []string{}, Conflicts: []string{}}
	var e error
	c.Base, e = gitrepo.Resolve(ctx, root, base)
	if e != nil {
		return c, e
	}
	for _, ref := range branches {
		sha, e := gitrepo.Resolve(ctx, root, ref)
		if e != nil {
			return c, e
		}
		c.Inputs = append(c.Inputs, sha)
	}
	validated := map[string]bool{}
	for _, sha := range append([]string{c.Base}, c.Inputs...) {
		if validated[sha] {
			continue
		}
		if e = validateTree(ctx, root, sha); e != nil {
			return c, e
		}
		validated[sha] = true
	}
	temp, e := os.MkdirTemp("", "radar-integration-")
	if e != nil {
		return c, e
	}
	c.Dir = temp
	// System temporary directories can have trusted symlink ancestors (for
	// example /var on macOS). Pin the new private root to its physical path
	// before Git creates anything beneath it. Object-store checks still reject
	// symlinks introduced inside this private root.
	temp, e = filepath.EvalSymlinks(temp)
	if e != nil {
		c.Close()
		return c, e
	}
	c.Dir = temp
	fail := func(e error) (*Candidate, error) {
		c.Close()
		return c, e
	}
	if _, e = gitrepo.RunPrivate(ctx, temp, "init", "--quiet"); e != nil {
		return fail(e)
	}
	// Pin checkout bytes in this private repository, not in the shared Git
	// command profile: dirty-input inspection must still see the source's real
	// normalization settings and refuse states it cannot reproduce.
	for _, setting := range [][2]string{{"core.autocrlf", "false"}, {"core.eol", "lf"}} {
		if _, e = gitrepo.RunPrivate(ctx, temp, "config", setting[0], setting[1]); e != nil {
			return fail(e)
		}
	}
	// Ordinary local stores already have immutable pack indexes. Copy their
	// bytes instead of rebuilding and revalidating a pack of the entire history.
	copied, copyErr := gitrepo.CopyObjectStore(ctx, root, temp)
	if copyErr != nil {
		return fail(copyErr)
	}
	if !copied {
		// Copy immutable objects through a pack stream. No alternates, hard links,
		// clone hooks, source config copying, source refs or source objects are written.
		copyCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		producer := gitrepo.PrivateCommand(copyCtx, root, "pack-objects", "--stdout", "--revs")
		producer.Stdin = strings.NewReader(strings.Join(append([]string{c.Base}, c.Inputs...), "\n") + "\n")
		pipe, e := producer.StdoutPipe()
		if e != nil {
			return fail(e)
		}
		consumer := gitrepo.PrivateCommand(copyCtx, temp, "index-pack", "--stdin")
		consumer.Stdin = pipe
		consumer.Stdout = io.Discard
		consumer.Stderr = io.Discard
		producer.Stderr = io.Discard
		if e = consumer.Start(); e != nil {
			return fail(e)
		}
		if e = producer.Start(); e != nil {
			_ = pipe.Close()
			_ = consumer.Wait()
			return fail(e)
		}
		producerErr := producer.Wait()
		consumerErr := consumer.Wait()
		if producerErr != nil || consumerErr != nil {
			return fail(errors.New("unable to copy integration objects"))
		}
	}
	// A shallow source copies history only down to its boundary; the private
	// repository shares that boundary so Git does not walk missing parents.
	boundary, e := gitrepo.ShallowBoundary(ctx, root)
	if e != nil {
		return fail(e)
	}
	c.shallow = len(boundary) > 0
	if len(boundary) > 0 {
		if e = os.WriteFile(filepath.Join(temp, ".git", "shallow"), []byte(strings.Join(boundary, "\n")+"\n"), 0o644); e != nil {
			return fail(e)
		}
	}
	if _, e = gitrepo.RunPrivate(ctx, temp, "checkout", "--quiet", "--detach", c.Base); e != nil {
		return fail(e)
	}
	return c, nil
}

func mergeCandidate(ctx context.Context, c *Candidate) (*Candidate, error) {
	temp := c.Dir
	fail := func(err error) (*Candidate, error) { c.Close(); return c, err }
	var e error
	for _, sha := range c.Inputs {
		out, mergeErr := gitrepo.RunPrivate(ctx, temp, "merge", "--no-ff", "--no-edit", "--no-verify", sha)
		if mergeErr != nil {
			unmerged, inspectErr := gitrepo.RunPrivate(ctx, temp, "diff", "--name-only", "--diff-filter=U", "-z")
			if inspectErr != nil {
				return fail(inspectErr)
			}
			for _, p := range strings.Split(unmerged, "\x00") {
				if p != "" {
					c.Conflicts = append(c.Conflicts, p)
				}
			}
			if len(c.Conflicts) == 0 {
				if line := lastLine(out); line != "" {
					mergeErr = fmt.Errorf("%w: %s", mergeErr, line)
				}
				if c.shallow {
					mergeErr = &gitrepo.ShallowHistoryError{Err: mergeErr}
				}
				return fail(mergeErr)
			}
			sort.Strings(c.Conflicts)
			return c, nil
		}
	}
	c.Commit, e = gitrepo.Resolve(ctx, temp, "HEAD")
	if e != nil {
		return fail(e)
	}
	c.Tree, e = gitrepo.RunPrivate(ctx, temp, "rev-parse", "HEAD^{tree}")
	if e != nil {
		return fail(e)
	}
	if e = validateTree(ctx, temp, c.Commit); e != nil {
		return fail(e)
	}
	return c, nil
}

// lastLine returns the last non-empty line of bounded Git diagnostics.
func lastLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
