package benchmark

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/measure"
	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
)

func equalValue[T comparable](a, b *T) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

func equalAggregate(a, b query.Aggregate) bool {
	return a.Count == b.Count && a.ErrorCount == b.ErrorCount &&
		a.DurationSumUS == b.DurationSumUS && equalValue(a.MeanDurationUS, b.MeanDurationUS) &&
		equalValue(a.MinDurationUS, b.MinDurationUS) && equalValue(a.MaxDurationUS, b.MaxDurationUS)
}

func sampleBatch(ctx context.Context, config Config, now func() time.Time,
	execute func(context.Context) (query.Result, error), expected query.Aggregate,
) (Sample, error) {
	var sample Sample
	if err := ctx.Err(); err != nil {
		return sample, err
	}
	start := now()
	var elapsed time.Duration
	for sample.Iterations < config.MaxIterations {
		if err := ctx.Err(); err != nil {
			return Sample{}, err
		}
		result, err := execute(ctx)
		if err != nil {
			return Sample{}, err
		}
		if !equalAggregate(result.Aggregate, expected) {
			return Sample{}, fmt.Errorf("aggregate changed during repeated queries")
		}
		sample.Iterations++
		elapsed = now().Sub(start)
		if sample.Iterations >= 3 && elapsed >= config.Window {
			sample.StableWindow = true
			break
		}
	}
	if err := ctx.Err(); err != nil {
		return Sample{}, err
	}
	if elapsed > 0 {
		sample.ElapsedMS = measure.Milliseconds(elapsed)
	}
	if sample.StableWindow {
		mean := *sample.ElapsedMS / float64(sample.Iterations)
		sample.BatchMeanMS = &mean
	}
	return sample, nil
}

func summarize(samples []Sample) Summary {
	if len(samples) == 0 {
		return Summary{}
	}
	values := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if !sample.StableWindow || sample.BatchMeanMS == nil {
			return Summary{}
		}
		values = append(values, *sample.BatchMeanMS)
	}
	sort.Float64s(values)
	low, high := values[0], values[len(values)-1]
	median := values[len(values)/2]
	if len(values)%2 == 0 {
		median = values[len(values)/2-1]/2 + median/2
	}
	return Summary{Min: &low, Median: &median, Max: &high, StableWindow: true}
}
