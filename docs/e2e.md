# CLI E2E Harness

Run:

```sh
make e2e
```

The harness builds `codemesh` once into a temp directory, runs cases against that binary, prints concise `PASS`, `FAIL`, and `SKIP` lines, and writes `tmp/e2e-report.json`.

Override the report path:

```sh
CODEMESH_E2E_REPORT=/tmp/codemesh-e2e.json make e2e
```

## Isolation

Each run creates a temp workspace with:

- `CODEMESH_HOME` under the harness temp directory.
- `HOME` under the harness temp directory.
- an empty temp Git config.
- local Git fixtures for future Project Registry, Readiness, Hydration, and Agent Prep cases.

The harness does not use GitHub, secrets, GUI automation, AppleScript, the user's `~/.codemesh`, or projects under `~/Projects`.

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

## Intentionally Skipped For Now

- Poll/wait helpers: no async CodeMesh behavior exists yet.
- Real command workflows: Project Registry, Readiness, Hydration, and Agent Prep commands are pending.
- Screenshot proof: not applicable to the current CLI-only harness.
