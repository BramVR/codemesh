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

The GitHub smoke discovers the remote `HEAD` default branch with `git ls-remote --symref`, clones a temporary seed checkout, registers it with `codemesh add`, removes the checkout so the project is missing, runs `codemesh status`, runs `codemesh agent prepare`, verifies the prepared workspace and `codemesh-run.json`, confirms `codemesh runs` can read the prepared run, runs one harmless `codemesh agent run` command inside the workspace, confirms `codemesh runs` reports the executed state, runs guarded cleanup, then runs `codemesh hydrate` and verifies the hydrated checkout origin and branch. All CodeMesh state, `HOME`, Git config, workspace paths, and command cwd remain under the harness temp root.

Strict mode turns missing live prerequisites into failures:

```sh
CODEMESH_E2E_LIVE=1 CODEMESH_E2E_LIVE_STRICT=1 make e2e-live
```

`CODEMESH_E2E_LIVE_TARGETS` may name comma-separated live target labels for report audit trails. Network unavailable, GitHub rate limiting, or a missing `git` executable records `SKIP` by default and exits 0; strict mode records the same condition as `FAIL`. Live checks must continue to skip unless free, safe prerequisites are present.

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
- `live`: live opt-in status, strict mode, target labels, skip reasons, GitHub remote URL, discovered default branch, command durations for GitHub status, Agent Prep, Agent Run, runs, cleanup, and hydration smoke steps, per-smoke secret-safety result, and the host-scoped lock path/label when a lock was acquired.
- `summary`: `pass`, `fail`, `skip`, and `total` counts derived from recorded case results.
- `secret_safety`: whether report redaction is active and how many known fake fixture values were redacted.
- `results`: per-case status, duration, exit code, and captured output for failing or diagnostic cases.

Reports may include fake env key names such as `CODEMESH_E2E_REQUIRED_ENV`, but must not include real secret values or fake fixture secret values. The harness checks command output, the JSON report, Agent Prep metadata, and SQLite state store bytes for fake env file/key secret markers.

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

The current CodeMesh e2e layer sits in the middle. It builds or receives a CLI binary, isolates `CODEMESH_HOME`, `HOME`, Git config, and workspace paths, then creates local Git remotes and clones under the e2e temp directory. These fixtures cover Project Registry, Readiness, Hydration, Agent Prep, run listing, and guarded cleanup without GitHub, secrets, or user workspace state.

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

Project Registry e2e coverage runs `codemesh scan` and `codemesh add` against local fixture workspaces, reruns them to prove no duplicate rows, verifies deterministic discovered aliases, verifies known-remote path updates, and checks State store rows for normalized remote, clone URL, alias, desired local path, temp-only isolation, and derived missing/present behavior.

Readiness e2e coverage runs `codemesh tree` and `codemesh status` against the same local Git fixtures. It verifies clean present, missing, dirty warning, missing base blocker, fetch failure stale blocker, invalid Project policy blocker, Env readiness warn/block behavior, and tree/status agreement on normalized states for projects both commands report.

Hydration e2e coverage uses the local bare Git remotes from the offline fixture set. It registers a known project, removes its desired local path to make it missing, runs `codemesh hydrate <project>`, and verifies the real CLI recreates the checkout without reaching GitHub or creating directories for unrelated missing projects.

Agent Prep e2e coverage uses the same local bare Git remotes and isolated CodeMesh home. It scans the fixture sources, runs `codemesh agent prepare <project>` for a clean source checkout, verifies `ready_path` points under CodeMesh-managed agents storage with a real Git checkout at the requested base and `codemesh-run.json`, checks the State store `agent_runs` metadata references the prepared workspace and preserves the Agent Run Contract version/producer metadata, checks dirty source checkout warnings do not block prep, verifies stdout reports `handoff_docs: N`, verifies default and policy-selected handoff docs are resolved from the prepared clone and recorded as paths without contents, verifies unmatched policy patterns emit `handoff-doc-missing` warnings, checks Env readiness warn mode still prepares with diagnostics and metadata, verifies blocking env readiness stops prep with missing file/key diagnostics only, and confirms present fake env values/file contents are not written to run metadata. Opted-in live coverage also proves Agent Prep can prepare from a public GitHub remote when the registered source checkout path is missing.

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

Run `make e2e-packaged` when changing packaging, installed-binary assumptions, source-relative paths, or release-smoke behavior. Run `make e2e-live` when changing the live harness or adding real provider, GUI, network, account, or multi-machine checks. Expect live mode to report `SKIP` unless `CODEMESH_E2E_LIVE=1` is set and free prerequisites are available.

If a command fails, use the printed failure block first. If a machine-readable audit trail is needed, inspect `tmp/e2e-report.json` or set `CODEMESH_E2E_REPORT` to a temp path before running the target.

## Handoff Gates

Before handoff after code changes, run:

```sh
make test
make e2e
make e2e-packaged
```

Run `make docs-site-test` when docs-site inputs change. Run default `make e2e-live` when changing live, provider, GUI, network, or multi-machine harness behavior; the default expected result is an audited `SKIP` unless live checks are explicitly opted in.

GitHub Actions adds CI-only cross-OS proof. Required PR CI runs `make test`, `make e2e`, and `make e2e-packaged` on Ubuntu and macOS with distinct `CODEMESH_E2E_REPORT` paths for source and packaged modes, then uploads the JSON reports as short-retention artifacts even when a test step fails. Windows required PR CI runs a targeted Go unit smoke, builds `dist/codemesh.exe`, and runs the binary help smoke; Windows e2e remains an explicit tracked skip for issue 92 while POSIX-mode e2e coverage runs on Ubuntu and macOS. Live/network and Peekaboo checks are not part of required PR CI unless a future workflow opts into them explicitly.

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
- Provider, GUI, and multi-machine checks: those live targets are not implemented yet, so future checks must record audited skips unless explicitly opted in and free prerequisites are available.
- Screenshot proof: not applicable to the current CLI-only harness.
