package landing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/pathutil"
)

// PreviewWithOptions analyzes the complete ordered candidate with the same
// explicitly authorized driver as its pair matrix. Execution authorization is
// restricted to that installed driver; selected tests remain proposals.
func PreviewWithOptions(ctx context.Context, root string, o Options) (integration.Report, error) {
	analysis, err := analysisOptions(o)
	if err != nil {
		return integration.Report{}, err
	}
	c, _, err := build(ctx, root, o.Base, o.Branches, o)
	defer c.Close()
	if err != nil {
		return integration.Report{}, err
	}
	r, err := integration.Analyze(ctx, c, analysis)
	if err == nil && o.Finalize != nil {
		err = o.Finalize(ctx, &r)
	}
	return r, err
}

func analysisOptions(o Options) (integration.Options, error) {
	a := o.Analysis
	if a.Verify || a.AllowExecution || len(a.Command) > 0 || a.Suite != "" || a.ObserveCoverage || a.AttributionRuns != 0 {
		return a, errors.New("landing analysis is static; use a separate gate for repository execution")
	}
	a.SuggestTests = true
	return a, nil
}

// A driver is an explicitly authorized installed program, never a repository
// merge-driver configuration or callback. Both adapters use separate output.
func validateDriver(root string, o Options) (string, error) {
	if o.Driver == "" {
		if o.AllowExecution {
			return "", errors.New("allow execution requires an explicit driver")
		}
		return "", nil
	}
	if !o.AllowExecution {
		return "", errors.New("structural merge driver requires explicit execution authorization")
	}
	name := o.Driver
	if name == "weave" {
		name = "weave-driver"
	}
	if name != "mergiraf" && name != "weave-driver" {
		return "", errors.New("driver must be mergiraf or weave")
	}
	executable, e := exec.LookPath(name)
	if e != nil {
		return "", fmt.Errorf("merge driver unavailable: %w", e)
	}
	executable, e = filepath.Abs(executable)
	if e != nil {
		return "", e
	}
	executable, e = filepath.EvalSymlinks(executable)
	if e != nil {
		return "", e
	}
	physical, e := filepath.EvalSymlinks(root)
	if e != nil {
		return "", e
	}
	physical, e = filepath.Abs(physical)
	if e != nil {
		return "", e
	}
	rel, e := filepath.Rel(physical, executable)
	if e != nil {
		return "", e
	}
	if rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", errors.New("merge driver executable must be installed outside the repository")
	}
	return executable, nil
}

func build(ctx context.Context, root, base string, branches []string, o Options) (*integration.Candidate, []string, error) {
	executable, e := validateDriver(root, o)
	if e != nil {
		return nil, nil, e
	}
	c, e := integration.BuildCandidate(ctx, root, base, branches)
	if e != nil || executable == "" || len(c.Conflicts) == 0 {
		return c, nil, e
	}
	resolved := []string{}
	// BuildCandidate stops on the first failed merge. MERGE_HEAD identifies its
	// pinned input, so resolution can resume the remaining ordered inputs.
	head, e := gitrepo.Resolve(ctx, c.Dir, "MERGE_HEAD")
	if e != nil {
		return c, resolved, e
	}
	at := -1
	for i, s := range c.Inputs {
		if s == head {
			at = i
			break
		}
	}
	if at < 0 {
		return c, resolved, errors.New("unrecognized private merge input")
	}
	for {
		remaining := []string{}
		for _, path := range c.Conflicts {
			ok, e := resolveFile(ctx, c, executable, o.Driver, path)
			if e != nil {
				return c, resolved, e
			}
			if ok {
				resolved = append(resolved, path)
			} else {
				remaining = append(remaining, path)
			}
		}
		c.Conflicts = remaining
		if len(remaining) > 0 {
			return c, resolved, nil
		}
		unmerged, e := gitrepo.RunPrivate(ctx, c.Dir, "diff", "--name-only", "--diff-filter=U", "-z")
		if e != nil {
			return c, resolved, e
		}
		if unmerged != "" {
			return c, resolved, errors.New("driver left unmerged index entries")
		}
		if _, e = gitrepo.RunPrivate(ctx, c.Dir, "commit", "--no-edit", "--no-verify"); e != nil {
			return c, resolved, e
		}
		at++
		if at >= len(c.Inputs) {
			break
		}
		_, mergeErr := gitrepo.RunPrivate(ctx, c.Dir, "merge", "--no-ff", "--no-edit", "--no-verify", c.Inputs[at])
		if mergeErr == nil {
			for at+1 < len(c.Inputs) {
				at++
				_, mergeErr = gitrepo.RunPrivate(ctx, c.Dir, "merge", "--no-ff", "--no-edit", "--no-verify", c.Inputs[at])
				if mergeErr != nil {
					break
				}
			}
			if mergeErr == nil {
				break
			}
		}
		paths, e := gitrepo.RunPrivate(ctx, c.Dir, "diff", "--name-only", "--diff-filter=U", "-z")
		if e != nil {
			return c, resolved, e
		}
		if paths == "" {
			return c, resolved, mergeErr
		}
		for _, path := range strings.Split(paths, "\x00") {
			if path != "" {
				c.Conflicts = append(c.Conflicts, path)
			}
		}
	}
	c.Commit, e = gitrepo.Resolve(ctx, c.Dir, "HEAD")
	if e == nil {
		c.Tree, e = gitrepo.Tree(ctx, c.Dir, c.Commit)
	}
	return c, resolved, e
}
func resolveFile(ctx context.Context, c *integration.Candidate, executable, driver, path string) (bool, error) {
	temp, e := os.MkdirTemp(c.Dir, ".radar-driver-")
	if e != nil {
		return false, e
	}
	defer os.RemoveAll(temp)
	files := []string{}
	for stage := 1; stage <= 3; stage++ {
		oid, e := gitrepo.RunPrivate(ctx, c.Dir, "rev-parse", fmt.Sprintf(":%d:%s", stage, path))
		if e != nil {
			return false, nil
		}
		blob, e := gitrepo.ReadBlob(ctx, c.Dir, oid)
		if e != nil {
			return false, e
		}
		p := filepath.Join(temp, fmt.Sprintf("stage%d", stage))
		if e = os.WriteFile(p, blob, 0600); e != nil {
			return false, e
		}
		files = append(files, p)
	}
	output := filepath.Join(temp, "output")
	args := []string{files[0], files[1], files[2], "-o", output, "-p", path}
	if driver == "mergiraf" {
		args = append([]string{"merge"}, args...)
	}
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, executable, args...)
	cmd.Dir = temp
	cmd.Env = []string{"PATH=" + filepath.Dir(executable), "HOME=" + temp, "TMPDIR=" + temp, "LANG=C", "LC_ALL=C"}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if e = cmd.Run(); e != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	stat, e := os.Lstat(output)
	if e != nil {
		return false, nil
	}
	if !stat.Mode().IsRegular() || stat.Size() > gitrepo.MaxFileBytes {
		return false, errors.New("merge driver produced an unsafe or oversized result")
	}
	data, e := os.ReadFile(output)
	if e != nil {
		return false, e
	}
	if strings.Contains(string(data), "<<<<<<< ") || strings.Contains(string(data), ">>>>>>> ") {
		return false, nil
	}
	destination, e := pathutil.ResolveInside(c.Dir, path)
	if e != nil {
		return false, e
	}
	if e = os.WriteFile(destination, data, 0600); e != nil {
		return false, e
	}
	if _, e = gitrepo.RunPrivate(ctx, c.Dir, "add", "--", path); e != nil {
		return false, e
	}
	return true, nil
}
