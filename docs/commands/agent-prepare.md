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
codemesh agent prepare <project> [--base branch] [--profile name] [--partial-clone] [--sparse path] [--env-provider fake] [--allow-env-scope scope] [--json]
```

## Purpose

Prepare a temporary agent workspace for one registered project. CodeMesh checks readiness, creates a temp clone under CodeMesh-managed agent storage through the selected Clone Strategy, records run metadata, and prints `handoff_docs: N` plus `ready_path`.

Preparation does not execute an agent command. Use [`codemesh agent run`](agent-run.md) after prepare when you want CodeMesh to run one explicit local command inside the prepared workspace and append command output paths to the run contract.

When `--base` is omitted, Agent Prep chooses the repo policy base, discoverable remote default branch, or `main` fallback in that order. `--profile` records the intended agent profile in run metadata.

By default Agent Prep uses `full-clone`: full Git history and a complete working tree. `--partial-clone` opts into Git partial clone with `blob:none`; repeatable `--sparse path` opts into Git sparse checkout for project-relative paths. If Git reports that the remote ignored or did not record the partial clone filter, Agent Prep fails with a clone diagnostic instead of recording misleading partial metadata. This is Git-native laziness only. It does not create placeholders, mounts, VFS behavior, daemon hydration, or file-level sync.

Use `--env-provider fake` with one or more `--allow-env-scope` flags to materialize fake-provider bindings into an agent-scoped env bundle. The bundle is written under the managed run directory, outside the prepared Git checkout. Binding setup uses [`codemesh env bind`](env-bind.md).

Use `--json` to emit the stable Command Result shape. The payload reports readiness, readiness diagnostics, handoff doc count, run id, ready path, run contract path, selected base, profile, resolved commit, selected clone strategy, and env materialization metadata. Blocked readiness uses exit class `readiness-blocked` and returns exit code 1 without creating an Agent Run. Secret values are not included.

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

`codemesh-run.json` is an Agent Run Contract. It records the contract version, producer/version metadata, run id, project identity, registered remote, selected base, resolved commit, checkout provenance, selected clone strategy, readiness decision, selected source mode, source checkout presence, declared `local_only_paths`, and diagnostics. Checkout provenance includes the fetched base, fetched commit, prepared HEAD, and whether the prepared HEAD matches the fetched commit. The default clone strategy is `full-clone`, with full history and a complete working tree. When partial or sparse options are used, the contract also records `filter` and `sparse_paths` strategy details. Handoff docs are recorded as paths and source metadata only; their file contents are not embedded.

When Project Policy declares toolchain requirements, the contract records each checked toolchain status as `present`, `missing`, or `unknown`. Project facts record the declared requirement. Host facts record the detected command name and version when available. CodeMesh reports and delegates toolchain readiness only; it does not install tools, run package-manager setup, write tool version files, create dependency directories, or build environments.

When env materialization runs, stdout also includes:

```txt
env_materialization: materialized
env_bundle: present
env_bundle_path: <path>
```

The Agent Run Contract records env requirement names, allowed scopes, materialization status, bundle presence/path/format, and `values: not-recorded`.

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
codemesh env bind demo-project CODEMESH_DEMO_KEY --provider fake --ref fake://demo-key --scope codex
codemesh agent prepare demo-project --base main --profile codex
codemesh agent prepare demo-project --base main --profile codex --env-provider fake --allow-env-scope codex
codemesh agent prepare demo-project --base main --profile codex --partial-clone --sparse README.md --json
```

## Current Limitations

- Defaults to temporary `full-clone` workspaces; partial clone and sparse checkout require explicit command flags. Shared object cache and worktree strategies are not implemented.
- Uses local policy and readiness checks; fake-provider bundles are deterministic test materialization only, and live secret providers are not implemented.
- Can prepare from the registered clone URL when the desired source checkout path is missing; unsafe registered clone sources are refused before Git runs, the Agent Run Contract records `project.source_mode: registry_clone` and `project.source_path_missing: true`, selected-base env key policy still applies, and required local env files are reported missing when no source checkout exists.
- Records local-only policy from the prepared clone; untracked machine-local directories in the source checkout are not copied into the prepared clone.
- Does not start or supervise a paid provider, remote agent, daemon, or long-lived process.
- Records handoff doc paths only; it does not include doc text in stdout, `codemesh-run.json`, or local state.
- Records env bundle metadata only; it does not include env values in stdout, `codemesh-run.json`, or local state metadata.
- Records toolchain status only; it does not install tools or build environments.
- Does not sync prepared workspaces between machines.

Back to [Command Catalog](../commands.md).
