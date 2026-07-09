---
summary: "Repo-local Project Policy reference for defaults, env readiness, toolchain readiness, include-docs, and no-secret-values behavior."
read_when:
  - Changing `.codemesh.yml`, policy parsing, env readiness, toolchain readiness, include-docs, Agent Prep policy behavior, or no-secret-values handling.
---

# Project Policy Reference

Project Policy is optional repo-local metadata. When present, CodeMesh reads `<project>/.codemesh.yml`; when absent, CodeMesh uses built-in defaults.

## Defaults

Absent `.codemesh.yml` means:

- base branch: discover the remote default branch, then fall back to `main` when it is not advertised
- env mode: `warn`
- required env files: none
- required env keys: none
- include docs: none from policy; Agent Prep may discover common project docs separately
- local-only paths: none

Callers may still pass `--base` to choose an exact branch, and repositories may set `agent.base` when policy should override the remote default.

## File Shape

<!-- codemesh-policy-example:start -->
```yaml
agent:
  base: main
  env:
    mode: block
    required_files:
      - .env.local
      - .env.agent
    required_keys:
      - CODEMESH_AGENT_TOKEN
      - CODEMESH_PROVIDER_PROFILE
  toolchain:
    mode: warn
    requirements:
      - go
      - mise
  include_docs:
    - AGENTS.md
    - CONTEXT.md
    - docs/adr/**
local_only:
  paths:
    - path: node_modules
      category: dependency
    - path: dist
      category: build
    - path: .cache/codemesh
      category: cache
    - path: generated/client
      category: generated
    - path: .env.local
      category: env-config
    - path: .DS_Store
      category: os-specific
```
<!-- codemesh-policy-example:end -->

## Fields

`agent.base`: Git branch name used when a command does not pass `--base`. When omitted, CodeMesh uses the discoverable remote default branch before falling back to `main`. The value must be a valid Git branch name.

`agent.env.mode`: action for missing env requirements. Allowed values: `warn` or `block`. Default: `warn`.

`agent.env.required_files`: project-relative file paths that must exist before handoff. CodeMesh checks presence and regular-file shape only. Absolute paths and paths escaping the checkout are invalid.

`agent.env.required_keys`: env variable names that must be present in the process environment. Entries are names only; assignments such as `TOKEN=value` are invalid.

`agent.toolchain.mode`: action for missing or unknown toolchain requirements. Allowed values: `warn` or `block`. Default: `warn`.

`agent.toolchain.requirements`: logical toolchain names that CodeMesh should report on before handoff, such as `go`, `node`, or `mise`. Entries are command names only, not paths, install commands, or build scripts.

`agent.include_docs`: project-relative docs or glob-like path patterns that express which project context should travel with an agent handoff. Absolute paths and paths escaping the checkout are invalid. The Policy Module parses and preserves the list; it does not read doc contents during readiness checks. Agent Prep treats these as additive handoff docs on top of the default docs it discovers for ordinary repos and records only matched project-relative paths plus source metadata.

`local_only.paths`: project-relative paths that CodeMesh should classify as machine-local rather than source content. Each entry has `path` and `category`. Paths must be relative to the project checkout and must not escape it. Allowed local-only categories are `dependency`, `build`, `cache`, `generated`, `env-config`, and `os-specific`. `source` is the implicit default for ordinary Git-managed content and is rejected inside `local_only.paths` because source-as-local-only is ambiguous and unsafe.

## Readiness Behavior

Readiness resolves policy from the source checkout before checking env requirements. Invalid policy blocks readiness with an actionable diagnostic naming the policy file and field.

Local-only policy is reporting and enforcement metadata. `tree`, `status`, Hydration Planner JSON, bootstrap JSON, hydrate/access JSON, and Agent Run Contract metadata include declared `local_only_paths` when policy is available. CodeMesh does not create dependency directories, build output, caches, generated files, env/config files, or OS-specific files from this policy.

Env requirements are checked without secret access:

- required files: `stat` only; file contents are never opened
- required keys: presence only; values are never read
- diagnostics name missing file paths or key names only
- missing requirements are warnings in `warn` mode and blockers in `block` mode

Provider-specific binding references do not belong in `.codemesh.yml`. Use local private Env Bindings to map logical env key requirements to provider references.

Toolchain requirements are reporting/delegation only. CodeMesh records `present`, `missing`, or `unknown` status from the active detection adapter; it does not install tools, run package-manager setup, write tool version files, create dependency directories, or build development environments. Unknown status means CodeMesh could not prove the toolchain is present with the current adapter. Missing and unknown requirements follow `agent.toolchain.mode`: warnings in `warn` mode, blockers in `block` mode.

Toolchain results separate project facts from host facts. Project facts identify the declared requirement from policy. Host facts identify the detected command name and version when a host detector can observe them. Host version probes reject project-local command matches; those remain `unknown` instead of executing checkout-controlled binaries. This lets agents distinguish a project policy problem from machine setup without asking CodeMesh to mutate the machine.

Agent Prep uses the requested base when passed. Without `--base`, it resolves `agent.base` from policy, then the discoverable remote default branch, then `main`. Missing or invalid selected bases block readiness. Agent Prep checks the policy from the fetched base for handoff env requirements, while env file presence is checked against the local source checkout because those files are usually untracked local setup.

Agent Prep resolves handoff docs from the prepared clone, not the source checkout, so metadata points at files available to the agent on the selected base. It records project-relative paths only; it does not copy docs, embed doc contents, or read doc contents into metadata. The default handoff docs are `AGENTS.md`, `CONTEXT.md`, `README.md`, and Markdown files directly under `docs/adr/`; `agent.include_docs` adds project-specific paths or patterns. Valid policy patterns that select no available docs produce `handoff-doc-missing` warnings, not blockers. Command stdout reports only `handoff_docs: N`; the selected paths and `default` or `policy` source metadata live in `codemesh-run.json`.

Agent Prep records local-only policy in `codemesh-run.json` from the prepared clone. Untracked machine-local directories such as `node_modules` remain outside the prepared clone because CodeMesh clones Git source content rather than copying the local source checkout.

## No Secret Values

Project Policy must contain names and paths, never secret values. CodeMesh does not read, store, print, or materialize secret values from policy, env files, env variables, readiness diagnostics, or agent-run metadata.
