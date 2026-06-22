# Context

CodeMesh is a developer workspace fabric for keeping code projects usable across laptops, servers, and cloud agents.

## Glossary

- Workspace: the user-visible root, usually `~/Code` or `~/Projects`.
- Canonical workspace: the intended project tree CodeMesh knows about, whether every project is currently hydrated locally or not.
- Project: a repo or local working directory managed by CodeMesh.
- Project index: metadata describing known projects, remotes, local paths, and desired placement.
- Project registry: the authoritative local view of the canonical workspace, including known projects, identities, aliases, desired paths, and presence.
- State store: local CodeMesh database containing project index, machine facts, scan results, and agent run metadata.
- Project identity: the stable way CodeMesh recognizes one project across paths and machines; initially a normalized Git remote URL plus local alias.
- Machine: a laptop, desktop, server, VM, or cloud agent host.
- Hydration: making a project or file content available on a machine.
- Placeholder: metadata-only path visible before content is hydrated.
- Missing project: a project known to the canonical workspace but absent from the local filesystem.
- Materialization: writing generated local files such as `.env` or tool config.
- Env requirement: a project-declared file or variable expected before an agent can safely run.
- Env readiness: the result of checking env requirements without reading, writing, or storing secret values.
- Local-only path: machine-specific files, for example `node_modules`, build output, caches.
- Policy: rules for what syncs, hydrates, materializes, or stays private.
- Project policy: project-specific readiness rules, optionally stored in `.codemesh.yml`.
- Secret reference: a pointer to a secret in an external store, never the secret value itself.
- Agent workspace: scoped project view prepared for an automated coding agent.
- Agent workspace prep: the act of making an agent workspace fresh, configured, policy-checked, and ready to hand to an agent.
- Agent run: one prepared agent workspace plus its metadata, created for a specific task or handoff.
- Readiness: the result of checking whether a project or agent workspace is present, fresh, configured, and safe enough for the requested action.
- Local discovery: finding projects by scanning existing workspace roots for Git repositories.
- Source checkout: the existing local project checkout CodeMesh uses to identify remotes, policy, and local warnings.

## Product Shape

CodeMesh should answer:

- Where is this project on this machine?
- Is this project stale, missing, dirty, or misconfigured?
- Can an agent get a fresh, configured workspace quickly?
- Can env/config readiness be checked now, and materialized later without committing it?
- Can a user keep one project layout across many machines?

CodeMesh should not initially answer:

- How do we replace Git history?
- How do we sync arbitrary user files?
- How do we become a general cloud drive?
