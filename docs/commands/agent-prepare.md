---
summary: "Command reference for codemesh agent prepare."
read_when:
  - "Updating public command reference pages."
title: "codemesh agent prepare"
description: "Prepare one temporary agent workspace from a registered project."
permalink: /commands/agent-prepare
---

# codemesh agent prepare

## Syntax

```sh
codemesh agent prepare <project> [--base branch] [--profile name]
```

## Purpose

Prepare a temporary agent workspace for one registered project. CodeMesh checks readiness, creates a temp clone under CodeMesh-managed agent storage, records run metadata, and prints `handoff_docs: N` plus `ready_path`.

When `--base` is omitted, CodeMesh lets Agent Prep choose its current default base. `--profile` records the intended agent profile in run metadata.

## Output Contract

On success, stdout includes:

```txt
agent workspace ready
project: <project>
base: <base>
warnings: none
blockers: none
handoff_docs: <count>
ready_path: <path>
```

When `--profile` is set, stdout also includes `profile: <profile>`.
When warnings exist, stdout prints one `warning: <code> <message>` line per warning instead of `warnings: none`.

`handoff_docs` is a count only. The selected doc paths and their source metadata live in `<ready_path>/codemesh-run.json`.

`codemesh-run.json` also records the run id, project identity, registered remote, selected base, resolved commit, readiness decision, and diagnostics. Handoff docs are recorded as paths and source metadata only; their file contents are not embedded.

## Handoff Docs

Agent Prep records handoff docs as project-relative paths only. It does not copy docs, embed doc contents, or read doc contents into metadata.

Default handoff docs are discovered from the prepared clone after checkout: `AGENTS.md`, `CONTEXT.md`, `README.md`, and Markdown files directly under `docs/adr/`.

Repo policy may add docs through `agent.include_docs`. Policy entries resolve against the prepared clone, not the source checkout, so metadata only points at files available to the agent on the selected base. Policy-selected entries record their original pattern in metadata.

If a policy pattern is valid but matches no available handoff docs, Agent Prep prints and records a `handoff-doc-missing` warning. This warning does not block the workspace.

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
codemesh agent prepare demo-project --base main --profile codex
```

## Current Limitations

- Prepares temporary clones only; it does not create shared worktrees from the source checkout.
- Uses local policy and readiness checks; it does not provision credentials or materialize secret values.
- Can prepare from the registered clone URL when the desired source checkout path is missing; selected-base env key policy still applies, and required local env files are reported missing when no source checkout exists.
- Does not start or manage an agent process.
- Records handoff doc paths only; it does not include doc text in stdout, `codemesh-run.json`, or local state.
- Does not sync prepared workspaces between machines.

Back to [Command Catalog](../commands.md).
