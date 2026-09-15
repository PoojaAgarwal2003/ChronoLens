# Remaining-work boundary

This is a scoped completion roadmap, not an estimate of equal engineering effort.
On 2026-09-15, the requested first half is **three of six remaining milestones**.
Only milestones 7-9 belong to that work session. Milestones 10-12 stay deferred.

| Milestone | Scope | Boundary |
|---|---|---|
| 7 | Versioned, bounded, checksummed immutable snapshot codec and safe JSONL conversion | Today |
| 8 | Snapshot input in query/catalog/server while keeping existing JSONL behavior | Today, after milestone 7 |
| 9 | Benchmark input-format support and measured JSONL-versus-snapshot startup evidence | Today, after milestone 8 |
| 10 | Concurrent-load, backpressure/cancellation, and end-to-end UI latency experiments | Deferred |
| 11 | Reproducible release packages and fresh-machine installation/release automation | Deferred |
| 12 | License owner decision, authentication design, and public-deployment readiness review | Deferred; no public exposure or automatic license selection |

The storage work targets the measured 42-47 second JSONL startup bottleneck.
It is not live ingestion, a mutable database, a WAL, a distributed system, or
memory-mapped query execution. Query semantics and the loopback-only safety
boundary remain unchanged.

Each milestone is reviewed, tested, committed, and published independently.
The working schedule retains a ten-minute gap between milestones. Historical
benchmark reports remain intact; new results have their own provenance.
