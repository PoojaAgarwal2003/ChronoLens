package benchmark

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"github.com/PoojaAgarwal2003/ChronoLens/internal/query"
)

type loader func(context.Context, string, query.Engine, int) (*query.Dataset, Load, error)

// Run loads and releases each layout in turn. A failure returns no partial
// report; matching hashes and full aggregates are prerequisites for success.
func Run(ctx context.Context, config Config) (Report, error) {
	return run(ctx, config, loadFile)
}

func run(ctx context.Context, config Config, load loader) (Report, error) {
	if err := config.Validate(); err != nil {
		return Report{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	report := Report{
		SchemaVersion: 1, Input: filepath.Base(config.Input), Runtime: runtimeInfo(),
		Settings: Settings{
			MaxEvents: config.MaxEvents, Samples: config.Samples,
			WindowMS: float64(config.Window) / float64(time.Millisecond), MinIterations: 3,
			MaxIterations: config.MaxIterations, Timeout: config.Timeout.String(), WarmupQueries: 1,
		},
		Engines: make([]EngineReport, 0, 3),
		Notes: []string{
			"Query samples are warm batch means, not request percentiles; min/median/max summarize batch means only.",
			"Each stable batch performs at least 3 queries and runs at least window_ms. A capped or unresolved batch has null batch_mean_ms and stable_window=false; any such sample makes its summary null.",
			"Query timing includes time.Now, loop, context, error, and full aggregate stability checks. Loading/hashing, explicit GC, warmup, metadata, profiles, charts, HTTP/UI, and report construction are excluded.",
			"Load timing includes file open/stat/read/validation, SHA256 of actual bytes, layout construction, and close; it excludes explicit GC and metadata collection.",
			"Heap snapshots are process-wide runtime.MemStats.HeapAlloc after forced GC, before/after loading one layout, with the dataset kept alive. Signed delta is not OS RSS, total allocations, or peak memory. Runtime noise and retained report values affect snapshots.",
			"Time fractions use [min_us,max_us+1), ceiling integer widths and lower-biased centering. A null to_us is unbounded (including conceptual MaxInt64+1). Ties and rounding change event selectivity; inspect endpoints, time_matched_rows, aggregate.count, and stats.rows_examined.",
			"Service-only selects the lexicographically first service with no time predicate. Engines run row, columnar, indexed in fixed order; filesystem cache, runtime GC, and process noise are not controlled.",
			"Timeout/cancellation is cooperative, checked before and after regular-file reads and in query loops; a blocked OS read or forced GC may delay cancellation.",
			"Build VCS settings are included when available; missing settings are not evidence of a clean checkout. Runtime memory limit is observed, never changed.",
		},
	}
	for _, engine := range []query.Engine{query.Row, query.Columnar, query.Indexed} {
		result, err := runEngine(ctx, config, engine, &report, load)
		if err != nil {
			return Report{}, fmt.Errorf("%s: %w", engine, err)
		}
		report.Engines = append(report.Engines, result)
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	return report, nil
}

func heapAlloc() uint64 {
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

// The dataset stays local to this call. Only scalar metadata and aggregates
// survive it, so the next engine's pre-load GC releases the previous layout.
func runEngine(ctx context.Context, config Config, engine query.Engine, report *Report, load loader) (EngineReport, error) {
	if err := ctx.Err(); err != nil {
		return EngineReport{}, err
	}
	before := heapAlloc()
	data, loading, err := load(ctx, config.Input, engine, config.MaxEvents)
	if err != nil {
		return EngineReport{}, err
	}
	after := heapAlloc()
	runtime.KeepAlive(data)
	if err := ctx.Err(); err != nil {
		return EngineReport{}, err
	}
	metadata := data.Metadata()
	result := EngineReport{
		Engine: engine, Load: loading,
		Heap:  Heap{BeforeBytes: before, AfterBytes: after, DeltaBytes: int64(after) - int64(before)},
		Cases: make([]CaseReport, 0, 5),
	}
	if len(report.Engines) == 0 {
		report.Cases, err = makeCases(metadata)
		if err != nil {
			return EngineReport{}, err
		}
		report.Source = Source{
			SHA256: loading.SHA256, Bytes: loading.Bytes, Events: metadata.Rows,
			Services: len(metadata.Services), MinUS: *metadata.MinUS, MaxUS: *metadata.MaxUS,
			TimeSpanUS: uint64(*metadata.MaxUS) - uint64(*metadata.MinUS) + 1,
		}
	} else if loading.SHA256 != report.Source.SHA256 || loading.Bytes != report.Source.Bytes {
		return EngineReport{}, fmt.Errorf("input SHA256/byte count differs between engine loads")
	}
	for i, test := range report.Cases {
		execute := func(ctx context.Context) (query.Result, error) { return data.Query(ctx, test.Filter) }
		warmup, err := execute(ctx)
		if err != nil {
			return EngineReport{}, fmt.Errorf("%s warmup: %w", test.Name, err)
		}
		if len(report.Engines) > 0 {
			if !equalAggregate(warmup.Aggregate, report.Engines[0].Cases[i].Result.Aggregate) {
				return EngineReport{}, fmt.Errorf("%s aggregate differs between engines", test.Name)
			}
		} else if test.SpanDivisor != 0 {
			report.Cases[i].TimeMatchedRows = warmup.Aggregate.Count
		}
		caseResult := CaseReport{Name: test.Name, Result: warmup, Samples: make([]Sample, 0, config.Samples)}
		for sampleIndex := 0; sampleIndex < config.Samples; sampleIndex++ {
			sample, err := sampleBatch(ctx, config, time.Now, execute, warmup.Aggregate)
			if err != nil {
				return EngineReport{}, fmt.Errorf("%s sample %d: %w", test.Name, sampleIndex+1, err)
			}
			caseResult.Samples = append(caseResult.Samples, sample)
		}
		caseResult.BatchMean = summarize(caseResult.Samples)
		result.Cases = append(result.Cases, caseResult)
	}
	runtime.KeepAlive(data)
	return result, nil
}
