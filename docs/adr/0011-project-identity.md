---
summary: "Accepted project identity: normalized Git remote URL plus human alias."
read_when:
  - Changing Project identity, remote normalization, alias semantics, path matching, or cross-machine project matching.
---

# Project identity

Status: Accepted

CodeMesh identifies projects by normalized Git remote URL plus a human alias. Paths vary across machines and remote URL forms vary between SSH and HTTPS, so the alias gives stable CLI UX while the normalized remote anchors the project across machines. Alias conflicts are errors.
