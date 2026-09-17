# A real, repeatable incident walkthrough

[Watch the 30–90 second WebM](media/final-showcase/demo.webm) ·
[desktop](media/final-showcase/desktop.png) · [mobile](media/final-showcase/mobile.png) ·
[engine comparison](media/final-showcase/comparison.png) · [capture manifest](media/final-showcase/capture.json)

Download/open the WebM in a Chromium-compatible browser if your repository viewer
does not play it inline. This is a paced recording of the running application,
not a benchmark, animation, or mocked API. There is no audio.

## Story

1. Start with 100,000 deterministic incident-profile events, 16 services, seed 42.
2. See the real timeline spike and corresponding failures/long durations.
3. Select 47–53% of the **time span**, surrounding the five-second incident.
   This includes boundary events; it is not an exact 20,000-event incident query.
4. Filter `service-001` and HTTP 500 to isolate the concentration of failures.
5. Inspect separate query/chart costs, then compare identical aggregates across
   row, columnar, and indexed engines. Warm batch means are not request p95.

The signal suggests where a synthetic service investigation should begin; it
does not establish causality, business losses, or a real production incident.
[Workload semantics](workloads.md) document the actual generator.

## Reproduce (optional; not part of normal tests)

From the repository root in PowerShell, with the [setup prerequisites](../README.md#run-locally):

```powershell
Set-Location web
npm run demo:capture -- my-new-capture
Set-Location ..
```

The command builds/typechecks the frontend, builds the real Go server, generates
a fresh dataset in an ignored unique `web\node_modules\.cache` workspace, and
listens on an OS-assigned numeric-loopback port. It requires the already installed
Playwright Chromium (`npx playwright install chromium` only if missing).
`CHRONOLENS_GO` may name an explicit Go executable; paths containing spaces work.

Output goes only to `docs\media\<capture-name>`; names cannot contain paths.
An existing directory is an error, never overwritten or recursively cleaned.
Failed captures may retain partial evidence there: inspect it, then choose a new
name. Only the process's own server and unique ignored workspace are stopped/
removed. Successful runs finalize the recording, decode it in Chromium, verify
30–90 seconds/1280×900/under 20 MB, check real result equivalence and mobile
overflow, then write source/dataset/artifact SHA256s and actual tool versions.
Capture timings vary; hashes identify this recording, not bit-reproducible video.

## Attribution and distribution boundary

The project has no selected license. The owner reports use of open-source code,
but copied-source origins and terms remain unresolved. See
[third-party notices](../THIRD_PARTY_NOTICES.md) before sharing source, captures,
or bundles. These local showcase artifacts are not a public release or clearance
to redistribute; existing candidate archives predate the showcase.
