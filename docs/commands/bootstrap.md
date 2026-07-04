---
summary: "Command reference for codemesh bootstrap."
read_when:
  - "Updating public command reference pages."
title: "codemesh bootstrap"
description: "Apply manifest topology without cloning project content."
permalink: /commands/bootstrap
---

# codemesh bootstrap

## Syntax

```sh
codemesh bootstrap <manifest-path> [--apply] [--json]
```

## Purpose

Read one Workspace Manifest JSON entry file or a directory of JSON entries, compare that shared topology with the registered machine workspace root, and report a plan.

By default bootstrap is a dry run. With `--apply`, it creates needed parent directories and local Project Registry rows only. It does not clone project content, create project placeholder directories, start a daemon, mount a filesystem, or sync files.

Use `--json` to emit the stable Command Result shape.

## Safe Example

```sh
demo="$(mktemp -d)"
export CODEMESH_HOME="$demo/codemesh-home"
workspace="$demo/workspace"
manifest="$demo/manifest"

mkdir -p "$manifest"
codemesh init "$workspace"
codemesh machine register "$workspace"

cat > "$manifest/demo.json" <<'JSON'
{
  "manifest_version": 1,
  "project": {
    "identity": "https://example.invalid/org/demo",
    "alias": "demo",
    "desired_path": "src/demo",
    "clone_hints": {
      "url": "https://example.invalid/org/demo.git"
    },
    "groups": []
  }
}
JSON

codemesh bootstrap "$manifest"
codemesh bootstrap "$manifest" --apply
codemesh tree
```

## Current Limitations

- Requires `codemesh machine register` first so the machine workspace root is known.
- Reads local manifest files only; it does not fetch, publish, or sync manifests.
- Refuses path conflicts without mutation.
- Creates parent directories and registry rows only. Project paths remain missing until [`codemesh hydrate`](hydrate.md).

Back to [Command Catalog](../commands.md).
