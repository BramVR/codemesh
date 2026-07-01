---
summary: "Command reference for codemesh doctor."
read_when:
  - "Updating public command reference pages."
title: "codemesh doctor"
description: "Preflight agent handoff readiness without creating an Agent Run."
permalink: /commands/doctor
---

# codemesh doctor

## Syntax

```sh
codemesh doctor <project> [--base branch] [--strict] [--json]
```

## Purpose

Preflight whether one registered project is ready for agent handoff. Doctor uses the same Readiness Decision semantics as `codemesh agent prepare`, including dirty checkout warnings, remote/base checks, project policy, and env readiness diagnostics.

Doctor does not create a temporary agent workspace, write `codemesh-run.json`, or record an Agent Run. Use it before handoff automation when you want a fast green/warning/blocked answer without creating run state.

Use `--json` to print the stable Command Result shape. The JSON includes `command`, `exit_class`, command-level `diagnostics`, and a payload with `handoff`, `strict`, project identity, selected base, source path presence, and readiness diagnostics.

Use `--strict` when warning-only readiness should fail automation. Strict mode does not change normal Agent Prep behavior: dirty checkout and warn-mode env diagnostics still remain warnings for `codemesh agent prepare`.

## Output Contract

On green human output, stdout includes:

```txt
handoff: green
project: <project>
state: present
path: <path>
path_present: true
remote: <remote>
base: <base>
source_path_missing: false
warnings: none
blockers: none
```

When blockers exist, stdout prints `handoff: blocked` plus one `blocker: <code> <message>` line per blocker and exits non-zero.

When warnings exist without blockers, stdout prints `handoff: warning` plus one `warning: <code> <message>` line per warning. Without `--strict`, warning-only readiness exits zero. With `--strict`, warning-only readiness exits non-zero while the JSON `exit_class` remains `readiness-warning`.

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
codemesh doctor demo-project --base main
codemesh doctor demo-project --base main --strict --json
```

## Current Limitations

- Preflights one project at a time.
- Does not create an Agent Run, select handoff docs, clone a workspace, or print a ready path.
- Uses readiness diagnostics only; it does not provision credentials or materialize secret values.
- May use temporary Git operations to evaluate missing-source handoff readiness, but it removes those probes and records no run metadata.

Back to [Command Catalog](../commands.md).
