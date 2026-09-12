// Package query compares immutable row, columnar, and indexed columnar datasets
// under identical aggregate query semantics.
package query

import (
	"context"
	"fmt"
	"io"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

type Engine string

const (
	Row      Engine = "row"
	Columnar Engine = "columnar"
	Indexed  Engine = "indexed"

	MaxServices = 1 << 16
)

func ParseEngine(value string) (Engine, error) {
	switch Engine(value) {
	case Row, Columnar, Indexed:
		return Engine(value), nil
	default:
		return "", fmt.Errorf("engine must be row, columnar, or indexed")
	}
}

// Dataset owns exactly one layout, not both. It is read-only after Load and
// supports concurrent queries without shared query state.
type Dataset struct {
	engine     Engine
	rows       []telemetry.Event
	timestamps []int64
	serviceIDs []uint16
	durations  []uint32
	statuses   []uint16
	names      []string
	dictionary map[string]uint16
}

// Load rejects the whole dataset on any malformed record or limit violation.
// maxEvents bounds accepted row count, not exact heap usage.
func Load(ctx context.Context, input io.Reader, engine Engine, maxEvents int) (*Dataset, error) {
	if _, err := ParseEngine(string(engine)); err != nil {
		return nil, err
	}
	if maxEvents <= 0 {
		return nil, fmt.Errorf("max-events must be greater than zero")
	}
	data := &Dataset{engine: engine, dictionary: make(map[string]uint16)}
	err := telemetry.ReadJSONL(ctx, input, func(event telemetry.Event) error {
		if data.Len() >= maxEvents {
			return fmt.Errorf("dataset exceeds max-events limit (%d)", maxEvents)
		}
		id, exists := data.dictionary[event.Service]
		if !exists {
			if len(data.names) >= MaxServices {
				return fmt.Errorf("dataset exceeds %d distinct services", MaxServices)
			}
			id = uint16(len(data.names))
			data.dictionary[event.Service] = id
			data.names = append(data.names, event.Service)
		}
		if engine == Row {
			// Intern service strings for both engines, not just the columnar one.
			event.Service = data.names[id]
			data.rows = append(data.rows, event)
		} else {
			data.timestamps = append(data.timestamps, event.TimestampUS)
			data.serviceIDs = append(data.serviceIDs, id)
			data.durations = append(data.durations, event.DurationUS)
			data.statuses = append(data.statuses, event.Status)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (d *Dataset) Len() int {
	if d.engine == Row {
		return len(d.rows)
	}
	return len(d.timestamps)
}

func (d *Dataset) ServiceCount() int { return len(d.names) }
