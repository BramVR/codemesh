---
summary: "Command reference for codemesh runs."
read_when:
  - "Updating public command reference pages."
title: "codemesh runs"
description: "List prepared agent runs."
permalink: /commands/runs
---

# codemesh runs

## Syntax

```sh
codemesh runs
```

## Purpose

List agent run metadata stored in the local CodeMesh home, including run ID, project alias, base, profile, lifecycle state, creation time, and workspace path.

Runs start as `state=prepared` after `codemesh agent prepare`. After `codemesh agent run` records at least one command, the listing reports `state=executed`. After `codemesh clean` removes the managed run, the row disappears from the listing.

## Safe Example

```sh
demo="$(mktemp -d)"
export CODEMESH_HOME="$demo/codemesh-home"
workspace="$demo/workspace"
seed="$demo/seed"
remote="$demo/demo.git"

mkdir -p "$workspace" "$seed"
git -C "$seed" init -b main
printf '# demo\n' > "$seed/README.md"
git -C "$seed" add README.md
git -C "$seed" -c user.name='CodeMesh Demo' -c user.email='demo@example.invalid' commit -m 'Initial demo'
git clone --bare "$seed" "$remote"
git clone "$remote" "$workspace/demo-project"

codemesh init "$workspace"
codemesh add "$workspace/demo-project" --alias demo-project
codemesh agent prepare demo-project --base main --profile codex
codemesh runs
```

## Current Limitations

- Lists local run metadata and derived lifecycle state only.
- Does not inspect agent process state or remote machine state.
- Does not prune runs; use [`codemesh clean`](clean.md) for cleanup.

Back to [Command Catalog](../commands.md).
