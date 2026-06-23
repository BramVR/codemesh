---
summary: "Accepted hydration boundary: explicit project hydration before placeholders, mounts, or daemon semantics."
read_when:
  - Changing hydration, missing project directories, placeholder behavior, mount/daemon plans, or file-level lazy hydration.
---

# Explicit hydration before placeholders

Status: Accepted

CodeMesh MVP shows missing projects in `tree` and `status` but does not create placeholder directories. Placeholder paths without mount or daemon semantics can mislead tools and agents into treating empty folders as real code. Hydration starts as an explicit command, with automatic placeholders deferred until CodeMesh has a clear workspace mount model.
