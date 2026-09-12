// Package measure reports elapsed durations without treating an unresolved
// clock tick as a zero-latency operation.
package measure

import "time"

func Milliseconds(elapsed time.Duration) *float64 {
	if elapsed == 0 {
		return nil
	}
	ms := float64(elapsed) / float64(time.Millisecond)
	return &ms
}
