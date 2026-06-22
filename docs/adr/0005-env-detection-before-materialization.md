# Env detection before materialization

CodeMesh MVP detects missing env files and variables but does not materialize secrets. Secret handling has high blast radius, and the first agent-prep workflow can deliver value by blocking unsafe handoffs with clear missing-env diagnostics. Secret backend integration can be added after project readiness checks are useful without touching secret values.
