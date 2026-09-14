package generator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

func eventsFor(t *testing.T, config Config) ([]telemetry.Event, []byte) {
	t.Helper()
	var output bytes.Buffer
	if err := Generate(context.Background(), &output, config); err != nil {
		t.Fatal(err)
	}
	data := append([]byte(nil), output.Bytes()...)
	var events []telemetry.Event
	err := telemetry.ReadJSONL(context.Background(), &output, func(event telemetry.Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return events, data
}

func TestUniformProfilePreservesOriginalBytes(t *testing.T) {
	const original = "b43192acd01023c86ab8ecafcdcfc004b5df8f1c5df08d79cf558b9fb3c969dc"
	for _, profile := range []string{"", Uniform} {
		config := validConfig()
		config.Profile = profile
		_, data := eventsFor(t, config)
		if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != original {
			t.Fatalf("uniform compatibility changed: got %s", got)
		}
	}
}

func TestIncidentReproducibilityAndRecovery(t *testing.T) {
	config := validConfig()
	config.Events = 10000
	baseline, _ := eventsFor(t, config)
	config.Profile = Incident
	events, encoded := eventsFor(t, config)
	_, again := eventsFor(t, config)
	if !bytes.Equal(encoded, again) {
		t.Fatal("incident dataset is not reproducible")
	}
	start, end := incidentBounds(config.Events)
	var hot, failures int
	var duration uint64
	for i, event := range events {
		inIncident := int64(i) >= start && int64(i) < end
		if i > 0 {
			gap := config.Interval.Microseconds()
			if int64(i-1) >= start && int64(i-1) < end {
				gap /= 4
			}
			if event.TimestampUS-events[i-1].TimestampUS != gap {
				t.Fatalf("incorrect gap before event %d", i)
			}
		}
		if !inIncident {
			expected := baseline[i]
			expected.TimestampUS = event.TimestampUS
			if !reflect.DeepEqual(event, expected) {
				t.Fatalf("baseline/recovery randomness changed at event %d", i)
			}
			continue
		}
		if event.DurationUS >= 1000000 {
			hot++
			if event.Service != "service-001" || event.DurationUS > 3000000 {
				t.Fatalf("invalid congested event: %+v", event)
			}
		}
		if event.Status == 500 {
			failures++
		}
		duration += uint64(event.DurationUS)
	}
	count := int(end - start)
	if hot < count*7/10 || hot > count*9/10 || failures < count/2 || duration/uint64(count) < 1000000 {
		t.Fatalf("incident failed to create the intended correlations: hot=%d errors=%d mean_us=%d", hot, failures, duration/uint64(count))
	}
	wantLast := config.Start.UnixMicro() + (config.Events-1)*config.Interval.Microseconds() -
		(end-start)*(config.Interval.Microseconds()*3/4)
	if events[len(events)-1].TimestampUS != wantLast {
		t.Fatal("incident compression produced the wrong total time span")
	}
	config.Seed++
	_, different := eventsFor(t, config)
	if bytes.Equal(encoded, different) {
		t.Fatal("changing the incident seed did not change the data")
	}
}

func TestIncidentValidationAndBoundaries(t *testing.T) {
	for _, change := range []func(*Config){
		func(c *Config) { c.Profile = "unknown" },
		func(c *Config) { c.Events = 4 },
		func(c *Config) { c.Interval = time.Microsecond },
		func(c *Config) { c.Interval = 6 * time.Microsecond },
	} {
		config := validConfig()
		config.Profile = Incident
		change(&config)
		var output bytes.Buffer
		if err := Generate(context.Background(), &output, config); err == nil || output.Len() != 0 {
			t.Fatal("invalid incident configuration was accepted or wrote output")
		}
	}
	config := validConfig()
	config.Profile, config.Events, config.Interval, config.ErrorPercent = Incident, 5, 4*time.Microsecond, 100
	events, _ := eventsFor(t, config)
	for _, event := range events {
		if event.Status != 500 {
			t.Fatal("incident lowered an explicitly requested 100% failure rate")
		}
	}
	first, end := incidentBounds(math.MaxInt64)
	if first <= 0 || end <= first || end >= math.MaxInt64 {
		t.Fatal("incident ordinal calculation overflowed")
	}
}

func ExampleGenerate_incident() {
	config := Config{
		Events: 5, Services: 1, Seed: 42, Profile: Incident,
		Start: time.Unix(0, 0), Interval: 4 * time.Microsecond,
	}
	var output bytes.Buffer
	if err := Generate(context.Background(), &output, config); err != nil {
		panic(err)
	}
	decoder := json.NewDecoder(&output)
	for range 5 {
		var event telemetry.Event
		if err := decoder.Decode(&event); err != nil {
			panic(err)
		}
		fmt.Println(event.TimestampUS)
	}
	// Output:
	// 0
	// 4
	// 8
	// 9
	// 13
}
