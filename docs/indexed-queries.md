# Indexed time-range queries

The `indexed` engine reuses the columnar layout's sorted timestamp array as its
index. There is no extra tree, duplicated timestamp array, service index, or
result cache. The loader's nondecreasing timestamp invariant makes binary
search valid; unsorted input is rejected instead of quietly returning bad results.

## Algorithm and semantics

For the same half-open range `[from, to)` used by the scan engines:

```text
timestamps: [1, 2, 2, 3, 4, 4, 5]
                   query [2, 4)
                |---------|
                first     end
candidate rows:    2, 2, 3
```

Find the first timestamp >= `from`, then the first timestamp >= `to` starting
at that position. An omitted upper bound uses the array length. Scan only
`[first, end)` and apply the same service/status predicates and accumulator.
Both endpoints use lower-bound searches, so ties are handled consistently.
No `timestamp + 1` operation is needed; the largest signed 64-bit timestamp
works without overflow.

Query work is O(log N + K), where K is the number of events in the requested
time range, not necessarily the number matching all predicates. Full-range
queries still visit N rows. A service-only query cannot benefit from this
time index.

| Statistic | Meaning |
|---|---|
| `total_rows` | All loaded events |
| `rows_examined` | Candidate row visits during aggregation, including filter rejections |
| `rows_skipped` | `total_rows - rows_examined`, not disk bytes or key probes avoided |
| `index_comparisons` | Timestamp comparisons performed by the binary searches |

A time-index probe reads a timestamp even though it is not an aggregate row
visit. Reporting probes separately avoids pretending the index does no work.
All data still loads into memory before querying; these are not disk-I/O savings.

## Run it

```sh
go run ./cmd/generator -events 1000000 -interval 1us -start 1970-01-01T00:00:00Z -seed 42 -output data/index-demo.jsonl
go run ./cmd/query -input data/index-demo.jsonl -engine indexed -from-us 499500 -to-us 500500
go run ./cmd/query -input data/index-demo.jsonl -engine row -from-us 499500 -to-us 500500
```

The aggregates must match. The row scan examines 1,000,000 rows, while indexed
execution examines 1,000 candidate rows plus its separately counted key probes.
The input remains unchanged. Choose a new output filename if it already exists.

## Measured results

```sh
go test ./internal/query -run '^$' -bench '^BenchmarkSelectivity$' -benchmem -benchtime=200ms -count=3
```

Measured on 2026-09-12 with Go 1.27.1, Windows x64, Intel Xeon Platinum 8370C
at 2.80 GHz, 8 reported cores / 16 logical processors. This is the development
environment, not a personal laptop or an isolated performance lab.

The workload contains exactly **1,000,000 events**, seed 42, 16 services, a 5%
error probability, an epoch start, and one-microsecond spacing. Each time range
is centered in the dataset. Only the time predicate is applied.

Values are the median of three run-level averages, converted from `ns/op`.
These are warm in-memory queries; loading, parsing, process startup, and JSON
reporting are excluded. They are not request-level latency percentiles.

| Selected time range | Row scan (ms) | Columnar scan (ms) | Indexed (ms) | Indexed candidate rows |
|---|---:|---:|---:|---:|
| 0.1% | 3.179237 | 1.927867 | 0.005480 | 1,000 |
| 1% | 3.055214 | 1.906122 | 0.054949 | 10,000 |
| 10% | 3.343961 | 2.284247 | 0.536757 | 100,000 |
| 100% | 5.659165 | 5.496063 | 5.529311 | 1,000,000 |

Both scan engines examined 1,000,000 rows in every case. All measurements
reported 40 bytes and two allocations per query; these figures do not include
loading or total dataset memory.

For the 0.1% range, this run showed approximately **580x less query time than
the row scan** and **352x less than the columnar scan**, with identical aggregate
results. The mechanism is fewer candidate visits, not an inherently faster
way to scan every row. At 100% selectivity, indexing offered no meaningful
advantage. Do not generalize the best-case ratio to arbitrary queries.

## Correctness and limitations

Tests compare indexed output with the reference engine across hand-calculated
fixtures and 300 seeded generated ranges. Cases include duplicated boundary
timestamps, empty ranges, empty datasets, absent services, the maximum integer
timestamp, cancellation, invalid ordering, and concurrent read-only queries.
Comparison counts are checked against a logarithmic upper bound.

These measurements belong to indexed-query milestone `d1453df`. The subsequently
added [local explorer](local-explorer.md) uses this engine, but browser/API work
is not included in these measurements. There is still no persistent index,
mutation protocol, sharding, or ten-million-event result. Single CLI timings can
be below clock resolution; unresolved readings remain null with an explanatory note.
