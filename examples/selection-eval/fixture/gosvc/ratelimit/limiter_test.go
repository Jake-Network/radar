package ratelimit

import (
	"testing"
	"time"

	"example.com/gosvc/clock"
)

func TestWindowResets(t *testing.T) {
	c := clock.NewManual(time.Unix(0, 0))
	l := New(c, time.Minute, 2)
	if !l.Allow() || !l.Allow() || l.Allow() {
		t.Fatal("limit not enforced")
	}
	c.Advance(time.Minute)
	if !l.Allow() {
		t.Fatal("window did not reset")
	}
}
