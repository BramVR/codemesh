---
summary: "Install CodeMesh from the current source checkout."
read_when:
  - "Updating public install instructions."
title: "Install"
description: "Build CodeMesh from source and verify the local CLI."
---

# Install

CodeMesh is currently built from source. The MVP is a Go CLI with local SQLite state under CodeMesh home.

## Requirements

- Go toolchain matching the module version.
- Git.
- Node.js 20 or newer for the local docs-site build and smoke tests.
- A terminal with a writable source checkout.

## Build From Source

```sh
git clone https://github.com/BramVR/codemesh.git
cd codemesh
make build
./dist/codemesh --help
```

The default build target writes `dist/codemesh`.

## Run Without Installing

```sh
go run ./cmd/codemesh --help
```

Use this path while developing CodeMesh or testing a branch.

## Install For Your User

```sh
mkdir -p ~/.local/bin
install -m 755 ./dist/codemesh ~/.local/bin/codemesh
codemesh --help
```

If your shell cannot find `codemesh`, add `~/.local/bin` to `PATH`.

## Verify The Docs And CLI

```sh
make docs-site
make docs-site-test
make test
```

Next: [Quickstart](quickstart.md).
