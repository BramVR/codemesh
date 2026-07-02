---
summary: "Accepted MVP command set and current-vs-planned command boundary."
read_when:
  - Changing shipped commands, CLI help, Command Catalog, MVP command direction, or planned daemon/mount/sync/materialization scope.
---

# MVP command set

Status: Accepted

CodeMesh v0 ships `init`, `scan`, `add`, `tree`, `status`, `hydrate`, `env bind`, `machine register`, `agent prepare`, `agent run`, `runs`, and `clean`. These commands prove the canonical workspace index, local machine identity, private fake-provider env binding, and the agent handoff workflow without a daemon, mount layer, automatic placeholders, live secret providers, cloud sync, UI, or file-level lazy hydration.

The runnable surface is the CLI help plus [Command Catalog](../commands.md). Future directions such as synced manifests, live secret providers, repo-local env file writing, and multi-machine sync remain planned until they appear in both places.
