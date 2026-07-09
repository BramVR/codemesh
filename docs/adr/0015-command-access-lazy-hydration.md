---
summary: "Accepted lazy Hydration slice: explicit command access before path hooks, daemons, mounts, or background sync."
read_when:
  - Changing lazy Hydration, access-triggered hydration, command-access behavior, mount/daemon boundaries, or background sync scope.
---

# Command-access lazy Hydration

Status: Accepted

CodeMesh may hydrate a known canonical Project when a user or agent explicitly runs `codemesh access <project>`. This is the first lazy Hydration slice after Placeholder sentinels: the Project is already in the local Registry, the Hydration Planner classifies the path, and the shared hydration executor performs the same safe clone or refusal as `codemesh hydrate`.

`access` records the command trigger and before/after workspace-state transition in its command result. `tree` and `status` continue to derive current state from the filesystem and Registry; CodeMesh does not persist a hydration event log in the Project row.

This does not authorize path-access hooks, editor/Finder integration, a daemon, a mount layer, automatic background sync, generic cloud-drive behavior, or hidden Git conflict handling. Those remain later product decisions.
