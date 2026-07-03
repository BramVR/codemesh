# CodeMesh

One coherent code workspace across machines and agents.

CodeMesh keeps local Git project inventory, readiness checks, explicit hydration, and temporary agent workspaces understandable without replacing Git or storing secret values.

## MVP

- Project Registry for a canonical workspace tree.
- `codemesh status` showing missing, stale, dirty, and misconfigured projects.
- Explicit repo hydration from known Git remotes.
- Optional repo-local policy for agent base, env readiness, and handoff docs.
- Env readiness checks without secret materialization.
- Agent workspace provisioning with scoped access.

## Non-goals

- Replacing Git.
- Syncing `node_modules` or build outputs by default.
- Storing raw secrets in the CodeMesh index.
- General-purpose Dropbox clone.

## Quickstart

These commands use isolated local state and a local Git remote. They do not touch the user's normal `~/.codemesh`, `~/Projects`, GitHub, or secrets.

```sh
make build

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

./dist/codemesh init "$workspace"
./dist/codemesh add "$workspace/demo-project" --alias demo-project
./dist/codemesh tree
./dist/codemesh status demo-project --base main
./dist/codemesh agent prepare demo-project --base main --profile codex
./dist/codemesh runs
./dist/codemesh clean --older-than 0d
```

For the full current-vs-planned surface, see the [Command Catalog](docs/commands.md).

## Testing

```sh
make docs:list
make test
make e2e
make e2e-packaged
```

Use `make docs:list` before docs or behavior changes to find relevant `Read when` hints. The CLI e2e harness is documented in [docs/e2e.md](docs/e2e.md).

## Status

Local state bootstrap, Project Registry, Readiness status reporting, explicit missing-project hydration, Agent Prep, run listing, and guarded cleanup. The current command surface is documented in the [Command Catalog](docs/commands.md) and checked against CLI help.

## Docs

- [Command Catalog](docs/commands.md)
- [MVP Spec](docs/mvp.md)
- [Project Policy Reference](docs/project-policy.md)
- [Local State Model](docs/state.md)
- [CLI E2E Harness](docs/e2e.md)
- [Dropbox for Devs research](docs/research/dropbox-for-devs.md)
