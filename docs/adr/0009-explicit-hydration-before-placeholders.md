---
summary: "Superseded boundary: explicit hydration came before placeholder sentinels; automatic placeholders remain deferred."
read_when:
  - Changing hydration, missing project directories, placeholder behavior, mount/daemon plans, or file-level lazy hydration.
---

# Explicit hydration before placeholders

Status: Superseded in part by [ADR 0014](0014-explicit-sentinel-placeholders.md)

CodeMesh MVP originally showed missing projects in `tree` and `status` but did not create placeholder directories. Placeholder paths without mount or daemon semantics can mislead tools and agents into treating empty folders as real code. Hydration starts as an explicit command, with automatic placeholders deferred until CodeMesh has a clear workspace mount model.

ADR 0014 keeps this safety boundary for automatic placeholders, mounts, daemons, and lazy path-access hydration, but allows explicit metadata-only sentinel placeholders that do not look like Git checkouts.
