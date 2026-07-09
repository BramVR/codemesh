---
summary: "CLI e2e harness, packaged smoke, isolation, report, and proof expectations."
read_when:
  - Changing CLI behavior, command examples, e2e fixtures, packaged smoke, report shape, or secret-safety verification.
---

# CLI E2E Harness

Run:

```sh
make e2e
```

The harness builds `codemesh` once into a temp directory, runs cases against that binary, prints concise `PASS`, `FAIL`, and `SKIP` lines, and writes `tmp/e2e-report.json`.

The build step may use the developer Go module cache and proxy. Command cases run with isolated state.

Packaged binary smoke:

```sh
make e2e-packaged
```

This target builds `dist/codemesh`, then reruns the smoke cases through the same e2e runner with `CODEMESH_E2E_BINARY` pointed at that packaged-style binary. In packaged mode, command cases run from a temp directory outside the repository checkout, with `CODEMESH_HOME`, `HOME`, Git config, and local fixtures still isolated under the harness temp directory.

Live e2e guardrails:

```sh
make e2e-live
```

Live mode is explicit and opt-in. The default `make e2e-live` path exits 0 and records a `SKIP` because `CODEMESH_E2E_LIVE=1` was not set. Use this target before adding or changing real provider, GUI, network, or multi-machine smoke checks so the report proves the live harness is isolated and safely skipped by default.

Opt in only when the live prerequisites are free and available:

```sh
CODEMESH_E2E_LIVE=1 make e2e-live
```

Opted-in live mode runs a read-only GitHub remote smoke through the real `codemesh` CLI. By default it targets the current public repository:

```sh
https://github.com/BramVR/codemesh.git
```

Override the public HTTPS GitHub remote with:

```sh
CODEMESH_E2E_LIVE=1 CODEMESH_LIVE_GITHUB_REPO=https://github.com/OWNER/REPO.git make e2e-live
```

The GitHub smoke discovers the remote `HEAD` default branch with `git ls-remote --symref`, clones a temporary seed checkout, registers it with `codemesh add`, removes the checkout so the project is missing, runs `codemesh status`, runs `codemesh agent prepare --json`, verifies the prepared workspace, command JSON, `codemesh-run.json`, and SQLite Agent Run metadata all record the selected `full-clone` strategy, confirms `codemesh runs` can read the prepared run, runs one harmless `codemesh agent run` command inside the workspace, confirms `codemesh runs` reports the executed state, runs guarded cleanup, then runs `codemesh hydrate --json` and verifies the hydrated checkout origin, branch, and `full-clone` command metadata. All CodeMesh state, `HOME`, Git config, workspace paths, and command cwd remain under the harness temp root.

The same GitHub smoke also runs opt-in Clone Strategy checks against the public remote after removing the registered source checkout again:

- `--partial-clone` Agent Prep verifies `partial-clone` with `blob:none` in command JSON, `codemesh-run.json`, SQLite run metadata, and the live report.
- `--sparse README.md` Agent Prep verifies `sparse-checkout` metadata, confirms `README.md` is present, and confirms an unrelated tracked path such as `go.mod` is not materialized.
- Unsupported partial or sparse behavior records `SKIP` with a diagnostic in non-strict live mode, and records `FAIL` with the same diagnostic when `CODEMESH_E2E_LIVE_STRICT=1`.
- The live smoke does not silently fall back from partial or sparse to full clone; any future fallback mode must be explicit in command output and report metadata.

Strict mode turns missing live prerequisites into failures:

```sh
CODEMESH_E2E_LIVE=1 CODEMESH_E2E_LIVE_STRICT=1 make e2e-live
```

`CODEMESH_E2E_LIVE_TARGETS` may name comma-separated live target labels for report audit trails. Network unavailable, GitHub rate limiting, or a missing `git` executable records `SKIP` by default and exits 0; strict mode records the same condition as `FAIL`. Live checks must continue to skip unless free, safe prerequisites are present.

Targeted host toolchain smoke is local-host only and does not use GitHub or providers:

```sh
CODEMESH_E2E_LIVE=1 CODEMESH_E2E_LIVE_TARGETS=toolchain make e2e-live
```

The toolchain smoke builds the current CLI, creates isolated Go and package-manager-style project fixtures under the harness temp root, and runs `codemesh doctor --json` plus `codemesh agent prepare --json` through the same readiness model. Present tools record detected command names and versions without installing or modifying tools. Optional absent host tools record `SKIP` by default and fail only with `CODEMESH_E2E_LIVE_STRICT=1`; the smoke includes one deliberately absent optional tool so the report proves this behavior. The live report writes `live.toolchain.fixtures[]` with separate project facts and host facts so failures can be traced to project policy or machine setup.

Local macOS desktop smoke is opt-in and uses Peekaboo to prove the packaged CLI in a real terminal window:

```sh
make e2e-peekaboo
```

The target builds `dist/codemesh`, runs the live harness with `CODEMESH_E2E_LIVE=1` and the `desktop` target, then checks `/opt/homebrew/bin/peekaboo permissions --json --no-remote` or `peekaboo` on `PATH`. It records `SKIP` when the host is not macOS, Peekaboo is missing, Screen Recording or Accessibility permission is unavailable, or a desktop terminal cannot be automated. Strict mode turns the same missing prerequisites into failures:

```sh
CODEMESH_E2E_LIVE_STRICT=1 make e2e-peekaboo
```

When prerequisites are available, Peekaboo launches or focuses Terminal with a generated `.command` file and captures a terminal screenshot. The command runs the packaged binary from the isolated live run directory with isolated `CODEMESH_HOME`, `HOME`, `CODEMESH_WORKSPACE`, and `GIT_CONFIG_GLOBAL`, then verifies visible transcript output for `codemesh --help`, `codemesh init`, and `codemesh status`. The stable artifacts are:

- `tmp/e2e-peekaboo-desktop.png`
- `tmp/e2e-peekaboo-transcript.txt`

The live JSON report references both artifact paths under `live.desktop`. The desktop smoke does not use AppleScript, does not read the user's normal CodeMesh state, and does not type secrets. CI should not run this target unless a standard/free macOS desktop runner is deliberately configured with Peekaboo and local TCC permissions. Required PR CI intentionally excludes Peekaboo.

Owned-host proof is opt-in and uses a packaged CodeMesh binary against Bram-owned local/static targets:

```sh
make e2e-owned-host
```

The target builds `dist/codemesh`, runs live mode with `CODEMESH_E2E_LIVE_TARGETS=owned-host`, and records a green `SKIP` unless a non-secret owned-host inventory is configured. Enable the first local slice with:

```sh
CODEMESH_E2E_OWNED_HOSTS=local-macos make e2e-owned-host
```

Known inventory labels:

- `local-macos`: local macOS host, packaged CLI, no SSH.
- `hermes-win`: static SSH target at `hermes-win`, native Windows doctor path.
- `hermes-vm`: static SSH target at `hermes-vm`, Linux/POSIX doctor path.
- `CODEMESH_E2E_EXTRA_LINUX_HOST=<host>` adds one optional Linux SSH target named `extra-linux`.

The inventory carries host names and addresses only; it must not contain credentials, tokens, or secret provider references. SSH targets use ordinary existing SSH configuration and `BatchMode=yes`. Missing SSH reachability, missing host tools, or unavailable hosts record `SKIP` by default and become `FAIL` with:

```sh
CODEMESH_E2E_LIVE_STRICT=1 make e2e-owned-host
```

The local owned-host slice proves the packaged binary from outside the source checkout, then runs the CodeMesh workspace-control-plane flow on two isolated machine homes: machine registration, manifest handoff, bootstrap dry-run, bootstrap apply without default cloning, explicit hydrate, agent prepare, harmless deterministic `agent run`, runs listing, guarded cleanup, and reconcile dry-run drift proof. Remote SSH targets currently run static doctor/reachability classification only; they never fake a successful workspace proof.

Owned-host evidence is written under `tmp/e2e-owned-host/`. The JSON report records `live.owned_hosts` with host facts, doctor outcomes, per-host lock metadata, command durations, stdout/stderr artifact paths, selected run IDs, machine IDs, manifest location, hydrated project identity, cleanup status, visual artifact metadata, skip/fail reasons, and secret-safety status. Screenshots, videos, contact sheets, and proof bundles must stay under `tmp/`, GitHub attachments, CI artifacts, or an external artifact manifest; they must not be committed to product branches. The current owned-host visual proof field is metadata-only and records `SKIP`; screenshot, video, contact-sheet capture, and strict visual-prerequisite enforcement are reserved until a free local capture path is implemented.

Live provider smoke is reserved but inert. After `CODEMESH_E2E_LIVE=1`, it records `SKIP` unless all exact provider contract variables are present:

```sh
CODEMESH_E2E_LIVE_PROVIDER=provider-name
CODEMESH_E2E_LIVE_PROVIDER_REQUIREMENT=CODEMESH_SINGLE_TARGET_KEY
CODEMESH_E2E_LIVE_PROVIDER_SECRET_REF=provider-specific-single-target-ref
CODEMESH_E2E_LIVE_PROVIDER_SCOPE=codex
```

The reserved contract is intentionally single-target: one logical requirement, one provider reference, and one scope. Tests must not call password managers, enumerate vaults, dump environment variables, or print the secret reference/value by default. Until a real provider implementation exists, even a fully configured provider smoke records `SKIP` with metadata that says the secret reference was configured but does not include it.

Override the report path:

```sh
CODEMESH_E2E_REPORT=/tmp/codemesh-e2e.json make e2e
```

## JSON Report

Inspect the report when terminal output is too noisy, CI needs a durable artifact, or a handoff needs proof of the exact e2e mode, binary, isolation roots, and case outcomes. Use `CODEMESH_E2E_REPORT` to write it outside the default `tmp/e2e-report.json` path.

The report includes:

- `started_at`: UTC run start time.
- `mode`: `source` for the normal source-built runner, `packaged` for `make e2e-packaged`, or `live` for `make e2e-live`.
- `binary`: executable path plus whether it was an external packaged binary.
- `host`: OS, architecture, and Go version for the machine that wrote the report.
- `isolation`: isolated `CODEMESH_HOME`, `HOME`, workspace, run directory, and Git config path.
- `live`: live opt-in status, strict mode, target labels, skip reasons, GitHub remote URL, discovered default branch, command durations for GitHub status, Agent Prep, Agent Run, runs, cleanup, hydration smoke steps, clone strategy records for full, partial, and sparse GitHub smokes, targeted toolchain fixture facts, reserved provider smoke skip metadata, per-smoke secret-safety result, and the host-scoped lock path/label when a lock was acquired.
- `live.desktop`: Peekaboo path, Terminal app, permission status, screenshot path, transcript path, SKIP reason or PASS status, and secret-safety status when the desktop target runs.
- `live.owned_hosts`: owned-host inventory status, proof-bundle path, host facts, doctor outcomes, per-host lock metadata, command durations, stdout/stderr artifact paths, CodeMesh report paths, selected run IDs, machine IDs, manifest location, hydrated project identity, cleanup status, visual artifact metadata, skip/fail reasons, and secret-safety status.
- `two_machine`: offline two-machine smoke summary with both machine IDs, manifest location, hydrated project identity, hydration provenance, drift summary, and cleanup status.
- `summary`: `pass`, `fail`, `skip`, and `total` counts derived from recorded case results.
- `secret_safety`: whether report redaction is active and how many known fake fixture values were redacted.
- `results`: per-case status, duration, exit code, and captured output for failing or diagnostic cases.

Reports may include fake env key names such as `CODEMESH_E2E_REQUIRED_ENV`, but must not include real secret values or fake fixture secret values. The harness checks command output, the JSON report, Agent Prep metadata, and SQLite state store bytes for fake env file/key and fake provider secret markers.

## CLI Contract Snapshots

Machine-readable CLI contracts live in `test/e2e/snapshots/*.json`. The e2e harness compares normalized JSON snapshots for `status`, `tree`, `hydrate`, and `agent prepare`, plus a command-misuse process contract.

Snapshots record the command args, process exit code, stable exit class, and normalized stdout JSON. Normalization replaces isolated temp paths, generated agent run ids, commit hashes, timestamps, and durations before comparison, so human output and local machine paths can change without breaking the JSON contract.

On mismatch, the harness prints a normalized JSON diff plus the original stdout/stderr file paths under the e2e temp directory. To intentionally refresh snapshots after a reviewed contract change, run:

```sh
CODEMESH_E2E_UPDATE_CONTRACTS=1 make e2e
```

## Isolation

Each run creates a temp workspace with:

- `CODEMESH_HOME` under the harness temp directory.
- `HOME` under the harness temp directory.
- a command run directory under the harness temp directory for packaged smoke checks.
- an empty temp Git config.
- local Git fixtures for future Project Registry, Readiness, Hydration, and Agent Prep cases.

Offline and packaged modes do not use GitHub, secrets, GUI automation, AppleScript, the user's normal CodeMesh home, or personal workspace projects. Live mode may use the configured public GitHub remote after explicit opt-in, but still keeps local state and workspace paths isolated under the harness temp root.

Live mode uses the same isolation roots before it evaluates opt-in state. If future live checks run commands, their default cwd is the isolated run directory under the harness temp root, not the repository checkout.

## Live Locking

Opted-in live mode acquires one host-scoped JSON lock before checking live prerequisites. The default lock directory is under the OS temp directory at `codemesh-e2e-live-locks`; `CODEMESH_E2E_LIVE_LOCK_DIR` may override it for CI or local test isolation.

The lock records:

- pid
- host
- label
- start time

Fresh locks serialize live checks on the same host. Stale locks are removed after the stale window before a new lock is written. The live runner releases the lock on normal exit.

## Test Layers

CodeMesh follows a bslog-inspired layering model:

- Unit tests cover pure package behavior and command helpers in-process.
- Offline integration-style e2e tests run the real CLI against local temp fixtures, mocked local state, and fake env requirements.
- Live/network e2e checks are intentionally limited and skipped until a feature needs real provider proof.

The current CodeMesh e2e layer sits in the middle. It builds or receives a CLI binary, isolates `CODEMESH_HOME`, `HOME`, Git config, and workspace paths, then creates local Git remotes and clones under the e2e temp directory. These fixtures cover Project Registry, Readiness, Doctor preflight, Hydration, Agent Prep, run listing, and guarded cleanup without GitHub, secrets, or user workspace state.

## Scenario Shape

Domain cases should use the scenario helpers in `test/e2e/main.go` instead of open-coding command setup.

Start each domain workflow with:

```go
s, err := h.newScenario("readiness status")
```

A scenario owns:

- a case-specific `CODEMESH_HOME` under the harness temp directory.
- the shared isolated `HOME`, empty Git config, temp workspace, and command runner.
- offline local Git fixtures created under the harness temp directory.
- command helpers that run the real `codemesh` binary with isolated env.
- assertion helpers for common output and durable path checks.

Use `s.command(...)` for commands that should exit successfully. Use `s.expectedFailure(...)` when a non-zero CLI exit is the expected user-visible behavior, then convert the result to `PASS` only after checking stderr/stdout. Use `s.expectOutput` and `s.expectNoOutput` for stdout assertions, and `s.expectPathExists` / `s.expectPathMissing` for durable filesystem effects.

Keep new offline cases vertical: arrange fixtures, run one real CLI command, assert user-visible output, then assert the durable filesystem or state effect. Avoid reading secrets, using host project paths, calling GitHub, or weakening the isolated `CODEMESH_HOME`, `HOME`, `GIT_CONFIG_GLOBAL`, local remotes, and temp workspace boundaries.

Offline Git fixtures cover:

- clean source checkout backed by a local bare remote.
- dirty source checkout with uncommitted local changes.
- missing project path with a known local bare remote.
- missing base branch on an otherwise valid local remote.
- fetch failure against an unreachable local remote.
- invalid project policy diagnostics.
- missing required env in warn and block modes using fake fixture names such as `CODEMESH_E2E_REQUIRED_ENV`.
- present env requirements using fake env values and fake env file contents that must not appear in public artifacts or state.
- fake-provider Env Binding for an agent-scoped bundle under managed run storage, including cleanup with the managed agent workspace.
- toolchain policy declarations that report host status and, when available, command/version facts without creating dependency directories, tool version files, or environment builds.

Project Registry e2e coverage runs `codemesh scan` and `codemesh add` against local fixture workspaces, reruns them to prove no duplicate rows, verifies deterministic discovered aliases, verifies known-remote path updates, and checks State store rows for normalized remote, clone URL, alias, desired local path, temp-only isolation, and derived missing/present behavior.

Readiness e2e coverage runs `codemesh tree` and `codemesh status` against the same local Git fixtures. It verifies clean present, missing, dirty warning, missing base blocker, fetch failure stale blocker, invalid Project policy blocker, Env readiness warn/block behavior, and tree/status agreement on normalized states for projects both commands report.

Doctor preflight e2e coverage runs `codemesh doctor` through the built CLI against local fixtures. It verifies green human output for a clean handoff, strict JSON failure for warning-only dirty checkout readiness, toolchain readiness in human and JSON output, actionable missing-base blockers, and no `agent_runs` rows or agent run directories after doctor checks.

Bootstrap e2e coverage writes isolated Workspace Manifest JSON entries, registers a fresh machine workspace root, verifies dry-run plan output does not mutate filesystem or registry state, applies topology, checks parent directories and Project Registry rows, verifies project directories are not created, and confirms `tree` and `status` still report bootstrapped projects as missing. It also covers path conflict refusal with no registry mutation and preserved local files.

Workspace Target e2e coverage uses only local fake target data. It bootstraps isolated manifest topology, registers local machine facts, stores fake-provider env binding references, runs `codemesh target export --json`, and verifies the export includes topology, machine/target facts, scoped references, and `values: not-recorded` without env values, readiness state, dirty/stale state, Agent Runs, or provider calls.

Hydration e2e coverage uses the local bare Git remotes from the offline fixture set. It registers a known project, removes its desired local path to make it missing, runs `codemesh hydrate <project>`, verifies the real CLI recreates the checkout through the default `full-clone` strategy, checks explicit partial/sparse strategy JSON in focused command tests, and confirms it does not reach GitHub or create directories for unrelated missing projects.

Two-machine e2e coverage uses two isolated local CodeMesh homes, Git configs, machine IDs, and workspace roots on one host. It registers topology from Machine A, writes manifest entries, bootstraps Machine B without default cloning, hydrates one selected project through a local-only Git endpoint, verifies Machine B did not reuse Machine A's source checkout, and confirms a changed manifest produces a reconcile dry-run drift plan before mutation.

Agent Prep e2e coverage uses the same local bare Git remotes and isolated CodeMesh home. It scans the fixture sources, runs `codemesh agent prepare <project>` for a clean source checkout, verifies `ready_path` points under CodeMesh-managed agents storage with a real Git checkout at the requested base and `codemesh-run.json`, checks the State store `agent_runs` metadata references the prepared workspace and preserves the Agent Run Contract version/producer/base-provenance/clone-strategy metadata, checks explicit partial/sparse strategy JSON and run-contract details in focused command tests, proves an omitted `--base` follows a non-main remote default branch, proves a registered project can prepare from its clone URL after the source checkout path is removed without creating a placeholder directory, checks dirty source checkout warnings do not block prep, verifies stdout reports `handoff_docs: N`, verifies default and policy-selected handoff docs are resolved from the prepared clone and recorded as paths without contents, verifies unmatched policy patterns emit `handoff-doc-missing` warnings, checks toolchain readiness is recorded in `codemesh-run.json` and SQLite metadata without creating environment artifacts, checks Env readiness warn mode still prepares with diagnostics and metadata, verifies blocking env readiness stops prep with missing file/key diagnostics only, confirms present fake env values/file contents are not written to run metadata, and proves fake-provider Env Binding materializes an agent-scoped bundle only when allowed scopes intersect. Opted-in live coverage also proves Agent Prep can prepare from a public GitHub remote when the registered source checkout path is missing.

Agent Run lifecycle coverage reuses that isolated Agent Prep state. It runs `codemesh runs` to verify `state=prepared`, runs one harmless `codemesh agent run` command through the prepared workspace, verifies command label, cwd, env summary, base provenance, exit code, duration, and output paths in both `codemesh-run.json` and SQLite without env values, verifies `codemesh runs` reports `state=executed`, then runs `codemesh clean --older-than 0d` to verify the guarded runner removes only the managed run path under the temp CodeMesh home and updates local metadata.

## Packaged Smoke Pattern

The packaged smoke target follows the Summarize release-smoke pattern: build the CLI artifact first, then invoke the artifact directly with basic commands such as `--help`. CodeMesh keeps the same installed-binary emphasis, but stays local and deterministic: no release package, no registry install, no network-backed provider, and no real user workspace.

Unit tests exercise Go packages and command helpers in-process. Normal e2e checks exercise the CLI runner with isolated state. Packaged smoke checks add one more boundary: the executable must work when invoked by absolute path from outside the source tree, so source-relative path assumptions and accidental use of the user's state are easier to catch.

## Runner Guardrails

The harness uses an Oracle-inspired command runner for every e2e command instead of raw `exec.Command` calls.

Default command timeout: 30 seconds.

Long command timeout: 2 minutes for intentionally slower checks, including the one-time CLI build.

Every recorded harness command prints one concise summary:

```txt
PASS help smoke (exit=0 duration=3ms)
```

On failure, including quiet setup commands, the runner prints the summary plus the error and captured stdout/stderr. Timeout failures use the same path and say which timeout was hit, so agents get deterministic output instead of a hanging terminal.

## Cleanup State Model

The e2e temp directory is the only destructive cleanup boundary. The runner removes paths only when they are inside the OS temp directory and their basename starts with `codemesh-e2e-`. Cleanup requests for the repo, parent temp directory, home directory, workspace roots, or arbitrary paths are rejected.

## Agent Workflow

Agents should run e2e checks with:

```sh
make e2e
```

Run `make e2e-packaged` when changing packaging, installed-binary assumptions, source-relative paths, or release-smoke behavior. Run `make e2e-live` when changing the live harness or adding real provider, GUI, network, account, or multi-machine checks. Expect live mode to report `SKIP` unless `CODEMESH_E2E_LIVE=1` is set and free prerequisites are available. Use `CODEMESH_E2E_LIVE=1 CODEMESH_E2E_LIVE_TARGETS=github make e2e-live` for the full, partial, and sparse Clone Strategy live smoke.

Run `make e2e-peekaboo` only on a local macOS desktop or deliberately configured free macOS desktop runner when changing Peekaboo automation, packaged CLI terminal behavior, or screenshot proof. A local `SKIP` is acceptable when Peekaboo or TCC permissions are unavailable; include the SKIP reason from `tmp/e2e-report.json` in handoff proof.

Run `make e2e-owned-host` when changing owned-host proof, static SSH target classification, packaged CLI workspace-control-plane proof, per-host evidence bundles, or visual proof metadata. A default `SKIP` is acceptable without `CODEMESH_E2E_OWNED_HOSTS`; include the owned-host section from `tmp/e2e-report.json` in handoff proof.

If a command fails, use the printed failure block first. If a machine-readable audit trail is needed, inspect `tmp/e2e-report.json` or set `CODEMESH_E2E_REPORT` to a temp path before running the target.

## Handoff Gates

Before handoff after code changes, run:

```sh
make test
make e2e
make e2e-packaged
```

Run `make docs-site-test` when docs-site inputs change. Run default `make e2e-live` when changing live, provider, GUI, network, or multi-machine harness behavior; the default expected result is an audited `SKIP` unless live checks are explicitly opted in.

GitHub Actions adds CI-only cross-OS proof. Required PR CI runs `make test`, `make e2e`, and `make e2e-packaged` on Ubuntu and macOS with distinct `CODEMESH_E2E_REPORT` paths for source and packaged modes, then uploads the JSON reports as short-retention artifacts even when a test step fails. Windows required PR CI runs a targeted Go unit smoke, builds `dist/codemesh.exe`, and runs the binary help smoke; Windows e2e remains an explicit tracked skip for issue 92 while POSIX-mode e2e coverage runs on Ubuntu and macOS. Live/network, including the Clone Strategy live smoke, and Peekaboo checks are not part of required PR CI unless a future workflow opts into them explicitly.

## Free Crabbox PR Proof

Every pull request runs the free Crabbox PR proof workflow on standard GitHub-hosted public-repo runners. The job first checks changed paths and exits green with a "not required" summary for unrelated PRs. Full visual proof is required for changes to canonical workspace topology, multi-machine placement or presence, manifest import/export, bootstrap, explicit hydration, source-less agent workspace prep, placeholders, lazy hydration, materialization boundaries, or the proof lane itself.

Run the same lane locally with:

```sh
make crabbox-pr-proof-free
```

The target builds the packaged CLI, creates isolated local Git remotes and two isolated CodeMesh machine homes, runs real `codemesh` commands, and writes visual proof under `tmp/crabbox-pr-proof/`. It must fail rather than skip when the packaged CLI cannot run, a required visual is missing, command provenance is fake-only, or the public artifact confidentiality scan finds raw secrets, private endpoints, personal local paths outside fixture placeholders, internal model names, or sensitive logs.

The GitHub Actions workflow is `crabbox-pr-proof`. Reviewers find the proof on the PR's Checks tab by opening the `Free Crabbox PR visual proof` job summary and downloading the `codemesh-crabbox-pr-proof` artifact. Required proof files include:

- `summary.md`
- `proof-manifest.json`
- `canonical-workspace-tree.svg`
- `machine-placement-presence.svg`
- `bootstrap-hydration-plan.svg`
- `mutating-flow-before-after.svg`
- matching `.txt` command-output companions and `commands/*.txt` transcripts

The visuals show canonical workspace tree/status output, manifest import/export placement, per-machine placement and presence, planned bootstrap/hydration actions, and before/after state for bootstrap apply plus idempotent hydrate. Paths are sanitized to isolated fixture placeholders before upload.

## Owned-Host Crabbox Proof

CodeMesh can run Peter-style Crabbox proof from the free `hermes-vm` static SSH target:

```sh
make crabbox-pr-proof
```

or directly:

```sh
scripts/crabbox-pr-proof
```

The proof command syncs the dirty checkout to Crabbox, runs `make e2e-packaged` on `hermes-vm`, requires `tmp/e2e-report.json`, writes a Crabbox run proof block, opens a visible terminal on the remote desktop, records MP4 proof, creates a GIF preview, captures a screenshot/contact sheet, and writes a local PR-comment markdown draft.

Artifacts land under `.crabbox/proof/<run>/`:

- `run-proof.md`
- `e2e-report.json`
- `terminal.mp4`
- `terminal.gif`
- `terminal.png`
- `terminal.contact.png`
- `comment-local.md`

Dry-run PR publishing:

```sh
scripts/crabbox-pr-proof --pr 123 --publish-dry-run
```

Real inline GitHub PR comments need public asset hosting. Crabbox supports broker/local/S3/Cloudflare/R2 publishing, but the no-spend default here is local proof plus dry-run markdown. If a free public base URL or bucket is configured later, publish with:

```sh
scripts/crabbox-pr-proof --pr 123 --publish \
  --publish-storage r2 \
  --publish-bucket codemesh-artifacts \
  --publish-endpoint-url https://<account-id>.r2.cloudflarestorage.com \
  --publish-base-url https://example.invalid/codemesh-artifacts
```

## Adopted Patterns

From `steipete/gifgrep`:

- build the CLI once into a temp binary.
- run deterministic checks in isolated temp directories.
- control process environment.
- print `PASS`, `FAIL`, and `SKIP`.
- include captured stdout/stderr on failure.

From `steipete/poltergeist`:

- run realistic workflows from temp fixtures.
- keep process execution behind reusable helpers.
- use local cleanup.
- persist a small machine-readable report.

From `steipete/oracle`:

- put command execution behind one runner.
- use bounded default and long timeout tiers.
- print command label, exit code, and duration for every command.
- capture stdout/stderr and surface them on failure.
- guard cleanup so test helpers cannot delete outside their temp boundary.

## Intentionally Not Run Live

- Poll/wait helpers: no async CodeMesh behavior exists yet.
- Real provider checks and any future GUI checks beyond Peekaboo/owned-host visual metadata: those live targets must record audited skips unless explicitly opted in and free prerequisites are available. Provider smoke must use the exact single-target env contract above and must not invoke password managers by default.
- Screenshot/video proof: limited to the opt-in Peekaboo desktop smoke, Crabbox PR proof workflow, and future owned-host visual proof when a free local capture path is explicitly configured; default source, packaged, and live GitHub/toolchain/provider lanes remain CLI/report-only.
