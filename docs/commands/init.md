---
summary: "Command reference for codemesh init."
read_when:
  - "Updating public command reference pages."
title: "codemesh init"
description: "Create local CodeMesh state for a workspace root."
permalink: /commands/init
---

# codemesh init

## Syntax

```sh
codemesh init [workspace-root]
```

## Purpose

Create CodeMesh home, the local SQLite database, and agent storage for one workspace root.

When `workspace-root` is omitted, CodeMesh resolves the workspace from the current directory. When `CODEMESH_HOME` is set, state is written there instead of the user's normal CodeMesh home.

## Safe Example

```sh
demo="$(mktemp -d)"
export CODEMESH_HOME="$demo/codemesh-home"
workspace="$demo/workspace"

mkdir -p "$workspace"
codemesh init "$workspace"
```

## Current Limitations

- Initializes local state only.
- Does not register remote machines, start a daemon, sync manifests, or create placeholders.
- Does not read, write, or materialize secret values.

Back to [Command Catalog](../commands.md).
