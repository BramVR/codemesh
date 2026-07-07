---
summary: "Prepared decision brief for the first CodeMesh release."
read_when:
  - "Deciding whether to run the first CodeMesh release."
---

# Release Proposal: v0.1.0

## Recommendation

Release `v0.1.0` after Bram explicitly authorizes release execution and chooses the versioning path. This should be a source/tag GitHub Release for the MVP CLI and public docs, not a registry or binary-distribution release.

`v0.1.0` is the right first tag: CodeMesh has a coherent MVP command surface, but no prior public release or compatibility promise. Use patch releases after this for compatible fixes; use minor releases for the next meaningful command/capability batch.

## What Ships

- Go CLI source release for the current command catalog: init, add, scan, tree, status, doctor, hydrate, bootstrap, target export, env bind, machine register, agent prepare, agent run, runs, and clean.
- Local SQLite state, Project Registry, Readiness, Env Readiness, Env Binding fake provider, Machine Registry, Workspace Manifest, Reconciliation dry-run planning, and Workspace Target Export.
- Agent handoff MVP with Agent Run Contract v1, checkout provenance, handoff docs metadata, toolchain readiness reporting, clone strategy metadata, and secret-free env-bundle metadata.
- Public docs site, reimagined docs-site hero, command references, install/quickstart docs, trust/state/MVP docs, and e2e proof docs.
- Test/proof lanes: unit tests, offline e2e, packaged e2e, live skip-by-default e2e, public GitHub live smoke, desktop smoke, two-machine smoke, owned-host proof, cross-OS CI, and Crabbox-style PR proof.

## Current State

- Candidate base: `origin/main` at `2fa3dc7f7266adcacd93230f205f4bff51d4a1f4`.
- Latest main change: direct push `docs: reimagine docs site hero`, with fresh `ci` run 28874863261 and `pages` run 28874863305 green.
- Latest landed PR before that: https://github.com/BramVR/codemesh/pull/131.
- Previous landed PR: https://github.com/BramVR/codemesh/pull/130, closing https://github.com/BramVR/codemesh/issues/129.
- Open PR queue: empty.
- Open ready-for-agent issue: https://github.com/BramVR/codemesh/issues/70, broad roadmap backlog; not a release blocker.
- Tags/releases: none.
- Package registry: none.
- Current CLI version: `0.0.0-dev`; release execution should not tag until this is resolved.

## Required Pre-Release Checks

After the authorized versioning choice, run locally on the exact final tag candidate:

```sh
make docs:list
make test
make e2e
make e2e-packaged
make docs-site-test
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go list -m -u all
```

Confirm GitHub exact-head checks on the same final tag candidate:

- `ci`: green.
- `pages`: green.

Optional live/free proof if prerequisites are present:

```sh
make e2e-live
CODEMESH_E2E_LIVE=1 make e2e-live
make e2e-owned-host
```

## Residual Risks

- No automated release workflow yet; GitHub Release notes and tag creation are manual.
- No binary artifacts or package registry path yet; users build from source.
- CLI currently reports `0.0.0-dev`, so the release needs a separate authorized versioning step before final checks, CI confirmation, and tag.
- Go dependency freshness has indirect update candidates; no direct dependency update is required for this proposal unless the final govulncheck finds a called vulnerability.
- Windows coverage is a targeted smoke, not full POSIX-style e2e.

## Bram Decision

Recommended choice: authorize `v0.1.0` release execution with a small versioning step, source/tag GitHub Release only, and no binary or registry publish.

Choices:

- Release now as `v0.1.0` after resolving CLI versioning, then rerunning/confirming the required gates on the final tag candidate.
- Hold release until a binary-distribution workflow exists.
- Hold release and first split more roadmap work from https://github.com/BramVR/codemesh/issues/70.
