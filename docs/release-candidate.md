# Final local candidate acceptance — 0.14.0-preview.1

**Local engineering acceptance completed 2026-09-17. Not a public release.**
The project license remains unselected, binaries are unsigned, and the server
remains unauthenticated and numeric-loopback only.
[Scope and measured limitations](release-notes.md) define what is complete.
The subsequent [showcase](demo.md) is separate from this historical acceptance.
**The owner reports copied open-source code whose origins/terms remain unmapped.**
Read [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md); this candidate is not
cleared for redistribution. Its archives predate that inventory and were not
rewritten to include it.

## Candidate identity

All three archives were built from clean commit
`f8ae6577361a97ba5b1426a371788cfa9d3bf5db`, including the latest interactive
performance source and cross-target `release-smoke -verify-only` support.
Every manifest records `git_dirty: false`, 71 source fingerprints, 16 hashed
payload files, and 17 ZIP entries. Each source SHA256/size was compared with the
checkout. The generated `PROJECT-LICENSE.txt` still says
`ChronoLens project license: not selected.`

Toolchain: Go 1.27.1 windows/amd64, Node 22.17.0, npm 10.9.2, Vite 6.4.3,
TypeScript 5.9.3; packaged browser smoke used Chromium 153.0.8010.12.

These ignored files remain **local artifacts**, not downloadable public links:

| Relative artifact path (adjacent `.sha256` retained) | ZIP bytes | SHA256 |
|---|---:|---|
| `releases/chronolens-0.14.0-preview.1-windows-amd64.zip` | 22,709,813 | `83b15a78ac33d46c8b005015d37d26ad7a97edd3c4e44b4ce02bf0e85c2b917b` |
| `releases/chronolens-0.14.0-preview.1-windows-amd64-repeat.zip` | 22,709,813 | `83b15a78ac33d46c8b005015d37d26ad7a97edd3c4e44b4ce02bf0e85c2b917b` |
| `releases/chronolens-0.14.0-preview.1-linux-amd64.zip` | 22,389,309 | `446b3e8f4f5dbac8219b1109aa28ca38134dba3bc4dd0c37ba87b61f7319a0e0` |

The [machine-readable acceptance report](release-candidate-validation.json)
records exact hashes, manifest hashes, tools, commands, target-header checks,
and execution boundaries. Full verification JSON and the Windows smoke log
are also retained alongside the ignored local archives.

## Observed acceptance

- `go test -count=1 ./...`, `go vet ./...`, and `go build ./...` passed.
  Targeted release/smoke tests cover verification-only JSON for both targets,
  deliberately non-executable fixture payloads, tamper rejection, missing input,
  invalid option combinations, and no created extraction workspace.
- Each packaging run rebuilt/typechecked the frontend. No frontend code changed
  in this milestone; the previous 42-test browser suite has its own
  [performance-milestone record](../benchmarks/interactive-performance.md).
- Windows was built twice with unchanged inputs/tools; entire ZIP hashes match.
  Sidecars intentionally name different output filenames. This is same-host
  reproducibility, not cross-host/toolchain reproducibility.
- All three archives passed `release.Verify` through `-verify-only`: external
  checksum, safe bounded entries, required files, complete manifest coverage,
  and payload SHA256/size/mode. No payload was extracted or executed in that mode.
- Each of six binaries per archive passed independent in-memory ZIP-header
  checks: Windows MZ/PE signature, AMD64 machine `0x8664`, PE32+ magic `0x020b`;
  Linux ELF magic, 64-bit class, little-endian data, x86-64 machine `62`.
  Headers alone do **not** demonstrate runtime compatibility.
- Native Windows smoke completed at **07:20:12 UTC** in a unique workspace
  outside the repository. All six packaged CLIs ran with empty application
  `PATH`; deterministic generation, conversion, all three engines across both
  formats, benchmark output, HTTP/static bytes, and two load requests passed.
- Real Chromium loaded the packaged snapshot UI (1,000 rows), selected 1%
  (10 rows), compared three engines, and reported no page errors. This is a
  correctness smoke, not a new performance measurement.
- The owned server stopped; the unique extracted workspace and its dedicated
  outside-repository parent were removed. No shared process was stopped.

**Linux was cross-compiled and integrity/header-verified, not executed.**
No new OS image was provisioned. Current native Linux and race/hosted CI results
remain pending an approved push/run; the old `3e4a347` CI result is historical
and does not validate this candidate.

## Reproduce and hand off

Use the recorded source commit and installed tool versions, a clean trusted
checkout, and **new output filenames** with the commands in the JSON report.
See [release instructions](releases.md) for setup, sidecar checking, extraction,
and native smoke. Do not overwrite these retained artifacts.

The evidence documentation was committed **after** building the clean candidate.
The packager fingerprints its allowlisted application/tooling source, not these
acceptance documents; artifact manifests correctly identify `f8ae657`, not the
later documentation commit. Rebuilding at a later HEAD changes manifest bytes
even if the six application binaries are unchanged. There is no circular
“artifact contains its own acceptance hash” claim.

Remaining distribution decisions are deliberately short:

- Verified copied-source mappings/notices and owner-approved compatible project
  license/redistribution terms; see [the unresolved inventory](../THIRD_PARTY_NOTICES.md).
- Explicit approval to push, then review current native hosted CI/race results.
- Owner signing/distribution decision and separate authorization for any tag or
  GitHub Release. A public service would additionally need a separately scoped
  authentication/exposure review; it is not part of this local product.

No license was selected, no push/tag/release or CI dispatch was performed, and
no public completion is claimed. [Final showcase](demo.md) is recorded separately;
its capture tooling/package script changes do not retroactively change these
archive manifests or their source commit.
See the [final handoff](final-handoff.md) for subsequent source/browser checks
and the still-uncompleted owner publication checklist.
