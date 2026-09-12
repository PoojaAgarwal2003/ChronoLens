package query

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

// Catalog loads one source snapshot and exposes all three engines. Indexed
// execution shares the immutable columnar arrays; only rows require another layout.
type Catalog struct {
	engines map[Engine]*Dataset
}

func LoadCatalog(ctx context.Context, input io.Reader, maxEvents int) (*Catalog, error) {
	columnar, err := Load(ctx, input, Columnar, maxEvents)
	if err != nil {
		return nil, err
	}
	row := &Dataset{
		engine: Row, names: columnar.names, dictionary: columnar.dictionary,
		rows: make([]telemetry.Event, columnar.Len()),
	}
	for i := range row.rows {
		if i%1024 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		row.rows[i] = telemetry.Event{
			TimestampUS: columnar.timestamps[i], Service: columnar.names[columnar.serviceIDs[i]],
			DurationUS: columnar.durations[i], Status: columnar.statuses[i],
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	indexed := *columnar
	indexed.engine = Indexed
	return &Catalog{engines: map[Engine]*Dataset{Row: row, Columnar: columnar, Indexed: &indexed}}, nil
}

func (c *Catalog) Engine(engine Engine) (*Dataset, error) {
	if c == nil || c.engines[engine] == nil {
		return nil, fmt.Errorf("engine is not available")
	}
	return c.engines[engine], nil
}

type Metadata struct {
	Rows     int
	Services []string
	Statuses []uint16
	MinUS    *int64
	MaxUS    *int64
}

func (c *Catalog) Metadata() Metadata {
	data := c.engines[Columnar]
	result := Metadata{Rows: data.Len(), Services: append([]string{}, data.names...), Statuses: []uint16{}}
	sort.Strings(result.Services)
	statuses := make(map[uint16]bool)
	for _, status := range data.statuses {
		statuses[status] = true
	}
	for status := range statuses {
		result.Statuses = append(result.Statuses, status)
	}
	sort.Slice(result.Statuses, func(i, j int) bool { return result.Statuses[i] < result.Statuses[j] })
	if data.Len() > 0 {
		first, last := data.timestamps[0], data.timestamps[data.Len()-1]
		result.MinUS, result.MaxUS = &first, &last
	}
	return result
}
