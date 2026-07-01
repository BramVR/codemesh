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
- [`codemesh tree`](commands/tree.md)
- [`codemesh status [project] [--base branch]`](commands/status.md)
- [`codemesh hydrate <project>`](commands/hydrate.md)
- [`codemesh machine register [workspace-root] [--json]`](commands/machine-register.md)
- [`codemesh agent prepare <project> [--base branch] [--profile name]`](commands/agent-prepare.md)
- [`codemesh agent run <run-id> --label label [--timeout duration] -- <command...>`](commands/agent-run.md)
- [`codemesh runs`](commands/runs.md)
- [`codemesh clean [--older-than age]`](commands/clean.md)

Each linked reference page documents the current syntax, purpose, safe local examples, and current limitations. If a command does not appear in this list, it is not part of the runnable MVP surface.

`codemesh agent prepare` prints the ready workspace path plus `handoff_docs: N`, where `N` is the count of selected handoff docs. The detailed handoff doc metadata is path-only in `codemesh-run.json`; CodeMesh does not copy docs, embed doc contents, or read doc contents into metadata.

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
codemesh status demo-project --base main
codemesh agent prepare demo-project --base main --profile codex
run_id="$(codemesh runs | awk '/^- / {print $2; exit}')"
codemesh agent run "$run_id" --label workspace-root -- git rev-parse --show-toplevel
codemesh runs
codemesh clean --older-than 0d
```

Use `codemesh scan "$workspace"` instead of `codemesh add ...` when discovering all Git checkouts under a workspace root.

Use `codemesh hydrate <project>` after a project is already registered and its desired local path is missing. Hydration clones the registered remote into that path and refuses existing non-empty path conflicts.

## Planned Or Unimplemented

These directions remain product direction, not runnable commands today:

- multi-machine sync
- synced manifests or remote project indexes
- secret materialization or env file writing
- daemon, mount, UI, placeholders, or file-level lazy hydration
- generic cloud drive behavior

Research docs may sketch future commands for those areas. Treat them as planned until they appear in current CLI help and this catalog.
