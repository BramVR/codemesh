---
summary: "Accepted implementation base: Go CLI with local SQLite state under CodeMesh home."
read_when:
  - Changing runtime language, CLI packaging, SQLite state, migrations, CodeMesh home layout, daemon plans, or manifest export/sync plans.
---

# Go CLI and local SQLite

Status: Accepted

CodeMesh starts as a Go CLI backed by local SQLite under `~/.codemesh`. Go fits the cross-platform CLI and future daemon shape, while SQLite fits project index, status, and agent run metadata better than hand-edited YAML. Exportable or synced manifests can come later after the local state model is stable.
