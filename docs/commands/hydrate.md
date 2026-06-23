---
summary: "Command reference for codemesh hydrate."
read_when:
  - "Updating public command reference pages."
title: "codemesh hydrate"
description: "Clone one registered missing project into its desired path."
permalink: /commands/hydrate
---

# codemesh hydrate

## Syntax

```sh
codemesh hydrate <project>
```

## Purpose

Clone one registered project into its desired local path when that path is missing. Hydration uses the remote already stored in the local Project Registry.

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
rm -rf "$workspace/demo-project"
codemesh hydrate demo-project
```

## Current Limitations

- Works only for projects already present in the local Project Registry.
- Refuses existing non-empty path conflicts.
- Performs an explicit Git clone; it does not create lazy placeholders, mounts, or file-level hydration.
- Does not fetch project definitions from a remote manifest.

Back to [Command Catalog](../commands.md).
