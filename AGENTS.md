# Agent Notes

Read `CONTEXT.md` before product/design work.

Read `docs/adr/` before architecture changes. Add ADRs for hard-to-reverse choices.

Product invariant: CodeMesh augments Git and local filesystems; it does not replace Git in the MVP.

Before coding sync, secrets, or filesystem behavior: document the state model first.

Before handoff after code changes: run relevant lint/typecheck/tests. If none exist yet, state that.
