package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// privateTimeout bounds one command against a private candidate repository.
const privateTimeout = 2 * time.Minute

// sanitizedEnv drops caller Git overrides, which could otherwise redirect
// reads, inject config, or replace objects, and appends the fixed settings.
func sanitizedEnv(fixed ...string) []string {
	var env []string
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			env = append(env, entry)
		}
	}
	return append(env, fixed...)
}

// PrivateCommand prepares Git for building a private candidate repository.
// Unlike the read-only profile it may write objects, refs and a worktree, so
// it also disables hooks, attributes, signing and the caller's identity, and
// pins commit dates so the candidate commit is reproducible. It is used to
// read from the source repository and write only the private one.
func PrivateCommand(ctx context.Context, root string, args ...string) *exec.Cmd {
	prefix := []string{"-C", root, "--no-pager", "-c", "core.hooksPath=" + NullPath, "-c", "core.fsmonitor=false", "-c", "core.attributesFile=" + NullPath, "-c", "commit.gpgSign=false", "-c", "protocol.allow=never", "-c", "user.name=Radar preview", "-c", "user.email=preview@radar.invalid"}
	c := exec.CommandContext(ctx, "git", append(prefix, args...)...)
	c.Env = sanitizedEnv("GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+NullPath, "GIT_NO_REPLACE_OBJECTS=1", "GIT_TERMINAL_PROMPT=0", "GIT_ATTR_NOSYSTEM=1", "GIT_NO_LAZY_FETCH=1", "GIT_AUTHOR_DATE=2000-01-01T00:00:00Z", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z")
	return c
}

// RunPrivate runs PrivateCommand and returns its trimmed output. On failure
// the returned text holds only bounded Git diagnostics.
func RunPrivate(ctx context.Context, root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, privateTimeout)
	defer cancel()
	c := PrivateCommand(ctx, root, args...)
	var out cappedWriter
	c.Stdout = &out
	c.Stderr = &out
	if e := c.Run(); e != nil {
		return out.String(), fmt.Errorf("git %s: %w", args[0], e)
	}
	return strings.TrimSpace(out.String()), nil
}

// cappedWriter retains only bounded Git diagnostics and silently drops the
// rest. Verification output is never emitted: it can contain credentials.
type cappedWriter struct{ data []byte }

const cappedWriterLimit = 8192

func (w *cappedWriter) Write(p []byte) (int, error) {
	n := len(p)
	if len(w.data) < cappedWriterLimit {
		remaining := cappedWriterLimit - len(w.data)
		if len(p) > remaining {
			p = p[:remaining]
		}
		w.data = append(w.data, p...)
	}
	return n, nil
}
func (w *cappedWriter) String() string { return string(w.data) }
