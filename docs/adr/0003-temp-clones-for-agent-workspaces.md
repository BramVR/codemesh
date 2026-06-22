# Temp clones for agent workspaces

Agent workspaces start as temporary clones under CodeMesh-managed storage, not Git worktrees. Temp clones are slower and less disk-efficient, but they avoid the branch-locking and shared-state confusion that makes worktrees brittle for concurrent agents. CodeMesh can add shared object caches, sparse checkout, partial clone, or worktree support later after the handoff workflow is correct.
