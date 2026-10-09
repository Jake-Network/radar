package clock

import (
	"testing"
	"time"
)

func TestAdvance(t *testing.T) {
	start := time.Unix(0, 0)
	c := NewManual(start)
	c.Advance(time.Second)
	if got := c.Now().Sub(start); got != time.Second {
		t.Fatal(got)
	}
}
