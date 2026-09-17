# Remaining-work boundary

This is a scoped completion roadmap, not an estimate of equal engineering effort.
On 2026-09-15, the requested first half is **three of six remaining milestones**.
Only milestones 7-9 belonged to that work session. Milestone 10 was subsequently
implemented and measured on 2026-09-16. Milestone 11 adds local-preview packaging
and isolated consumer smoke automation; milestone 12 still requires owner decisions.
Publication requires approval.

The final local engineering boundary is defined in the
[candidate release notes](release-notes.md). License/distribution approval is an
owner handoff, not an invitation to add authentication, live ingestion, or
distributed features to the local-first product.

| Milestone | Scope | Boundary |
|---|---|---|
| 7 | Versioned, bounded, checksummed immutable snapshot codec and safe JSONL conversion | Completed 2026-09-15; `a83d4e9` |
| 8 | Snapshot input in query/catalog/server while keeping existing JSONL behavior | Completed 2026-09-15; `2135237` |
| 9 | Benchmark input-format support and measured JSONL-versus-snapshot startup evidence | Completed 2026-09-15; [results](../benchmarks/snapshot-startup.md) |
| 10 | Concurrent-load, backpressure/cancellation, and end-to-end UI latency experiments | Completed and published 2026-09-16; `5e9d541`; [one-million-event results](../benchmarks/concurrency-latency.md) |
| 11 | Reproducible release packages and fresh-machine installation/release automation | Completed and published 2026-09-16 through `3e4a347`; [unsigned local-preview guide](releases.md); prior Windows, Linux, and explorer CI passed |
| 12 | License owner decision, authentication design, and public-deployment readiness review | Deferred; no public exposure or automatic license selection |
| 13 | Interactive exact-profile and explicit-preset performance | Completed locally as four tested tasks; [matched evidence](../benchmarks/interactive-performance.md); push/release approval outstanding |
| 14 | Final local candidate scope, artifact verification, and retained acceptance evidence | Completed locally 2026-09-17; [unsigned `0.14.0-preview.1` evidence](release-candidate.md); Windows native/browser passed, Linux integrity/header verified only; no public release or license selection |
| 15 | Final presentation/showcase of the verified local product | Engineering complete locally 2026-09-17 in three tasks: [verified real recording](demo.md), [architecture/docs](architecture.md), [validated handoff](final-handoff.md); publication awaits project license selection and owner approval |

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

Each milestone is reviewed, tested, and committed independently, then published
after approval.
The working schedule retains a ten-minute gap between milestones. Historical
benchmark reports remain intact; new results have their own provenance.
The actual ten-minute gap before milestone 11 completed at
2026-09-16 04:08:40 UTC.
The actual ten-minute gap before milestone 15 completed at
2026-09-17 07:33:30.352 UTC. Its three tasks are demo, documentation, and handoff;
there is no artificial waiting interval between those task commits.

The owner requests proper attribution for third-party components used in the
implementation. [Third-party notices](../THIRD_PARTY_NOTICES.md) record verified
dependency credits without selecting a project license. Local completion is not
legal clearance or publication approval.
