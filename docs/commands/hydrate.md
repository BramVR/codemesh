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
codemesh hydrate <project> [--partial-clone] [--sparse path] [--json]
```

## Purpose

Clone one registered project into its desired local path when that path is missing or contains an unmodified CodeMesh-owned placeholder. Hydration first consumes the shared Hydration Planner, which classifies the registered Project as present, placeholder, missing, path-conflicted, unsafe, or unknown without contacting the Git remote. Execution then uses the same planned clone input.

By default hydration uses `full-clone`: full Git history and a complete working tree. `--partial-clone` opts into Git partial clone with `blob:none`; repeatable `--sparse path` opts into Git sparse checkout for project-relative paths. If Git reports that the remote ignored or did not record the partial clone filter, hydration fails with a clone diagnostic instead of recording misleading partial metadata. This is Git-native laziness only. It does not create placeholders, mounts, VFS behavior, daemon hydration, or file-level sync.

Use `--json` to emit the stable Command Result shape. The payload reports `outcome` as `hydrated`, `already-present`, `path-conflict`, `unsafe-path`, `unknown-project`, or `failed`, plus the project alias, path, path presence, normalized remote when a registered project was resolved, selected clone strategy metadata, and the planner action. The planner action includes `local_only_paths` when policy is available before the clone decision. User-action failures such as path conflicts, unsafe paths, invalid policy, and unknown projects use exit class `readiness-blocked`; operational clone or tool failures use exit class `internal-error`. Both failure classes return exit code 1 without overwriting local files.

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
codemesh hydrate demo-project --partial-clone --sparse README.md --json
```

## Current Limitations

- Works for canonical Projects already present in the local Project Registry, including manifest-imported or bootstrapped rows whose checkout is absent locally.
- Refuses existing non-empty path conflicts.
- Defaults to an explicit `full-clone` Git clone; partial clone and sparse checkout require explicit command flags. It does not create placeholders, mounts, VFS behavior, or file-level hydration.
- Does not fetch project definitions from a remote manifest.

Back to [Command Catalog](../commands.md).
