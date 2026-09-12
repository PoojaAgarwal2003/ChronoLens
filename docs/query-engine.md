# Scan query engines

Milestone 2 establishes correctness and measurement baselines before indexing.
The system runs locally and uses only the Go standard library.

## Architecture

```text
Immutable JSONL input
         |
         v
Record-size, schema, ordering, and event-count validation
         |
         v
Choose exactly one representation during loading
         |
         +-- row: []telemetry.Event, interned service strings
         |
         +-- columnar: []int64 timestamps
                       []uint16 service IDs
                       []uint32 durations
                       []uint16 statuses
                       service dictionary
         |
         v
Time / service / status predicates
         |
         v
Count, error count, duration sum, min, max
         |
         v
Final mean + rows-examined statistics
```

`internal/telemetry.ReadJSONL` visits one validated event at a time.
`internal/query.Load` builds only the requested layout. On error it returns
no dataset, even if earlier records were valid. Service strings are interned
for **both** layouts so the row baseline is not deliberately burdened with
duplicate string storage.

A columnar dataset uses separate typed slices and a dictionary mapping service
names to 16-bit IDs. It does not store a second row-oriented copy. Neither
layout is a persistent column-file format. Data is parsed again on each CLI run.

`Dataset.Query` is read-only and keeps aggregate state local to each call.
Concurrent queries may share one successfully loaded dataset. Callers must not
mutate a filter's upper-bound pointer while that filter is being used.

## Input contract

Input is UTF-8 JSONL, with exactly one object per nonempty record. A final
newline is optional; LF and CRLF are accepted. Empty files are valid datasets.
Blank lines are not silently skipped.

| Field | Accepted input |
|---|---|
| `timestamp_us` | Required integer, 0 through `9223372036854775807` |
| `service` | Required nonempty string, at most 128 UTF-8 bytes, no surrounding whitespace |
| `duration_us` | Required integer, 0 through `4294967295` |
| `status` | Required integer, 100 through 599 |

Field names are case-sensitive. Unknown, duplicate, missing, null, and
incorrectly typed fields are rejected. Two objects on one line are rejected.
Fields may appear in any order. Timestamps must be nondecreasing; ties are valid.
The generator uses a narrower, documented synthetic distribution.

Records are limited to 4,096 bytes, excluding the line ending. The dictionary
supports at most 65,536 distinct services; exceeding this limit is an error,
not ID wraparound. `-max-events` defaults to 1,000,000 and must be positive.
Exactly that many events are permitted; an additional event rejects the
whole load. These are explicit bounds, not a guarantee about exact heap usage.

The source file should not change during loading. The reader does not lock it
or provide filesystem snapshot isolation. Cancellation is checked between
records; it cannot interrupt an arbitrary blocked `io.Reader.Read`.

## Query contract

```sh
go run ./cmd/query -input data/events.jsonl -engine row
go run ./cmd/query -input data/events.jsonl -engine columnar -service service-001 -status 500
```

| Flag | Default | Meaning |
|---|---|---|
| `-input` | `data/events.jsonl` | Read-only local JSONL file |
| `-engine` | `row` | `row` or `columnar` |
| `-max-events` | `1000000` | Positive maximum accepted event count |
| `-from-us` | `0` | Inclusive lower timestamp in Unix microseconds |
| `-to-us` | Unbounded | Exclusive upper timestamp; must not be below `from-us` |
| `-service` | All | Exact, case-sensitive service name |
| `-status` | `0` (all) | Exact HTTP status code, 100-599, or 0 for no filter |

Filters combine with AND. `[t, t)` is an empty range. An omitted upper bound
can include the maximum signed 64-bit timestamp. A service absent from the
dictionary simply matches nothing.

The aggregate reports:

- `count`: matching events.
- `error_count`: matching events with a 5xx status. A 4xx response is not counted here.
- `duration_sum_us`: exact unsigned 64-bit sum, with overflow detection.
- `mean_duration_us`: floating-point sum/count, or null for no matches.
- `min_duration_us` / `max_duration_us`: integer extrema, or null for no matches.

Counts, sums, and extrema are exact within their declared integer types.
The mean is floating point, not an exact rational or percentile. Consumers
such as JavaScript must handle 64-bit JSON integers carefully if values exceed
their exact numeric range. This milestone has no browser consumer.

Each result includes its engine, total row count, and actual rows examined.
Both implementations examine every row: a selective predicate reduces
aggregation work, **not scan length**. There is no binary-search index,
block pruning, result cache, approximate aggregation, or hidden sampling.

Cancellation is checked before execution, periodically during the scan, and
before returning. An error returns no partial aggregate. The CLI emits errors
on stderr and successful reports on stdout; a failed output stream may contain
partial JSON and must be discarded by the consumer.

The standalone executable returns 0 for success/help, 2 for invalid arguments,
and 1 for input/query/output errors. `go run` may translate child exit codes.

## Measurement method

The CLI reports:

| Metric | Includes | Excludes |
|---|---|---|
| `load_ms` | File open/read/close, JSON validation, dictionary and layout construction | Process startup, flag parsing |
| `query_ms` | One in-memory aggregate query and filter validation | Input loading, JSON result encoding, terminal rendering |

One CLI invocation is not a latency percentile or a warm-throughput benchmark.
On systems with coarse clock readings, a short operation may begin and end
within the same observable clock tick. The CLI reports that timing as `null`
with `timing_note`, never as zero-millisecond execution. Positive single-shot
timings can also be noisy; they are diagnostic observations, not precise
sub-millisecond performance claims.

The Go benchmark reuses loaded data and intentionally isolates query execution:

```sh
go test ./internal/query -run '^$' -bench '^BenchmarkQuery$' -benchmem -benchtime=300ms -count=3
```

`BenchmarkQuery` generates exactly 100,000 events with seed 42, 16 services,
5% independent error probability, an epoch start, and one-microsecond spacing.
The same bytes are loaded into each layout before its timed sub-benchmarks.
No queries run concurrently within this benchmark.

| Case | Predicate |
|---|---|
| `all` | No filter |
| `time_1pct` | `[50000, 51000)` microseconds: exactly 1,000 matching events |
| `service_errors` | `service-001` AND status 500 |

Each query still examines 100,000 rows. Generation and loading are outside
the timed loop. Allocations reported by `-benchmem` are per query, not dataset
storage, ingestion allocation, process RSS, or total memory consumption.
Use equivalent predicates and compare results before comparing speed.

## Measured baseline

Measured on 2026-09-12 in this Windows x64 development environment:
Go 1.27.1, Intel Xeon Platinum 8370C at 2.80 GHz, 8 reported cores / 16 logical
processors. This is **not a measurement on a personal laptop** or isolated
benchmark hardware.

The values below are medians of three run-level `ns/op` measurements converted
to milliseconds. Each run-level measurement is itself an average across its
timed iterations. They are not request-level p50, p95, or p99 latencies.

| Query over 100,000 rows | Row scan (ms/op) | Columnar scan (ms/op) |
|---|---:|---:|
| Full aggregation | 0.496770 | 0.530926 |
| 1% time range | 0.169225 | 0.133603 |
| Service + error status | 0.226116 | 0.237898 |

All six cases reported 100,000 rows examined, 40 bytes allocated per operation,
and two allocations per operation. The fixed result allocations do not scale
with the number of matching rows.

Columnar scanning was faster for this narrow time predicate, but **slower**
for full aggregation and the service/error predicate. These results do not
support a universal columnar speedup. Hardware noise, cache residency,
predicate order, and the cost of aggregate updates all matter.

Indexing is deliberately deferred: the next experiment can compare these full
scans against a time-range index using identical data and result semantics.
There are no ten-million-event, sub-100 ms end-to-end, memory-limit, or
production-throughput claims in this milestone.

## Correctness and failure testing

```sh
go test -count=1 ./...
go test ./internal/telemetry -run '^$' -fuzz '^FuzzReadJSONL$' -fuzztime=5s -parallel=2
go test -race -count=1 ./internal/query
```

The race detector requires a supported platform and C compiler; Linux CI runs
it so the local Windows setup does not require an extra compiler installation.

Tests include manually calculated aggregates, 200 reproducible generated
filter comparisons, endpoint ties, empty datasets, integer boundaries,
unsigned-sum overflow, dictionary capacity, malformed tails, cancellation
after work has begun, concurrent reads, CLI output failures, and source-file
preservation. Reader fuzzing checks that callbacks never receive invalid or
unordered events. Fuzzing complements these tests; it is not a proof that all
malformed inputs have been enumerated.
