# Agent Notes

Read `~/Projects/agent-scripts/AGENTS.MD` before anything.

## Project Shape

Scaffold only. Docs and ADRs define the product before code.

CodeMesh augments Git and local filesystems; it does not replace Git in the MVP.

No build system yet. If one appears, add exact lint/typecheck/test commands to `AGENTS.md`.

## Workflow

Read `CONTEXT.md` before product/design work.

Read `docs/adr/` before architecture changes. Add ADRs for hard-to-reverse choices.

Before coding sync, secrets, or filesystem behavior: document the state model first.

Before handoff after code changes: run relevant lint/typecheck/tests. If none exist yet, state that.
