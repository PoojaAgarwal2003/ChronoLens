# Portfolio material — draft, not approved for posting

**Pending source-provenance review and owner verification of personal
contributions.** The owner reports using copied open-source code whose origins
and terms remain unmapped. Before using these drafts, complete
[THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md), retain upstream credits, and
replace/remove any wording that does not describe work you personally performed.
Project measurements do not establish authorship of the underlying code.

ChronoLens is a local evaluation project, **not a Microsoft product**. Use of
open-source components or tooling does not imply upstream/employer endorsement.
There is no selected project license or approved public release.

## Resume bullet drafts

Use these only for verified personal contributions; the linked evidence describes
the repository's results, not a claim that one person wrote every component.

- Evaluated row, columnar, and indexed telemetry queries with exact aggregate
  equivalence and explicit work counters; a historical 10M-event, 0.1%-window
  experiment measured **42.60 ms row vs 0.0644 ms indexed median warm batch means**,
  not request latency ([method and raw evidence](../benchmarks/README.md)).
- Investigated exact chart-profile and UI dispatch costs on 1M events; matched
  project measurements changed **53.79 → 17.00 ms profile batch means** and
  **194.9 → 60.5 ms Chromium click medians**, with separate clock boundaries and
  workload/allocation caveats ([evidence](../benchmarks/interactive-performance.md)).
- Validated a local Go/React explorer with deterministic incident data,
  real-server browser tests, and a reproducible **41.88-second recorded demo**;
  documented unsigned candidate limits and unresolved upstream provenance
  ([demo](demo.md), [handoff](final-handoff.md)).

## LinkedIn draft

> I've been exploring a practical telemetry question with ChronoLens: when an
> incident becomes visible, how much work did the query and charts actually do?
>
> The local Go/React explorer compares row, columnar, and indexed execution over
> the same immutable data. A recorded walkthrough follows a generated traffic
> spike, narrows its time window, filters one service's failures, and checks
> equivalent results across all three engines.
>
> The most useful lesson was to keep the clocks separate. In one matched
> million-event experiment, exact-profile batch means changed from 53.79 to
> 17.00 ms, while Chromium click medians changed from 194.9 to 60.5 ms. Those are
> different measurements, not portable performance guarantees.
>
> The project uses open-source components. Upstream credits and the provenance
> of copied code are still under review; no project license or public release
> is approved. I'm treating attribution and reproducible evidence as part of
> the engineering work—not an afterthought.

Attach/link the demo only after the publication review, not as a claim of
production adoption, original invention, or upstream endorsement.

## Technical talking points

- **Why immutable?** Query readers share loaded columns without a mutation
  protocol. The trade-off is startup loading and no incremental ingestion.
- **Why binary search?** Strict timestamp ordering makes time bounds cheap;
  broad windows and service-only predicates can still scan the whole dataset.
- **Why adaptive grouping?** Dense tables help broad, low-cardinality windows;
  sparse grouping avoids allocating the whole dictionary for tiny windows.
- **Why separate profiling?** A fast aggregate does not make an exact chart free.
  Candidate scans, grouping/sorting, network work, and rendering remain distinct.
- **Why two slots?** Bound local execution and report overload explicitly rather
  than accumulating an unbounded queue. Cooperative cancellation is not instant
  preemption.
- **Why not call this production-ready?** It is unauthenticated and loopback-only;
  current native Linux/hosted CI, licensing, and distribution approval remain
  separate from local Windows acceptance.

## Demonstration and claims to avoid

Follow [the repeatable demo guide](demo.md), or run the app locally and select
47–53% of its incident dataset's time span, `service-001`, then HTTP 500.
Explain the synthetic signal before showing warm engine comparisons.

Do not claim real customer incidents, business savings, production scale,
all-original implementation, an open-source project license, cold-disk
guarantees, sub-millisecond end-to-end UI, or current Linux execution.
Use [architecture details](architecture.md) and the original benchmark reports
to answer follow-up questions instead of extrapolating from one headline.
