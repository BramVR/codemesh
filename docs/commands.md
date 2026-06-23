# Command Catalog

Read when: changing CLI commands, CLI help, quickstart examples, MVP command direction, or command-oriented docs.

CodeMesh command docs separate current commands from planned behavior. The current command list below is checked against top-level CLI help by `TestCommandCatalogMatchesTopLevelHelp`.

## Current Commands

- `codemesh init [workspace-root]`
- `codemesh add <path> [--alias name]`
- `codemesh scan [workspace-root]`
- `codemesh tree`
- `codemesh status [project] [--base branch]`
- `codemesh hydrate <project>`
- `codemesh agent prepare <project> [--base branch] [--profile name]`
- `codemesh runs`
- `codemesh clean [--older-than age]`

## Safe Local Example

This example uses a temp CodeMesh home and local Git remote only. It does not touch the user's normal `~/.codemesh`, `~/Projects`, GitHub, or secrets.

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
codemesh runs
codemesh clean --older-than 0d
```

Use `codemesh scan "$workspace"` instead of `codemesh add ...` when discovering all Git checkouts under a workspace root.

Use `codemesh hydrate <project>` after a project is already registered and its desired local path is missing. Hydration clones the registered remote into that path and refuses existing non-empty path conflicts.

## Planned Or Unimplemented

These directions remain product direction, not runnable commands today:

- machine registration and multi-machine sync
- synced manifests or remote project indexes
- secret materialization or env file writing
- daemon, mount, UI, placeholders, or file-level lazy hydration
- generic cloud drive behavior

Research docs may sketch future commands for those areas. Treat them as planned until they appear in current CLI help and this catalog.
