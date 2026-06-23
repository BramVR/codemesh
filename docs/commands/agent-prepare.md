---
summary: "Command reference for codemesh agent prepare."
read_when:
  - "Updating public command reference pages."
title: "codemesh agent prepare"
description: "Prepare one temporary agent workspace from a registered project."
permalink: /commands/agent-prepare
---

# codemesh agent prepare

## Syntax

```sh
codemesh agent prepare <project> [--base branch] [--profile name]
```

## Purpose

Prepare a temporary agent workspace for one registered project. CodeMesh checks readiness, creates a temp clone under CodeMesh-managed agent storage, records run metadata, and prints `ready_path`.

When `--base` is omitted, CodeMesh lets Agent Prep choose its current default base. `--profile` records the intended agent profile in run metadata.

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
```

## Current Limitations

- Prepares temporary clones only; it does not create shared worktrees from the source checkout.
- Uses local policy and readiness checks; it does not provision credentials or materialize secret values.
- Does not start or manage an agent process.
- Does not sync prepared workspaces between machines.

Back to [Command Catalog](../commands.md).
