# ChronoLens

A local telemetry-analysis project exploring how storage layout and indexing
affect interactive queries over large event datasets.

**Current milestone: matching scan query engines.** A deterministic generator,
validated JSONL reader, row-scan engine, columnar-scan engine, and query CLI are
implemented. Indexing, the HTTP API, and the visual explorer are not implemented
yet. [Measured scan baselines](docs/query-engine.md#measured-baseline) describe a
100,000-event workload, not the future ten-million-event target.

## Why this exists

ChronoLens compares a straightforward event scan with column-oriented execution
over the **same data and query semantics**. Reproducible datasets, known-answer
tests, and explicit rows-examined statistics distinguish a real improvement
from a different answer or a misleading benchmark.

## Run locally

Install [Go 1.27 or later](https://go.dev/dl/) and Git. No external services,
API keys, environment variables, or third-party Go dependencies are required.

```sh
git clone https://github.com/PoojaAgarwal2003/ChronoLens.git
cd ChronoLens
go version
go run ./cmd/generator -events 100000 -seed 42 -output data/events.jsonl
```

These commands work in PowerShell and POSIX shells. The generator creates
parent directories and **refuses to overwrite an existing output file**.
Choose a new filename for each run, or deliberately remove an old dataset.
Generated files in `data/` are ignored by Git.

For JSONL on standard output:

```sh
go run ./cmd/generator -events 3 -output -
```

Status messages go to standard error, never into the JSONL stream. Prefer
`-output` for files: older PowerShell redirection can change text encoding.

## Dataset contract

Each UTF-8 line contains one JSON event, with a trailing newline:

```json
{"timestamp_us":1767225600000000,"service":"service-001","duration_us":12000,"status":200}
```

This is a schema illustration, not a promised first record for seed 42.

| Field | Meaning |
|---|---|
| `timestamp_us` | Unix timestamp in microseconds; strictly increasing |
| `service` | Synthetic service name, such as `service-001` |
| `duration_us` | Integer request duration from 100 to 500,000 microseconds |
| `status` | 200 for success or 500 for a synthetic server error |

Services and durations are sampled uniformly. Error events are independent
Bernoulli samples: a 5% probability does not guarantee exactly 5% errors in a
finite dataset. This is a baseline workload, not a model of production traffic.

The reader accepts a broader input contract: nonnegative signed 64-bit
timestamps, unsigned 32-bit durations (including zero), HTTP status codes
100-599, and nonempty service names up to 128 UTF-8 bytes. Input timestamps must
be nondecreasing; equal timestamps are allowed even though the generator emits
strictly increasing timestamps. See the [reader contract](docs/query-engine.md#input-contract).

## Generator options

```sh
go run ./cmd/generator -help
go run ./cmd/generator -events 1000000 -services 32 -seed 7 -interval 1ms -error-percent 10 -output data/million.jsonl
```

| Flag | Default | Constraint |
|---|---|---|
| `-events` | `100000` | Positive integer; resulting timestamps must fit signed 64-bit microseconds |
| `-services` | `16` | 1 through 1,024 |
| `-seed` | `42` | Signed 64-bit integer |
| `-start` | `2026-01-01T00:00:00Z` | RFC3339 time, at or after the Unix epoch, through year 9999; no sub-microsecond precision |
| `-interval` | `1ms` | Positive whole microseconds, expressed as a Go duration |
| `-error-percent` | `5` | Integer probability from 0 through 100 |
| `-output` | `data/events.jsonl` | New file path, or `-` for standard output |

The same configuration and generator version produce byte-identical output.
The generator does not read the wall clock or buffer the whole dataset.
Additional working memory is proportional to the configured service count,
plus a fixed output buffer.

Invalid arguments fail before creating a dataset. Handled write failures and
Ctrl+C cancellation remove an incomplete file created by that invocation.
Force-killing the process, power loss, or cleanup permission errors can leave
a partial file; outputs are not atomically published or guaranteed crash-durable.
Standard-output consumers must discard partial output if the command fails.

The built executable uses exit code 0 for success/help, 2 for argument errors,
and 1 for generation or I/O errors. `go run` may translate a child failure to
its own exit code.

## Query a dataset

```sh
go run ./cmd/query -input data/events.jsonl -engine row
go run ./cmd/query -input data/events.jsonl -engine columnar
go run ./cmd/query -input data/events.jsonl -engine columnar -from-us 1767225620000000 -to-us 1767225630000000 -service service-001 -status 500
go run ./cmd/query -help
```

Queries return JSON containing count, server-error count, duration sum, mean,
minimum, maximum, and scan statistics. The time range is **inclusive at
`-from-us`, exclusive at `-to-us`**; an omitted upper bound is unbounded.
Service and status filters are exact matches. An unknown service returns zero
matches, not an input error.

Each invocation loads **only the selected layout** into memory, then executes
one query. Both engines scan every row, even for narrow ranges. `load_ms` includes
file reading, validation, and layout construction; `query_ms` measures only
in-memory execution. Neither number is a repeated-run latency percentile.
If the clock does not advance during an operation, its timing is `null` with
an explanatory note, not a claim of zero-millisecond execution.

The default `-max-events 1000000` prevents accidentally loading more than one
million events; exceeding the limit is an error, never silent truncation.
Raise this explicitly for larger inputs if sufficient memory is available.
It is a row-count guard, not an exact memory limit.

Malformed input is reported with a line number and no success-shaped partial
result. Missing, duplicate, unknown, null, and incorrectly typed fields are
rejected. Empty files produce a zero-count result with null mean/min/max.

Read the [query design and benchmark methodology](docs/query-engine.md) for
the complete CLI contract, architecture, measured results, and limitations.

## Architecture and structure

```text
Seeded generator -> ordered JSONL file
                            |
                            v
                  Validated streaming reader
                            |
                            v
                  One selected memory layout
                    /                 \
                   v                   v
               Event rows         Typed columns
                    \                 /
                     v               v
                      Full-scan query
                            |
                            v
                  Aggregates + scan statistics
```

```text
cmd/generator/          CLI, output handling, and CLI tests
cmd/query/              Query CLI and JSON reporting
internal/generator/    Deterministic generation and validation tests
internal/telemetry/    Shared schema, strict JSONL reader, and fuzz tests
internal/query/        Memory layouts, aggregation, equivalence tests, benchmarks
docs/                  Query semantics and measured benchmark methodology
.github/workflows/     Go checks on Windows and Linux
go.mod                 Module and minimum Go version
```

## Development

```sh
go test -count=1 ./...
go test -cover ./...
go test ./internal/query -run '^$' -bench '^BenchmarkQuery$' -benchmem -count=3
go vet ./...
go build ./...
gofmt -w cmd internal
```

To build a standalone generator, first create a `bin` directory, then run:

```sh
go build -o bin/chronolens-generator ./cmd/generator
go build -o bin/chronolens-query ./cmd/query
```

On Windows, add `.exe` to each output filename.
There is no server or deployment configuration in this milestone.

Tests cover reproducibility, timestamp order, schema bounds, invalid arguments,
overflow, writer failures, cancellation, and preservation of existing files.
Query tests additionally cover known aggregates, engine equivalence, half-open
time ranges, dictionary limits, concurrent reads, and malformed input. The CI
workflow runs tests, vet, build, and formatting checks on Windows and Linux,
plus the query race detector on Linux.

### Troubleshooting

- **`go` is not recognized:** install Go, reopen your terminal, and run `go version`.
- **Output already exists:** choose a new `-output` path; overwrites are intentionally forbidden.
- **Permission denied or disk full:** choose a writable directory with sufficient free space.
- **Unexpected sub-microsecond precision error:** use a start time and interval aligned to microseconds.
- **Dataset exceeds max-events:** explicitly increase the query limit or choose a smaller dataset.
- **Reader error on line N:** check the exact field names, types, and timestamp order; the reader does not repair or silently skip records.

## Next milestones

1. Indexed time-range queries and selectivity experiments against these scan baselines.
2. Local API and interactive timeline explorer.

Ten million events and sub-100 ms selective queries are **future experimental
targets**, not demonstrated capabilities of the current repository.

## Contributing and licensing

Keep changes focused, include tests for behavior changes, and run the development
checks above. Never commit generated datasets, credentials, or benchmark claims
without the corresponding workload and measurement method.

A license has not been selected yet. Public source availability alone does not
grant an open-source license.
