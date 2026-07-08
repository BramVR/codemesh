---
summary: "Command reference for codemesh machine status."
read_when:
  - "Updating public command reference pages."
---

# codemesh machine status

## Syntax

```sh
codemesh machine status [--json]
```

## Purpose

Shows the current local machine registration facts for troubleshooting: machine ID, display name, hostname, OS, architecture, CodeMesh home, workspace root, and timestamps.

Machine status reads persisted local state. It does not compute project readiness, sync topology, or contact another machine.

## Safe Example

```sh
demo="$(mktemp -d)"
export CODEMESH_HOME="$demo/codemesh-home"
workspace="$demo/workspace"

mkdir -p "$workspace"
codemesh init "$workspace"
codemesh machine register "$workspace" --name "Demo Machine"
codemesh machine status
codemesh machine status --json
```

## Current Limitations

- Shows only the current local machine row.
- Requires `codemesh machine register` before status is available.
- Does not include derived readiness, freshness, or placement drift.

See the [Command Catalog](../commands.md) for the full current command list.
