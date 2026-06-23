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

Override the report path:

```sh
CODEMESH_E2E_REPORT=/tmp/codemesh-e2e.json make e2e
```

## JSON Report

Inspect the report when terminal output is too noisy, CI needs a durable artifact, or a handoff needs proof of the exact e2e mode, binary, isolation roots, and case outcomes. Use `CODEMESH_E2E_REPORT` to write it outside the default `tmp/e2e-report.json` path.

The report includes:

- `started_at`: UTC run start time.
- `mode`: `source` for the normal source-built runner or `packaged` for `make e2e-packaged`.
- `binary`: executable path plus whether it was an external packaged binary.
- `isolation`: isolated `CODEMESH_HOME`, `HOME`, workspace, run directory, and Git config path.
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

The harness does not use GitHub, secrets, GUI automation, AppleScript, the user's `~/.codemesh`, or projects under `~/Projects`.

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

Keep new cases vertical: arrange fixtures, run one real CLI command, assert user-visible output, then assert the durable filesystem or state effect. Avoid reading secrets, using host project paths, calling GitHub, or weakening the isolated `CODEMESH_HOME`, `HOME`, `GIT_CONFIG_GLOBAL`, local remotes, and temp workspace boundaries.

Offline Git fixtures cover:

- clean source checkout backed by a local bare remote.
- dirty source checkout with uncommitted local changes.
- missing project path with a known local bare remote.
- missing base branch on an otherwise valid local remote.
- fetch failure against an unreachable local remote.
- invalid project policy diagnostics.
- missing required env in warn and block modes using fake fixture names such as `CODEMESH_E2E_REQUIRED_ENV`.
- present env requirements using fake env values and fake env file contents that must not appear in public artifacts or state.

Project Registry e2e coverage runs `codemesh scan` against the fixture source workspace, reruns scan to prove idempotent unchanged reporting, and verifies `codemesh tree` shows scanned projects with normalized local states and paths.

Readiness e2e coverage runs `codemesh tree` and `codemesh status` against the same local Git fixtures. It verifies clean present, missing, dirty warning, missing base blocker, fetch failure stale blocker, invalid Project policy blocker, Env readiness warn/block behavior, and tree/status agreement on normalized states for projects both commands report.

Hydration e2e coverage uses the local bare Git remotes from the offline fixture set. It registers a known project, removes its desired local path to make it missing, runs `codemesh hydrate <project>`, and verifies the real CLI recreates the checkout without reaching GitHub or creating directories for unrelated missing projects.

Agent Prep e2e coverage uses the same local bare Git remotes and isolated CodeMesh home. It scans the fixture sources, runs `codemesh agent prepare <project>` for a clean source checkout, verifies `ready_path` points under CodeMesh-managed agents storage with a real Git checkout at the requested base and `codemesh-run.json`, checks the State store `agent_runs` metadata references the prepared workspace, checks dirty source checkout warnings do not block prep, checks Env readiness warn mode still prepares with diagnostics and metadata, verifies blocking env readiness stops prep with missing file/key diagnostics only, and confirms present fake env values/file contents are not written to run metadata.

Agent Run cleanup coverage reuses that isolated Agent Prep state. It runs `codemesh runs` to verify stored run metadata, then `codemesh clean --older-than 0d` to verify the guarded runner removes only the prepared workspace under the temp CodeMesh home and updates local metadata.

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

If a command fails, use the printed failure block first. If a machine-readable audit trail is needed, inspect `tmp/e2e-report.json` or set `CODEMESH_E2E_REPORT` to a temp path before running the target.

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
- Live/network checks: out of scope for MVP fixture coverage; the harness records an offline boundary pass instead of a skip.
- Screenshot proof: not applicable to the current CLI-only harness.
