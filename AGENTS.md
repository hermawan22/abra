# Agent Instructions

Before answering architecture questions or changing code in this repository, use Abra MCP when it is available.

1. Use exact scope `repo:abra`.
2. If discovering scopes first, call `discover_scopes` with `expected_scope: "repo:abra"` so this repo is not hidden by unrelated release or perf scopes.
3. Call `working_memory_compose` with the current task, scope `repo:abra`, and `agent: "codex"` before implementation work.
4. Follow the returned `agent_decision`, `verification`, `memory_health`, conflicts, impact map, and validation plan.
5. If Abra MCP tools are unavailable or an AI client says Abra has no context, run `abra agents verify . --scope repo:abra --agent codex --json` first, then run `abra doctor` and fix MCP/API/token/model readiness before re-ingesting.
6. If `server_ready=true` but `agent_ready=false`, reinstall the MCP integration and fully restart the AI client. Run ingest only when verify proves the exact scope is missing or source-backed memory is empty: `abra ingest . --code --scope repo:abra`.
7. Do not include secrets, API keys, local tokens, or private business context in committed files.
