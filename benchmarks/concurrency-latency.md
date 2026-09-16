# Concurrent load and actual browser latency

Measured 2026-09-16 on **one million deterministic events**, using a snapshot
loaded into the unchanged three-engine catalog. Client, server, and headless
Chromium ran on the **same shared Windows host**. These are bounded experiments,
not capacity certification, production SLOs, or portable performance targets.

The key distinction: an indexed full-range aggregate took a browser-observed
median **5.06 ms of server query work**, while the matching complete UI result
took **194.50 ms from a preset click**. The latter includes the application's
100 ms debounce, chart profiling, HTTP, React work, and a browser paint
opportunity. They are not interchangeable measurements.

## Controlled HTTP phases

All phases used the same full range `[1767225600000000, dataset end]`, all 16
services/statuses, indexed selection and 100 chart buckets. `/api/query` does
aggregate **and profile**; `/api/compare` runs three sequential aggregate
measurement windows (at least 50 ms each where the iteration cap permits), not
the chart/UI path. The phases ran sequentially, in the order below.

| Phase | Arrivals/s | Client cap | Scheduled / sent / skipped | Success / 429 / client timeout | Successful complete p50 / p95 / p99 (ms) |
|---|---:|---:|---|---|---|
| Query baseline | 2 | 2 | 10 / 10 / 0 | 10 / 0 / 0 | 60.10 / 62.19 / 62.19 |
| Compare baseline | 2 | 8 | 10 / 10 / 0 | 10 / 0 / 0 | 159.34 / 164.55 / 164.55 |
| Compare overload | 40 | 8 | 200 / 200 / 0 | 58 / 142 / 0 | 158.33 / 165.10 / 166.31 |
| Compare local cap | 40 | 1 | 200 / 29 / 171 | 29 / 0 / 0 | 159.58 / 162.72 / 163.12 |
| Compare cancellation | 10 | 2 | 50 / 50 / 0 | 0 / 0 / 50 | unavailable: no completed success |
| Compare recovery | 2 | 2 | 10 / 10 / 0 | 10 / 0 / 0 | 160.52 / 164.89 / 164.89 |

Each arrival window was 5 seconds, with the first arrival at zero and no arrival
at the exclusive endpoint. Actual elapsed, **including drain**, was respectively
4.559, 4.659, 5.084, 5.060, 4.921, and 4.664 seconds. A low-rate run can finish
before the end of the configured window because the last scheduled arrival was
at 4.5 seconds; it does not sleep an artificial trailing interval.

The cancellation phase used a **20 ms client deadline**; other phases used one
second. All 50 cancellations were client timeouts, not successful fast replies.
All 10 following comparison requests succeeded. Client disconnect timing alone
does not establish exactly where the server stopped each operation.

The overload phase returned **142 explicit 429s (71%)**, all with `Retry-After: 1`.
Their fast rejection times are retained in raw samples, **excluded from success
percentiles**. The single-client-slot phase skipped 171 arrivals locally instead
of quietly turning the workload into a closed-loop test. No scheduler-late skips,
transport errors, server timeouts, or other HTTP errors occurred in these runs.
The overload scheduler-lag p95 was 0.795 ms (all observed arrivals, not HTTP time).
These observations support working backpressure, not a claim of limitless
throughput or guaranteed tail latency.

### Deterministic cancellation and admission evidence

`TestAcceptedCancellationSharedCapacityAndRecovery` starts a query and a
comparison, gates each at the context-deadline boundary **after admission**,
then checks both routes return 429 with `Retry-After: 1` while health returns
200. It cancels those accepted requests, releases the gates, checks 408 and zero
remaining permits, then verifies both routes succeed again. Channels establish
the ordering; no dataset-speed assumption, sleep-only synchronization, or
production test hook is used. This exercises the existing shared two-slot
policy rather than replacing it.

The load package tests also cover schedule boundaries, request caps, local
admission decisions, nearest-rank arithmetic, cancellation accounting, timeout,
transport/body errors, invalid responses, redirects, and prohibited targets.
The normal Go runner executes these tests. Linux CI includes the new packages
in its existing race-detector invocation.

## Real Chromium results

Forty recorded **individual clicks**, twenty per range, followed four unrecorded
warmup clicks and the initial full-range page load. The actual browser clicked
alternating 10% and 100% presets, using indexed execution, no filters and 100
buckets. Expected counts were checked against 100,000 and 1,000,000. No API
responses were mocked; no HTTP load ran concurrently with this phase.

| Boundary | 10% range p50 / p95 / p99 (ms) | 100% range p50 / p95 / p99 (ms) |
|---|---|---|
| Click → visible-commit proxy | 144.10 / 145.70 / 145.80 | 194.50 / 196.10 / 196.80 |
| Fetch dispatch → visible-commit proxy | 33.20 / 34.40 / 34.40 | 83.30 / 85.20 / 85.60 |
| HTTP requestStart → responseEnd | 6.70 / 7.90 / 8.20 | 59.20 / 61.30 / 62.40 |
| Server combined work | 5.65 / 6.78 / 7.21 | 57.59 / 60.06 / 61.28 |
| Server aggregate only | 0.55 / 1.12 / 1.52 | 5.06 / 6.33 / 8.09 |

**Exact boundary:** start is the browser's capture-phase preset click event, not
the Playwright process's clock or the start of its auto-wait. A MutationObserver
detects the new matching count, `aria-busy=false`, a timeline SVG, and the
visible matching-count element wholly inside the 1440×1080 viewport.
The end is the second subsequent
`requestAnimationFrame` callback. This gives the committed result a paint
opportunity; **it is not a measured physical display presentation timestamp**,
especially in headless Chromium. Raw DOM-commit timing is also retained.

Fetch dispatch is captured immediately before the native `fetch` call.
ResourceTiming supplies `requestStart` and `responseEnd`; the raw report also
retains fetch-resource duration, which includes earlier browser scheduling.
HTTP includes the complete response, not just first byte. Server query,
profile, and combined-work timers come from that same response and exclude
JSON serialization, network transfer and rendering. Instrumentation itself has
some overhead. Do not subtract independently computed percentile columns to
infer a component's percentile.

Every displayed percentile uses **nearest rank** over the individual samples:
sort ascending, take index `ceil(p*N)-1`. With N=20, p99 is simply the maximum
observation; with N=10, p95 and p99 are both the maximum. These small samples do
not estimate a stable population tail. There are no machine-specific latency
assertions in the test.

## Raw evidence and environment

- [Query baseline](results/windows-concurrency-20260916-query-baseline.json)
- [Compare baseline](results/windows-concurrency-20260916-compare-baseline.json)
- [Compare overload](results/windows-concurrency-20260916-compare-overload.json)
- [Client-cap skips](results/windows-concurrency-20260916-compare-local-cap.json)
- [Client cancellations](results/windows-concurrency-20260916-compare-cancel.json)
- [Recovery](results/windows-concurrency-20260916-compare-recovery.json)
- [Browser: all forty samples, requests, ranges and paired server timers](results/windows-concurrency-20260916-browser.json)
- [Environment, dataset fingerprints and executed commands](results/windows-concurrency-20260916-environment.json)

Intel Xeon Platinum 8370C at 2.80 GHz, 16 logical processors, about 63.95 GiB RAM;
Go 1.27.1 windows/amd64 and GOMAXPROCS=16; Node 22.17.0. The browser version,
viewport, snapshot format, rows and dataset bounds are in the raw browser report.
The seed-42 uniform generator used 16 services and 1 ms event spacing from
2026-01-01. JSONL SHA-256:
`b0c448c1734c27c841a5cf90343728b3017c5d2c613156c7e97d9c34eca785fa`.
Snapshot SHA-256:
`816c1d5c3410977c081001b293115a81cc2720cfd078d3de2734d1d892a4e760`.

The filesystem was not cold; packing had just read the source. There were no
project test suites running during these phases, but the shared host is not
isolated from other activity. No format speedup, cold-start result, user-device
latency, sustained capacity, or statistically stable p95 is inferred here.

## Reproduce

With Go on PATH and the existing frontend dependencies/Chromium installed:

```powershell
cd web
npm run experiment:latency
```

This **opt-in** script builds the frontend and Go tools, generates exactly one
million events, packs a snapshot, starts its own server on an OS-assigned
loopback port, runs the six load phases then Chromium, and writes new reports
under `benchmarks/results`. It removes only its unique ignored workspace and
stops only its own child server. It does not run in `npm run test:e2e`.
`CHRONOLENS_GO` can select a Go executable.
`CHRONOLENS_EXPERIMENT_FORMAT=jsonl` selects the compatible JSONL server path;
the published experiment used the default `snapshot`.
`CHRONOLENS_EXPERIMENT_PREFIX` chooses a new alphanumeric/hyphen filename prefix;
the default is timestamped. Existing reports are never overwritten.

For a small standalone test of an already running local server, from the root:

```powershell
go run ./cmd/load -target http://127.0.0.1:8080/api/query -duration 5s -rate 2 -inflight 2
go run ./cmd/load -target http://127.0.0.1:8080/api/compare -duration 5s -rate 40 -inflight 8 -output benchmarks/results/my-new-overload.json
```

`-payload` accepts a JSON object up to 4096 bytes for stable custom filters.
Default payload is indexed/all time/all services/all statuses/100 buckets.
Only `http://numeric-loopback:explicit-port/api/query` or `/api/compare` is
accepted; hostnames, non-loopback IPs, credentials, URL query/fragment, encoded
paths and other schemes/routes are rejected. Proxies are disabled, numeric
loopback is rechecked at dial, and redirects are recorded but never followed.

The dependency-free load runner bounds duration (1 ms–1 minute), rate
(1–1000/s), inflight (1–64), requests/raw samples (1–10,000), payload (4096 bytes),
headers (16 KiB), response (1 MiB) and per-request timeout (1 ms–10 seconds).
Report memory is bounded by these caps; non-success response bodies are retained
up to 4096 bytes with an explicit truncation marker. No large datasets or
binaries belong in Git.

The scheduler uses fixed monotonic due times, never completion-driven arrivals.
An arrival at least one period late is skipped, not replayed in a catch-up burst.
Every planned arrival has a sample, including inflight, late and cancelled
skips; `scheduled = sent + skipped`. `sent` means a dispatched client attempt,
not proof of server admission. Normal completion drains attempts; parent
cancellation aborts them and records the unattempted remainder. A separate
bounded `/api/meta` probe is excluded from workload accounting and elapsed.

Complete client latency ends after reading/closing the body and validating
success JSON. Success, 429 rejection, server timeout (408/504), client timeout,
cancellation, other HTTP errors, invalid response and transport errors remain
separate, with raw HTTP status preserved even on body-read failure. No workload
request retries occur. Successful experiment execution exits zero even when
HTTP failures were observed; **inspect outcomes**, not just the exit code.
Configuration errors exit 2; interrupted runs and report-write failures exit 1.
An interrupted run still writes its partial report where possible.

## Remaining limits

Validation on this host passed `go test ./...`, `go vet ./...`, `go build ./...`,
ten repeated runs of the load/API tests, frontend typechecking, and all **34**
existing JSONL/snapshot browser/tool test combinations. The opt-in latency test
also passed against the generated million-event server. Local `go test -race`
could not run because this Go environment has CGO disabled; no compiler or
dependencies were installed to change the host. Linux CI is configured to run
that check, not claimed as observed local evidence.

Longer independently repeated runs, larger datasets, multiple browser/device
types, browser interaction under concurrent API load, scheduler-stress runs,
and isolated-host measurements remain future experiments. This milestone does
not tune admission capacity or debounce, change query/storage semantics, add
authentication, or justify public deployment.
