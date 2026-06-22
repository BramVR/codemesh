# Go CLI and local SQLite

CodeMesh starts as a Go CLI backed by local SQLite under `~/.codemesh`. Go fits the cross-platform CLI and future daemon shape, while SQLite fits project index, status, and agent run metadata better than hand-edited YAML. Exportable or synced manifests can come later after the local state model is stable.
