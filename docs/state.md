---
summary: "Local CodeMesh home, SQLite schema, Project Registry, readiness, policy, env, Agent Runs, and cleanup state model."
read_when:
  - Changing CodeMesh home, SQLite schema, migrations, Project Registry storage, readiness state, hydration, Agent Runs, cleanup, or local filesystem behavior.
---

# Local State Model

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
    <run-id>/
      workspace/
        codemesh-run.json
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
- `agent_runs`: prepared agent workspace audit rows.

Migrations are idempotent. Re-running `init` must not duplicate migrations or remove existing settings.

## Project Registry

`codemesh add <path>` records one existing Git checkout in `projects`.

Stored fields:

- `alias`: human CLI name; defaults to the checkout directory name and must be unique.
- `normalized_remote`: stable project identity anchor. GitHub SSH and HTTPS remotes normalize to the same value.
- `clone_url`: last known usable Git remote URL/path for cloning, preserving SSH or local transport when different from normalized identity. HTTP(S) userinfo and URL passwords are stripped before storage so local state does not keep embedded credentials.
- `local_path`: absolute path to the current checkout on this machine.

When migrating older state, CodeMesh backfills `clone_url` from a present checkout's `origin` when possible. If the checkout is already missing during migration, the normalized remote remains the fallback clone source until the project is re-added or rediscovered.

Presence is derived from the filesystem when reading the registry. The MVP does not store readiness, dirty, stale, env, hydration, or agent-prep state in project rows.

`codemesh hydrate <project>` resolves an existing registry row by alias and clones `clone_url` into `local_path` when that path is absent. Hydration may create the parent directory needed for the desired path, but it must not create placeholder project directories for other missing rows. If `local_path` already contains files, CodeMesh refuses to overwrite it with an actionable path-conflict error. If `local_path` is already a present checkout matching the registered project identity, hydration reports that no clone was needed.

`codemesh scan [workspace-root]` walks a requested workspace root for local Git checkouts and upserts discovered projects by normalized remote. If a known remote appears at a new absolute path, scan updates `local_path` and keeps the existing alias. New projects use the checkout directory name as the alias, with deterministic numeric suffixes when another project already owns that alias.

Scan reports added, updated, unchanged, and skipped candidates. Skips are runtime diagnostics only; unsupported Git candidates and nested repositories are not stored in the Project Registry.

## Readiness

Project readiness is derived when `tree`, `status`, `hydrate`, or Agent Prep reads the Project Registry. It is not stored in `projects`.

Normalized states:

- `present`: local path exists and no warning or blocker changes the summary state.
- `missing`: local path from the registry does not exist.
- `dirty`: source checkout has uncommitted or untracked changes; this is a warning, not a blocker.
- `stale`: remote freshness could not be checked, or the local base branch is behind or diverged from the fetched remote base.
- `blocked`: local path or requested base branch prevents safe use.

Diagnostics are split into warnings and blockers. Dirty source checkouts and stale local base branches are warnings so unrelated local work does not prevent temp-clone agent handoff. Missing local paths, fetch failures, and missing requested base branches are blockers for `status` until hydration or base selection exists.

`tree` consumes the same normalized states for local filesystem and dirty-checkout summaries. `status` runs the fuller readiness check, including fetch and base branch validation.

## Project Policy

Project policy is resolved at readiness time from the source checkout. See [Project Policy Reference](project-policy.md) for `.codemesh.yml` fields, defaults, validation, include-docs intent, and no-secret-values behavior.

Resolution:

1. `<project>/.codemesh.yml` when present.
2. Built-in defaults when absent.

Policy is metadata only. The MVP does not store effective policy in SQLite.

## Env Readiness

Env readiness is derived from the effective project policy.

Checks and warn/block handling follow [Project Policy Reference](project-policy.md). Env readiness never reads, writes, stores, or prints secret values.

## Agent Runs

`codemesh agent prepare <project>` creates one run directory under `agents/`.

Run layout:

- `workspace/`: temporary Git clone from the registered clone URL.
- `codemesh-run.json`: handoff metadata written inside the ready workspace.

Agent Prep resolves the project by alias, chooses the requested base or source policy/default base, fetches that base, and gates the handoff on the policy from the fetched base before cloning. Env file presence is still checked against the local source checkout, without reading file contents, because these files are usually untracked local setup. Readiness blockers stop prep before a run directory or clone is created. Warnings, including dirty source checkout and env warnings, are recorded and printed but do not block.

The clone checks out the requested `--base` when provided. Otherwise it checks out the repo-local policy base, falling back to `main`. CodeMesh uses Git for clone and checkout; it does not copy uncommitted source files, create Git worktrees, or replace Git state.

`codemesh-run.json` records metadata only:

- run id and ready path
- project alias, normalized remote, redacted clone URL, and source path
- effective base and profile
- warnings and blockers from readiness
- created timestamp

The `agent_runs` SQLite row stores the same metadata JSON for local audit and future cleanup/listing. Secret values are never included; env readiness records only missing file/key names and warn/block diagnostics, and clone URLs in metadata omit userinfo, query strings, and fragments.

`codemesh runs` reads `agent_runs` and lists prepared runs from local metadata. The user-facing row includes project alias, base, profile, created time, and workspace path so temporary workspaces are auditable without inspecting SQLite.

`codemesh clean --older-than <age>` removes only matching Agent Run directories created under `<codemesh-home>/agents`. Age is evaluated from the stored `created_at` timestamp. After successful deletion, CodeMesh removes the matching `agent_runs` rows so `runs` no longer reports cleaned workspaces.

Cleanup safety rules:

- only `agent_runs` rows are candidates
- workspace path must resolve inside configured CodeMesh `agents/`
- workspace path must be the `workspace/` child of a run directory whose basename matches the run id
- unsafe candidates abort cleanup before deletion
- missing managed run directories are treated as already gone and their metadata rows may be removed
- arbitrary paths outside CodeMesh-managed storage are never deleted

## Secrets

No secret values are stored, read, or materialized by `init`, Project Policy, or Env Readiness.
