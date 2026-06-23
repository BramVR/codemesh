# MVP command set

CodeMesh v0 ships `init`, `scan`, `add`, `tree`, `status`, `hydrate`, `agent prepare`, `runs`, and `clean`. These commands prove the canonical workspace index and the agent handoff workflow without a daemon, mount layer, automatic placeholders, secret materialization, cloud sync, UI, or file-level lazy hydration.

The runnable surface is the CLI help plus [Command Catalog](../commands.md). Future directions such as machine registration, synced manifests, secret materialization, and multi-machine sync remain planned until they appear in both places.
