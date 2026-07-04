---
summary: "Current CLI command catalog, safe examples, and planned command boundary."
read_when:
  - Changing CLI commands, CLI help, quickstart examples, MVP command direction, or command-oriented docs.
---

# Command Catalog

CodeMesh command docs separate current commands from planned behavior. The current command list below is checked against top-level CLI help and the stable command reference pages by `TestCommandCatalogMatchesTopLevelHelp` and `TestCommandReferencePagesMatchCatalog`.

## Current Commands

- [`codemesh init [workspace-root]`](commands/init.md)
- [`codemesh add <path> [--alias name]`](commands/add.md)
- [`codemesh scan [workspace-root]`](commands/scan.md)
- [`codemesh tree [--json]`](commands/tree.md)
- [`codemesh status [project] [--base branch] [--json]`](commands/status.md)
- [`codemesh doctor <project> [--base branch] [--strict] [--json]`](commands/doctor.md)
- [`codemesh hydrate <project> [--partial-clone] [--sparse path] [--json]`](commands/hydrate.md)
- [`codemesh bootstrap <manifest-path> [--apply] [--json]`](commands/bootstrap.md)
- [`codemesh env bind <project> <requirement> --provider fake --ref secret-ref --scope scope`](commands/env-bind.md)
- [`codemesh machine register [workspace-root] [--json]`](commands/machine-register.md)
- [`codemesh agent prepare <project> [--base branch] [--profile name] [--partial-clone] [--sparse path] [--env-provider fake] [--allow-env-scope scope] [--json]`](commands/agent-prepare.md)
- [`codemesh agent run <run-id> --label label [--timeout duration] -- <command...>`](commands/agent-run.md)
- [`codemesh runs`](commands/runs.md)
- [`codemesh clean [--older-than age]`](commands/clean.md)

Each linked reference page documents the current syntax, purpose, safe local examples, and current limitations. If a command does not appear in this list, it is not part of the runnable MVP surface.

`codemesh env bind` stores provider-specific secret references in local CodeMesh state, outside repo-local Project Policy. The first runnable provider is deterministic `fake`, for local tests and agent-scoped bundle proof only.

`codemesh hydrate` and `codemesh agent prepare` use the `full-clone` Clone Strategy by default: full Git history and a complete working tree. `--partial-clone` opts into Git partial clone with `blob:none`; repeatable `--sparse path` opts into Git sparse checkout. These are Git-native lazy clone/checkouts, not placeholders, mounts, VFS behavior, daemon hydration, or file-level sync.

`codemesh agent prepare` prints the ready workspace path plus `handoff_docs: N`, where `N` is the count of selected handoff docs. The detailed handoff doc metadata is path-only in the versioned `codemesh-run.json` Agent Run Contract; CodeMesh does not copy docs, embed doc contents, or read doc contents into metadata. With `--env-provider fake` and matching `--allow-env-scope`, Agent Prep can materialize an env bundle under the managed run directory, outside the prepared Git checkout. The contract records clone strategy metadata, requirement names, allowed scopes, bundle presence/path, and `values: not-recorded`.

`codemesh doctor` runs the same handoff readiness preflight as Agent Prep but does not create an agent workspace, write `codemesh-run.json`, or record an Agent Run. `--strict` makes warning-only readiness exit non-zero for automation while preserving normal Agent Prep warning behavior.

Repo-local Project Policy may declare toolchain readiness requirements. `doctor` reports checked toolchain status in human and JSON output, and `agent prepare` records status in the Agent Run Contract. Present host detections record command names and versions while project facts keep the declared requirement separate. Toolchain readiness is report/delegate only; CodeMesh does not install tools or build environments.

`codemesh agent run` is separate from prepare. Prepare creates and records the workspace; run executes one explicitly supplied local command inside that prepared workspace, captures stdout/stderr to managed files, and records command metadata without env values.

## Safe Local Example

This example uses a temp CodeMesh home and local Git remote only. It does not touch the user's normal CodeMesh home, personal workspace, GitHub account, or secrets.

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
codemesh tree
codemesh tree --json
codemesh status demo-project --base main
codemesh status demo-project --base main --json
codemesh doctor demo-project --base main
codemesh doctor demo-project --base main --strict --json
codemesh env bind demo-project CODEMESH_DEMO_KEY --provider fake --ref fake://demo-key --scope codex
codemesh agent prepare demo-project --base main --profile codex
codemesh agent prepare demo-project --base main --profile codex --env-provider fake --allow-env-scope codex
codemesh agent prepare demo-project --base main --profile codex --json
run_id="$(codemesh runs | awk '/^- / {print $2; exit}')"
codemesh agent run "$run_id" --label workspace-root -- git rev-parse --show-toplevel
codemesh runs
codemesh clean --older-than 0d
```

Use `codemesh scan "$workspace"` instead of `codemesh add ...` when discovering all Git checkouts under a workspace root.

Use `codemesh hydrate <project>` after a project is already registered and its desired local path is missing. Hydration clones the registered remote into that path and refuses existing non-empty path conflicts.

Use `codemesh bootstrap <manifest-path>` after `codemesh machine register` to preview shared Workspace Manifest topology on the current machine. Add `--apply` to create only parent directories and local Project Registry rows; project content stays missing until explicit hydration.

## Planned Or Unimplemented

These directions remain product direction, not runnable commands today:

- workspace manifest export/import commands for Git-backed desired topology
- multi-machine sync
- synced manifests or remote project indexes
- live secret providers or env file writing
- daemon, mount, UI, placeholders, or file-level lazy hydration
- generic cloud drive behavior

Research docs may sketch future commands for those areas. Treat them as planned until they appear in current CLI help and this catalog.

The current Workspace Manifest input for `bootstrap` is one JSON entry file or a directory of small per-project desired-topology entries: project identity, alias, relative desired path, clone hints, and grouping. It must not export observed readiness, dirty/stale status, Agent Runs, machine facts, env values, or secret values. Manifest export/import commands remain planned until they appear in CLI help and this catalog.
