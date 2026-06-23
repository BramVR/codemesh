# CodeMesh

One coherent code workspace across machines and agents.

CodeMesh keeps a developer's project tree, repo inventory, machine-local setup, and safe config materialization in sync without replacing Git.

## MVP

- Project index for a canonical workspace tree.
- `codemesh status` showing missing, stale, dirty, and misconfigured projects.
- Lazy repo setup from known Git remotes.
- Per-project policy for ignored/local-only paths.
- Env readiness checks without secret materialization.
- Agent workspace provisioning with scoped access.

## Non-goals

- Replacing Git.
- Syncing `node_modules` or build outputs by default.
- Storing raw secrets in the CodeMesh index.
- General-purpose Dropbox clone.

## First CLI Sketch

```sh
codemesh init ~/Projects
codemesh scan ~/Projects
codemesh tree
codemesh status
codemesh add ~/Projects/acme-site
codemesh hydrate acme-site
codemesh agent prepare acme-site --profile codex
```

## Testing

```sh
make test
make e2e
```

The CLI e2e harness is documented in [docs/e2e.md](docs/e2e.md).

## Status

Scaffold plus local state bootstrap, Project Registry tracer bullets, Readiness status reporting, explicit missing-project hydration, and first Agent Prep flow. The CLI supports help/version smoke behavior, `codemesh init [workspace-root]`, `codemesh add <path> [--alias name]`, `codemesh scan [workspace-root]`, `codemesh tree`, `codemesh status [project] [--base branch]`, `codemesh hydrate <project>`, and `codemesh agent prepare <project> [--base branch] [--profile name]`.

## Research

- [Dropbox for Devs](docs/research/dropbox-for-devs.md)
- [MVP Spec](docs/mvp.md)
- [Project Policy Reference](docs/project-policy.md)
