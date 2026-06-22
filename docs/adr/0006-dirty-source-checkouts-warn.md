# Dirty source checkouts warn

`codemesh agent prepare` warns about dirty source checkouts but does not block on them. Agent workspaces are temporary clones prepared from the remote base, so local uncommitted changes are not part of the agent input unless explicitly requested. The warning prevents silent surprise without making unrelated local work block agent handoff.
