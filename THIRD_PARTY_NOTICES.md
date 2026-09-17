# Third-party notices and dependency attribution

**ChronoLens has no selected project license. No `LICENSE` file is supplied.**
Public visibility is not a grant of redistribution rights. Dependency licenses
below apply to their respective upstream components, **not automatically to
ChronoLens**. This document is an attribution inventory, not legal clearance.

## Scope of the owner's request

The owner requests proper attribution and license notices for the third-party
components used in implementing ChronoLens. They did **not** report personally
copying unidentified upstream files. The earlier interpretation to that effect
was mistaken and is superseded by this clarification.

No externally copied project-code source has been identified in the available
implementation record. This is not a categorical claim that every part is
original, nor a legal-clearance opinion. The verified dependencies below retain
their own licenses and notices. If separately incorporated upstream material is
identified in future work, record its actual source and applicable notices then;
there is no owner-reported unknown-source inventory awaiting completion.

The request for upstream attribution does not select MIT or any other project
license. Choosing project terms and approving publication remain owner decisions.

## Verified dependency inventory

Verified 2026-09-17 against installed `web/node_modules/<package>/package.json`
and its upstream notice filenames, with pinned versions in
[`web/package-lock.json`](web/package-lock.json). Repository links below come
from package metadata; they identify **dependency** sources, not the provenance
of the project's entire implementation.

| Component / installed version | Role | Declared license | Upstream repository | Installed notices |
|---|---|---|---|---|
| React 19.2.8 | Browser runtime | MIT | [react/react](https://github.com/react/react) | `react/LICENSE` |
| react-dom 19.2.8 | Browser runtime | MIT | [react/react](https://github.com/react/react) | `react-dom/LICENSE` |
| scheduler 0.27.0 | Transitive browser runtime | MIT | [facebook/react](https://github.com/facebook/react) | `scheduler/LICENSE` |
| Vite 6.4.3 | Development/build | MIT | [vitejs/vite](https://github.com/vitejs/vite) | `vite/LICENSE.md` |
| TypeScript 5.9.3 | Development/typechecking | Apache-2.0 | [microsoft/TypeScript](https://github.com/microsoft/TypeScript) | `typescript/LICENSE.txt` |
| @playwright/test, playwright, playwright-core 1.63.0 each | Test/demo tooling, not shipped app runtime | Apache-2.0 | [microsoft/playwright](https://github.com/microsoft/playwright) | Each package's `LICENSE` and `NOTICE` |
| @types/node 22.19.15 | Development types | MIT | [DefinitelyTyped/DefinitelyTyped](https://github.com/DefinitelyTyped/DefinitelyTyped) | `@types/node/LICENSE` |
| @types/react 19.2.18 | Development types | MIT | [DefinitelyTyped/DefinitelyTyped](https://github.com/DefinitelyTyped/DefinitelyTyped) | `@types/react/LICENSE` |
| @types/react-dom 19.2.7 | Development types | MIT | [DefinitelyTyped/DefinitelyTyped](https://github.com/DefinitelyTyped/DefinitelyTyped) | `@types/react-dom/LICENSE` |
| Go 1.27.1 toolchain / standard library | Build tooling and compiled runtime | BSD-style license plus patent grant | [Go](https://go.dev/) | SDK `LICENSE` and `PATENTS` |

The lockfile inventories additional transitive/build/platform packages; this
table does not claim to enumerate or clear every dependency. Browser automation
also uses the separately installed Chromium distribution, with its own notices;
Chromium is not included in the application preview archives.

## Existing bundle notices and showcase artifacts

The packager preserves the installed React, react-dom, and scheduler `LICENSE`
texts plus Go `LICENSE` and `PATENTS` under archive `licenses/`. Its
`PROJECT-LICENSE.txt` states that the project license is not selected. No
upstream notices have been removed or replaced by this inventory.

The retained [local candidate](docs/release-candidate.md) was built at `f8ae657`
**before this document**. Its existing hashes remain historical evidence;
archives have not been silently rewritten, and they do not contain this new
inventory. They are unsigned, local-only evaluation
artifacts, **not cleared for redistribution**. Any later approved distribution
must include the applicable dependency inventory and all required upstream notices.

[Showcase captures](docs/demo.md) record the actual running project and generated
data. They do not assert ownership of all visible implementation/design.
Benchmark reports are project measurements under documented conditions, not
claims to have invented the underlying algorithms or to own upstream code.
