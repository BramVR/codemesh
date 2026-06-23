---
summary: "Accepted MVP shape: visible canonical workspace index through scan, tree, and status."
read_when:
  - Changing canonical workspace behavior, Project Registry visibility, scan/tree/status semantics, missing project display, or hydration plans.
---

# Canonical workspace index in MVP

Status: Accepted

CodeMesh MVP includes a visible canonical workspace index through commands like `scan`, `tree`, and `status`, not only `agent prepare`. Agent prep is the wedge, but the product is a workspace fabric: users should see the intended project tree, present or missing projects, freshness, and readiness before lazy hydration or multi-machine sync exists.
