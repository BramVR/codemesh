# Project Policy Reference

Read when: changing `.codemesh.yml`, policy parsing, env readiness, or Agent Prep policy behavior.

Project Policy is optional repo-local metadata. When present, CodeMesh reads `<project>/.codemesh.yml`; when absent, CodeMesh uses built-in defaults.

## Defaults

Absent `.codemesh.yml` means:

- base branch: `main`
- env mode: `warn`
- required env files: none
- required env keys: none
- include docs: none from policy; Agent Prep may discover common project docs separately

CodeMesh does not infer the remote default branch yet. A repository whose agent base is not `main` must set `agent.base` or callers must pass `--base`.

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
  include_docs:
    - AGENTS.md
    - CONTEXT.md
    - docs/adr/**
```
<!-- codemesh-policy-example:end -->

## Fields

`agent.base`: Git branch name used when a command does not pass `--base`. Default: `main`. The value must be a valid Git branch name.

`agent.env.mode`: action for missing env requirements. Allowed values: `warn` or `block`. Default: `warn`.

`agent.env.required_files`: project-relative file paths that must exist before handoff. CodeMesh checks presence and regular-file shape only. Absolute paths and paths escaping the checkout are invalid.

`agent.env.required_keys`: env variable names that must be present in the process environment. Entries are names only; assignments such as `TOKEN=value` are invalid.

`agent.include_docs`: project-relative docs or glob-like path patterns that express which project context should travel with an agent handoff. The Policy Module parses and preserves the list; it does not read doc contents during readiness checks.

## Readiness Behavior

Readiness resolves policy from the source checkout before checking env requirements. Invalid policy blocks readiness with an actionable diagnostic naming the policy file and field.

Env requirements are checked without secret access:

- required files: `stat` only; file contents are never opened
- required keys: presence only; values are never read
- diagnostics name missing file paths or key names only
- missing requirements are warnings in `warn` mode and blockers in `block` mode

Agent Prep uses the requested base when passed. Without `--base`, it resolves `agent.base` from policy, falling back to `main`. It checks the policy from the fetched base for handoff env requirements, while env file presence is checked against the local source checkout because those files are usually untracked local setup.

## No Secret Values

Project Policy must contain names and paths, never secret values. CodeMesh does not read, store, print, or materialize secret values from policy, env files, env variables, readiness diagnostics, or agent-run metadata.
