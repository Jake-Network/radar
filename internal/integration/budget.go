package integration

import (
	"context"
	"time"
)

// commandBudget spans generators, declared setup and selected tests. A setup
// cache hit consumes no command; an omitted required operation stays unknown.
type commandBudget struct {
	left    int
	parent  context.Context
	timeout time.Duration
	ctx     context.Context
	cancel  context.CancelFunc
}

func (b *commandBudget) context() context.Context {
	if b.ctx == nil {
		b.ctx, b.cancel = context.WithTimeout(b.parent, b.timeout)
	}
	return b.ctx
}
func (b *commandBudget) close() {
	if b.cancel != nil {
		b.cancel()
	}
}

func (b *commandBudget) take(n int) bool {
	if n < 0 || n > b.left {
		return false
	}
	b.left -= n
	return true
}
