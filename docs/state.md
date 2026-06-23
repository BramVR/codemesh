# Local State Model

Read when: changing CodeMesh home, SQLite schema, migrations, or local filesystem behavior.

## Home

CodeMesh local state lives in CodeMesh home.

Resolution:

1. `CODEMESH_HOME` when set.
2. `$HOME/.codemesh` otherwise.

Tests and e2e must set `CODEMESH_HOME` so user state is never touched.

Layout:

```txt
<codemesh-home>/
  codemesh.db
  agents/
```

`init` creates the home directory, `agents/`, and `codemesh.db`.

## Database

SQLite is local and machine-owned. It stores metadata only.

Initial tables:

- `schema_migrations`: applied migration versions.
- `settings`: key/value settings, including `default_workspace_root`.
- `projects`: Project Registry rows.
- `machines`: future machine facts.
- `scans`: future local discovery runs.
- `agent_runs`: future agent workspace audit rows.

Migrations are idempotent. Re-running `init` must not duplicate migrations or remove existing settings.

## Project Registry

`codemesh add <path>` records one existing Git checkout in `projects`.

Stored fields:

- `alias`: human CLI name; defaults to the checkout directory name and must be unique.
- `normalized_remote`: stable project identity anchor. GitHub SSH and HTTPS remotes normalize to the same value.
- `local_path`: absolute path to the current checkout on this machine.

Presence is derived from the filesystem when reading the registry. The MVP does not store readiness, dirty, stale, env, hydration, or agent-prep state in project rows.

`codemesh scan [workspace-root]` walks a requested workspace root for local Git checkouts and upserts discovered projects by normalized remote. If a known remote appears at a new absolute path, scan updates `local_path` and keeps the existing alias. New projects use the checkout directory name as the alias, with deterministic numeric suffixes when another project already owns that alias.

Scan reports added, updated, unchanged, and skipped candidates. Skips are runtime diagnostics only; unsupported Git candidates and nested repositories are not stored in the Project Registry.

## Secrets

No secret values are stored, read, or materialized by `init`.
