---
summary: "Public trust boundaries for CodeMesh local state, Git ownership, and secret-free readiness."
read_when:
  - "Changing public trust, privacy, or safety docs."
title: "Trust Boundaries"
description: "What CodeMesh owns, what stays with Git and the local filesystem, and how env readiness avoids secret values."
---

# Trust Boundaries

CodeMesh augments Git and local filesystems. It does not replace either one.

## Local First

CodeMesh state is local machine metadata. The MVP uses a local SQLite database under CodeMesh home to store project registry rows, settings, local machine facts, and agent run metadata.

## Git Remains The Source Of Code Truth

CodeMesh records normalized remotes and clone URLs, then uses Git for clone, fetch, checkout, and hydration. The default Clone Strategy is `full-clone`, meaning full Git history and a complete working tree; `--partial-clone` and repeatable `--sparse path` are explicit Git-native lazy checkout opt-ins. CodeMesh does not own Git history, source merges, conflict resolution, or uncommitted local work.

## Secret-Free Readiness

Env readiness checks names and paths:

- required env files are checked with filesystem metadata only
- required env keys are checked for presence only
- values are not read, stored, printed, or materialized

## Toolchain Readiness

Toolchain readiness reports declared tool names and statuses only. CodeMesh does not install tools, run package-manager setup, create dependency directories, or build environments.

## Explicit Non-Goals

| Boundary | MVP stance |
| --- | --- |
| Git replacement | Out of scope. |
| Cloud sync | Out of scope. |
| Daemon or mount layer | Out of scope. |
| Automatic placeholders | Out of scope. |
| Secret materialization | Out of scope. |
| Toolchain installation | Out of scope. |
| Build artifact sync | Out of scope by default. |

## Agent Handoff Safety

Agent Prep creates a temporary workspace from the registered remote and requested base. It records handoff metadata, including the selected clone strategy and any partial/sparse details, so the run can be audited and cleaned, but it does not copy uncommitted source files or secret values into the prepared workspace.

For implementation details, see [Local State Model](state.md) and [Project Policy Reference](project-policy.md).
