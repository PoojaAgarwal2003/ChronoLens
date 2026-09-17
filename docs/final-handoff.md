# Final local handoff — 2026-09-17

**Local product engineering and showcase are complete. Publication is blocked.**
The project has no selected license and its candidate is unsigned.
[THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) credits verified dependencies
without granting project rights. The owner's request concerns proper upstream
attribution, not a claim of personally copying unknown files. No externally
copied project-code source has been identified in the available implementation
record; this is not categorical originality or legal clearance.
No push, tag, release, or workflow dispatch was
performed in this final milestone.

## What is ready locally

| Area | Entry point / evidence |
|---|---|
| Run and understand the project | [README](../README.md), [contribution/setup guide](../CONTRIBUTING.md) |
| Six application CLIs: generator, pack, query, bench, load, server | [CLI/query design](query-engine.md), [snapshot format](snapshots.md), [load experiment](../benchmarks/concurrency-latency.md) |
| Go immutable row/columnar/indexed catalog and loopback API | [Architecture and trade-offs](architecture.md), [API and UI guide](local-explorer.md) |
| React/TypeScript/Vite UI, exact charts, time/service/status filters, engine comparison | [Real desktop](media/final-showcase/desktop.png), [mobile](media/final-showcase/mobile.png), [comparison](media/final-showcase/comparison.png) |
| Reproducible incident story | [41.88-second video](media/final-showcase/demo.webm), [capture guide](demo.md), [manifest](media/final-showcase/capture.json) |
| Measurements, not guarantees | [10M warm queries](../benchmarks/README.md), [snapshot startup](../benchmarks/snapshot-startup.md), [matched profile/UI experiment](../benchmarks/interactive-performance.md) |
| Unsigned evaluation archives | [Candidate acceptance](release-candidate.md), [machine-readable historical checks](release-candidate-validation.json), [packaging guide](releases.md) |
| Presentation material pending review | [Resume bullets, LinkedIn draft, and talking points](portfolio.md) |
| Final source/browser/setup checks | [Final validation record](final-validation.json) |

The system remains one process loading an immutable dataset, not live ingestion,
distributed storage, a mutable database, or a public service. Indexed and
columnar execution share arrays; row storage adds memory. Snapshot load
improvement is not mmap or a full-catalog startup guarantee. Exact chart work
and query work are distinct, and query/comparison requests share two execution
slots. Rejections/cancellation are explicit; neither proves a capacity SLO.

## Commit and artifact identity

- `c3bcee2`: actual recorded incident walkthrough, capture validation tests,
  desktop/mobile/comparison images, and third-party/provenance notices.
- `5b6a1e4`: measured README, architecture, fresh-checkout instructions, and
  candidate/roadmap licensing notes.
- `f2ba16c`: the third local showcase task commit, containing the handoff,
  portfolio drafts, and validation record after the checks below.

A subsequent corrective commit applies the owner's attribution clarification
without amending those three commits: the request was for proper dependency
credits, not an assertion of unknown copied files. Project license selection
and publication approvals remain outstanding.

The retained `0.14.0-preview.1` archives still identify clean source
`f8ae6577361a97ba5b1426a371788cfa9d3bf5db`; their exact sizes/hashes remain in
[candidate acceptance](release-candidate.md). They were **not rebuilt** here and
do **not** contain the later dependency inventory. Application source
(`cmd`, `internal`, `web/src`) and the dependency lockfile are unchanged from
that candidate; the new package script is capture tooling. A future rebuild at
a new HEAD changes manifest identity even if application source is unchanged.

The video was captured on a working tree based on `665d71b`, before committing
the capture tooling; its manifest truthfully records dirty status and **69
source fingerprints**. It is not represented as a clean-candidate recording.
Those fingerprints describe the original capture inputs. A later descriptive
notice correction in `web/showcase/run.mjs` is intentionally not substituted
into the historical source hashes. The manifest's attribution annotation was
corrected without changing the media, dataset hash, or measured results.
All four retained media artifacts were hash/size-verified after capture.
The WebM is **3,050,658 bytes**, 1280×900, **41.88 seconds**, with actual Chromium
metadata/decoded-frame validation. The synthetic dataset SHA256 and exact
generator settings are retained; screenshots contain incidental real timings,
not overwritten benchmark claims. All three screenshots were visually reviewed;
mobile is 390 CSS pixels wide with no horizontal page overflow.
An independent Chromium playback reached the end of the committed WebM at 4×
speed (1,047 total video frames; 18 dropped during accelerated playback); this
checks media usability, not application latency. The architecture SVG was also
rendered and visually reviewed.

## Original final verification and coverage

On Windows amd64, Go 1.27.1, Node 22.17.0, npm 10.9.2, Playwright 1.63.0,
Chromium 153.0.8010.12:

- `go test -count=1 ./...`, `go vet ./...`, and `go build ./...` passed.
- `npm run test:e2e` rebuilt/typechecked the UI and passed **46 tests**:
  23 JSONL + 23 snapshot. This includes all 42 pre-existing checks and two new
  capture-validation cases executed under each configuration.
- A fresh local clone of `c3bcee2`, in a new path containing spaces, restored
  dependencies, built the UI/server, generated 100,000 incident events, and
  passed real Chromium loading/three-engine comparison without page errors.
  This reused the host toolchains/browser, not a freshly provisioned machine.
- Capture source/artifact hashes and 169 local links across 21 Markdown files
  (including eight heading anchors) were checked with no missing targets;
  existing raw benchmark reports were untouched.
- Owned capture/test/setup servers stopped and uniquely owned workspaces were
  removed. No unrelated process was terminated.

Initial environment failures were corrected, not hidden: the fresh offline
restore lacked a cached Vite archive, so ordinary `npm ci` was used. An initial
browser run could not find `go` in an existing storage test; adding the SDK to
that command's process-local `PATH` yielded the complete passing rerun.
No global configuration or Git identity was changed.

**Current native Linux execution and hosted CI/race validation remain pending.**
The old CI run at `3e4a347` and the candidate's Linux archive/header verification
are historical, different evidence—not a successful native Linux run for these
commits. No new benchmark or production deployment was performed.

## Short publication checklist — owner actions, not done

- [ ] Select compatible project terms only for material the owner can license;
  preserve required dependency notices and include the applicable inventory in
  any later distribution. Dependency licenses do not supply project terms.
- [ ] Review portfolio drafts for factual accuracy and personal contributions.
- [ ] Explicitly approve pushing the local commits, then review current hosted
  native Windows/Linux and race results.
- [ ] Separately approve distribution/signing policy and any tag or release.

No further feature milestone is required for the local scope. Authentication
and public deployment would require a separately requested design/review, not
silently broadening this handoff.
