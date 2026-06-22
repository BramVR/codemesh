# Deep MVP modules

CodeMesh MVP should deepen four modules before implementation: Project Registry, Readiness, Agent Prep, and Env Readiness. These modules concentrate the core rules from the PRD and ADRs: project identity and canonical workspace state, warn/block diagnostics, agent workspace handoff, and secret-free env checks. Commands should stay thin adapters over these modules so behavior is testable through stable interfaces instead of duplicated across CLI paths.
