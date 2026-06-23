# Architecture

Abra is a governed external brain for AI agents. MCP is the canonical agent interface.
The CLI is the operator surface; HTTP is service transport for MCP, CLI
fallbacks, gateways, and private automation.

![Abra system architecture](assets/abra-system.svg)

## Core Rules

Core owns governance, not plugins or agent clients. Trusted memory is created
only through the governed learning loop: observe, propose, verify, promote, and
use. Agents may observe, challenge, and propose. Agents must not silently
promote trusted facts.

Default recall and brain composition are deterministic and no-LLM. Optional
synthesis can only run after citation, verification, and evidence-anchor gates
pass.

Plugins do not write trusted memory directly. Plugins adapt external systems
into source-backed normalized documents, signed webhook events, or MCP exporter
responses. Abra core then handles validation, chunking, embedding, graph
extraction, citations, conflicts, approvals, memory health, and decision gates.

## Package Map

- `cmd/abra`: operator CLI, setup, source sync, MCP installation, eval, and
  compatibility commands. Keep new command families in focused files.
- `cmd/abra-api`: API entrypoint.
- `cmd/abra-worker`: scheduled ingestion worker entrypoint.
- `cmd/abra-migrate`: migration entrypoint.
- `internal/brain`: ingestion and claim/relation extraction service layer,
  split into provider, recall, ingest, graph persistence, summaries, and text
  processing files.
- `internal/memory`: working-memory composition, governed answers, evidence
  anchors, scorecards, maintenance, evaluation, and synthesis gates. Composer
  code is split by recall, health, strategy, citations, impact, and validation.
- `internal/server`: HTTP and MCP protocol surface, with route bootstrap,
  readiness, ingestion, memory/learning, source/graph handlers, MCP schemas,
  and argument parsing kept in focused files.
- `internal/store`: Postgres access split by aggregate, including documents,
  claims, conflicts, observations, audit, memory health, recall, graph, source
  configs, and governance records.
- `internal/jobs`: source config execution, remote Git, MCP source ingestion,
  and worker job orchestration.
- `internal/ingest`: local/git/source document normalization.
- `internal/graph`: code and text graph extraction helpers.
- `internal/policy`: policy decisions and ACL helpers.
- `internal/ai`: provider interfaces, embedding calls, retries, and redacted
  provider errors.

## Request Paths

Agent brain path: MCP tool calls enter the server dispatcher, compose or think
over scoped memory, retrieve claims, chunks, graph relations, and evidence
anchors, then return a structured packet with citations and decision gates.

Source ingestion path: local, Git, MCP, and webhook sources become normalized
documents, then chunks, embeddings, claims, graph relations, and source-backed
memory records.

Governed learning path:

![Governed learning loop](assets/governed-learning-loop.svg)

## Change Placement

Put a change in core only when it improves every source or every agent:

- retrieval, ranking, evidence, citations, or anchors;
- governance, policy, approval, or audit;
- source-neutral ingestion and normalized document handling;
- MCP brain contracts or small operator CLI workflows;
- performance, reliability, observability, or security.

Put a change in a plugin or overlay when it names a source system, tenant policy,
private identity system, or business-specific workflow. Community plugins should
emit normalized documents and let Abra core decide what is trusted.

## Maintainability Contract

`npm test` runs `scripts/abra-maintainability-check.mjs`. The check keeps known
hotspots from growing without deliberate refactoring and verifies that OSS
contributor and plugin-authoring documents remain present.

When a hotspot budget fails, split by responsibility instead of increasing the
budget:

- CLI command family: new `cmd/abra/<family>.go` file.
- Store aggregate: new `internal/store/<aggregate>.go` file.
- MCP tool family: new `internal/server/mcp_<family>_tools.go` file.
- Brain algorithm: new `internal/memory/<capability>.go` file.
