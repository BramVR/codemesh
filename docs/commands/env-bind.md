---
summary: "Command reference for codemesh env bind."
read_when:
  - "Updating public command reference pages."
title: "codemesh env bind"
description: "Bind one logical env requirement to a private provider reference."
permalink: /commands/env-bind
---

# codemesh env bind

## Syntax

```sh
codemesh env bind <project> <requirement> --provider fake --ref secret-ref --scope scope
```

## Purpose

Store a private mapping from one project-declared logical env requirement to a provider-specific secret reference. Bindings live in the local CodeMesh SQLite state, not in repo-local Project Policy.

The first provider is deterministic `fake`. It exists so Agent Prep can prove scope checks, bundle metadata, and no-secret-value behavior without live secret services.

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
cat > "$seed/.codemesh.yml" <<'EOF'
agent:
  env:
    mode: block
    required_keys:
      - CODEMESH_DEMO_KEY
EOF
git -C "$seed" add README.md .codemesh.yml
git -C "$seed" -c user.name='CodeMesh Demo' -c user.email='demo@example.invalid' commit -m 'Initial demo'
git clone --bare "$seed" "$remote"
git clone "$remote" "$workspace/demo-project"

codemesh init "$workspace"
codemesh add "$workspace/demo-project" --alias demo-project
codemesh env bind demo-project CODEMESH_DEMO_KEY --provider fake --ref fake://demo-key --scope codex
codemesh agent prepare demo-project --base main --env-provider fake --allow-env-scope codex
```

## Current Limitations

- Supports the deterministic `fake` provider only.
- Stores provider references, scopes, and requirement names only; it does not store raw secret values.
- Does not write repo-local env files.
- Does not configure live providers such as 1Password or SOPS.
- Scope denial blocks Agent Prep when the requested allowed scopes do not intersect the binding scopes.

Back to [Command Catalog](../commands.md).
