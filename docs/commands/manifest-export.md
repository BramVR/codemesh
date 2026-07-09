---
summary: "Command reference for codemesh manifest export."
read_when:
  - "Updating public command reference pages."
title: "codemesh manifest export"
description: "Export a portable canonical Workspace Manifest."
permalink: /commands/manifest-export
---

# codemesh manifest export

## Syntax

```sh
codemesh manifest export [--output path]
```

## Purpose

Write the registered canonical Workspace as deterministic portable JSON. The manifest records schema version, sorted project identities, aliases, normalized remotes, clone hints, relative desired paths, and groups.

The manifest is the portable interface between machines. SQLite remains the local operational store.

When `--output` is omitted, JSON is written to stdout.

## Safe Example

```sh
demo="$(mktemp -d)"
export CODEMESH_HOME="$demo/codemesh-home"
workspace="$demo/workspace-a"
project="$workspace/src/demo"
manifest="$demo/workspace-manifest.json"

mkdir -p "$project"
git -C "$project" init -b main
git -C "$project" remote add origin "https://example.invalid/org/demo.git"

codemesh init "$workspace"
codemesh machine register "$workspace"
codemesh add "$project" --alias demo
codemesh manifest export --output "$manifest"
```

## Current Limitations

- Requires `codemesh machine register` first so desired paths are derived from the registered workspace root.
- Reads local Project Registry rows only; it does not publish, sync, or fetch manifests.
- Refuses machine-local project identities and omits machine-local clone hints.
- Exports topology metadata only; readiness, machine facts, Agent Runs, env values, and secret values are not included.

Back to [Command Catalog](../commands.md).
