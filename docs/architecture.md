# Architecture and engineering trade-offs

![Current single-machine execution model](images/architecture.svg)

## One validated, immutable dataset

The generator writes timestamp-ordered JSONL. `snapshot.Read` validates bounded
blocks, checksums, trailer totals, and semantic constraints; format selection is
explicit, never inferred from the filename.
[`Dataset.LoadFormat`](../internal/query/dataset.go) rejects the entire load on
failure. [`LoadCatalogFormat`](../internal/query/catalog.go) publishes only after
validation and construction of the comparison layouts.

The server retains event structs **and** typed column arrays. Indexed execution
shares the latter arrays and service dictionary rather than duplicating an
index. A single-layout CLI does not build the full comparison catalog.
Snapshots reduce parsing and disk bytes, but still decode into heap objects:
there is no mmap execution, on-disk query engine, WAL, or live mutation.
`-max-events` is a row-count guard, not a memory budget.

## Sortedness and dictionary IDs

[`Dataset.Query` and `timeRange`](../internal/query/query.go) implement common
half-open time bounds, service equality, and status predicates. Strictly
increasing timestamps permit binary search over the existing timestamp column.
Indexed work is index comparisons plus candidate visits, **not constant-time
querying**. Full-range and service-only filters can still scan all candidates.

[`Dataset`](../internal/query/dataset.go) stores service IDs as `uint16`, allowing
65,536 distinct services, including ID 65,535. Dictionary lookup avoids repeated
string comparisons in columnar execution; it is not a service posting-list
index. Each query owns its state, and loaded arrays are immutable.

## Exact profiles are separate work

[`Dataset.Profile`](../internal/query/profile.go) makes its own indexed pass.
It constructs exact timeline and fixed-boundary duration counts, accumulates
service summaries, sorts them, and returns at most 20 services with an explicit
truncation flag. Timeline buckets are capped at 240. Bounded response size does
not mean bounded scanning or constant-memory grouping for every workload.

The adaptive path uses a single accumulator for a service filter, a lazily
allocated dense table when dictionary size fits the candidate count, or sparse
grouping for narrow/high-cardinality windows. This trades allocation/lookup work
against candidate density; high-cardinality exact sorting remains a cost.
[Matched measurements](../benchmarks/interactive-performance.md) document the
speedups and the unknown-service allocation trade-off, rather than claiming
every query became faster.

## HTTP and browser interaction

[`Server.prepare`, `execute`, and `compare`](../internal/api/server.go) share
**two execution slots** across query and comparison requests. Saturation returns
429 with `Retry-After`, not an unbounded queue. A query request runs the selected
aggregate engine and a separate indexed chart pass sequentially within its slot.
A comparison runs warm batches for three engines, verifies equivalent
aggregates, and excludes chart profiling.

Request-derived cancellation and server deadlines bound execution; scan loops
check context periodically. This is cooperative cancellation, not instant
preemption. The server is unauthenticated, read-only, and numeric-loopback only;
origin/request guards are not a substitute for authentication.
[API contracts and limits](local-explorer.md) are the operational reference.

[`Workbench`](../web/src/main.tsx) dispatches explicit presets/filters immediately
and coalesces slider changes for 100 ms. Effect cleanup cancels timers, in-flight
requests, and stale comparisons. [`timeBounds`](../web/src/api.ts) uses BigInt
and decimal strings to preserve timestamp precision across JSON.

## Keep the clocks separate

| Metric | Includes | Does not establish |
|---|---|---|
| CLI indexed-layout load | Decode/build one selected layout | Full server catalog startup, cold-disk guarantee |
| Aggregate query time | One engine's aggregate work | Chart construction, network, UI |
| Profile time | Exact chart pass/grouping/sorting | Aggregate query work |
| Warm comparison batch mean | Repeated sequential engine execution | Individual-request p95, chart cost |
| Browser click-to-visible commit | Request, response, DOM, two animation frames | Physical screen presentation, portable SLO |

[Query methodology](query-engine.md), [snapshots](snapshots.md), and
[concurrency evidence](../benchmarks/concurrency-latency.md) retain their own
workloads and provenance. Memory-versus-disk, cold-versus-warm, and isolated-
versus-contended observations must not be mixed into a single headline.

This describes the checked-in implementation, not authorship of every component.
[Upstream attribution and copied-source review](../THIRD_PARTY_NOTICES.md) remain
separate from engineering acceptance.
