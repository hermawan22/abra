# Abra CLI

Abra is CLI-first. The public command surface is intentionally small so the
tool stays easy to install, script, and explain.

## First Run

```sh
abra setup
abra doctor
```

`setup` creates local configuration and starts the default stack. The default
embedding path is local Qwen; no OpenAI, npm, or cloud vendor is required.

For a project:

```sh
cd /path/to/project
abra scope
abra agent bootstrap --agent codex
```

Fully quit and reopen the AI client after bootstrap if it installed MCP config.
Then verify the exact scope:

```sh
abra agent ready . --scope <scope-from-abra-scope> --json
```

Ask Abra:

```sh
abra ask "What should I know before changing this project?" --scope <scope-from-abra-scope>
```

## Canonical Commands

```text
abra setup      configure Abra
abra up         start the local stack
abra down       stop the local stack
abra doctor     diagnose runtime, MCP, token, model, and memory readiness
abra scope      print the recommended scope for the current project

abra connect    register durable local, git, MCP, or webhook sources
abra sync       refresh a source or ingest a local path now
abra ask        answer with governed, cited memory
abra context    compose task-specific working context for an agent

abra agent      install, initialize, and verify AI-agent integrations
abra model      configure and operate embedding providers
abra brain      inspect recall and source-backed memory health
abra govern     inspect approvals, observations, and decision gates
abra plugin     inspect extension contracts
```

Use `abra help <command>` for focused help.

## Source Flow

For a one-time local project sync:

```sh
abra sync . --code --scope <scope>
```

For a durable local source watched by Abra workers:

```sh
abra connect local . --scope <scope> --schedule "@every 10m"
```

For a remote git source:

```sh
abra connect git https://github.com/owner/repo.git \
  --ref main \
  --scope repo:owner-repo \
  --schedule "@every 10m"
```

For a user-owned MCP exporter:

```sh
abra connect mcp https://mcp.example.com/mcp \
  --tool export_documents \
  --scope team:docs \
  --dry-run
```

Run the same command without `--dry-run` after the export returns normalized
Abra documents.

Refresh an existing source:

```sh
abra sync <source-config-id> --wait --wait-timeout 10m
```

Inspect source health:

```sh
abra connect status <source-config-id>
abra connect logs <source-config-id> --limit 20
```

## Agent Flow

Bootstrap Codex:

```sh
abra agent bootstrap --agent codex
```

Manual steps:

```sh
abra agent install codex
abra agent init --agent codex
abra agent verify . --scope <scope>
```

Use `agent verify` before assuming an AI client can see Abra. If the server is
ready but the active client still has no Abra tools, reinstall the MCP config
and fully restart the client.

## Model Flow

Use the built-in local Qwen embedding runner:

```sh
abra model local
abra model up
abra model status
```

Use any compatible embedding provider instead:

```sh
abra model compatible \
  --base-url https://embedding.example/v1 \
  --model embedding-model \
  --dimensions 1024
```

Custom providers replace the local Qwen path. Abra does not lock users to
OpenAI, Qwen, DeepSeek, Voyage, or any other ecosystem.

## Working With Agents

`abra context` is the agent-first interface:

```sh
abra context "Implement the payment retry fix" --scope <scope> --prompt
```

It returns scope-aware memory, citations, conflicts, impact map, validation
plan, and the decision gate. Agents should call this before implementation work.

`abra ask` is for human questions:

```sh
abra ask "Which files explain deployment?" --scope <scope>
```

## Plugins

Core Abra does not ship vendor-specific Jira, Slack, Drive, or private-company
logic. Extensions adapt those systems into normalized documents:

```sh
abra plugin list
abra plugin contract
```

Advanced connectors can be implemented as:

- MCP exporters consumed by `abra connect mcp`;
- signed webhook producers;
- HTTP batch ingestion jobs;
- private deployment overlays.

## Compatibility Aliases

These commands remain for scripts and existing users, but they are not the
primary documentation path:

```text
ingest      -> sync
think       -> ask
compose     -> context
agents      -> agent
models      -> model
sources     -> connect/sync
connectors  -> plugin/connect
mcp         -> agent install/status
```

Do not add new top-level aliases without updating
[FEATURE_FREEZE.md](FEATURE_FREEZE.md).
