---
summary: "Command reference for codemesh status."
read_when:
  - "Updating public command reference pages."
title: "codemesh status"
description: "Report project readiness."
permalink: /commands/status
---

# codemesh status

## Syntax

```sh
codemesh status [project] [--base branch]
```

## Purpose

Report readiness for all registered projects or one project. Status includes local path presence, normalized remote, selected base branch, warnings, and blockers.

When `--base` is omitted, CodeMesh uses `main`.

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
codemesh status demo-project --base main
```

## Current Limitations

- Reports readiness; it does not modify the project checkout.
- Dirty source checkouts warn but do not block agent handoff by themselves.
- Env readiness checks names and paths only; secret values are not read or stored.
- Multi-machine freshness and remote manifest status are not implemented.

Back to [Command Catalog](../commands.md).
