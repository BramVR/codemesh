---
summary: "Accepted registry source: local discovery and explicit add before synced manifests."
read_when:
  - Changing Project Registry discovery, synced manifest plans, scan/add behavior, multi-machine index scope, or local-first assumptions.
---

# Local project index first

Status: Accepted

CodeMesh first learns projects from local discovery and explicit `codemesh add`, not from a synced manifest service. This keeps the first agent-prep workflow usable on one machine immediately, while leaving room for a synced multi-machine project manifest once the local project index and readiness checks prove useful.
