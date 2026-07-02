---
summary: "Accepted env boundary: detect missing env before live secret or config materialization."
read_when:
  - Changing Env Readiness, missing-env diagnostics, env file/key handling, secret backend plans, or materialization boundaries.
---

# Env detection before materialization

Status: Accepted

CodeMesh MVP first detects missing env files and variables before materializing anything. Secret handling has high blast radius, and the first agent-prep workflow can deliver value by blocking unsafe handoffs with clear missing-env diagnostics. Secret backend integration can be added after project readiness checks are useful without touching secret values.

Follow-up: deterministic fake-provider Env Binding may materialize agent-scoped bundles under managed run storage after readiness and scope checks. Live providers and repo-local env file writing remain outside this ADR's safe baseline.
