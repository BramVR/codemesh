---
summary: "Accepted deep MVP module boundaries for Project Registry, Readiness, Agent Prep, Agent Run Contract, Env Readiness, and Env Binding."
read_when:
  - Changing module ownership, Project Registry, Readiness, Agent Prep, Agent Run Contract, Env Readiness, command adapters, or cross-module contracts.
---

# Deep MVP modules

Status: Accepted

CodeMesh MVP deepens Project Registry, Readiness, Agent Prep, Agent Run Contract, Env Readiness, and Env Binding. These modules concentrate the core rules from the PRD and ADRs: project identity and canonical workspace state, warn/block diagnostics, agent workspace handoff, versioned run metadata, secret-free env checks, and private fake-provider binding/materialization. Current commands stay thin adapters over these modules so behavior is testable through stable interfaces instead of duplicated across CLI paths.
