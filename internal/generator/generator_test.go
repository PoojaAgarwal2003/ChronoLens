package generator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

func validConfig() Config {
	return Config{
		Events: 1000, Services: 4, Seed: 42,
		Start:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Interval: time.Millisecond, ErrorPercent: 5,
	}
}

func TestGenerateDeterministic(t *testing.T) {
	config := validConfig()
	var first, second, different bytes.Buffer
	for _, dst := range []*bytes.Buffer{&first, &second} {
		if err := Generate(context.Background(), dst, config); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("same configuration produced different output")
	}
	config.Seed++
	if err := Generate(context.Background(), &different, config); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Bytes(), different.Bytes()) {
		t.Fatal("different seeds produced identical output")
	}
}

func TestGenerateSchemaAndOrdering(t *testing.T) {
	config := validConfig()
	var output bytes.Buffer
	if err := Generate(context.Background(), &output, config); err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(output.Bytes(), []byte("\n")); int64(got) != config.Events {
		t.Fatalf("got %d JSONL lines, want %d", got, config.Events)
	}
	decoder := json.NewDecoder(&output)
	decoder.DisallowUnknownFields()
	seen := make(map[string]bool)
	for i := int64(0); i < config.Events; i++ {
		var event telemetry.Event
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if want := config.Start.UnixMicro() + i*config.Interval.Microseconds(); event.TimestampUS != want {
			t.Fatalf("event %d timestamp: got %d, want %d", i, event.TimestampUS, want)
		}
		if event.DurationUS < 100 || event.DurationUS > 500000 {
			t.Fatalf("duration out of range: %d", event.DurationUS)
		}
		if event.Status != 200 && event.Status != 500 {
			t.Fatalf("invalid status: %d", event.Status)
		}
		if event.Service < "service-001" || event.Service > "service-004" {
			t.Fatalf("invalid service: %q", event.Service)
		}
		seen[event.Service] = true
	}
	if len(seen) != config.Services {
		t.Fatalf("got %d services, want %d", len(seen), config.Services)
	}
	var extra telemetry.Event
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("expected end of dataset, got %v", err)
	}
}

func TestErrorPercentBoundaries(t *testing.T) {
	for _, percent := range []int{0, 100} {
		t.Run(strconv.Itoa(percent), func(t *testing.T) {
			config := validConfig()
			config.Events, config.ErrorPercent = 20, percent
			var output bytes.Buffer
			if err := Generate(context.Background(), &output, config); err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(&output)
			for i := int64(0); i < config.Events; i++ {
				var event telemetry.Event
				if err := decoder.Decode(&event); err != nil {
					t.Fatal(err)
				}
				want := uint16(200)
				if percent == 100 {
					want = 500
				}
				if event.Status != want {
					t.Fatalf("got status %d, want %d", event.Status, want)
				}
			}
		})
	}
}

func TestInvalidConfigWritesNothing(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{"zero events", func(c *Config) { c.Events = 0 }},
		{"negative events", func(c *Config) { c.Events = -1 }},
		{"zero services", func(c *Config) { c.Services = 0 }},
		{"too many services", func(c *Config) { c.Services = MaxServices + 1 }},
		{"negative error percent", func(c *Config) { c.ErrorPercent = -1 }},
		{"large error percent", func(c *Config) { c.ErrorPercent = 101 }},
		{"zero interval", func(c *Config) { c.Interval = 0 }},
		{"negative interval", func(c *Config) { c.Interval = -time.Second }},
		{"fractional microsecond interval", func(c *Config) { c.Interval = 1500 * time.Nanosecond }},
		{"pre-epoch start", func(c *Config) { c.Start = time.Unix(-1, 0) }},
		{"fractional microsecond start", func(c *Config) { c.Start = c.Start.Add(time.Nanosecond) }},
		{"unrepresentable start year", func(c *Config) { c.Start = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }},
		{"timestamp overflow", func(c *Config) { c.Events = math.MaxInt64 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := validConfig()
			test.change(&config)
			var output bytes.Buffer
			if err := Generate(context.Background(), &output, config); err == nil {
				t.Fatal("expected validation error")
			}
			if output.Len() != 0 {
				t.Fatal("invalid configuration wrote output")
			}
		})
	}
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestGenerateWriteError(t *testing.T) {
	failure := errors.New("simulated disk full")
	err := Generate(context.Background(), failingWriter{failure}, validConfig())
	if !errors.Is(err, failure) || !strings.Contains(err.Error(), "event 1") {
		t.Fatalf("expected wrapped writer error with event index, got %v", err)
	}
}

func TestGenerateCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var output bytes.Buffer
	if err := Generate(ctx, &output, validConfig()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}

	if output.Len() != 0 {
		t.Fatal("cancelled generation wrote output")
	}
}

type cancelOnWrite struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancelOnWrite) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.cancel()
	return n, err
}

func TestGenerateCancelledDuringOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	output := &cancelOnWrite{cancel: cancel}
	if err := Generate(ctx, output, validConfig()); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation after first event, got %v", err)
	}
	if got := bytes.Count(output.Bytes(), []byte("\n")); got != 1 {
		t.Fatalf("expected one event before cancellation, got %d", got)
	}
}

func TestOneEventAtEpoch(t *testing.T) {
	config := validConfig()
	config.Events, config.Services = 1, 1
	config.Start, config.Interval = time.Unix(0, 0), time.Microsecond
	var output bytes.Buffer
	if err := Generate(context.Background(), &output, config); err != nil {
		t.Fatal(err)
	}
	var event telemetry.Event
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event); err != nil {
		t.Fatal(err)
	}
	if event.TimestampUS != 0 || event.Service != "service-001" {
		t.Fatalf("unexpected event: %+v", event)
	}
}
