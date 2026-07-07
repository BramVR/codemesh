# Vision

CodeMesh is a local-first developer workspace fabric for making a developer's code folder usable across laptops, servers, NAS-backed machines, and cloud agents without replacing Git.

The north star is Theo Browne's "Dropbox for developers": one predictable code workspace structure everywhere, projects appearing in the same places without manual setup, stale or missing state made obvious, env/config readiness following the work safely, and generated or OS-specific directories handled differently from source.

CodeMesh should implement that vision in slices:

- Current MVP: local Project Registry, Readiness, explicit Hydration, and Agent workspace prep.
- First Dropbox-for-developers slice: canonical manifest import/export, Machine registration, per-Machine placement/presence, bootstrap, and Agent workspace prep from registry without a local Source checkout.
- North-star implementation slices: placeholder structure, lazy Hydration, safe env/config Materialization from Secret references, Local-only path policy enforcement, optional transport adapters for selected non-Git content, and managed Workspace integration that still feels boring from editors, shells, agents, and Git.

CodeMesh should not become a generic cloud drive. It should stay developer-aware, Git-compatible, secret-safe, and boring to use from existing editors, shells, agents, and Git workflows.

## Merge by Default

- Bug fixes with clear cause in registry, readiness, hydration, manifest, or agent-prep paths.
- Small CLI improvements that preserve stable `--json` output and exit-code contracts.
- ADR-aligned implementation slices from the MVP command and module boundaries.
- Tests, e2e harness improvements, and docs-list/front-matter cleanup.
- Documentation updates that sharpen glossary terms, state model, trust, or command behavior.
- First-slice Dropbox-for-developers work that preserves current local MVP behavior and keeps sync explicit: manifest import/export, Machine registration, per-Machine placement, bootstrap dry-run/clone, and source-less Agent workspace prep.

## Needs Sign-Off

These are high-risk north-star decisions, not exclusions from the vision.

- Replacing Git, syncing arbitrary user files, or becoming a general cloud drive.
- Secret storage, broad env materialization, or hidden credential handling.
- Daemon, mount, placeholder filesystem, or automatic hydration semantics.
- Broad command-surface changes, schema migrations, or manifest format changes.
- Toolchain installation or machine bootstrap behavior beyond reporting and explicit Git hydration.
- Lazy Hydration triggered by path access, filesystem hooks, or editor/Finder integration.
- Transport adapters for non-Git content, including R2/S3, Syncthing/Mutagen-style sync, or NAS-backed mirroring.
