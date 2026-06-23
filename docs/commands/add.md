---
summary: "Command reference for codemesh add."
read_when:
  - "Updating public command reference pages."
title: "codemesh add"
description: "Add one existing Git checkout to the Project Registry."
permalink: /commands/add
---

# codemesh add

## Syntax

```sh
codemesh add <path> [--alias name]
```

## Purpose

Add one existing Git checkout to the local Project Registry. CodeMesh records the checkout path, alias, and normalized Git remote identity.

If `--alias` is omitted, the alias defaults to the checkout directory name.

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
```

## Current Limitations

- Adds one project path at a time.
- Requires an existing supported Git checkout.
- Does not clone missing projects; use [`codemesh hydrate`](hydrate.md) after a project is registered.
- Does not upload registry data or sync it to another machine.

Back to [Command Catalog](../commands.md).
