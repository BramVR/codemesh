---
summary: "Command reference for codemesh scan."
read_when:
  - "Updating public command reference pages."
title: "codemesh scan"
description: "Scan a workspace root for Git projects."
permalink: /commands/scan
---

# codemesh scan

## Syntax

```sh
codemesh scan [workspace-root]
```

## Purpose

Discover supported Git checkouts under a workspace root and add or update them in the local Project Registry.

When `workspace-root` is omitted, CodeMesh scans the current directory.

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
codemesh scan "$workspace"
```

## Current Limitations

- Scans local filesystem paths only.
- Reports unsupported or nested Git candidates as skipped.
- Does not hydrate missing projects or sync a remote manifest.
- Does not inspect secret values or materialize env files.

Back to [Command Catalog](../commands.md).
