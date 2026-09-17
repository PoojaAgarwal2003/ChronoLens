# Working on ChronoLens

This is a local-first evaluation project, not a cleared public contribution
program. No project license is selected. Read
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md): the owner-reported copied-source
inventory is unresolved. Do not submit material you cannot identify and license,
remove upstream notices, or infer project rights from dependency licenses.

## Setup and focused changes

Use the [README setup](README.md#run-locally) with Go 1.27+, Node 22.17+ in the
Node 22 line, and npm. No API keys are needed. Keep the server on numeric
loopback. Preserve existing generated outputs; CLI overwrite refusal is
intentional. Do not commit datasets, executables, credentials, or local bundles.

Make a small change, add/adjust the existing Go or Playwright tests for changed
behavior, and format only touched Go files with `gofmt -w <file>`.
Use targeted tests while iterating, then run:

```powershell
go test -count=1 ./...
go vet ./...
go build ./...
Set-Location web
npm run test:e2e
Set-Location ..
```

The browser command builds/typechecks the UI and runs real-server Chromium
checks for JSONL and snapshot input. Run `npm ci` when setting up dependencies;
if Chromium is absent, install it with `npx playwright install chromium`.
The [developer guide](docs/local-explorer.md#development-and-tests) covers Linux
browser dependencies. Never mark native Linux/race/hosted CI as passed from a
Windows build or an old CI run.

Setup was rechecked on 2026-09-17 using a new local clone of `c3bcee2` in a
PowerShell path containing spaces, `npm ci`, the frontend build, and a real
100,000-event Go server. Chromium loaded the UI and compared all three engines
without page errors. The first offline restore lacked a cached Vite archive;
ordinary `npm ci` succeeded. This is a fresh checkout, not a newly provisioned OS.

## Evidence and review

- Preserve exact-result equivalence, int64 timestamp handling, cancellation,
  bounded admission, and immutable catalog behavior.
- Link benchmark changes to real raw samples, workload/host/tool versions,
  source fingerprints, and precise clock boundaries. Retain old measurements.
- Use [demo capture](docs/demo.md) only when refreshing presentation artifacts;
  it takes about a minute plus build time and is not a latency benchmark.
- Update the relevant command/API/architecture docs. Check relative links and
  visually inspect screenshots; a saved file alone does not validate a capture.
- Map copied/adapted code to actual upstream URLs/revisions/files and required
  notices before any publication review. Unknown origins stay explicitly unknown.

Commits and local checks do not authorize pushing, tagging, workflow dispatch,
or releasing. The owner separately approves source provenance/licensing,
publication, current CI review, and any signed/distributed release.
