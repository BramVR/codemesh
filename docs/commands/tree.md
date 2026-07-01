---
summary: "Command reference for codemesh tree."
read_when:
  - "Updating public command reference pages."
title: "codemesh tree"
description: "Show the canonical workspace tree."
permalink: /commands/tree
---

# codemesh tree

## Syntax

```sh
codemesh tree [--json]
```

## Purpose

Print the known canonical workspace from the local Project Registry, including each project's readiness state and local path.

Use `--json` to emit the stable Command Result shape with `command`, `exit_class`, `diagnostics`, and `payload.projects`. Each project includes alias, normalized state, local path, path presence, remote, base, and readiness diagnostics. Human output is unchanged when `--json` is omitted.

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
codemesh tree
codemesh tree --json
```

## Current Limitations

- Reads local registry state only.
- Does not create directories, placeholders, mounts, or clones.
- Does not check remote freshness; use [`codemesh status`](status.md) for readiness details.

Back to [Command Catalog](../commands.md).
