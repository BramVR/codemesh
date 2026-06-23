---
summary: "Safe local CodeMesh quickstart with isolated state and a local Git remote."
read_when:
  - "Updating public quickstart examples."
title: "Quickstart"
description: "Try CodeMesh with isolated local state, a temp workspace, and a local Git remote."
---

# Quickstart

This quickstart uses isolated local state and a local Git remote. It does not touch the user's normal CodeMesh home, project workspace, GitHub account, or secrets.

## 1. Build The CLI

```sh
make build
```

## 2. Create A Temp Workspace

```sh
demo="$(mktemp -d)"
export CODEMESH_HOME="$demo/codemesh-home"
workspace="$demo/workspace"
seed="$demo/seed"
remote="$demo/demo.git"

mkdir -p "$workspace" "$seed"
git -C "$seed" init -b main
printf '# demo\n' > "$seed/README.md"
git -C "$seed" add README.md
git -C "$seed" -c user.name='CodeMesh Demo' -c user.email='demo@example.invalid' commit -m 'Initial demo'
git clone --bare "$seed" "$remote"
git clone "$remote" "$workspace/demo-project"
```

## 3. Register And Inspect

```sh
./dist/codemesh init "$workspace"
./dist/codemesh add "$workspace/demo-project" --alias demo-project
./dist/codemesh tree
./dist/codemesh status demo-project --base main
```

## 4. Prepare An Agent Workspace

```sh
./dist/codemesh agent prepare demo-project --base main --profile codex
./dist/codemesh runs
```

Agent Prep creates a temporary clone under the isolated `CODEMESH_HOME`, writes metadata, and reports the ready workspace path.

## 5. Clean The Demo Run

```sh
./dist/codemesh clean --older-than 0d
rm -rf "$demo"
```

## Safety Checklist

- `CODEMESH_HOME` points inside the temp directory.
- The remote is a local bare Git repository.
- No GitHub remote, provider token, env file content, or secret value is needed.
- Cleanup removes only the temp demo directory you created.

For the full command list, continue to [Command Catalog](commands.md). Direct references: [init](commands/init.md), [add](commands/add.md), [scan](commands/scan.md), [tree](commands/tree.md), [status](commands/status.md), [hydrate](commands/hydrate.md), [agent prepare](commands/agent-prepare.md), [runs](commands/runs.md), and [clean](commands/clean.md).
