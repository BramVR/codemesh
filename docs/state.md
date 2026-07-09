---
summary: "Local CodeMesh home, SQLite schema, Project Registry, readiness, policy, env, toolchain, Agent Runs, and cleanup state model."
read_when:
  - Changing CodeMesh home, SQLite schema, migrations, Project Registry storage, readiness state, hydration, toolchain readiness, Agent Runs, cleanup, or local filesystem behavior.
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
      env/
        env.bundle
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
- `machines`: local machine identity and mutable machine facts.
- `scans`: future local discovery runs.
- `agent_runs`: prepared agent workspace audit rows.
- `env_bindings`: local private mappings from logical env requirements to provider references.

Migrations are idempotent. Re-running `init` must not duplicate migrations or remove existing settings.

## Project Registry

`codemesh add <path>` records one existing Git checkout in `projects`.

Stored fields:

- `alias`: human CLI name; defaults to the checkout directory name and must be unique.
- `normalized_remote`: stable project identity anchor. GitHub SSH and HTTPS remotes normalize to the same value.
- `clone_url`: last known usable Git remote URL/path for cloning, preserving SSH or local transport when different from normalized identity. HTTP(S) userinfo and URL passwords are stripped before storage so local state does not keep embedded credentials.
- `canonical_path`: absolute desired path for the current machine's canonical workspace layout.
- `local_path`: absolute observed checkout path on this machine. For local-only rows it is also the desired path; for imported canonical rows it may differ when local discovery finds the same Project somewhere else.
- `source`: `canonical` for manifest/bootstrap-imported Projects, `local-only` for Projects learned only through `add` or `scan`.

When migrating older state, CodeMesh backfills `clone_url` from a present checkout's `origin` when possible. If the checkout is already missing during migration, the normalized remote remains the fallback clone source until the project is re-added or rediscovered.

Presence is derived from the filesystem when reading the registry. `canonical_path_present` reports whether the desired layout path exists. `machine_path_present` reports whether the observed current-machine checkout path exists. The MVP does not store readiness, dirty, stale, env, hydration, or agent-prep state in project rows.

## Machine Registry

`codemesh machine register [workspace-root] [--name name]` creates or reuses one persistent local machine ID in the State Store.

Stored fields:

- `machine_id`: locally minted stable machine identity.
- `name`: user-facing display name. New registrations default to hostname when omitted; re-registration without `--name` preserves the existing display name.
- `hostname`: current host name.
- `os`: Go runtime OS name.
- `architecture`: Go runtime architecture name.
- `codemesh_home`: local CodeMesh home for this machine.
- `workspace_root`: local workspace root for this machine.
- `registered_at`: first registration timestamp.
- `updated_at`: latest registration timestamp.

Re-running registration updates mutable facts without changing `machine_id` or `registered_at`. `codemesh machine status [--json]` reads these persisted facts for troubleshooting. Machine status does not store readiness, freshness, dirty state, or env readiness; those remain derived when registry/readiness commands run.

Machine rows are local observed state. They are not exported to shared topology by default.

The Hydration Planner is the shared safety model for bootstrap previews, placeholder materialization, bootstrap execution, and explicit hydration. It reads the local Project Registry, current Machine workspace root when available, and filesystem path shape only; it does not contact Git remotes or shell out to Git. It classifies each planned action as present, placeholder, missing, path-conflict, unsafe-path, or unknown-project, with `clone`, `none`, or `refuse` as the execution action.

`codemesh hydrate <project>` resolves an existing registry row by alias, consumes the Hydration Planner, and clones `clone_url` into the planned desired path when that path is absent or contains an unmodified CodeMesh-owned placeholder. `codemesh bootstrap --all` and `codemesh bootstrap <project>... --apply` consume the same planned clone/refusal actions for registered Projects; `codemesh bootstrap <manifest-path> --apply` first imports manifest topology, then executes the same planned clone actions. Hydration defaults to the `full-clone` Clone Strategy: full Git history and a complete working tree. `--partial-clone` and repeatable `--sparse path` explicitly opt into Git-native partial clone and sparse checkout for `hydrate`. Hydration and bootstrap may create the parent directory needed for the desired path. If the target path already contains user files, a changed placeholder, or a mismatched placeholder, CodeMesh refuses to overwrite it with an actionable path-conflict error before invoking Git. If the target path already looks like a present checkout, hydration reports that no clone was needed.

`codemesh bootstrap --placeholders` materializes metadata-only placeholder directories for planned missing Projects. Each placeholder contains `.codemesh-placeholder.json`, a visible `CODEMESH_PLACEHOLDER.txt` note that says the directory is not a Git checkout, and an intentionally invalid `.git` file that stops Git from walking up to an ancestor worktree. Placeholders make the canonical workspace structure visible to editors and terminals without pretending source content exists; ordinary Git commands run inside a placeholder fail because the placeholder is not a valid repository. This is explicit sentinel materialization only, not a mount, daemon, file-level lazy hydration trigger, or automatic background sync.

`codemesh scan [workspace-root]` walks a requested workspace root for local Git checkouts and upserts discovered projects by normalized remote. If a known remote appears at a new absolute path, scan updates `local_path` and keeps the existing alias. For `canonical` Projects, scan preserves `canonical_path` and `source` so local discovery records current-machine placement without rewriting imported manifest layout. For `local-only` Projects, scan updates both observed and canonical path because no portable desired layout exists yet. New projects use the checkout directory name as the alias, with deterministic numeric suffixes when another project already owns that alias.

Scan reports added, updated, unchanged, and skipped candidates. Skips are runtime diagnostics only; unsupported Git candidates and nested repositories are not stored in the Project Registry.

## Workspace Manifest

Workspace Manifest files are desired shared topology, not observed local state. The current command surface uses one deterministic JSON file with `manifest_version` and sorted `projects`. Each project records project identity, alias, relative desired path, clone hints, and grouping. Export derives desired paths from Project Registry rows relative to the registered machine workspace root and omits machine-local paths from the portable shape.

`codemesh manifest export [--output path]` writes that portable file from local SQLite metadata. `codemesh manifest import <path>` validates schema version, unknown fields, aliases, normalized remotes, clone hints, relative paths, duplicate topology, and no-secret-value rules before mutating SQLite. Import separates canonical Project identity from machine-local placement by resolving each desired path under the importing machine's registered workspace root and marking rows as `source: canonical`.

SQLite remains the local operational store. The manifest is the portable reviewable interface between machines.

Manifest files must not include readiness results, dirty/stale state, Agent Runs, machine facts, env values, or secret values.

Reconciliation compares Workspace Manifest desired topology with the local State Store machine workspace root and observed Project Registry rows. It produces a drift plan: added, missing, moved, conflicting, and unchanged projects. Desired paths are resolved under the registered machine workspace root. Path conflicts are blockers.

`codemesh bootstrap <manifest-path>` reads one legacy manifest entry file or a directory of JSON entries and reports the plan by default; `--dry-run` is the explicit spelling for the same preview. The output includes the reconciliation drift plan plus the shared Hydration Planner clone/refusal actions. With `--placeholders`, bootstrap refuses blockers before mutation, inserts or updates local Project Registry rows for desired topology, and writes CodeMesh-owned placeholder sentinels for planned missing Projects. With `--apply`, bootstrap refuses blockers before mutation, creates only needed parent directories, inserts or updates local Project Registry rows for desired topology, and clones planned missing Projects from their clone hints or registered clone URLs. `codemesh bootstrap --all` and `codemesh bootstrap <project>...` skip manifest import and operate on already registered Projects. Bootstrap does not start a daemon, mount a filesystem, sync arbitrary files, install tools, or touch env materialization.

`codemesh target export <target-name> --scope scope --json` is a local contract tracer over Workspace Manifest, Machine Registry, Project Registry, and Env Binding. It packages desired topology, local machine facts, explicit target facts, and scope-matched env binding references. It does not include observed readiness state, dirty/stale source state, Agent Runs, raw env values, secret values, or fake-provider materialized values. Coder, DevPod, and Daytona are future adapters over this target shape; the export command does not call or mutate those systems.

## Readiness

Project readiness is derived when `tree`, `status`, `doctor`, `hydrate`, or Agent Prep reads the Project Registry. It is not stored in `projects`.

Normalized states:

- `present`: local path exists and no warning or blocker changes the summary state.
- `placeholder`: local path is a CodeMesh-owned metadata-only placeholder, not a source checkout.
- `missing`: local path from the registry does not exist.
- `dirty`: source checkout has uncommitted or untracked changes; this is a warning, not a blocker.
- `stale`: remote freshness could not be checked, or the local base branch is behind or diverged from the fetched remote base.
- `blocked`: local path or requested base branch prevents safe use.

Diagnostics are split into warnings and blockers. Dirty source checkouts and stale local base branches are warnings so unrelated local work does not prevent temp-clone agent handoff. Missing local paths, fetch failures, and missing requested base branches are blockers for `status` until hydration or base selection exists.

`tree` consumes the same normalized states for local filesystem and dirty-checkout summaries, and includes canonical path versus current-machine path presence. `status` runs the fuller readiness check, including fetch and base branch validation when a source checkout exists, and includes the same canonical/current-machine placement fields. Both commands include `workspace_state` so automation can distinguish `hydrated`, `placeholder`, `missing`, and `blocked` path states without inferring from readiness diagnostics. `doctor` consumes the Agent Prep handoff readiness decision and reports whether the handoff is green, warning-only, or blocked without recording an Agent Run.

## Project Policy

Project policy is resolved at readiness time from the source checkout. See [Project Policy Reference](project-policy.md) for `.codemesh.yml` fields, defaults, validation, env readiness, toolchain readiness, include-docs intent, and no-secret-values behavior.

Resolution:

1. `<project>/.codemesh.yml` when present.
2. Built-in defaults when absent.

Policy is metadata only. The MVP does not store effective policy in SQLite.

## Env Readiness

Env readiness is derived from the effective project policy.

Checks and warn/block handling follow [Project Policy Reference](project-policy.md). Env readiness never reads, writes, stores, or prints secret values.

## Toolchain Readiness

Toolchain readiness is derived from the effective project policy when `agent.toolchain.requirements` is present.

Each requirement records one status:

- `present`: the active detector reported the named toolchain available, with host command/version facts when observable.
- `missing`: the active detector reported it absent.
- `unknown`: the active detector could not prove presence or absence.

Toolchain status separates project facts from host facts. Project facts contain the policy requirement name. Host facts contain the detected command name and version when the host detector can observe them.

Missing and unknown statuses follow policy `agent.toolchain.mode`: warning diagnostics in `warn` mode and blocker diagnostics in `block` mode. CodeMesh does not install tools, run package-manager setup, write toolchain files, create dependency directories, or build environments.

## Env Binding

`codemesh env bind <project> <requirement> --provider fake --ref secret-ref --scope scope` stores private env binding metadata in local SQLite. Bindings map repo-declared logical env key requirements to provider-specific secret references outside repo-local Project Policy.

Stored fields:

- project id
- logical requirement name
- provider name
- provider-specific secret reference
- allowed binding scopes

The first provider is deterministic `fake`. It exists for tests and offline proof. It is not a live secret provider and does not make CodeMesh a secrets manager.

## Agent Runs

`codemesh agent prepare <project>` creates one run directory under `agents/`.

Run layout:

- `workspace/`: temporary Git clone from the registered clone URL.
- `env/env.bundle`: optional agent-scoped env bundle materialized from allowed private bindings.
- `codemesh-run.json`: handoff metadata written inside the ready workspace.

Agent Prep resolves the project by alias, chooses the requested base, repo policy base, discoverable remote default branch, or `main` fallback in that order, fetches that base when a source checkout is present, and gates the handoff on the policy from the fetched base before cloning. If the registered desired source path is missing, Agent Prep requires a registered `clone_url`, refuses unsafe clone sources before Git runs, reads policy from the remote default branch through a temporary Git clone to honor `agent.base`, validates the selected base against the registered clone URL, and prepares from that remote instead of treating the missing path as a blocker. Env readiness still follows the selected-base policy: required keys are checked from the process environment, and required local env files are treated as missing when the source checkout is absent. Env file contents are never read. Readiness blockers stop prep before a run is recorded, and any temporary clone made to read selected-base policy is removed before returning the blocker. Warnings, including dirty source checkout and env warnings, are recorded and printed but do not block.

`codemesh doctor <project>` runs the same handoff readiness gate before Agent Prep's clone/run-recording step. It does not create an agent run directory, write `codemesh-run.json`, or insert an `agent_runs` row. Warning-only readiness exits zero by default and exits non-zero with `--strict`; blockers exit non-zero in both modes.

The clone checks out the requested `--base` when provided. Otherwise it checks out the repo-local policy base, then the remote default branch, then `main`. CodeMesh uses the `full-clone` Clone Strategy for the default baseline: full Git history and a complete working tree for the selected branch. `--partial-clone` and repeatable `--sparse path` explicitly opt into Git-native partial clone and sparse checkout. CodeMesh uses Git for clone and checkout; it does not copy uncommitted source files, create Git worktrees, replace Git state, create placeholders, mount a VFS, or do file-level hydration.

`codemesh-run.json` is the Agent Run Contract. The current contract is `contract_version: 1` and includes producer metadata with the CodeMesh producer name and binary version.

The Agent Run Contract module owns JSON encoding/decoding, validation, clone URL redaction, safe file writing, the State Store metadata shape, and `codemesh runs` list projections.

The contract records metadata only:

- run id and ready path
- contract version and producer/version
- project alias, normalized remote, redacted clone URL, and source path
- selected source mode: `source_checkout` for an existing local Source checkout or `registry_clone` for source-less prep from the registered clone URL
- whether the registered source checkout path was missing during prep
- effective base and profile
- resolved commit and readiness decision
- base provenance with fetched base, fetched commit, prepared HEAD, and whether the prepared HEAD matches the fetched commit
- selected Clone Strategy as `clone_strategy.name`, `clone_strategy.history`, `clone_strategy.working_tree`, and optional `clone_strategy.filter` / `clone_strategy.sparse_paths` when partial or sparse options are used
- handoff docs as project-relative paths available in the prepared clone, with source metadata such as `default` or `policy` and the original policy pattern when applicable
- env requirements, allowed scopes, materialization status, and bundle presence/path/format with values marked not recorded
- toolchain status results when checked
- warnings and blockers from readiness
- created timestamp

The `agent_runs` SQLite row stores the same contract JSON for local audit and future cleanup/listing. Secret values are never included; env readiness records only missing file/key names and warn/block diagnostics, handoff docs record paths only and not file contents, and clone URLs in metadata omit userinfo, query strings, and fragments.

Unmatched policy handoff doc patterns are warnings, not blockers. They indicate stale or overly broad project policy without preventing a fresh agent workspace.

Default handoff docs are resolved from the prepared clone after checkout: `AGENTS.md`, `CONTEXT.md`, `README.md`, and Markdown files directly under `docs/adr/`. Policy-selected docs are additive and also resolve from the prepared clone, so source-only uncommitted docs are not recorded unless they exist on the selected base.

`codemesh runs` reads `agent_runs` and lists prepared runs through the Agent Run Contract list projection. The user-facing row includes project alias, base, profile, created time, and workspace path so temporary workspaces are auditable without inspecting SQLite.

`codemesh agent run <run-id> --label label -- <command...>` is the execution step after preparation. It runs the supplied command with cwd set to the prepared workspace, captures stdout and stderr under the CodeMesh-managed run directory, and appends a command record to `codemesh-run.json` plus the matching `agent_runs` SQLite metadata row.

Command execution has an explicit timeout, defaulting to 10 minutes in the CLI. CodeMesh serializes command recording per run ID so concurrent commands cannot overwrite output paths or drop audit records.

Command records include:

- command label
- cwd
- env binding summary with key names only and values marked not recorded
- base provenance from the prepared run
- exit code and duration
- stdout and stderr output paths
- execution timestamp

Command output is stored in local managed files, not embedded in metadata. Env values are not recorded.

`codemesh runs` derives lifecycle state from local metadata: `prepared` before any command is recorded and `executed` after one or more command records exist.

`codemesh agent prepare` prints `handoff_docs: N`, where `N` is the selected-doc count. The detailed selected paths and source metadata live in `codemesh-run.json`. When fake-provider env materialization runs, stdout prints materialization status and bundle path only, not values.

`codemesh clean --older-than <age>` removes only matching Agent Run directories created under `<codemesh-home>/agents`. Age is evaluated from the stored `created_at` timestamp. After successful deletion, CodeMesh removes the matching `agent_runs` rows so `runs` no longer reports cleaned workspaces.

Cleanup safety rules:

- only `agent_runs` rows are candidates
- workspace path must resolve inside configured CodeMesh `agents/`
- workspace path must be the `workspace/` child of a run directory whose basename matches the run id
- unsafe candidates abort cleanup before deletion
- missing managed run directories are treated as already gone and their metadata rows may be removed
- arbitrary paths outside CodeMesh-managed storage are never deleted

## Secrets

No secret values are stored, read, or printed by `init`, Project Policy, or Env Readiness. Fake-provider Agent Prep materialization writes deterministic test values only to the agent-scoped env bundle under the managed run directory; contract bytes, SQLite run metadata, command output, and e2e reports record metadata only.
