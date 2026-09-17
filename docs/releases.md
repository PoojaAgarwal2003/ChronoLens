# Unsigned local-preview packages

These are **local evaluation bundles, not production releases**. The project
license remains **unselected**. Dependency notices do not grant permission to
redistribute ChronoLens publicly. Authentication, public-deployment review,
signing/installer decisions, and the project license remain milestone 12 owner
decisions. Nothing here creates a GitHub Release, tag, commit, or push.
Read [THIRD_PARTY_NOTICES.md](../THIRD_PARTY_NOTICES.md) for verified dependency
credits and license scope. The retained candidate predates that inventory;
its existing dependency notices are not a project license or publication approval.

Latest local acceptance: [0.14.0-preview.1 candidate evidence](release-candidate.md)
and [scoped release notes](release-notes.md). The milestone-11 verification record
below is historical; it does not substitute for the current candidate checks.

## Consumer: extract and run, without a development toolchain

Supported targets: **Windows amd64** and **Linux amd64** (Go `GOAMD64=v1`,
`CGO_ENABLED=0`). Choose the archive for your OS. Verify its adjacent SHA256
sidecar, then extract to a **new directory**:

```powershell
# PowerShell: compare this hash with the first field in preview.zip.sha256.
Get-FileHash .\preview.zip -Algorithm SHA256
Expand-Archive .\preview.zip .\chronolens-preview
Set-Location .\chronolens-preview
.\bin\generator.exe -events 10000 -output events.jsonl
.\bin\pack.exe -input events.jsonl -output events.clens
.\bin\server.exe -input events.clens -format snapshot -web web\dist -listen 127.0.0.1:8080
```

```sh
sha256sum --check preview.zip.sha256
unzip preview.zip -d chronolens-preview
cd chronolens-preview
chmod +x bin/* # only if the extractor did not preserve Unix execute permissions
./bin/generator -events 10000 -output events.jsonl
./bin/pack -input events.jsonl -output events.clens
./bin/server -input events.clens -format snapshot -web web/dist -listen 127.0.0.1:8080
```

Open `http://127.0.0.1:8080`. Keep the terminal open; Ctrl+C stops your server.
`RUN.txt` includes query, benchmark, and load examples. All six executables accept
`-help`. Run commands from the extracted root, or supply explicit dataset and
`-web` paths. **No Go, Node, npm, source checkout, Internet connection, or absolute
build-machine path is needed by the extracted application.**

The ZIP contains only `bin/{generator,pack,query,bench,load,server}` (with `.exe`
on Windows), `web/dist` assets, `RUN.txt`, `PROJECT-LICENSE.txt`, `manifest.json`,
and `licenses/`. No dataset, source tree, `node_modules`, credentials, or
developer packaging/smoke executable is shipped.

Windows binaries are **unsigned**: SmartScreen/antivirus may warn. Inspect
provenance and hashes; do not disable system-wide protections. There is no
installer or auto-update. SHA256 detects accidental/tampered transfer against a
trusted expected hash; it is **not publisher authentication or a signature**.
Use trusted local input only. There is **no authentication**; bind only to numeric
loopback and never expose this server through a proxy, tunnel, or public bind.

## Developer: build a preview from trusted source

Developers (unlike consumers) need Git, Go **1.27.0+**, Node **22.17+ in the Node 22
line**, npm, and the checked-in frontend dependencies. Use the existing lockfile;
no bundler or Go dependency has been added:

```sh
npm ci --prefix web
mkdir releases
go run ./cmd/release -version 0.11.0-preview.1 -target windows-amd64 -output releases/preview.zip
# On Linux, use -target linux-amd64 (or omit -target for the native default).
```

The packager **runs `npm run build` itself**, including TypeScript checking. It
requires the installed frontend dependencies; it never installs them. Build
only a trusted checkout: npm scripts and Go compilation execute source.
Uncommitted local work requires explicit `-allow-dirty`; the manifest records
`git_dirty: true`, the actual HEAD, and individual source fingerprints. A clean
checkout is the default. Local frontend `.env*` files (except `.example`) and
`VITE_*` environment overrides are rejected to avoid silently embedding local
configuration. Preview versions must be `N.N.N-preview[.N]`.

Choose **new** `.zip` and `.zip.sha256` paths in an existing directory. Staging
is in that same directory. Exclusive hard-link publication never replaces an
existing path; unsupported filesystems fail without an unsafe rename/copy
fallback. Failure removes owned staging and, if publication fails after linking
the checksum, the checksum created by that attempt. This is not a two-file
atomic transaction or a crash-durability guarantee: an abrupt process/machine
failure can leave a checksum without an archive or hidden staging. Inspect and
remove only confirmed abandoned outputs before retrying.
Staging-cleanup errors are reported even after publication; an error at that
point can coexist with a valid archive and checksum, which are not rolled back.

Both targets can be cross-compiled, but the smoke runner refuses to execute a
non-native target. Native CI jobs test the respective OS. Source/input selection
is bounded and allowlisted; symlinks, unsafe/colliding archive paths, unexpected
assets/production dependencies, missing assets/licenses, and failed builds are
rejected. The builder snapshots fingerprinted Go inputs before compiling.
Installer-managed links are allowed only in the trusted Go SDK root;
project paths and individual notice files still reject links.

## Provenance and reproducibility boundary

`manifest.json` schema 1 records:

- preview version, OS/architecture, actual Git HEAD and dirty state;
- actual Go, Node, npm, Vite, and TypeScript versions;
- source-file SHA256/size records, including frontend source and lockfile;
- frontend production package versions and lockfile integrity identifiers;
- the actual installed React, ReactDOM, and scheduler MIT license texts, plus
  the installed Go SDK `LICENSE` and `PATENTS` (as hashed bundle payload files);
- payload paths, sizes, fixed modes, and SHA256 hashes. The manifest excludes
  itself from its payload hash list; the archive checksum covers it.

The installed production package versions are checked against the lockfile;
the recorded npm integrity fields are lockfile provenance, **not an independent
re-hash/attestation of every installed `node_modules` file**. Use `npm ci` from a
trusted registry/cache before building when that assurance is needed.

ZIP entries are sorted, stamped `1980-01-01T00:00:00Z`, and assigned fixed
permissions. Go builds use `-trimpath -buildvcs=false -ldflags=-buildid=`,
disabled CGO, `GOAMD64=v1`, a local toolchain, and isolated Go build settings.
The manifest includes no wall-clock build time or absolute working directory.
Build twice from **unchanged inputs/toolchains** to two new output paths:

```sh
go run ./cmd/release -version 0.11.0-preview.1 -output releases/repeat.zip
```

Compare archive SHA256s using `Get-FileHash` or `sha256sum`. Sidecars intentionally
name their respective archive and need not have identical bytes. Same-host
identical-input archive equality is the tested claim; **cross-host/toolchain
byte reproducibility is not claimed**. Dirty-source fingerprints can differ
from the commit named in the manifest, explicitly and visibly.

## Isolated package smoke

### Verify without running the download

Developers/reviewers can verify **either target on either host** without
extracting or executing any payload (including a Linux candidate on Windows):

```powershell
go run ./cmd/release-smoke -archive releases\preview.zip -verify-only
```

This reuses the same bounded checksum, safe-entry, required-file, and complete
manifest verification as native smoke. Success writes a single JSON object with
`verified: true`, `extracted: false`, `executed: false`, and the complete
`manifest`; it creates no workspace. `-work-root` and `-browser-script` cannot
be combined with this mode. The built developer command exits 0 on success,
1 on verification/output failure, and 2 on invalid arguments (`go run` itself
may wrap a nonzero child exit). Errors go to stderr, not a success JSON record.
This is integrity inspection, **not executable-format validation, native smoke,
publisher authentication, or approval of manifest claims**. Consumers still use
their OS checksum tools above; no seventh executable is added to the bundle.

### Execute the native package in isolation

From the checkout, select an existing workspace parent **outside the repository**
(`$env:RUNNER_TEMP` in CI, or a dedicated local verification directory):

```powershell
go run ./cmd/release-smoke -archive releases\preview.zip -work-root C:\verification-workspaces
# Optional real browser using the already installed Playwright dependency:
go run ./cmd/release-smoke -archive releases\preview.zip -work-root C:\verification-workspaces -browser-script "$PWD\web\release-browser-smoke.mjs"
```

Install Chromium once with the existing `npx playwright install chromium` in
`web` if missing. Browser smoke needs developer Node/Playwright/Chromium; it is
not a consumer runtime requirement.

The smoke verifies the external archive checksum, bounded safe ZIP entries,
complete manifest hashes and required content **before extraction**. It creates
a unique clean workspace, invokes all six **packaged** binaries by absolute
path with an empty `PATH`, generates the fixture twice, packs it, compares exact
aggregates across all three engines and both formats, and validates a small
benchmark report. It starts the packaged server on an OS-assigned numeric
loopback port, checks health, snapshot metadata, query count, exact static index
and referenced JS/CSS bytes, then runs two successful load requests. Optional
Chromium checks rendered snapshot counts, a narrow query, engine comparison,
and page errors. Child operations have deadlines; cleanup stops only the
server process owned by the smoke and removes its unique workspace.

Tests cover invalid versions/targets, traversal/collisions/symlinks, archive
tamper, invalid/missing packages and sidecars, payload hash/coverage failures,
missing assets, deterministic ZIP metadata/bytes, overwrite refusal,
publication failures and failed-build cleanup.

## CI and review boundary

Regular `ci.yml` builds and smoke-tests native packages on Windows and Linux,
alongside existing tests. Manual **Local preview packages (manual)** workflow
dispatch additionally rebuilds twice, compares SHA256, runs packaged Chromium
smoke, and uploads short-lived Actions artifacts. Workflow token permissions
are `contents: read`; there is **no GitHub Release publication**.

Local Windows evidence is separate from hosted CI validation. After the
approved milestone-11 push, regular [CI run 35062923130](https://github.com/PoojaAgarwal2003/ChronoLens/actions/runs/35062923130)
passed Windows, Linux, and explorer jobs at `3e4a347`. This does not establish
that the separate manual preview workflow ran, nor validate later local
milestone-13 commits. Publication still requires approval. Benchmark snapshot
and concurrency histories are not modified by packaging.

## Historical milestone-11 local verification record — 2026-09-16

Before final review, Windows amd64 preview `0.11.0-preview.3` was built twice
from the same unchanged working tree and toolchain. Both ZIP archives had SHA256:

```text
3eec6d807c5f4f40ce374e1bb96cbfa2a1da8db45d350cbbf744618ceb45c30b
```

Retained local artifact: `releases/chronolens-0.11.0-preview.3-windows-amd64.zip`
(plus its `.sha256` and the `-repeat.zip` comparison artifact; all ignored).
Its manifest honestly records dirty local-preview source based on
`5e9d541eee753758e8fc04eb3d15728b0cfb3268`, 68 source fingerprints, 16 payload
files, and 17 total ZIP entries. Tools were Go 1.27.1, Node 22.17.0, npm 10.9.2,
Vite 6.4.3, and TypeScript 5.9.3.

`go test -count=1 ./...`, `go vet ./...`, and `go build ./...` passed. Packaging
and smoke units passed, including real filesystem symlink rejection on this
Windows host. `npm run test:e2e` rebuilt/typechecked the frontend and passed
all **34 existing browser tests** (17 JSONL and 17 snapshot).

The final archive passed an actual isolated Windows extraction outside the
repository, all-six-executable smoke with empty application `PATH`, and the
opt-in real Chromium packaged-UI test at approximately **04:26:40 UTC**.
The dedicated session-artifact workspace (not the repository) was removed and
the owned server stopped. A subsequent attempt to rebuild over the retained
artifact was rejected without changing its SHA256.

After final review of tool-version collection and cleanup-error propagation,
preview `0.11.0-preview.4` also passed the full isolated executable and Chromium
smoke at approximately **04:32:51 UTC**, with archive SHA256
`08f7fba58741be83cf19c0a6cf6e587e594d82f891c696d6585cdbd4ad4d9d50`.
This intermediate review archive is not retained; the older repeatability
record above describes its own fingerprinted inputs, not subsequent changes.

This demonstrates isolated consumer execution on the existing Windows host,
not a newly provisioned OS image. Linux was not run locally; the subsequent
successful regular hosted CI run is recorded separately above. No milestone-13
package, tag, GitHub Release, or hosted CI success is claimed.
