// Package benchmark measures single-layout aggregate queries against a verified
// JSONL source. It does not measure the server, profiles, or browser rendering.
package benchmark

import (
	"fmt"
	"time"
)

type Config struct {
	Input         string
	MaxEvents     int
	Samples       int
	Window        time.Duration
	MaxIterations int
	Timeout       time.Duration
}

func DefaultConfig() Config {
	return Config{
		Input: "data/events.jsonl", MaxEvents: 1_000_000, Samples: 5,
		Window: 100 * time.Millisecond, MaxIterations: 1_000_000,
		Timeout: 10 * time.Minute,
	}
}

func (c Config) Validate() error {
	switch {
	case c.Input == "":
		return fmt.Errorf("input must be nonempty")
	case c.MaxEvents < 1 || c.MaxEvents > 100_000_000:
		return fmt.Errorf("max-events must be between 1 and 100000000 (not a byte limit)")
	case c.Samples < 1 || c.Samples > 100:
		return fmt.Errorf("samples must be between 1 and 100")
	case c.Window <= 0 || c.Window > time.Minute:
		return fmt.Errorf("window must be greater than zero and at most 1m")
	case c.MaxIterations < 3 || c.MaxIterations > 100_000_000:
		return fmt.Errorf("max-iterations must be between 3 and 100000000")
	case c.Timeout <= 0 || c.Timeout > 24*time.Hour:
		return fmt.Errorf("timeout must be greater than zero and at most 24h")
	}
	return nil
}
