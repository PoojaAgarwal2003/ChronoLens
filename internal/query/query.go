package query

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/telemetry"
)

// Filter selects [FromUS, ToUS). A nil ToUS has no upper bound; an empty
// Service or zero Status leaves that dimension unfiltered.
type Filter struct {
	FromUS  int64  `json:"from_us"`
	ToUS    *int64 `json:"to_us"`
	Service string `json:"service"`
	Status  uint16 `json:"status"`
}

func (f Filter) Validate() error {
	if f.FromUS < 0 || (f.ToUS != nil && *f.ToUS < f.FromUS) {
		return fmt.Errorf("time range must satisfy 0 <= from-us <= to-us")
	}
	if f.Service != "" {
		if err := telemetry.ValidateService(f.Service); err != nil {
			return err
		}
	}
	if f.Status != 0 && (f.Status < 100 || f.Status > 599) {
		return fmt.Errorf("status must be 0 (all) or between 100 and 599")
	}
	return nil
}

type Aggregate struct {
	Count          uint64   `json:"count"`
	ErrorCount     uint64   `json:"error_count"`
	DurationSumUS  uint64   `json:"duration_sum_us"`
	MeanDurationUS *float64 `json:"mean_duration_us"`
	MinDurationUS  *uint32  `json:"min_duration_us"`
	MaxDurationUS  *uint32  `json:"max_duration_us"`
}

type Stats struct {
	Engine           Engine `json:"engine"`
	TotalRows        int    `json:"total_rows"`
	RowsExamined     int    `json:"rows_examined"`
	RowsSkipped      int    `json:"rows_skipped"`
	IndexComparisons int    `json:"index_comparisons"`
}

type Result struct {
	Aggregate Aggregate `json:"aggregate"`
	Stats     Stats     `json:"stats"`
}

type accumulator struct {
	count, errors, sum uint64
	min, max           uint32
}

func (a *accumulator) add(duration uint32, status uint16) error {
	if uint64(duration) > math.MaxUint64-a.sum {
		return fmt.Errorf("duration sum overflows uint64")
	}
	if a.count == 0 || duration < a.min {
		a.min = duration
	}
	if a.count == 0 || duration > a.max {
		a.max = duration
	}
	a.count++
	a.sum += uint64(duration)
	if status >= 500 {
		a.errors++
	}
	return nil
}

func (a accumulator) aggregate() Aggregate {
	result := Aggregate{Count: a.count, ErrorCount: a.errors, DurationSumUS: a.sum}
	if a.count != 0 {
		mean := float64(a.sum) / float64(a.count)
		result.MeanDurationUS = &mean
		result.MinDurationUS = &a.min
		result.MaxDurationUS = &a.max
	}
	return result
}

func matchesNumeric(timestamp int64, status uint16, f Filter) bool {
	return timestamp >= f.FromUS && (f.ToUS == nil || timestamp < *f.ToUS) &&
		(f.Status == 0 || status == f.Status)
}

// Query applies identical predicates to either the full dataset or an indexed
// time slice. RowsExamined counts candidate row visits; timestamp probes used
// to find the slice are counted separately as IndexComparisons.
func (d *Dataset) Query(ctx context.Context, filter Filter) (Result, error) {
	if err := filter.Validate(); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	stats := Stats{Engine: d.engine, TotalRows: d.Len()}
	var totals accumulator
	switch d.engine {
	case Row:
		for i, event := range d.rows {
			if i%1024 == 0 {
				if err := ctx.Err(); err != nil {
					return Result{}, err
				}
			}
			stats.RowsExamined++
			if !matchesNumeric(event.TimestampUS, event.Status, filter) ||
				(filter.Service != "" && event.Service != filter.Service) {
				continue
			}
			if err := totals.add(event.DurationUS, event.Status); err != nil {
				return Result{}, err
			}
		}
	case Columnar, Indexed:
		id, known := d.dictionary[filter.Service]
		first, end := 0, len(d.timestamps)
		if d.engine == Indexed {
			first, end, stats.IndexComparisons = d.timeRange(filter)
		}
		for i := first; i < end; i++ {
			if i%1024 == 0 {
				if err := ctx.Err(); err != nil {
					return Result{}, err
				}
			}
			stats.RowsExamined++
			if !matchesNumeric(d.timestamps[i], d.statuses[i], filter) ||
				(filter.Service != "" && (!known || d.serviceIDs[i] != id)) {
				continue
			}
			if err := totals.add(d.durations[i], d.statuses[i]); err != nil {
				return Result{}, err
			}
		}
	default:
		return Result{}, fmt.Errorf("dataset is not initialized by Load")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	stats.RowsSkipped = stats.TotalRows - stats.RowsExamined
	return Result{Aggregate: totals.aggregate(), Stats: stats}, nil
}

// The validated, sorted timestamp column is itself the index. Lower bounds
// include every tie at FromUS and exclude every tie at ToUS without +1 overflow.
func (d *Dataset) timeRange(filter Filter) (first, end, comparisons int) {
	first = sort.Search(len(d.timestamps), func(i int) bool {
		comparisons++
		return d.timestamps[i] >= filter.FromUS
	})
	end = len(d.timestamps)
	if filter.ToUS != nil {
		end = first + sort.Search(len(d.timestamps)-first, func(i int) bool {
			comparisons++
			return d.timestamps[first+i] >= *filter.ToUS
		})
	}
	return
}
