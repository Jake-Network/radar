// Package clock provides a manual clock for deterministic rate limiting.
package clock

import "time"

type Manual struct{ now time.Time }

func NewManual(start time.Time) *Manual { return &Manual{now: start} }
func (m *Manual) Now() time.Time        { return m.now }
func (m *Manual) Advance(d time.Duration) {
	m.now = m.now.Add(d)
}
