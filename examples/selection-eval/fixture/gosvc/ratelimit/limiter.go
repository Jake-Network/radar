// Package ratelimit implements a fixed-window request limiter.
package ratelimit

import (
	"time"

	"example.com/gosvc/clock"
)

type Limiter struct {
	clock  *clock.Manual
	window time.Duration
	limit  int
	start  time.Time
	used   int
}

func New(c *clock.Manual, window time.Duration, limit int) *Limiter {
	return &Limiter{clock: c, window: window, limit: limit, start: c.Now()}
}

func (l *Limiter) Allow() bool {
	if now := l.clock.Now(); now.Sub(l.start) >= l.window {
		l.start, l.used = now, 0
	}
	if l.used >= l.limit {
		return false
	}
	l.used++
	return true
}
