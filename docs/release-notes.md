# Local candidate notes — 0.14.0-preview.1

This candidate closes the **local-first product-engineering scope**, not a public
v1.0 release. It is an unsigned evaluation preview; the project license remains
**unselected**. No publication, authentication, or public deployment is implied.
The owner reports using copied open-source code; its actual origins, file
mappings, and terms remain unresolved. [Third-party notices](../THIRD_PARTY_NOTICES.md)
credit verified dependencies without claiming complete source clearance.
**Distribution remains blocked**, independently of local engineering acceptance.

## Included

- Deterministic uniform/incident telemetry generation, validated JSONL, and
  bounded, versioned, checksummed immutable snapshots with safe conversion.
- Exact row, columnar, and indexed queries with common semantics; query,
  benchmark, load, generator, pack, and loopback-only server CLIs.
- A built React explorer with exact charts, filters, engine comparison,
  cancellation, and explicit overload/backpressure behavior.
- Latest interactive work (`1543a3b`, `70bb062`, `d295d78`, `b494184`):
  adaptive exact-profile accumulation and direct histogram classification;
  immediate explicit actions while continuous slider changes coalesce for
  100 ms; independent exact-reference tests and matched measurement evidence.
- Windows amd64 and Linux amd64 preview packaging with payload hashes,
  source/toolchain provenance, dependency notices, deterministic ZIP metadata,
  and isolated consumer smoke automation. See [packaging instructions](releases.md).

## Measured improvements, not guarantees

The [matched interactive experiment](../benchmarks/interactive-performance.md)
reported one-million-row, 16-service full-range exact-profile batch means of
**53.79 → 17.00 ms** (five Go batches per version) and Chromium click medians of
**194.9 → 60.5 ms** (20 full-range individual samples per version). These are
different measurement populations on one shared Windows host. Browser completion
means visible DOM plus two animation frames, not physical display presentation.
The aggregate-only query implementation is unchanged; its mean increased in
those browser runs, so this is not a query-only speedup.

Unknown-service calls add one 32-byte allocation in the measured compiler build;
high-cardinality windows still incur exact grouping/sorting work. Snapshot
startup and concurrent-load results retain their own earlier provenance:
[startup](../benchmarks/snapshot-startup.md),
[concurrency](../benchmarks/concurrency-latency.md). No portable latency SLO,
cold-disk guarantee, or new concurrency claim is made.

## Completion definition and owner handoff

Local completion means documented behavior and limits, tested code, reproducible
same-host candidate archives, verified Windows/Linux payload integrity, and
native Windows packaged CLI/browser execution with truthful platform coverage.
Native Linux execution and hosted CI must be reported separately, never inferred
from successful cross-compilation or older CI.
The [candidate acceptance record](release-candidate.md) documents the completed
local checks and the remaining platform/approval boundaries.

The finished scope is a local, read-only analysis tool loading one immutable
dataset at startup. It is **not** live ingestion, a mutable database/WAL,
distributed storage, an authenticated service, or public deployment.

Before any distribution beyond local evaluation, the owner must:

1. Resolve copied-source provenance and preserve required upstream notices,
   then select/approve compatible project terms for material the owner can
   license; dependency notices alone are not a ChronoLens redistribution grant.
2. Approve pushing the local commits and evaluate current native hosted CI
   (including Linux/race results); historical CI does not validate this candidate.
3. Decide signing/distribution policy and explicitly authorize any tag or release.
   Authentication/public-exposure review is required only if that separate scope
   is later requested; the current server remains numeric-loopback only.

No future feature expansion is required to call this local engineering scope
complete. The [real incident demo](demo.md) and
[architecture guide](architecture.md) complete the separate presentation work;
they do not confer a license or turn the candidate into a public release.
