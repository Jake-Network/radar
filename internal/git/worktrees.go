package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// WorktreeStatus is a read-only boundary observation. Dirty files never enter
// committed integration candidates; ignored files are not enumerated.
type WorktreeStatus struct {
	Path                string   `json:"path"`
	Branch              string   `json:"branch,omitempty"`
	Commit              string   `json:"commit"`
	Detached            bool     `json:"detached"`
	Staged              []string `json:"staged"`
	Unstaged            []string `json:"unstaged"`
	Untracked           []string `json:"untracked"`
	InspectionError     string   `json:"inspection_error,omitempty"`
	CommitIncluded      bool     `json:"commit_included"`
	UncommittedIncluded bool     `json:"uncommitted_included"`
}

func InspectWorktrees(ctx context.Context, root string) ([]WorktreeStatus, error) {
	b, err := runListing(ctx, root, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	out := []WorktreeStatus{}
	for _, record := range strings.Split(string(b), "\x00\x00") {
		bare := false
		w := WorktreeStatus{Staged: []string{}, Unstaged: []string{}, Untracked: []string{}}
		for _, field := range strings.Split(record, "\x00") {
			switch {
			case strings.HasPrefix(field, "worktree "):
				w.Path = strings.TrimPrefix(field, "worktree ")
			case strings.HasPrefix(field, "HEAD "):
				w.Commit = strings.TrimPrefix(field, "HEAD ")
			case strings.HasPrefix(field, "branch refs/heads/"):
				w.Branch = strings.TrimPrefix(field, "branch refs/heads/")
			case field == "bare":
				bare = true
			case field == "detached":
				w.Detached = true
			}
		}
		if w.Path == "" || bare {
			continue
		}
		out = append(out, w)
	}
	// Git listings define output order. Workers own distinct result slots so
	// bounded concurrent status calls cannot reorder observations.
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range min(4, len(out)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				inspectWorktree(ctx, &out[i])
			}
		}()
	}
	for i := range out {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	return out, nil
}

// globalExcludesFile reads one data-only global setting, without enabling
// global hooks or fsmonitor for repository operations. Includes are data-only
// and evaluated for this worktree so includeIf.gitdir retains its semantics.
// --path lets Git expand ~/ and %(prefix)/ using its own platform rules.
func globalExcludesFile(ctx context.Context, root string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, shortTimeout)
	defer cancel()
	cmd := command(ctx, root, "config", "--global", "--includes", "--path", "--get", "core.excludesFile")
	var env []string
	for _, entry := range cmd.Env {
		if !strings.HasPrefix(entry, "GIT_CONFIG_GLOBAL=") {
			env = append(env, entry)
		}
	}
	// Honor the user's explicit global config location, but no other GIT_ override.
	if path, ok := os.LookupEnv("GIT_CONFIG_GLOBAL"); ok {
		env = append(env, "GIT_CONFIG_GLOBAL="+path)
	}
	cmd.Env = env
	out := &boundedBuffer{limit: MaxFileBytes}
	errout := &boundedBuffer{limit: 8192}
	cmd.Stdout, cmd.Stderr = out, errout
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return "", nil // Setting is absent; Git's default ignore remains active.
		}
		return "", fmt.Errorf("global ignore configuration unavailable: %w: %s", err, strings.TrimSpace(errout.String()))
	}
	return strings.TrimSuffix(out.String(), "\n"), nil
}

func inspectWorktree(ctx context.Context, w *WorktreeStatus) {
	globalExcludes, globalErr := globalExcludesFile(ctx, w.Path)
	args := []string{}
	if globalExcludes != "" {
		// Repository/worktree settings have precedence over global settings.
		_, err := run(ctx, w.Path, "config", "--path", "--get", "core.excludesFile")
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			args = append(args, "-c", "core.excludesFile="+globalExcludes)
		}
	}
	args = append(args, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none")
	b, err := runListing(ctx, w.Path, args...)
	if err != nil {
		w.InspectionError = err.Error()
		return
	}
	if globalErr != nil {
		w.InspectionError = globalErr.Error()
	}
	fields := splitNUL(b)
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if len(f) < 4 {
			continue
		}
		path := f[3:]
		if f[:2] == "??" {
			w.Untracked = append(w.Untracked, path)
			continue
		}
		if f[0] != ' ' {
			w.Staged = append(w.Staged, path)
		}
		if f[1] != ' ' {
			w.Unstaged = append(w.Unstaged, path)
		}
		if f[0] == 'R' || f[0] == 'C' || f[1] == 'R' || f[1] == 'C' {
			i++
		}
	}
}
