# Development history and project scope

**The local explorer is complete.** ChronoLens includes deterministic telemetry,
strict ingestion, row/columnar/indexed execution, exact charts, immutable
snapshots, performance experiments, and reproducible preview packaging.
No further feature milestone is required for the documented local workflow.

This table preserves the later development milestones and their evidence.
Licensing and binary distribution are separate decisions. Authentication and
public hosting were considered during planning but are outside the completed
local-first scope; they are not promised follow-up milestones.

| Milestone | Scope | Boundary |
|---|---|---|
| 7 | Versioned, bounded, checksummed immutable snapshot codec and safe JSONL conversion | Completed 2026-09-15; `a83d4e9` |
| 8 | Snapshot input in query/catalog/server while keeping existing JSONL behavior | Completed 2026-09-15; `2135237` |
| 9 | Benchmark input-format support and measured JSONL-versus-snapshot startup evidence | Completed 2026-09-15; [results](../benchmarks/snapshot-startup.md) |
| 10 | Concurrent-load, backpressure/cancellation, and end-to-end UI latency experiments | Completed and published 2026-09-16; `5e9d541`; [one-million-event results](../benchmarks/concurrency-latency.md) |
| 11 | Reproducible release packages and fresh-machine installation/release automation | Completed and published 2026-09-16 through `3e4a347`; [unsigned local-preview guide](releases.md); prior Windows, Linux, and explorer CI passed |
| 12 | License/distribution decisions and possible public-deployment review | Project license not granted; authentication/public hosting outside the local scope |
| 13 | Interactive exact-profile and explicit-preset performance | Completed as four tested tasks; [matched evidence](../benchmarks/interactive-performance.md) |
| 14 | Final local candidate scope, artifact verification, and retained acceptance evidence | Completed locally 2026-09-17; [unsigned `0.14.0-preview.1` evidence](release-candidate.md); Windows native/browser passed, Linux integrity/header verified only; no public release or license selection |
| 15 | Final presentation/showcase of the verified local product | Completed locally 2026-09-17: [real recording](demo.md), [architecture/docs](architecture.md), and [historical acceptance record](final-handoff.md) |

Milestone 13 tasks are: (1) exact-reference tests and profile/browser baselines,
(2) measured adaptive service accumulation and direct histogram classification,
(3) immediate explicit actions with tested slider coalescing/cancellation, and
(4) matched raw reports, reproducible chart, and honest boundary documentation.
Those four tasks did not include final candidate acceptance or public deployment.

Prior [CI run 35062923130](https://github.com/PoojaAgarwal2003/ChronoLens/actions/runs/35062923130)
passed Windows, Linux, and explorer jobs at `3e4a347`. This is not a claim that
milestone 13's new local commits have run hosted CI.

The storage work targets the measured 42-47 second JSONL startup bottleneck.
It is not live ingestion, a mutable database, a WAL, a distributed system, or
memory-mapped query execution. Query semantics and the loopback-only safety
boundary remain unchanged.

Historical benchmark reports remain intact; new results carry their own
workload and source provenance. [Third-party notices](../THIRD_PARTY_NOTICES.md)
record dependency credits. See [project licensing](../README.md#contributing-and-licensing)
for the separate project terms and [package documentation](releases.md) for
unsigned preview limitations.
