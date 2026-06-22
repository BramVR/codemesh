# Explicit hydration before placeholders

CodeMesh MVP shows missing projects in `tree` and `status` but does not create placeholder directories. Placeholder paths without mount or daemon semantics can mislead tools and agents into treating empty folders as real code. Hydration starts as an explicit command, with automatic placeholders deferred until CodeMesh has a clear workspace mount model.
