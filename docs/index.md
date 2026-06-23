---
summary: "Public CodeMesh docs-site home page."
read_when:
  - "Changing the public docs-site home page."
title: "CodeMesh"
description: "CodeMesh keeps local code workspaces, Git-backed project inventory, readiness checks, and agent handoff state understandable across machines."
---

# CodeMesh

CodeMesh is a local-first developer workspace fabric for Git projects, machines, and coding agents.

It keeps the canonical project tree visible, checks whether projects are present and ready, prepares temporary agent workspaces, and records local handoff metadata without replacing Git or storing secret values.

## Start Here

- [Install](install.md): build the current CLI from source and verify it runs.
- [Quickstart](quickstart.md): try CodeMesh against a local Git remote with isolated state.
- [Command Catalog](commands.md): see the current runnable command surface.
- [Trust Boundaries](trust.md): review what CodeMesh does and does not own.
- [Local State Model](state.md): understand CodeMesh home, SQLite metadata, and agent run storage.

## What It Does

- Tracks a Project Registry for the intended workspace tree.
- Shows present, missing, dirty, stale, and env-missing readiness signals.
- Hydrates registered missing projects through explicit Git clones.
- Prepares temporary agent workspaces from clean Git bases.
- Checks env readiness by names and paths only.

## What It Does Not Do

CodeMesh does not replace Git, sync arbitrary files, become a cloud drive, create daemon or mount behavior, create automatic placeholders, store raw secrets, or materialize secret values in the MVP.

## Current Shape

| Area | Current MVP stance |
| --- | --- |
| Project Registry | Local SQLite metadata and Git remote identity. |
| Readiness | Warnings and blockers derived from the local filesystem and Git state. |
| Agent Prep | Temporary clone, selected base, policy check, and run metadata. |
| Env Readiness | Missing file/key detection without reading values. |

## Source

CodeMesh is open source at [BramVR/codemesh](https://github.com/BramVR/codemesh).
