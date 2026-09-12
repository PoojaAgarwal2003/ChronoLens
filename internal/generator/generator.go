// Package generator produces reproducible, timestamp-ordered synthetic events.
package generator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

const MaxServices = 1024

// Config completely specifies a dataset; generation never reads the wall clock.
type Config struct {
	Events       int64
	Services     int
	Seed         int64
	Start        time.Time
	Interval     time.Duration
	ErrorPercent int
}

// Validate checks bounds before a caller creates an output file.
func (c Config) Validate() error {
	if c.Events <= 0 {
		return fmt.Errorf("events must be greater than zero")
	}
	if c.Services < 1 || c.Services > MaxServices {
		return fmt.Errorf("services must be between 1 and %d", MaxServices)
	}
	if c.ErrorPercent < 0 || c.ErrorPercent > 100 {
		return fmt.Errorf("error-percent must be between 0 and 100")
	}
	if c.Interval < time.Microsecond || c.Interval%time.Microsecond != 0 {
		return fmt.Errorf("interval must be a positive whole number of microseconds")
	}
	if c.Start.Before(time.Unix(0, 0)) || c.Start.Year() > 9999 || c.Start.Nanosecond()%1000 != 0 {
		return fmt.Errorf("start must be between the Unix epoch and year 9999 with microsecond precision")
	}
	start, step := c.Start.UnixMicro(), c.Interval.Microseconds()
	if c.Events-1 > (math.MaxInt64-start)/step {
		return fmt.Errorf("event timestamps would overflow int64 microseconds")
	}
	return nil
}

// Generate streams JSONL using O(Services) working memory, excluding dst's buffer.
// Output is reproducible for the same configuration and generator version.
// Callers own dst, including any buffering, closing, or partial-file cleanup.
func Generate(ctx context.Context, dst io.Writer, c Config) error {
	if err := c.Validate(); err != nil {
		return err
	}
	services := make([]string, c.Services)
	for i := range services {
		services[i] = fmt.Sprintf("service-%03d", i+1)
	}
	rng := rand.New(rand.NewSource(c.Seed))
	encoder := json.NewEncoder(dst)
	timestamp, step := c.Start.UnixMicro(), c.Interval.Microseconds()
	for i := int64(0); i < c.Events; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		event := telemetry.Event{
			TimestampUS: timestamp,
			Service:     services[rng.Intn(len(services))],
			DurationUS:  uint32(100 + rng.Intn(499901)),
			Status:      200,
		}
		if rng.Intn(100) < c.ErrorPercent {
			event.Status = 500
		}
		if err := encoder.Encode(event); err != nil {
			return fmt.Errorf("write event %d: %w", i+1, err)
		}
		if i < c.Events-1 {
			timestamp += step
		}
	}
	return nil
}
