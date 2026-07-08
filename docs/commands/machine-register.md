---
summary: "Command reference for codemesh machine register."
read_when:
  - "Updating public command reference pages."
---

# codemesh machine register

## Syntax

```sh
codemesh machine register [workspace-root] [--name name] [--json]
```

## Purpose

Creates or reuses a persistent local machine ID, then records mutable local facts: display name, hostname, OS, architecture, CodeMesh home, workspace root, and timestamps.

Machine facts stay in the local CodeMesh state store. They are observed machine state, not shared topology.

## Safe Example

```sh
demo="$(mktemp -d)"
export CODEMESH_HOME="$demo/codemesh-home"
workspace="$demo/workspace"

mkdir -p "$workspace"
codemesh init "$workspace"
codemesh machine register "$workspace" --name "Demo Machine"
codemesh machine register "$workspace" --json
```

## Current Limitations

- Registers only the current local machine.
- Re-running without `--name` preserves the existing display name while updating placement facts.
- Does not publish facts to a manifest or remote service.
- Does not sync projects or bootstrap another machine.

See the [Command Catalog](../commands.md) for the full current command list.
