# Agent Instructions

Before answering architecture questions or changing code in this repository, use Abra MCP when it is available.

1. Use scope `repo:abra`.
2. Call `working_memory_compose` with the current task and `agent: "codex"` before implementation work.
3. Follow the returned `agent_decision`, `verification`, `memory_health`, conflicts, impact map, and validation plan.
4. If Abra MCP is unavailable, run `abra scope` to confirm the scope and continue with normal repository inspection.
5. Do not include secrets, API keys, local tokens, or private business context in committed files.

