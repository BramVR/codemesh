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
- `projects`: future project registry rows.
- `machines`: future machine facts.
- `scans`: future local discovery runs.
- `agent_runs`: future agent workspace audit rows.

Migrations are idempotent. Re-running `init` must not duplicate migrations or remove existing settings.

## Secrets

No secret values are stored, read, or materialized by `init`.
