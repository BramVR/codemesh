---
summary: "Accepted wedge: agent workspace prep before general machine bootstrap or generic file sync."
read_when:
  - Changing Agent Prep priority, machine bootstrap, general sync scope, or first-workflow positioning.
---

# Agent workspace prep first

Status: Accepted

CodeMesh starts with agent workspace prep rather than general new-machine bootstrap or generic file sync. The video's concrete pain is agentic development across many machines: stale bases, missing env, inconsistent repo locations, and cloud agents needing the right project structure. This gives the product a sharp first workflow we can use immediately while preserving the broader workspace fabric direction.
