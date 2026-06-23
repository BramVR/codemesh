---
summary: "Accepted policy shape: optional repo-local `.codemesh.yml` before global or machine-local overrides."
read_when:
  - Changing Project Policy location, policy defaults, `.codemesh.yml`, global overrides, machine-local overrides, or readiness policy resolution.
---

# Optional repo-local policy

Status: Accepted

Project policy is optional and repo-local when present, using `.codemesh.yml`. Agent prep must work for ordinary Git repos without configuration, but project-specific readiness rules belong near the project so they can travel across machines and eventually be committed when useful. Global or machine-local overrides can come later for private policy.
