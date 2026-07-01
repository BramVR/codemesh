---
summary: "Command reference for codemesh agent run."
read_when:
  - "Updating public command reference pages."
title: "codemesh agent run"
description: "Run one command inside a prepared agent workspace."
permalink: /commands/agent-run
---

# codemesh agent run

## Syntax

```sh
codemesh agent run <run-id> --label label [--timeout duration] -- <command...>
```

## Purpose

Run one explicitly supplied local command inside a prepared agent workspace. CodeMesh uses the recorded run workspace as cwd, captures stdout and stderr under the managed run directory, and appends command metadata through the Agent Run Contract to `codemesh-run.json` and local state.

The command record includes the command label, cwd, env binding summary, base provenance, exit code, duration, and output file paths. Env values and command output are not embedded in metadata.

`--timeout` accepts Go durations such as `30s`, `5m`, or `1h`. When omitted, CodeMesh uses a 10 minute timeout. Only one command may run against a given run ID at a time; concurrent attempts are refused so output files and audit metadata cannot clobber each other.

## Safe Example

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

codemesh init "$workspace"
codemesh add "$workspace/demo-project" --alias demo-project
codemesh agent prepare demo-project --base main --profile codex
run_id="$(codemesh runs | awk '/^- / {print $2; exit}')"
codemesh agent run "$run_id" --label workspace-root -- git rev-parse --show-toplevel
codemesh runs
```

## Current Limitations

- Runs one local command only; it does not launch or supervise a paid provider, remote agent, daemon, or long-lived process.
- Inherits the current process environment but records only a secret-free env summary.
- Captures stdout and stderr to local managed files; it does not sync output to another machine.
- Refuses unknown runs and run workspaces outside CodeMesh-managed agent storage.

Back to [Command Catalog](../commands.md).
