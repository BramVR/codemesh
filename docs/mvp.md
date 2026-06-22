# MVP Spec

## Goal

CodeMesh v0 makes agentic development handoff fast and explicit.

Primary workflow:

```sh
codemesh agent prepare goggquote --base main --profile codex
```

Expected result:

- fresh temporary clone from the correct remote/base
- source checkout warnings surfaced
- env readiness checked
- project docs discovered
- ready workspace path printed
- run metadata stored for cleanup and audit

## Commands

### `codemesh init [workspace-root]`

Creates `~/.codemesh/codemesh.db` and records the default workspace root.

### `codemesh scan [workspace-root]`

Finds Git repos under the workspace root and adds or updates project records.

### `codemesh add <path> [--alias name]`

Adds one Git repo to the project index.

Project identity:

- normalized Git remote URL
- human alias
- current local path

### `codemesh tree`

Shows the canonical workspace index.

States:

- present
- missing
- dirty
- stale
- env-missing

### `codemesh status [project]`

Shows readiness detail for one project or the whole index.

Checks:

- local path exists
- Git remote exists
- fetch status
- dirty source checkout
- base branch exists
- optional `.codemesh.yml` env requirements

### `codemesh hydrate <project>`

Clones a missing project into its desired local path.

Does not create placeholders.

### `codemesh agent prepare <project> [--base branch] [--profile name]`

Creates a temp clone under `~/.codemesh/agents`.

Rules:

- clone from normalized remote
- checkout requested base or policy/default base
- warn if source checkout is dirty
- warn or block on env based on policy
- write `codemesh-run.json`
- print ready path

### `codemesh runs`

Lists prepared agent runs.

### `codemesh clean [--older-than 7d]`

Deletes old agent runs.

## Policy

Repo-local policy is optional.

File: `.codemesh.yml`

```yaml
agent:
  base: main
  env:
    mode: warn
    required_files:
      - .env.local
    required_keys:
      - OPENAI_API_KEY
  include_docs:
    - AGENTS.md
    - CONTEXT.md
    - docs/adr/**
```

If absent:

- infer remote default branch
- no env requirements
- discover common docs only

## State

Local state lives under:

```txt
~/.codemesh/
  codemesh.db
  agents/
```

Initial tables:

- `settings`
- `projects`
- `machines`
- `scans`
- `agent_runs`

## First Implementation Phases

1. Go module, CLI skeleton, config path handling.
2. SQLite store and migrations.
3. Git remote normalization and repo inspection.
4. `init`, `add`, `scan`, `tree`.
5. `status` readiness checks.
6. Policy parser.
7. `agent prepare`, `runs`, `clean`.
8. `hydrate`.

## Non-Goals

- daemon
- mount
- automatic placeholders
- secret materialization
- cloud sync
- UI
- file-level lazy hydration
