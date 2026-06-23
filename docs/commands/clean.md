---
summary: "Command reference for codemesh clean."
read_when:
  - "Updating public command reference pages."
title: "codemesh clean"
description: "Delete old CodeMesh-managed agent runs."
permalink: /commands/clean
---

# codemesh clean

## Syntax

```sh
codemesh clean [--older-than age]
```

## Purpose

Delete CodeMesh-managed agent runs older than the selected age and report how many were deleted or kept.

`age` accepts Go durations and day values such as `7d`. When omitted, CodeMesh uses its default cleanup age.

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
codemesh clean --older-than 0d
```

## Current Limitations

- Deletes CodeMesh-managed agent run directories and metadata only.
- Does not clean source checkouts, arbitrary workspace files, or external agent state.
- Does not sync cleanup decisions to another machine.

Back to [Command Catalog](../commands.md).
