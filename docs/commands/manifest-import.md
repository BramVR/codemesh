---
summary: "Command reference for codemesh manifest import."
read_when:
  - "Updating public command reference pages."
title: "codemesh manifest import"
description: "Import a portable canonical Workspace Manifest."
permalink: /commands/manifest-import
---

# codemesh manifest import

## Syntax

```sh
codemesh manifest import <path>
```

## Purpose

Validate and persist a portable Workspace Manifest into the local Project Registry. Import checks schema version, unknown fields, aliases, normalized remotes, clone hints, relative desired paths, and secret-free topology before writing rows.

Import separates canonical Project identity from machine-local placement: each project's relative desired path is resolved under this machine's registered workspace root.

## Safe Example

```sh
demo="$(mktemp -d)"
export CODEMESH_HOME="$demo/codemesh-home"
workspace="$demo/workspace-b"
manifest="$demo/workspace-manifest.json"

cat > "$manifest" <<'JSON'
{
  "manifest_version": 1,
  "projects": [
    {
      "identity": "https://example.invalid/org/demo",
      "alias": "demo",
      "desired_path": "src/demo",
      "clone_hints": {
        "url": "https://example.invalid/org/demo.git"
      },
      "groups": []
    }
  ]
}
JSON

codemesh init "$workspace"
codemesh machine register "$workspace"
codemesh manifest import "$manifest"
codemesh tree
```

## Current Limitations

- Requires `codemesh machine register` first so relative paths can be placed under the local workspace root.
- Reads local manifest files only; it does not fetch, publish, or sync manifests.
- Writes Project Registry rows only. It does not clone project content, create placeholders, or create project directories.
- Refuses unsupported schema, unknown fields, unsafe clone hints, duplicate aliases, duplicate identities, duplicate desired paths, and non-relative paths before mutation.

Back to [Command Catalog](../commands.md).
