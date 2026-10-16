package landing

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	gitrepo "github.com/Jake-Network/radar/internal/git"
	"github.com/Jake-Network/radar/internal/integration"
	"github.com/Jake-Network/radar/internal/pathutil"
)

// A single private base checkout supplies an isolated object database for all
// standalone merge-tree probes. Merge-tree writes only there, never in source.
// Drivers and unsupported transports use the existing full candidate builder.
type standaloneProbe struct {
	root, base string
	options    Options
	private    *integration.Candidate
}

func newStandaloneProbe(ctx context.Context, root, base string, o Options) *standaloneProbe {
	p := &standaloneProbe{root: root, base: base, options: o}
	if o.Driver == "" {
		c, err := integration.BuildCandidate(ctx, root, base, nil)
		if err == nil {
			p.private = c
		} else {
			c.Close()
		}
	}
	return p
}

func (p *standaloneProbe) Close() { p.private.Close() }

func (p *standaloneProbe) full(ctx context.Context, head string) ([]string, error) {
	c, _, err := build(ctx, p.root, p.base, []string{head}, p.options)
	defer c.Close()
	if err != nil {
		return nil, err
	}
	return append([]string{}, c.Conflicts...), nil
}

func (p *standaloneProbe) conflicts(ctx context.Context, head string) ([]string, error) {
	if p.private == nil {
		return p.full(ctx, head)
	}
	// The fast local copy includes all objects; pack-stream fallback may only
	// contain base history. Never infer compatibility from an absent input.
	if _, err := gitrepo.Resolve(ctx, p.private.Dir, head); err != nil {
		return p.full(ctx, head)
	}
	entries, err := gitrepo.Entries(ctx, p.private.Dir, head)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if _, err := pathutil.RepoRelative(entry.Path); err != nil {
			return nil, &integration.UnsupportedEntryError{Path: entry.Path, Unsafe: err}
		}
		if entry.Mode != "100644" && entry.Mode != "100755" {
			return nil, &integration.UnsupportedEntryError{Path: entry.Path}
		}
	}
	runCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := gitrepo.PrivateCommand(runCtx, p.private.Dir, "merge-tree", "--write-tree", "--name-only", "-z", "--no-messages", p.base, head)
	var output probeOutput
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	err = cmd.Run()
	if runCtx.Err() != nil {
		return nil, runCtx.Err()
	}
	var exit *exec.ExitError
	if err != nil && (!errors.As(err, &exit) || exit.ExitCode() != 1) {
		// Old Git, missing history or transport limitations are retried through
		// the existing builder, which retains shallow-history diagnostics.
		return p.full(ctx, head)
	}
	if output.overflow {
		return nil, fmt.Errorf("standalone merge-tree output exceeds size limit")
	}
	return parseProbeOutput(output.Bytes(), err != nil)
}

type probeOutput struct {
	buffer   bytes.Buffer
	overflow bool
}

func (b *probeOutput) Len() int      { return b.buffer.Len() }
func (b *probeOutput) Bytes() []byte { return b.buffer.Bytes() }

func (b *probeOutput) Write(data []byte) (int, error) {
	const limit = 1 << 20
	n := len(data)
	if n > limit-b.Len() {
		data = data[:limit-b.Len()]
		b.overflow = true
	}
	_, _ = b.buffer.Write(data)
	return n, nil
}

func parseProbeOutput(data []byte, conflicted bool) ([]string, error) {
	fields := strings.Split(string(data), "\x00")
	if len(fields) < 2 || fields[len(fields)-1] != "" || !probeOID(fields[0]) {
		return nil, fmt.Errorf("invalid standalone merge-tree output")
	}
	paths := fields[1 : len(fields)-1]
	if conflicted != (len(paths) > 0) {
		return nil, fmt.Errorf("standalone merge-tree status does not match conflict paths")
	}
	for _, path := range paths {
		if _, err := pathutil.RepoRelative(path); err != nil {
			return nil, fmt.Errorf("unsafe standalone conflict path: %w", err)
		}
	}
	return append([]string{}, paths...), nil
}

func probeOID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
