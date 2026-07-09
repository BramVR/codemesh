---
summary: "Command reference for codemesh bootstrap."
read_when:
  - "Updating public command reference pages."
title: "codemesh bootstrap"
description: "Clone missing registered projects through the Hydration Planner."
permalink: /commands/bootstrap
---

# codemesh bootstrap

## Syntax

```sh
codemesh bootstrap [--all | project... | <manifest-path>] [--dry-run|--apply] [--json]
```

## Purpose

Clone missing registered Projects into their desired local paths after the shared Hydration Planner classifies clone/refusal actions. Use `--all` for every registered Project, or pass one or more Project aliases.

Bootstrap also keeps the manifest topology path from earlier MVP slices: when the positional argument is a manifest file or directory, CodeMesh reads the Workspace Manifest entries, compares them with the registered machine workspace root, inserts or updates local Project Registry rows on `--apply`, and then consumes the same planned clone/refusal actions.

By default bootstrap is a dry run; `--dry-run` makes that explicit. The dry-run output includes planned clones for missing Projects and refusals for path conflicts, unsafe paths, or unknown Projects. With `--apply`, bootstrap refuses blockers before invoking Git, then clones planned missing Projects from their registered clone source. It does not create project placeholders, start a daemon, mount a filesystem, sync arbitrary files, install tools, or touch env materialization.

Use `--json` to emit the stable Command Result shape.

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
    "desired_path": "src/demo",
    "clone_hints": {
      "url": "$remote"
    },
    "groups": []
  }
}
JSON

codemesh bootstrap "$manifest"
codemesh bootstrap "$manifest" --dry-run
codemesh bootstrap "$manifest" --apply
codemesh bootstrap --all --dry-run
codemesh bootstrap demo --apply
codemesh tree
```

## Current Limitations

- Requires `codemesh machine register` first so the machine workspace root is known.
- Reads local manifest files only; it does not fetch, publish, or sync manifests.
- Refuses path conflicts and unsafe paths without mutation.
- Executes Git clones only for planned missing registered Projects. Existing present Projects are left alone, and conflicts are refused before Git.

Back to [Command Catalog](../commands.md).
