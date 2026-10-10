package cli

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/Jake-Network/radar/internal/integration"
)

// liveProgress shows one rewritable status line on a terminal's stderr while
// a gate works, and erases it before the report is printed. It is never used
// for pipes, files, CI logs or MCP, so captured output stays clean.
type liveProgress struct {
	w       io.Writer
	p       palette
	started time.Time
	mu      sync.Mutex
	text    string
	frame   int
	stop    chan struct{}
	stopped chan struct{}
}

var sweep = []string{"◜", "◝", "◞", "◟"}

// startProgress returns nil when a has no live terminal; a nil progress
// ignores every call.
func (a *app) startProgress(first string) *liveProgress {
	if a.live == nil {
		return nil
	}
	l := &liveProgress{w: a.live, p: paletteOf(a.live), started: time.Now(), text: first, stop: make(chan struct{}), stopped: make(chan struct{})}
	go func() {
		defer close(l.stopped)
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		for {
			l.draw()
			select {
			case <-l.stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return l
}

func (l *liveProgress) draw() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.frame++
	elapsed := time.Since(l.started).Truncate(100 * time.Millisecond)
	fmt.Fprintf(l.w, "\r\x1b[2K%s %s %s", l.p.green(sweep[l.frame%len(sweep)]), l.text, l.p.dim(elapsed.String()))
}

// step describes an integration step in a few words.
func (l *liveProgress) step(s integration.Step) {
	if l == nil {
		return
	}
	text := ""
	switch s.Stage {
	case integration.StageCombine:
		text = "combining branches in private Git state"
	case integration.StageAnalyze:
		text = "checking contracts, imports and test relationships"
	case integration.StageTest:
		cmd := displayCommand(s.Command)
		if len(cmd) > 60 {
			cmd = cmd[:57] + "…"
		}
		text = fmt.Sprintf("running test command %d/%d  %s", s.Index, s.Total, l.p.cyan(cmd))
	default:
		return
	}
	if s.Repo != "" {
		text = l.p.bold(s.Repo) + "  " + text
	}
	l.mu.Lock()
	l.text = text
	l.mu.Unlock()
}

// done stops the animation and erases the status line.
func (l *liveProgress) done() {
	if l == nil {
		return
	}
	close(l.stop)
	<-l.stopped
	fmt.Fprint(l.w, "\r\x1b[2K")
}

// callback adapts l to integration.Options.Progress.
func (l *liveProgress) callback() func(integration.Step) {
	if l == nil {
		return nil
	}
	return l.step
}
