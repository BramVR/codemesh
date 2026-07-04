---
summary: "Command reference for codemesh target export."
read_when:
  - "Updating public command reference pages."
title: "codemesh target export"
description: "Export a target-ready workspace spec without live provider integration."
permalink: /commands/target-export
---

# codemesh target export

## Syntax

```sh
codemesh target export <target-name> --scope scope [--kind kind] [--workspace-root path] [--json]
```

## Purpose

Export a local Workspace Target spec for future cloud, dev, or agent destinations. The spec packages Workspace Manifest topology, registered machine facts, target facts, and scoped Env Binding references. It does not include raw env values, observed readiness state, dirty or stale source checkout state, Agent Runs, or secret values.

The command is a contract tracer only. Coder, DevPod, and Daytona can become future adapters over this shape, but this command does not call them or mutate any external system.

Use `--json` to emit the stable Command Result shape.

## Safe Example

```sh
demo="$(mktemp -d)"
export CODEMESH_HOME="$demo/codemesh-home"
workspace="$demo/workspace"
manifest="$demo/manifest"

mkdir -p "$manifest"
cat > "$manifest/demo.json" <<'JSON'
{
  "manifest_version": 1,
  "project": {
    "identity": "https://example.invalid/org/demo-project",
    "alias": "demo-project",
    "desired_path": "demo-project",
    "clone_hints": {
      "url": "https://example.invalid/org/demo-project.git"
    },
    "groups": []
  }
}
JSON

codemesh init "$workspace"
codemesh machine register "$workspace"
codemesh bootstrap "$manifest" --apply
codemesh env bind demo-project CODEMESH_DEMO_KEY --provider fake --ref fake://demo-key --scope codex
codemesh target export local-fake-target --scope codex --json
```

## Current Limitations

- Requires `codemesh machine register` first so local machine facts are available.
- Reads local Project Registry rows and local repo policy only.
- Includes provider references, requirement names, and scopes only; it never resolves or materializes env values.
- Does not call Coder, DevPod, Daytona, live secret providers, remote APIs, custom runners, or paid infrastructure.
- Does not import, sync, hydrate, clone, or prepare an agent run.

Back to [Command Catalog](../commands.md).
