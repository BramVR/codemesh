---
summary: "Command reference for codemesh machine register."
read_when:
  - "Updating public command reference pages."
---

# codemesh machine register

## Syntax

```sh
codemesh machine register [workspace-root] [--json]
```

## Purpose

Creates or reuses a persistent local machine ID, then records mutable local facts: hostname, OS, architecture, workspace root, and timestamps.

Machine facts stay in the local CodeMesh state store. They are observed machine state, not shared topology.

## Safe Example

```sh
demo="$(mktemp -d)"
export CODEMESH_HOME="$demo/codemesh-home"
workspace="$demo/workspace"

mkdir -p "$workspace"
codemesh init "$workspace"
codemesh machine register "$workspace"
codemesh machine register "$workspace" --json
```

## Current Limitations

- Registers only the current local machine.
- Does not publish facts to a manifest or remote service.
- Does not sync projects or bootstrap another machine.

See the [Command Catalog](../commands.md) for the full current command list.
