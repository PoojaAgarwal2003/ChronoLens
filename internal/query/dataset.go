// Package query compares immutable row, columnar, and indexed columnar datasets
// under identical aggregate query semantics.
package query

import (
	"context"
	"fmt"
	"io"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/snapshot"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

type InputFormat string

const (
	JSONLFormat    InputFormat = "jsonl"
	SnapshotFormat InputFormat = "snapshot"
)

func ParseInputFormat(value string) (InputFormat, error) {
	switch InputFormat(value) {
	case JSONLFormat, SnapshotFormat:
		return InputFormat(value), nil
	default:
		return "", fmt.Errorf("format must be jsonl or snapshot")
	}
}

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

// Load is the JSONL compatibility wrapper for LoadFormat.
func Load(ctx context.Context, input io.Reader, engine Engine, maxEvents int) (*Dataset, error) {
	return LoadFormat(ctx, input, engine, maxEvents, JSONLFormat)
}

// LoadFormat rejects the whole dataset on any record, integrity, or limit failure.
// The explicit format is never inferred from the input. maxEvents bounds accepted
// row count, not exact heap usage.
func LoadFormat(ctx context.Context, input io.Reader, engine Engine, maxEvents int, format InputFormat) (*Dataset, error) {
	if _, err := ParseInputFormat(string(format)); err != nil {
		return nil, err
	}
	if _, err := ParseEngine(string(engine)); err != nil {
		return nil, err
	}
	if maxEvents <= 0 {
		return nil, fmt.Errorf("max-events must be greater than zero")
	}
	data := &Dataset{engine: engine, dictionary: make(map[string]uint16)}
	visit := func(event telemetry.Event) error { return data.appendEvent(event, maxEvents) }
	var err error
	switch format {
	case JSONLFormat:
		err = telemetry.ReadJSONL(ctx, input, visit)
	case SnapshotFormat:
		_, err = snapshot.Read(ctx, input, maxEvents, visit)
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (d *Dataset) appendEvent(event telemetry.Event, maxEvents int) error {
	if d.Len() >= maxEvents {
		return fmt.Errorf("dataset exceeds max-events limit (%d)", maxEvents)
	}
	id, exists := d.dictionary[event.Service]
	if !exists {
		if len(d.names) >= MaxServices {
			return fmt.Errorf("dataset exceeds %d distinct services", MaxServices)
		}
		id = uint16(len(d.names))
		d.dictionary[event.Service] = id
		d.names = append(d.names, event.Service)
	}
	if d.engine == Row {
		// Intern service strings for every layout, including across snapshot blocks.
		event.Service = d.names[id]
		d.rows = append(d.rows, event)
	} else {
		d.timestamps = append(d.timestamps, event.TimestampUS)
		d.serviceIDs = append(d.serviceIDs, id)
		d.durations = append(d.durations, event.DurationUS)
		d.statuses = append(d.statuses, event.Status)
	}
	return nil
}

func (d *Dataset) Len() int {
	if d.engine == Row {
		return len(d.rows)
	}
	return len(d.timestamps)
}

func (d *Dataset) ServiceCount() int { return len(d.names) }
