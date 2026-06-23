# Repository Layout

Abra keeps runtime code, operational assets, documentation, and examples in
separate top-level directories so new contributors can find the right owner
quickly.

| Path | Responsibility |
| --- | --- |
| `cmd/` | Go entrypoints for the CLI, API, worker, and migrator. |
| `internal/` | Private Go packages grouped by runtime responsibility. |
| `migrations/` | Postgres schema baseline and future migrations. |
| `deploy/` | Docker, Kubernetes, and Helm deployment assets. |
| `docs/` | Architecture, CLI, benchmark, extension, brain, and release policy docs. |
| `examples/` | Public, fake-data examples for env files, evals, connectors, and MCP. |
| `scripts/` | Maintainer QA, release, eval, backup, and install scripts. |
| `.github/` | Issue templates, PR template, and CI/security workflows. |

## Entry Points

- `cmd/abra`: operator CLI and compatibility commands.
- `cmd/abra-api`: HTTP API and MCP server process.
- `cmd/abra-worker`: scheduled ingestion worker.
- `cmd/abra-migrate`: database migration runner.

## Core Packages

- `internal/brain`: ingestion service, embeddings, claims, graph extraction,
  summaries, and text processing in focused files.
- `internal/memory`: governed working memory, answers, scorecards, maintenance,
  evidence anchors, evals, and synthesis gates. Composer logic is split by
  recall, health, strategy, citations, impact, and validation.
- `internal/server`: HTTP/MCP protocol surface, auth, approvals, policies,
  learning, metrics, and routing.
- `internal/store`: Postgres persistence split by aggregate. New storage
  features should use focused files instead of growing `store.go`.
- `internal/jobs`: source configs, worker jobs, remote Git, and MCP source sync.
- `internal/ingest`: source normalization for local files, repos, and documents.
- `internal/graph`: source and code graph extraction helpers.
- `internal/policy`: policy and ACL decision helpers.
- `internal/ai`: provider interfaces, retries, and sanitized provider errors.

## Documentation Assets

Public architecture and benchmark diagrams live in `docs/assets/` as image
assets. Use image references for flow diagrams so GitHub, package viewers, and
external documentation sites render the same visual structure.

## Public Contracts

- MCP is the agent-facing brain contract.
- CLI is the operator contract.
- HTTP is service transport for MCP, CLI fallbacks, gateways, and private
  automation.
- Plugins and community connectors emit normalized documents through MCP,
  signed webhooks, or approved HTTP ingestion.

## Structure Rules

- Keep source-specific source logic out of core.
- Keep trusted-memory promotion inside governance paths.
- Keep new CLI command families in focused `cmd/abra/<family>.go` files.
- Keep the current OSS baseline in `001_init.sql`; after release, add schema
  changes as append-only ordered migrations.
- Keep examples generic and fake; never commit private source exports,
  credentials, embeddings, logs, or database dumps.
