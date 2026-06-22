# Project identity

CodeMesh identifies projects by normalized Git remote URL plus a human alias. Paths vary across machines and remote URL forms vary between SSH and HTTPS, so the alias gives stable CLI UX while the normalized remote anchors the project across machines. Alias conflicts are errors.
