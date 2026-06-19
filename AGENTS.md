# Agent Instructions

Before answering architecture questions or changing code in this repository, use Abra MCP when it is available.

1. Use exact scope `repo:abra`.
2. If discovering scopes first, call `discover_scopes` with `expected_scope: "repo:abra"` so this repo is not hidden by unrelated release or perf scopes.
3. Call `working_memory_compose` with the current task, scope `repo:abra`, and `agent: "codex"` before implementation work.
4. Follow the returned `agent_decision`, `verification`, `memory_health`, conflicts, impact map, and validation plan.
5. If Abra MCP is unavailable, run `abra scope` to confirm the scope and continue with normal repository inspection.
6. Do not include secrets, API keys, local tokens, or private business context in committed files.
