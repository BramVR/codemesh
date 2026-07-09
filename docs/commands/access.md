---
summary: "Command reference for codemesh access."
read_when:
  - "Updating public command reference pages."
title: "codemesh access"
description: "Lazily hydrate one known project through explicit command access."
permalink: /commands/access
---

# codemesh access

## Syntax

```sh
codemesh access <project> [--partial-clone] [--sparse path] [--json]
```

## Purpose

Access one known Project and hydrate it when its desired path is missing or contains an unmodified CodeMesh-owned Placeholder. This is command-triggered lazy Hydration: it reuses the same Hydration Planner and clone executor as [`codemesh hydrate`](hydrate.md), records the before/after transition in the command result, and leaves `tree` and `status` to report the derived final workspace state.

Use `--json` to emit the stable Command Result shape. The payload reports `trigger: command-access`, `outcome`, the project path, the transition from `missing`, `placeholder`, or `hydrated` to the final state, and the nested hydrate payload with the planner action. The planner action includes `local_only_paths` when policy is available before the clone decision. Path conflicts, unsafe paths, invalid policy, unknown projects, modified placeholders, and mismatched placeholders are refused before Git runs.

Like `hydrate`, `access` defaults to the `full-clone` Clone Strategy. `--partial-clone` and repeatable `--sparse path` are explicit Git-native clone strategy options, not background file sync.

## Safe Example

```sh
demo="$(mktemp -d)"
export CODEMESH_HOME="$demo/codemesh-home"
workspace="$demo/workspace"
manifest="$demo/manifest"
seed="$demo/seed"
remote="$demo/demo.git"

mkdir -p "$manifest" "$seed"
git -C "$seed" init -b main
printf '# demo\n' > "$seed/README.md"
git -C "$seed" add README.md
git -C "$seed" -c user.name='CodeMesh Demo' -c user.email='demo@example.invalid' commit -m 'Initial demo'
git clone --bare "$seed" "$remote"

codemesh init "$workspace"
codemesh machine register "$workspace"

cat > "$manifest/demo.json" <<JSON
{
  "manifest_version": 1,
  "project": {
    "identity": "https://example.invalid/org/demo",
    "alias": "demo",
    "desired_path": "demo",
    "clone_hints": {
      "url": "$remote"
    },
    "groups": []
  }
}
JSON

codemesh bootstrap "$manifest" --placeholders
codemesh access demo --json
codemesh tree
codemesh status demo --json
```

## Current Limitations

- Requires the Project to already be known in the local Project Registry.
- Hydrates only through explicit command access. It does not watch filesystem paths, intercept editor/Finder access, mount a workspace, start a daemon, or sync content in the background.
- Uses Git clone semantics and preserves Git's normal history, checkout, merge, and conflict behavior.
- Does not fetch project definitions from a remote manifest.

Back to [Command Catalog](../commands.md).
