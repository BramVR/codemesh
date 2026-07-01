# Vision

CodeMesh is a local-first developer workspace fabric for keeping Git-backed projects usable across machines and agents without replacing Git. It should deepen project registry, readiness, hydration, and agent handoff while keeping secrets, raw env values, and arbitrary file sync out of scope.

## Merge by Default

- Bug fixes with clear cause in registry, readiness, hydration, manifest, or agent-prep paths.
- Small CLI improvements that preserve stable `--json` output and exit-code contracts.
- ADR-aligned implementation slices from the MVP command and module boundaries.
- Tests, e2e harness improvements, and docs-list/front-matter cleanup.
- Documentation updates that sharpen glossary terms, state model, trust, or command behavior.

## Needs Sign-Off

- Replacing Git, syncing arbitrary user files, or becoming a general cloud drive.
- Secret storage, broad env materialization, or hidden credential handling.
- Daemon, mount, placeholder filesystem, or automatic hydration semantics.
- Broad command-surface changes, schema migrations, or manifest format changes.
- Toolchain installation or machine bootstrap behavior beyond reporting and delegation.
