---
summary: "Accepted placeholder shape: explicit metadata sentinels before daemon, mount, or lazy file hydration."
read_when:
  - Changing Placeholder materialization, Hydration Planner state, bootstrap placeholder behavior, lazy Hydration, mount/daemon behavior, or source checkout detection.
---

# Explicit sentinel placeholders

Status: Accepted

CodeMesh may materialize Placeholder structure only through explicit commands such as `codemesh bootstrap --placeholders`. A Placeholder is a normal directory containing CodeMesh metadata, a visible note that says it is not a Git checkout, and an intentionally invalid `.git` file that prevents Git from discovering an ancestor worktree. It is not an empty directory and it never includes a `.git` directory or source files.

Placeholders reuse the Hydration Planner safety model. Unsafe paths, local path conflicts, mismatched placeholder metadata, and edited placeholders are blockers before mutation. An unmodified CodeMesh-owned Placeholder may be replaced by explicit `codemesh hydrate <project>` or bootstrap clone execution.

This does not introduce automatic placeholders, path-access-triggered lazy Hydration, a daemon, a mount layer, or file-level hydration. Those remain later decisions after sentinel behavior is proven.
