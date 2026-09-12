// Package telemetry defines the synthetic event schema consumed by ChronoLens.
package telemetry

// Event is one telemetry observation. TimestampUS is Unix time in microseconds.
// DurationUS is a request duration, not a wall-clock timestamp.
type Event struct {
	TimestampUS int64  `json:"timestamp_us"`
	Service     string `json:"service"`
	DurationUS  uint32 `json:"duration_us"`
	Status      uint16 `json:"status"`
}
