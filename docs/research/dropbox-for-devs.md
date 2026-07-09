# Dropbox for Devs Research

Date: 2026-06-22

## Video-Derived Brief

Source video: https://youtu.be/wEAb0x3wTRc?is=Jv4uofUg2prjELPI

The "Dropbox for devs" section is framed as a response to filesystem pain, multi-machine agent work, and Git/worktree friction.

Observed pains:

- Many build machines: laptops, Mac minis, Linux boxes, cloud agents.
- Worktrees drift: agent work starts from stale `main`.
- Env drift: one machine has the right variables, another does not.
- Layout drift: projects live in different directories on each machine.
- Git is not enough: managing many Git repos through another Git repo leads to submodule/repo-management pain.
- Dependency folders are OS-specific: `node_modules` cannot be blindly shared.
- Full file sync is not quite right: desired behavior is a canonical code tree with lazy/project-aware hydration.

Desired behavior:

- Same code folder structure on every machine.
- Existing tools still work.
- Code folders appear automatically on new machines or cloud agents.
- At minimum, structure exists before content.
- Touching/exploring a path pulls that part down on demand.
- Dropbox/Google Drive-style ignore rules for developer folders.
- Machine-local handling for generated/dependency directories.

Key quote paraphrase:

- Not a Git replacement.
- Not generic file sync.
- A developer-aware workspace tree that can populate itself where needed.

## Existing Project Families

### General file sync

- Syncthing: https://github.com/syncthing/syncthing
  - 85.7k stars, active 2026-06-22.
  - Continuous peer-to-peer file synchronization.
  - Strong safety/security posture.
  - Gap: not Git-aware, not project-status-aware, not secret/env materialization, not agent workspace provisioning.

- Mutagen: https://github.com/mutagen-io/mutagen
  - 4.2k stars, active 2026-06-22.
  - High-performance file sync and network forwarding for remote development.
  - Strong fit for local-to-remote development loops.
  - Gap: session-level sync, not canonical project inventory across all machines.

- Unison: https://github.com/bcpierce00/unison
  - 5.3k stars, active 2026-06-22.
  - Mature two-way file synchronizer.
  - Gap: low-level sync primitive, not developer workspace control plane.

### Git-scale and lazy checkout

- VFS for Git: https://github.com/microsoft/VFSForGit
  - 6.1k stars, active 2026-06-22.
  - Virtualizes a Git working directory and downloads objects as needed.
  - Gap: Windows/Git-service-specific, large-monorepo oriented, not cross-repo workspace fabric.

- Scalar: https://github.com/microsoft/scalar
  - 1.5k stars, active 2026-06-18.
  - Large-monorepo Git performance tooling without filesystem virtualization.
  - Gap: repo-local Git performance, not cross-machine project topology.

- Git sparse checkout / partial clone
  - Native Git feature set.
  - Lets working directories contain only selected paths and fetch missing blobs on demand.
  - Gap: per-repo mechanism; needs a higher-level tool to manage roles, policies, many repos, and agents.

- Sapling: https://github.com/facebook/sapling
  - 6.8k stars, active 2026-06-22.
  - Scalable source control system with Git interop.
  - Gap: source-control replacement/augmentation, not the `~/Code` sync layer.

- JJ: https://github.com/jj-vcs/jj
  - 29.7k stars, active 2026-06-22.
  - Git-compatible VCS with better change/snapshot ergonomics.
  - Gap: improves source control UX, not machine/project/env sync.

### Multi-repo inventory

- ghq: https://github.com/x-motemen/ghq
  - 3.7k stars, active 2026-06-22.
  - Remote repository management.
  - Gap: clone/path convention only; no stale/env/agent readiness model.

- Android repo: https://github.com/GerritCodeReview/git-repo
  - 435 stars, active 2026-06-20.
  - Multiple Git repository tool.
  - Gap: manifest-driven source checkout, mostly for large source trees; no local machine fabric or secrets.

- git-remote-dropbox: https://github.com/anishathalye/git-remote-dropbox
  - 3.1k stars, active 2026-06-22.
  - Makes Dropbox act as a Git remote.
  - Gap: answers "Dropbox as origin", not "developer workspace across machines".

### Dotfiles, env, and secrets

- chezmoi: https://github.com/twpayne/chezmoi
  - 20.3k stars, active 2026-06-22.
  - Secure dotfile management across diverse machines.
  - Gap: home config, not repo inventory and Git readiness.

- direnv: https://github.com/direnv/direnv
  - 15.2k stars, active 2026-06-22.
  - Directory-scoped env loading.
  - Gap: loads env, does not distribute policy or secrets.

- mise: https://github.com/jdx/mise
  - 29.9k stars, active 2026-06-22.
  - Dev tools, env vars, task runner.
  - Gap: project runtime setup, not multi-machine code tree sync.

- SOPS: https://github.com/getsops/sops
  - 22.2k stars, active 2026-06-22.
  - Encrypted secrets files.
  - Gap: secret storage primitive, not project/machine materialization workflow.

- git-crypt: https://github.com/AGWA/git-crypt
  - 9.8k stars, active 2026-06-22.
  - Transparent file encryption in Git.
  - Gap: encrypted committed files; does not solve local `.env` drift, agent scopes, or project readiness.

- dotenvx: https://github.com/dotenvx/dotenvx
  - 5.6k stars, active 2026-06-22.
  - Secure dotenv workflow.
  - Gap: env-file focused, not full workspace topology.

- Infisical: https://github.com/Infisical/infisical
  - 27.5k stars, active 2026-06-22.
  - Open-source secrets/certificates/access platform.
  - Gap: full secrets platform; CodeMesh should integrate, not compete.

### Reproducible dev environments

- Devbox: https://github.com/jetify-com/devbox
  - 12.1k stars, active 2026-06-22.
  - Predictable dev environments.
  - Gap: per-project environment, not global project tree.

- devenv: https://github.com/cachix/devenv
  - 7.0k stars, active 2026-06-22.
  - Nix-based reproducible dev environments.
  - Gap: environment construction, not repo placement/state.

- Flox: https://github.com/flox/flox
  - 4.0k stars, active 2026-06-22.
  - Deterministic SDLC foundation.
  - Gap: environment/runtime layer, not machine workspace inventory.

- devcontainers/cli: https://github.com/devcontainers/cli
  - 2.8k stars, active 2026-06-22.
  - Reference CLI for `devcontainer.json`.
  - Gap: container spec runner, not project fabric.

### Remote/cloud/agent workspaces

- DevPod: https://github.com/loft-sh/devpod
  - 15.0k stars, active 2026-06-22.
  - Open-source Codespaces-like client for devcontainer environments on any backend.
  - Gap: provisions one workspace from a repo/config; does not manage a user's full project tree across machines.

- Coder: https://github.com/coder/coder
  - 13.6k stars, active 2026-06-22.
  - Self-hosted cloud dev environments and AI coding agents.
  - Gap: infrastructure/workspace platform; CodeMesh could feed it project manifests and env policy.

- Gitpod: https://github.com/gitpod-io/gitpod
  - 13.7k stars, active 2026-06-22.
  - Cloud development environments.
  - Gap: hosted workspace platform, not local-first cross-machine project topology.

- Daytona: https://github.com/daytonaio/daytona
  - 72.3k stars, active 2026-06-22.
  - Secure and elastic infrastructure for running AI-generated code.
  - Gap: agent/runtime sandbox side; not the local `~/Code` source-of-truth layer.

## Gap Analysis

No single project found combines all of:

- Canonical multi-machine project tree.
- Git-aware freshness and dirty-state checks across many repos.
- Machine-specific ignore/materialization policy.
- Safe `.env` and config materialization from secret references.
- Lazy repo setup or partial hydration.
- Agent workspace provisioning with scoped project access.
- Local-first UX that works before adopting a cloud dev platform.

The closest technical neighbors:

- Syncthing/Mutagen for transport and sync mechanics.
- VFS for Git/sparse checkout for lazy content ideas.
- ghq/Android repo for multi-repo inventory.
- chezmoi/SOPS/dotenvx/Infisical for config/secrets patterns.
- DevPod/Coder/Daytona for remote/agent workspaces.

The product opportunity is the control plane between these layers.

## Product Thesis

CodeMesh should be a workspace control plane, not a sync engine.

It should model:

- Projects.
- Machines.
- Desired paths.
- Git remotes.
- Current local state.
- Required env/config.
- Local-only directories.
- Agent access profiles.

It should orchestrate existing primitives:

- `git clone`, `git fetch`, sparse checkout, partial clone.
- Secret backends such as 1Password, SOPS, dotenvx, Infisical, or OS keychains.
- Dev environment tools such as mise, devbox, devenv, direnv, or devcontainers.
- Optional file sync engines later.

## PR Proof Requirement

Changes that affect the Dropbox-for-developers workflow need reviewer-visible proof, not only unit tests. The required free lane is the `crabbox-pr-proof` GitHub Actions workflow. It runs on standard GitHub-hosted public-repo runners with isolated local fixtures, then uploads `codemesh-crabbox-pr-proof` artifacts showing the canonical workspace tree, per-machine placement or presence, planned bootstrap or hydration actions, and before/after state for mutating fixture flows.

The lane must fail when proof is skipped, fake-only, incomplete, or unsafe to publish. Public proof must not include raw secrets, private endpoints, personal local paths outside isolated fixture placeholders, internal model names, or sensitive generated logs.

## MVP Direction

This section is historical research direction. The current runnable command surface is [Command Catalog](../commands.md); commands or behavior below are planned unless the catalog lists them as current.

Build the boring version first:

1. Project inventory.
   - `codemesh add ~/Projects/foo`
   - current: records path and remote.
   - planned: branch and package/runtime hints.

2. Machine inventory.
   - `codemesh machine register`
   - current: records hostname, OS, arch, workspace root, and timestamps in local state.
   - planned: allowed secret scopes.

3. Status.
   - `codemesh status`
   - current: shows missing projects, stale default branches, dirty repos, and missing env.
   - planned: missing toolchain.

4. Bootstrap.
   - `codemesh bootstrap`
   - planned: creates canonical folders and clones missing repos into correct paths.

5. Policy file.
   - `.codemesh.yml`
   - current: optional repo-local policy for local-only paths, env readiness, docs intent, and agent defaults.
   - planned: user-level policy, env materialization, hydration profile, and richer agent profiles.

6. Env materialization.
   - `codemesh env materialize foo`
   - planned: writes `.env.local` or similar from secret references.
   - never stores raw secrets in CodeMesh state.

7. Agent workspace.
   - `codemesh agent prepare foo --profile test`
   - current: creates a temp clone and verifies readiness/freshness without reading or writing secret values.
   - planned: worktree options and approved config materialization.

## Later Bets

- Lazy path hydration through sparse checkout and partial clone.
- Background daemon for freshness notifications.
- UI showing project tree health across machines.
- Remote machine provisioning.
- Integration with Coder/DevPod/Daytona as workspace targets.
- Optional transport adapter using Syncthing/Mutagen for non-Git files.
- Virtual filesystem only if the boring model proves insufficient.

## Sharp Product Boundaries

Avoid:

- Syncing `.git` folders through generic file sync.
- Becoming a secrets manager.
- Becoming a Nix/devcontainer competitor.
- Replacing Git before project inventory and status are excellent.
- Syncing dependency/build artifacts by default.

Must be excellent:

- Never lose local changes.
- Never leak secrets into Git, logs, or agent scopes.
- Make stale state obvious before an agent starts.
- Make a new machine useful in minutes.
- Keep the user-visible folder layout boring and predictable.

## Open Questions

- Should state live in a local SQLite DB, a Git-backed manifest repo, or both?
- Should project identity be remote URL, stable UUID, path, or a tuple?
- How should two machines with different canonical roots map the same project?
- Should `.codemesh.yml` live inside each project or in a global workspace manifest?
- How much should CodeMesh understand package managers and toolchains?
- Which secret backend should be first-class for the first Bram-local MVP?
- Is lazy file hydration worth doing early, or is lazy clone/sparse checkout enough?
- Should agent workspaces use Git worktrees, temp clones, or jj-style snapshots?
