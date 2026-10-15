package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/project"
)

// hookConflictHunks compares the conflicting content, excluding Git's marker
// labels (which contain synthetic commit IDs) and non-conflicting context.
// Binary and markerless conflicts instead use the unmerged index blob IDs:
// Git may leave ours on disk even though the other side changed.
func hookConflictHunks(ctx context.Context, candidate *integration.Candidate, path string) ([]string, error) {
	filename, err := project.SafePath(candidate.Dir, path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(filename)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	var data []byte
	if err == nil {
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return nil, err
		}
		if !st.Mode().IsRegular() {
			return nil, fmt.Errorf("conflicted path is not a regular file")
		}
		data, err = io.ReadAll(io.LimitReader(f, gitrepo.MaxFileBytes+1))
		if err != nil {
			return nil, err
		}
		if len(data) > gitrepo.MaxFileBytes {
			return nil, fmt.Errorf("conflict comparison exceeds file size limit")
		}
	}
	var hunks []string
	var hunk strings.Builder
	active, divided := false, false
	valid := true
	for _, line := range strings.Split(string(data), "\n") {
		switch {
		case strings.HasPrefix(line, "<<<<<<< "):
			if active {
				valid = false
			}
			active, divided = true, false
			hunk.Reset()
		case active && line == "=======":
			if divided {
				valid = false
			}
			divided = true
			hunk.WriteString("=======\n")
		case active && strings.HasPrefix(line, ">>>>>>> "):
			if !divided {
				valid = false
			}
			hunks = append(hunks, hunk.String())
			active = false
		case active:
			hunk.WriteString(line)
			hunk.WriteByte('\n')
		}
	}
	if valid && !active && len(hunks) > 0 {
		return hunks, nil
	}
	// This also handles modify/delete conflicts with an absent checkout file.
	stages, err := gitrepo.RunPrivate(ctx, candidate.Dir, "ls-files", "--unmerged", "--format=%(objectmode) %(objectname) %(stage)", "--", path)
	if err != nil {
		return nil, err
	}
	return []string{"index-stages:" + stages}, nil
}

func hookHasNewConflicts(ctx context.Context, candidate, committed *integration.Candidate) (bool, error) {
	prior := map[string]bool{}
	for _, path := range committed.Conflicts {
		prior[path] = true
	}
	for _, path := range candidate.Conflicts {
		if !prior[path] {
			return true, nil
		}
		before, err := hookConflictHunks(ctx, committed, path)
		if err != nil {
			return false, err
		}
		after, err := hookConflictHunks(ctx, candidate, path)
		if err != nil {
			return false, err
		}
		counts := map[string]int{}
		for _, hunk := range before {
			counts[hunk]++
		}
		for _, hunk := range after {
			if counts[hunk] == 0 {
				return true, nil
			}
			counts[hunk]--
		}
	}
	return false, nil
}
