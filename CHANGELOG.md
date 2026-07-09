# Changelog

## Unreleased

Target next patch: `v0.1.1`.

- Added explicit Placeholder workspace structure, so `codemesh bootstrap --placeholders` writes metadata-only sentinels for missing canonical Projects while `tree`, `status`, and `hydrate` distinguish placeholder, missing, blocked, and hydrated states: https://github.com/BramVR/codemesh/issues/140
- Added registry-clone Agent Prep for source-less Projects, with selected source mode in Agent Run metadata and Crabbox PR proof from an isolated local Git remote: https://github.com/BramVR/codemesh/issues/139
- Added bootstrap execution for registered missing Projects, so `codemesh bootstrap --all`, project-targeted bootstrap, and manifest bootstrap `--apply` now reuse the Hydration Planner to refuse unsafe paths before Git and clone from registered clone sources: https://github.com/BramVR/codemesh/issues/138
- Tightened Workspace Manifest clone-hint validation so hostful or non-absolute `file:` clone hints are refused while local absolute proof remotes remain supported.
- Added a shared Hydration Planner for `bootstrap --dry-run` and `hydrate`, so canonical missing Projects now show planned clone/refusal actions before any Git remote is touched: https://github.com/BramVR/codemesh/issues/137
- Added canonical versus local-only placement reporting in `codemesh tree` and `codemesh status`, so imported manifest Projects remain visible when missing locally and scans can record current-machine presence without rewriting desired layout: https://github.com/BramVR/codemesh/issues/136
- Added `codemesh manifest export` and `codemesh manifest import` for deterministic portable Workspace Manifest files that validate topology before persisting Project Registry rows on another machine: https://github.com/BramVR/codemesh/issues/135
- Added named machine registration plus `codemesh machine status` so local placement facts include display name, CodeMesh home, workspace root, and persisted troubleshooting output: https://github.com/BramVR/codemesh/issues/134
- Added a free GitHub-hosted Crabbox PR proof lane that publishes sanitized visual artifacts for Dropbox-for-developers workspace changes: https://github.com/BramVR/codemesh/issues/133

## v0.1.0 - 2026-07-08

First CodeMesh MVP source release.

### Highlights

- Local-first workspace control plane: project registry, readiness, explicit hydration, bootstrap, and topology export for Git-backed projects.
- Agent handoff MVP: `codemesh agent prepare`, Agent Run Contract v1, handoff docs metadata, deterministic `agent run`, run listing, and guarded cleanup.
- Secret-safe readiness: env requirements and fake-provider env bundles prove scope and presence without storing raw secret values.
- Multi-machine foundation: machine registry, workspace manifest entries, reconciliation dry-run planning, two-machine e2e proof, and owned-host proof lanes.
- Public docs and proof lanes: command catalog, north-star Vision, untrusted-comment agent guidance, reimagined docs-site hero, source/packaged e2e, live GitHub smoke, desktop smoke, cross-OS CI, and Crabbox-style PR proof.

### Changes

- Added local state init, Project Registry scan/add/tree/status, readiness diagnostics, and explicit hydration for registered Git projects: https://github.com/BramVR/codemesh/pull/19 https://github.com/BramVR/codemesh/pull/20 https://github.com/BramVR/codemesh/pull/21 https://github.com/BramVR/codemesh/pull/23
- Added repo-local project policy, env readiness checks, default/policy handoff docs, and command docs for current CLI behavior: https://github.com/BramVR/codemesh/pull/22 https://github.com/BramVR/codemesh/pull/64 https://github.com/BramVR/codemesh/pull/65 https://github.com/BramVR/codemesh/pull/66 https://github.com/BramVR/codemesh/pull/67 https://github.com/BramVR/codemesh/pull/68 https://github.com/BramVR/codemesh/pull/69
- Added Agent Prep, run listing, guarded cleanup, Agent Run Contract v1, deterministic `agent run`, source-checkout-independent prep, base-selection provenance, and JSON command contracts: https://github.com/BramVR/codemesh/pull/35 https://github.com/BramVR/codemesh/pull/36 https://github.com/BramVR/codemesh/pull/108 https://github.com/BramVR/codemesh/pull/105 https://github.com/BramVR/codemesh/pull/113 https://github.com/BramVR/codemesh/pull/112 https://github.com/BramVR/codemesh/pull/111
- Added shared readiness semantics, `doctor`, stable command result/presentation behavior, CLI contract snapshots, and toolchain readiness reporting/delegation: https://github.com/BramVR/codemesh/pull/107 https://github.com/BramVR/codemesh/pull/110 https://github.com/BramVR/codemesh/pull/116 https://github.com/BramVR/codemesh/pull/117
- Added Git Operations identity/redaction, machine registration, Workspace Manifest entries, bootstrap without default cloning, reconciliation drift planning, target export, and clone strategy controls for full/partial/sparse checkout: https://github.com/BramVR/codemesh/pull/99 https://github.com/BramVR/codemesh/pull/100 https://github.com/BramVR/codemesh/pull/122 https://github.com/BramVR/codemesh/pull/125 https://github.com/BramVR/codemesh/pull/120 https://github.com/BramVR/codemesh/pull/121
- Added fake-provider Env Binding and smoke coverage for secret-free agent-scoped bundles: https://github.com/BramVR/codemesh/pull/114 https://github.com/BramVR/codemesh/pull/115
- Added public docs inventory, command catalog/reference pages, docs site/theme, GitHub Pages workflow, README alignment, and release-lane setup docs: https://github.com/BramVR/codemesh/pull/46 https://github.com/BramVR/codemesh/pull/53 https://github.com/BramVR/codemesh/pull/60 https://github.com/BramVR/codemesh/pull/61 https://github.com/BramVR/codemesh/pull/62 https://github.com/BramVR/codemesh/pull/63
- Updated the Vision for the Dropbox-for-developers north star and first implementation slice: https://github.com/BramVR/codemesh/pull/145
- Added agent-facing GitHub safety guidance for untrusted non-collaborator comments: https://github.com/BramVR/codemesh/commit/40bd20ed7f4b13a29976148ab2b7eeaf67a7f982
- Reimagined the docs-site hero for the first release candidate: https://github.com/BramVR/codemesh/commit/2fa3dc7f7266adcacd93230f205f4bff51d4a1f4
- Added offline and packaged e2e harnesses, local Git fixtures, e2e report audit checks, expanded registry/readiness/hydration/agent-prep coverage, and packaged negative CLI proof: https://github.com/BramVR/codemesh/pull/14 https://github.com/BramVR/codemesh/pull/17 https://github.com/BramVR/codemesh/pull/18 https://github.com/BramVR/codemesh/pull/39 https://github.com/BramVR/codemesh/pull/40 https://github.com/BramVR/codemesh/pull/41 https://github.com/BramVR/codemesh/pull/42 https://github.com/BramVR/codemesh/pull/43 https://github.com/BramVR/codemesh/pull/44 https://github.com/BramVR/codemesh/pull/45
- Added live/free proof lanes: live e2e guardrails, public GitHub remote smoke, live agent prepare, cross-OS CI artifacts, toolchain host smoke, two-machine manifest smoke, Peekaboo desktop smoke, owned-host e2e proof, and Crabbox-style PR proof workflow: https://github.com/BramVR/codemesh/pull/101 https://github.com/BramVR/codemesh/pull/102 https://github.com/BramVR/codemesh/pull/104 https://github.com/BramVR/codemesh/pull/106 https://github.com/BramVR/codemesh/pull/118 https://github.com/BramVR/codemesh/pull/127 https://github.com/BramVR/codemesh/pull/128 https://github.com/BramVR/codemesh/pull/130 https://github.com/BramVR/codemesh/pull/131
