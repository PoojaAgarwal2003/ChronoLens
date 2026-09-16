# Remaining-work boundary

This is a scoped completion roadmap, not an estimate of equal engineering effort.
On 2026-09-15, the requested first half is **three of six remaining milestones**.
Only milestones 7-9 belonged to that work session. Milestone 10 was subsequently
implemented and measured on 2026-09-16. The next work session covers milestones
10-11; milestone 12 still requires owner decisions. Publication requires approval.

| Milestone | Scope | Boundary |
|---|---|---|
| 7 | Versioned, bounded, checksummed immutable snapshot codec and safe JSONL conversion | Completed 2026-09-15; `a83d4e9` |
| 8 | Snapshot input in query/catalog/server while keeping existing JSONL behavior | Completed 2026-09-15; `2135237` |
| 9 | Benchmark input-format support and measured JSONL-versus-snapshot startup evidence | Completed 2026-09-15; [results](../benchmarks/snapshot-startup.md) |
| 10 | Concurrent-load, backpressure/cancellation, and end-to-end UI latency experiments | Completed locally 2026-09-16; [one-million-event results](../benchmarks/concurrency-latency.md); push awaiting approval |
| 11 | Reproducible release packages and fresh-machine installation/release automation | Next authorized milestone for 2026-09-16 |
| 12 | License owner decision, authentication design, and public-deployment readiness review | Deferred; no public exposure or automatic license selection |

The storage work targets the measured 42-47 second JSONL startup bottleneck.
It is not live ingestion, a mutable database, a WAL, a distributed system, or
memory-mapped query execution. Query semantics and the loopback-only safety
boundary remain unchanged.

Each milestone is reviewed, tested, and committed independently, then published
after approval.
The working schedule retains a ten-minute gap between milestones. Historical
benchmark reports remain intact; new results have their own provenance.
