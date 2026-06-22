# Abra

Abra is a small CLI-first governed brain for AI agents.

It gives Codex, Claude Code, custom agents, and MCP-capable tools a shared,
source-cited memory layer before they change code or answer operational
questions. Abra is not a web app, chatbot, vector database UI, or model vendor
wrapper.

## Product Shape

Abra stays intentionally small:

- CLI for humans.
- MCP and HTTP for agents and automation.
- Postgres + pgvector for durable memory.
- Built-in local Qwen embedding/reranker defaults.
- Compatible provider support when teams bring their own embedding endpoint.
- Plugin contracts for external systems, without putting vendor logic in core.

Core intelligence is not a plugin bypass: normalization, chunking, claim
extraction, graph extraction, embeddings, source authority, citations, conflicts,
approvals, memory health, and agent decision gates stay inside Abra core.

## What Abra Does

Abra turns source-backed documents into governed memory:

```text
source -> normalize -> transform -> embed -> extract claims/graph -> govern -> recall/context
```

Agents can then:

- ask cited questions with `abra ask`;
- compose task-specific working context with `abra context`;
- verify exact project scope with `abra agent verify`;
- use the same context through MCP tools such as `discover_scopes` and
  `working_memory_compose`.

Raw observations are not trusted facts. They are captured, proposed, reviewed,
and only then promoted into trusted memory.

## Install

Abra is not distributed through npm. The npm files in this repository are
maintainer scripts for release checks only.

Install the latest release binary:

```sh
curl -fsSL https://github.com/hermawan22/abra/releases/latest/download/install.sh | sh
```

For repeatable production workstations, pin and verify the release:

```sh
curl -fsSLO https://github.com/hermawan22/abra/releases/download/vX.Y.Z/install.sh
gh attestation verify --repo hermawan22/abra install.sh
ABRA_VERSION=vX.Y.Z ABRA_VERIFY_ATTESTATION=1 sh install.sh
```

From a source checkout, run commands with `go run ./cmd/abra ...` until a release
binary is installed.

## 3-Minute Flow

```sh
abra setup
abra doctor

cd /path/to/project
abra scope
abra agent bootstrap --agent codex
```

Fully quit and reopen Codex Desktop after bootstrap so the active process reads
the MCP config and token environment.

Then:

```sh
abra agent ready . --scope <scope-from-abra-scope> --json
abra ask "What should I know before changing this project?" --scope <scope-from-abra-scope>
```

Manual equivalent:

```sh
abra agent install codex
abra agent init --agent codex
abra agent verify . --scope <scope-from-abra-scope>
abra sync . --code --scope <scope-from-abra-scope>   # only when verify proves memory is missing
abra agent verify . --scope <scope-from-abra-scope>
```

## CLI Surface

Canonical human commands:

```text
abra setup
abra connect
abra sync
abra ask
abra context
abra agent
abra model
abra brain
abra govern
abra plugin
abra doctor
```

Compatibility commands such as `ingest`, `think`, `compose`, `agents`,
`models`, `sources`, `connectors`, and `mcp` remain for automation and advanced
operators. Run:

```sh
abra help advanced
```

## Models

The default local embedding path is:

- `Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0`
- optional `Qwen/Qwen3-Reranker-0.6B-GGUF:Q8_0`

Use it with:

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

Custom providers replace the local Qwen path. Abra is not locked to OpenAI,
DeepSeek, Qwen, or any single ecosystem.

## Sources And Plugins

Built-in source paths:

- local files and repos: `abra sync . --code --scope repo:project`
- durable local/git/MCP sources: `abra connect ...`
- signed webhooks and HTTP/MCP ingestion for external systems

Plugin contracts adapt vendor systems into normalized Abra documents. A Jira,
Confluence, Slack, Drive, Git provider, or internal system integration should
live as an adapter, MCP exporter, webhook producer, or private overlay.

Plugins must output source-backed documents. They do not own governance,
embeddings, citations, graph extraction, or decision gates.

See [docs/EXTENSIONS.md](docs/EXTENSIONS.md).

## Production

Production deployments must provide:

- generated API keys and webhook secrets;
- production approval enforcement;
- Postgres with pgvector and backups;
- internal network exposure or a gateway;
- digest-pinned container images;
- a measured embedding provider capacity profile.

See [PRODUCTION.md](PRODUCTION.md).

## Feature Freeze

Abra's pre-OSS public surface is frozen around the small CLI above. New work
must fit one of these categories:

- core intelligence or governance quality;
- bug fix, security fix, or production hardening;
- provider-neutral CLI/MCP/HTTP contract improvement;
- external-system adapter expressed as a plugin contract, not core vendor logic.

See [docs/FEATURE_FREEZE.md](docs/FEATURE_FREEZE.md).

## Development

```sh
go test ./...
npm test
```

Full release gate:

```sh
ABRA_RELEASE_PROFILE=full ABRA_RELEASE_MANAGE_STACK=1 npm run release:gate
```

Do not commit secrets, private business context, customer data, source-system
exports, embeddings, database dumps, or organization-specific policies.
