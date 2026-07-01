---
summary: "MVP product direction, current modules, command surface, policy, state, phases, and non-goals."
read_when:
  - Changing product scope, MVP modules, command direction, Agent Prep, Agent Run Contract, Project Registry, Readiness, Env Readiness, policy, state, or non-goals.
---

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
- project docs discovered from the prepared clone
- ready workspace path and `handoff_docs: N` count printed
- run metadata stored for cleanup and audit

## Core Modules

### Project Registry

Owns the canonical workspace view.

Responsibilities:

- project identity
- alias conflicts
- normalized remotes
- desired local paths
- present and missing projects
- scan and add behavior
- project persistence

Used by:

- `scan`
- `add`
- `tree`
- `status`
- `hydrate`
- `agent prepare`

### Readiness

Owns project and agent handoff diagnostics.

Responsibilities:

- present/missing state
- stale/fetch state
- dirty source checkout warnings
- base branch blockers
- env readiness results
- final warn/block action

Used by:

- `tree`
- `status`
- `agent prepare`

### Env Readiness

Owns secret-free env checks.

Responsibilities:

- required env files
- required env keys
- warn/block mode
- missing-env diagnostics

Non-responsibilities:

- reading secret values
- writing `.env` files
- integrating secret backends

Used by:

- Readiness

### Agent Prep

Owns agent workspace prep.

Responsibilities:

- resolve project through Project Registry
- resolve effective project policy
- run Readiness
- create temp clone
- checkout base
- discover project docs
- write run metadata
- return ready path and diagnostics

Used by:

- `agent prepare`

### Agent Run Contract

Owns versioned agent run metadata.

Responsibilities:

- contract version and producer metadata
- project and checkout provenance
- readiness diagnostics and handoff docs
- command execution records
- redaction, validation, JSON encoding/decoding, and file writes
- State Store metadata shape and run-list projections

Used by:

- `agent prepare`
- `agent run`
- `runs`
- `clean`

## Commands

The current runnable command surface is maintained in [Command Catalog](commands.md). Future command ideas in research or roadmap docs are planned until they appear in that catalog and top-level CLI help.

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

### `codemesh status [project] [--base branch]`

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
- resolve default and policy-selected handoff docs from the prepared clone
- write `codemesh-run.json`
- print ready path and handoff doc count

### `codemesh runs`

Lists prepared agent runs.

### `codemesh clean [--older-than age]`

Deletes old agent runs.

## Policy

Repo-local policy is optional.

File: `.codemesh.yml`

See [Project Policy Reference](project-policy.md) for the current `.codemesh.yml` interface, defaults, env readiness behavior, include-docs intent, and no-secret-values rule.

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

## Implemented MVP Phases

1. Go module, CLI skeleton, config path handling.
2. SQLite store and migrations.
3. Git remote normalization and repo inspection.
4. Project Registry.
5. Env Readiness and policy parser.
6. Readiness.
7. `init`, `add`, `scan`, `tree`, `status`.
8. Agent Prep, `agent prepare`, `runs`, `clean`.
9. `hydrate`.
10. Machine Registry and `machine register`.

## Planned Later

- multi-machine sync
- synced manifests or remote project indexes
- secret materialization or env file writing
- daemon, mount, UI, placeholders, or file-level lazy hydration

## Non-Goals

- daemon
- mount
- automatic placeholders
- secret materialization
- cloud sync
- UI
- file-level lazy hydration
