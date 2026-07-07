---
summary: "Release lane, preflight checks, version choice, confidentiality gate, and closeout."
read_when:
  - "Preparing, proposing, tagging, or verifying a CodeMesh release."
---

# Releasing

CodeMesh releases are GitHub tag and GitHub Release based. There is no package registry, binary-distribution workflow, or release automation yet.

Release execution needs explicit Bram authorization. Setup/proposal work may prepare docs, checks, and a release branch, but must stop before version bumps, tags, GitHub Releases, registry publishing, or release-script execution.

## Version Policy

- First MVP release: `v0.1.0`.
- Patch: compatible fixes, docs, CI, tests, or small behavior hardening after `v0.1.0`.
- Minor: new backward-compatible user-facing command surface or meaningful MVP capability batch.
- Major: breaking CLI, JSON contract, state schema, or workflow change; requires explicit approval.

Do not tag a release while the CLI reports `0.0.0-dev`. Until version injection exists, release execution must include an authorized version bump or release-specific build/version change.

## Required Gates

Run from a clean, fast-forward-current `main` checkout:

```sh
make docs:list
make test
make e2e
make e2e-packaged
make docs-site-test
```

Recommended security/dependency check:

```sh
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
go list -m -u all
```

Optional live/free proof when prerequisites are available:

```sh
make e2e-live
CODEMESH_E2E_LIVE=1 make e2e-live
make e2e-owned-host
```

Required GitHub checks for the exact candidate commit:

- `ci`: green for Ubuntu, macOS, and Windows smoke.
- `pages`: green for docs-site build and deploy-precheck path.

## Confidentiality Gate

Before any public PR update, tag, GitHub Release, release notes, binary artifact, screenshot, or proof comment:

- Audit the exact diff, release notes, generated reports, docs-site output, proof bundles, and any attached artifacts.
- Confirm no credentials, raw env values, private endpoints, private host data, internal-only model identifiers, or sensitive local paths are exposed.
- Keep generated proof artifacts under ignored paths, GitHub artifacts/attachments, or an approved external artifact manifest; never commit them to product branches.

Record `PASS` or `BLOCKED` in the PR/release proof.

## First Release Checklist

1. Refresh `main`: fetch, fast-forward pull, clean status.
2. Confirm no open PRs and no release-specific blockers.
3. After explicit Bram release authorization, perform the versioning choice and commit it if needed.
4. Confirm `CHANGELOG.md` target section matches the exact final candidate commit and release notes.
5. Run required local gates and dependency/security checks against the final tag candidate.
6. Confirm GitHub `ci` and `pages` are green for the exact final tag candidate.
7. Pass the confidentiality gate for release notes and candidate artifacts.
8. Tag `v0.1.0` and create the GitHub Release using the changelog section.
9. Verify the tag and GitHub Release exist, release notes include the changelog section, and install-from-source still works.
10. After verified release and explicit closeout authorization, open the next patch `Unreleased` section.

Open roadmap issue https://github.com/BramVR/codemesh/issues/70 is backlog, not a release blocker unless Bram scopes a specific slice into the target release.
