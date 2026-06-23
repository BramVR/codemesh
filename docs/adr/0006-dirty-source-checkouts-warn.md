---
summary: "Accepted readiness rule: dirty source checkouts warn but do not block agent handoff."
read_when:
  - Changing dirty checkout detection, readiness warning/blocker semantics, Agent Prep handoff gates, or source checkout diagnostics.
---

# Dirty source checkouts warn

Status: Accepted

`codemesh agent prepare` warns about dirty source checkouts but does not block on them. Agent workspaces are temporary clones prepared from the remote base, so local uncommitted changes are not part of the agent input unless explicitly requested. The warning prevents silent surprise without making unrelated local work block agent handoff.
