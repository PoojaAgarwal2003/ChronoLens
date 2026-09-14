package benchmark

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
)

func tickingClock(step time.Duration) func() time.Time {
	value := time.Unix(0, 0)
	return func() time.Time {
		now := value
		value = value.Add(step)
		return now
	}
}

func TestBatchTimingWindowsAndCap(t *testing.T) {
	for _, test := range []struct {
		name       string
		step       time.Duration
		window     time.Duration
		cap        int
		iterations int
		stable     bool
	}{
		{"minimum three", time.Second, time.Millisecond, 10, 3, true},
		{"minimum time", time.Millisecond, 5 * time.Millisecond, 10, 5, true},
		{"iteration cap", time.Millisecond, time.Second, 3, 3, false},
		{"frozen clock", 0, time.Millisecond, 3, 3, false},
		{"backwards clock", -time.Millisecond, time.Millisecond, 3, 3, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := quickConfig("unused")
			config.Window, config.MaxIterations = test.window, test.cap
			calls := 0
			sample, err := sampleBatch(context.Background(), config, tickingClock(test.step), func(context.Context) (query.Result, error) {
				calls++
				return query.Result{}, nil
			}, query.Aggregate{})
			if err != nil || sample.Iterations != test.iterations || calls != test.iterations ||
				sample.StableWindow != test.stable || (sample.BatchMeanMS != nil) != test.stable {
				t.Fatalf("incorrect batch: %+v, %v, calls=%d", sample, err, calls)
			}
			if test.stable && *sample.BatchMeanMS != float64(test.step)/float64(time.Millisecond) {
				t.Fatal("incorrect batch mean")
			}
			if test.step <= 0 && sample.ElapsedMS != nil {
				t.Fatal("unresolved clock reported elapsed time")
			}
			encoded, err := json.Marshal(sample)
			if err != nil {
				t.Fatal(err)
			}
			if !test.stable && (!strings.Contains(string(encoded), `"batch_mean_ms":null`) ||
				!strings.Contains(string(encoded), `"stable_window":false`)) {
				t.Fatal("unstable sample must explicitly report null")
			}
		})
	}
}

func TestBatchChecksEveryAggregateField(t *testing.T) {
	original := query.Aggregate{
		Count: 2, ErrorCount: 1, DurationSumUS: 10, MeanDurationUS: pointer(5.0),
		MinDurationUS: pointer(uint32(0)), MaxDurationUS: pointer(uint32(10)),
	}
	for _, change := range []func(*query.Aggregate){
		func(a *query.Aggregate) { a.Count++ },
		func(a *query.Aggregate) { a.ErrorCount++ },
		func(a *query.Aggregate) { a.DurationSumUS++ },
		func(a *query.Aggregate) { a.MeanDurationUS = pointer(6.0) },
		func(a *query.Aggregate) { a.MinDurationUS = pointer(uint32(1)) },
		func(a *query.Aggregate) { a.MaxDurationUS = pointer(uint32(11)) },
		func(a *query.Aggregate) { a.MeanDurationUS = nil },
		func(a *query.Aggregate) { a.MinDurationUS = nil },
		func(a *query.Aggregate) { a.MaxDurationUS = nil },
	} {
		changed := original
		change(&changed)
		if _, err := sampleBatch(context.Background(), quickConfig("unused"), tickingClock(time.Second),
			func(context.Context) (query.Result, error) { return query.Result{Aggregate: changed}, nil }, original); err == nil {
			t.Fatal("changed aggregate accepted")
		}
	}
}

func TestBatchErrorsAndCancellation(t *testing.T) {
	wantErr := errors.New("query failure")
	_, err := sampleBatch(context.Background(), quickConfig("unused"), tickingClock(time.Second),
		func(context.Context) (query.Result, error) { return query.Result{}, wantErr }, query.Aggregate{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("query error ignored: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sample, err := sampleBatch(ctx, quickConfig("unused"), tickingClock(time.Second),
		func(context.Context) (query.Result, error) {
			cancel()
			return query.Result{}, nil
		}, query.Aggregate{})
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(sample, Sample{}) {
		t.Fatalf("cancelled batch exposed partial timing: %+v, %v", sample, err)
	}
	calls := 0
	_, err = sampleBatch(ctx, quickConfig("unused"), tickingClock(time.Second),
		func(context.Context) (query.Result, error) { calls++; return query.Result{}, nil }, query.Aggregate{})
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("pre-cancelled batch executed a query")
	}
}

func TestBatchMeanSummary(t *testing.T) {
	for _, test := range []struct {
		values []float64
		median float64
	}{{[]float64{9, 1, 5}, 5}, {[]float64{8, 1, 2, 4}, 3}, {[]float64{7}, 7}} {
		samples := make([]Sample, len(test.values))
		for i, value := range test.values {
			samples[i] = Sample{StableWindow: true, BatchMeanMS: pointer(value)}
		}
		result := summarize(samples)
		if !result.StableWindow || *result.Median != test.median {
			t.Fatalf("incorrect median: %+v", result)
		}
		for _, value := range test.values {
			if *result.Min > value || *result.Max < value {
				t.Fatal("incorrect summary range")
			}
		}
		samples[0].StableWindow = false
		if result := summarize(samples); !reflect.DeepEqual(result, Summary{}) {
			t.Fatal("unstable sample must invalidate entire summary")
		}
	}
	if result := summarize(nil); !reflect.DeepEqual(result, Summary{}) {
		t.Fatal("empty summary must be null")
	}
}
