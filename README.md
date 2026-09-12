# ChronoLens

A local telemetry-analysis project exploring how storage layout and indexing
affect interactive queries over large event datasets.

**Current milestone: reproducible dataset generation.** The Go generator and
its tests are implemented. The query engine, HTTP API, and visual explorer are
not implemented yet. No query-performance results are claimed.

## Why this exists

ChronoLens will compare a straightforward event scan with more efficient query
execution over the **same data and query semantics**. Before comparing query
engines, we need datasets that are reproducible, timestamp-ordered, and easy to
inspect. This first milestone provides that foundation.

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

## Architecture and structure

```text
CLI flags
    |
    v
Validated configuration
    |
    v
Seeded generator -> typed telemetry events -> JSON encoder
                                                  |
                                                  v
                                      Buffered JSONL file / stdout
```

```text
cmd/generator/          CLI, output handling, and CLI tests
internal/generator/    Deterministic generation and validation tests
internal/telemetry/    Shared event schema
.github/workflows/     Go checks on Windows and Linux
go.mod                 Module and minimum Go version
```

## Development

```sh
go test -count=1 ./...
go test -cover ./...
go vet ./...
go build ./...
gofmt -w cmd internal
```

To build a standalone generator, first create a `bin` directory, then run:

```sh
go build -o bin/chronolens-generator ./cmd/generator
```

On Windows, use `bin/chronolens-generator.exe` as the output path.
There is no server or deployment configuration in this milestone.

Tests cover reproducibility, timestamp order, schema bounds, invalid arguments,
overflow, writer failures, cancellation, and preservation of existing files.
The CI workflow runs tests, vet, build, and formatting checks on Windows and Linux.

### Troubleshooting

- **`go` is not recognized:** install Go, reopen your terminal, and run `go version`.
- **Output already exists:** choose a new `-output` path; overwrites are intentionally forbidden.
- **Permission denied or disk full:** choose a writable directory with sufficient free space.
- **Unexpected sub-microsecond precision error:** use a start time and interval aligned to microseconds.

## Next milestones

1. Reference and columnar query engines with matching-result tests.
2. Indexed time-range queries and reproducible performance experiments.
3. Local API and interactive timeline explorer.

Ten million events and sub-100 ms selective queries are **future experimental
targets**, not demonstrated capabilities of the current repository.

## Contributing and licensing

Keep changes focused, include tests for behavior changes, and run the development
checks above. Never commit generated datasets, credentials, or benchmark claims
without the corresponding workload and measurement method.

A license has not been selected yet. Public source availability alone does not
grant an open-source license.
