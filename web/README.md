# ChronoLens workbench

React + TypeScript + Vite. All charts use exact aggregates returned by the local
Go API; the browser never generates a pretend telemetry series.

## Run

```powershell
cd web
npm ci
npm run build
```

From the repository root, run the Go server with a generated dataset:

```powershell
go run ./cmd/server -input data/events.jsonl -web web/dist -listen 127.0.0.1:8080
```

Open `http://127.0.0.1:8080`. For frontend development, keep the Go server running
and run `npm run dev` inside `web`. The Vite API proxy preserves the browser Host.
The production bundle needs no CDN, fonts, external images, or remote services.
The lockfile pins package versions and integrity hashes without registry-specific
tarball URLs, so `npm ci` uses your configured npm registry rather than an
environment-specific mirror.

## Validate

```powershell
npm run typecheck
npm run test:e2e
```

The browser suite builds the production UI and a real Go server, generates 10,000
deterministic events, and hosts the application on loopback port 8090. It requires
Go on PATH (or `CHRONOLENS_GO` set to the Go executable). If Playwright reports a
missing Chromium browser, run `npx playwright install chromium` and retry.

The wrapper keeps unique dataset/binary files under `node_modules/.cache` and
cleans them on normal process termination. This deliberately avoids OS temporary
directories. Cleanup validates workspace ownership and asks the live wrapper to
stop its own child; it never kills a process using a saved PID that may have been
recycled. Do not reuse an unrelated server on port 8090. Browser artifacts
are in `test-results`, including original desktop and mobile screenshots.

Most tests use real, unmodified API responses. Fault-injection tests explicitly
intercept requests for network errors, late responses, and null timer values.
The suite also checks int64 range arithmetic, keyboard input, empty intersections,
engine equivalence, and mobile overflow.

## Measurement semantics

- Timestamp bounds stay decimal strings and use `BigInt` for range arithmetic.
  Slider percentages describe **time span**, not an assumed event-count fraction.
  The full end passes `null` so the maximum int64 timestamp remains included.
- Event duration is telemetry latency, not query latency.
- Query diagnostics exclude the separate indexed chart-profile pass.
- Comparisons are warm sequential batch means, not p95 or end-to-end benchmarks.
  Null timers are labeled “Below clock resolution”; an insufficient comparison
  window is labeled explicitly rather than displayed as a false zero.
- Query changes abort in-flight requests and invalidate previous comparisons.
  Empty results, request failures, and loading are separate states.
