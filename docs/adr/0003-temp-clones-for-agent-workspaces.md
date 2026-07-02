---
summary: "Accepted agent workspace strategy: temporary clones before Git worktrees or shared checkout state."
read_when:
  - Changing Agent Prep checkout strategy, temp clone layout, worktree support, object caching, sparse checkout, or partial clone behavior.
---

# Temp clones for agent workspaces

Status: Accepted

Agent workspaces start as temporary full clones under CodeMesh-managed storage, not Git worktrees. The current Clone Strategy is `full-clone`: full Git history with a complete working tree for the selected branch. Temp full clones are slower and less disk-efficient, but they avoid the branch-locking and shared-state confusion that makes worktrees brittle for concurrent agents.

CodeMesh can add shared object caches, partial clone, sparse checkout, or worktree support later as explicit opt-in strategies after the handoff workflow is correct.
