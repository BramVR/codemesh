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

The current CodeMesh e2e layer sits in the middle. It builds or receives a CLI binary, isolates `CODEMESH_HOME`, `HOME`, Git config, and workspace paths, then creates local Git remotes and clones under the e2e temp directory. These fixtures establish the shape for Project Registry, Readiness, Hydration, and Agent Prep coverage without requiring those commands to exist yet.

Offline Git fixtures cover:

- clean source checkout backed by a local bare remote.
- dirty source checkout with uncommitted local changes.
- missing project path with a known local bare remote.
- missing base branch on an otherwise valid local remote.
- missing required env using fake fixture names such as `CODEMESH_E2E_REQUIRED_ENV`.

Project Registry e2e coverage runs `codemesh scan` against the fixture source workspace, reruns scan to prove idempotent unchanged reporting, and verifies `codemesh tree` shows scanned projects with normalized local states and paths.

Readiness e2e coverage runs `codemesh status` against the same local Git fixtures, including a dirty source checkout warning and a missing base branch blocker.

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

## Intentionally Skipped For Now

- Poll/wait helpers: no async CodeMesh behavior exists yet.
- Real command workflows beyond `init`, Project Registry scan/add/tree, and Readiness status: Hydration and Agent Prep commands are pending.
- Live/network checks: out of scope for MVP fixture coverage.
- Screenshot proof: not applicable to the current CLI-only harness.
