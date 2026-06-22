# ADR 0001: Product Boundary

Status: Accepted

## Context

The product idea is "Dropbox for devs": one reliable code workspace across machines and agents. The tempting failure mode is building a full file sync product or a Git replacement too early.

## Decision

CodeMesh MVP augments Git and the local filesystem.

It owns:

- project inventory
- canonical workspace layout
- machine readiness checks
- lazy repo setup
- local-only path policy
- env readiness checks
- agent workspace provisioning

It does not own:

- Git history
- arbitrary file sync
- build artifact sync
- secret materialization
- raw secret storage
- merge/conflict semantics for source code

## Consequences

The first version can be useful without kernel extensions, FUSE, or custom VCS semantics.

The system can later add lazy file hydration or virtual filesystem behavior after the state model is proven.
