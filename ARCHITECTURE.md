# Architecture

Abra is built around cited claims, not anonymous chunks.

## V1 Stack Decision

The current production-oriented implementation is a Go service. The v1 runtime preserves the public contracts:

- HTTP API endpoints.
- Stateless MCP Streamable HTTP through `POST /mcp`.
- Postgres schema semantics for documents, chunks, claims, evidence, feedback, and audit events.
- Prometheus-compatible request metrics and persisted ingestion job history.

Go is the preferred v1 stack because Abra is mostly HTTP, Postgres, background jobs, policy checks, and structured IO. A single static binary per role, predictable runtime profile, straightforward concurrency, and mature `pgx`/OpenTelemetry/logging libraries fit production self-hosting better than a general Node service runtime.

## Runtime

```text
Agent runtime
  -> Abra MCP
    -> Abra API logic
      -> Postgres + pgvector
```

The MCP server is the agent-facing interface. The API is the service interface for ingestion and operational integrations. The worker handles revalidation jobs that do not belong in request paths.

Abra exposes MCP through stateless Streamable HTTP via `POST /mcp` on the API service for remote agent runtimes and gateways. The endpoint declares tool, resource, and prompt capabilities: tools cover recall, ingestion-adjacent controls, policy gates, approvals, conflicts, and learning proposals; resources expose read-only workflow, health, and working-memory context; prompts give MCP clients reusable before-code and memory-review workflows.

Production deployments must set `ABRA_API_KEYS`; all non-health endpoints, including `/mcp`, require a bearer token or `x-api-key`.

Operators can use the CLI, `GET /metrics` for Prometheus scraping, optional OpenTelemetry OTLP traces for request-level latency diagnosis, `GET /memory/health` for scoped memory-quality scorecards, and `GET /ingestion/jobs` for source ingestion history. The memory health response includes stable structured `signals` with severity, category, count, score impact, and recommended action, so CLI tooling, alerting, and agents can gate behavior without parsing human-readable reason strings. Working-memory metrics also count health statuses and bounded health signal labels, so operators can alert on critical or review-gated agent packets without high-cardinality task or scope labels. Trace attributes follow the same privacy stance: bounded operation metadata is allowed, while raw scope names, prompts, task text, principal IDs, and tokens are not attached.

## Claim Trust

Every claim has status:

- `verified`: supported by source evidence.
- `unverified`: proposed by an agent or user without a durable source.
- `inferred`: derived from repeated evidence, but not directly stated by an authoritative source.
- `challenged`: corrected or disputed.
- `deprecated`: intentionally retired.
- `expired`: TTL has elapsed.

Agents should prefer `verified` claims. `inferred` claims should be worded as observed patterns. `unverified`, `challenged`, and `expired` claims are not safe as final answers unless the agent explicitly explains the uncertainty.

## Ranking

Recall ranks claims with hybrid retrieval:

- pgvector nearest-neighbor similarity over query, claim, and chunk embeddings
- full-text keyword match
- source authority
- freshness
- claim status
- confidence

Recall API responses expose both `text_score` and `vector_score` beside the final `rank_score`. The final rank remains the agent-facing ordering signal, while the component scores let operators and agent policies distinguish lexical hits, semantic hits, and fallback behavior.
- feedback

The current implementation keeps the formula simple and transparent. It is meant to be tuned per deployment.

## Graph Model

Abra should model graph relationships in Postgres for v1, not Neo4j. Claim relationships such as `contradicts`, `supersedes`, `duplicates`, and `derived_from` are part of the same trust transaction as source evidence, embeddings, and audit history. Keeping them in Postgres gives one backup path, one authorization boundary, and one consistency model.

Neo4j or another graph database should be considered only if measured workloads require deep, high-volume graph traversal that cannot be handled with relational edges, recursive queries, and targeted indexes. The default product should optimize for operational simplicity and evidence integrity.

## Scope

Scope is required. Examples:

```text
company
team:example
team:design-system
agent:agent-alpha
user:U123
```

Scope is the first safety boundary. Company deployments should add ACL filtering before returning records to an agent.

## Ingestion

The OSS implementation accepts generic documents through HTTP and stores approved source configs for the worker. It chunks content, extracts candidate claims from knowledge documents, embeds chunks and extracted claims, extracts graph entities/relations, and stores source evidence. Re-ingesting the same source refreshes all derived layers for that source: removed claims and graph relations are deprecated, still-present claims and relations are reactivated, and source summaries are replaced.

For Git/local-repo sources, code ingestion is explicit opt-in through source config. Local repos read mounted worker paths, while `git_repo` sources use a bounded shallow clone/fetch into the worker Git cache before using the same deterministic repo ingestor. Code documents create chunks, graph relations, and deterministic summaries, but they do not create verified natural-language claims from raw source text, comments, or syntax-shaped strings. When enabled for JS/TS files, Abra extracts structural code graph records from imports, exports, package dependencies, Next.js pages routes, and React-style component symbols. When enabled for Go files, Abra extracts package declarations, imports, functions, methods, constants, variables, and types through the standard Go parser. This path is deterministic and does not call an LLM, keeping ingestion cost, latency, and claim quality predictable.

Ingestion also writes deterministic memory summaries. The first hierarchy levels are `file`, `module`, and `source`; the schema also reserves `repo`, `symbol`, and `decision` so richer compilers can be added without changing the agent API. Summaries are precomputed because agents should not pay chunk-scanning cost for every broad task. Source-scoped summaries are deleted before refreshed summaries are written, preventing old routes, packages, or source decisions from remaining active after a file changes.

Existing scopes can be upgraded with `POST /memory/summaries/rebuild`, which compiles summaries from already-ingested documents and chunks. This keeps schema evolution practical for self-hosted deployments: new memory layers can be backfilled without re-ingesting every source system.

When the same source is ingested again, extracted claims from that source are first marked `deprecated`, then claims still present in the refreshed source are reactivated. This prevents removed source facts from staying trusted forever.

Private deployments can add ingestion workers for Confluence, Jira, Slack decisions, or provider-specific repository discovery without changing the MCP surface. OSS source configs support local markdown/repo-style paths and provider-neutral remote Git; private overlays should add connector credentials, webhook diffing, and ACL normalization where needed.

Production ingestion should be automatic but not uncontrolled. A connector should poll or receive webhooks from approved source systems, normalize each record into the generic document ingestion API, preserve source ACL and scope metadata, and re-ingest idempotently. Connector-specific auth, source mapping, and enterprise ACL rules belong in a private overlay or deployment integration.

The core worker schedules `markdown`, `local_repo`, and `git_repo` source configs. `markdown` and `local_repo` configs are validated at write time and must reference a local path or `file://` URL visible to the worker. `git_repo` configs are provider-neutral and use non-interactive Git clone/fetch; platform-owned secrets or credential helpers provide access for private repositories. Source configs and ingestion jobs are controlled through the same HTTP and MCP surfaces: operators or agent gateways can upsert/list source configs, enqueue/list ingestion jobs, and retry or cancel queued work without needing a product CLI. Other remote source systems remain connector overlays: they discover and diff external data, then push normalized documents or mount local checkouts. This keeps OSS Abra self-hostable without baking enterprise provider SDKs into the core runtime.

## Agent Auto-Policy

Agents can call `POST /policy/plan` or the `policy_plan` MCP tool to turn task hooks into concrete recall queries. The default hooks are `before_task`, `before_code`, and `after_task`.

Agents that want the smart path should call `POST /memory/compose` or the `working_memory_compose` MCP tool. The composer is Abra's working-memory compiler: it classifies the task, expands it into retrieval queries, runs independent summary and hybrid lexical/vector recall retrieval in parallel, deterministically merges the results, expands graph context from task and memory seeds with parallel bounded seed expansion, extracts relevant files, compiles an impact map, compiles a validation plan, groups evidence, checks scoped memory health, surfaces stale or unsafe memory, evaluates stored agent-action policies for standard risky actions, verifies the packet, packs a bounded prompt-ready context window, and returns a deterministic agent decision gate plus suggested next steps. Retrieval planning carries an intent-specific coverage contract for summaries, facts, source chunks, graph relations, and evidence sources; verification checks actual packet coverage against that contract before allowing autonomy. This matters for code intelligence because code sources intentionally create graph context and summaries without turning raw code into trusted claim facts, while migration and debugging tasks can still require explicit source-backed claims. Verification also includes retrieval-quality scoring over rank, text, and vector signals, so a source-backed packet can still be marked weak when recall evidence is too low-signal to trust. Low-confidence retrieval also emits a reviewable learning suggestion to improve queries, ingest stronger sources, or rebuild embeddings/reindex before agents reuse weak context. Actionable learning suggestions from HTTP and MCP compose calls are automatically persisted as pending learning proposals with application and database deduplication; no-op suggestions are returned but not queued, and proposals never become trusted memory without a separate review/apply workflow. Non-safety retrieval branches degrade independently: failed recall, summary, graph expansion, or health lookup branches become explicit warnings or critical health signals, verification becomes action-required where appropriate, and agent autonomy is reduced; conflict lookup and agent-action policy checks still fail closed. The composer also emits `graph_warnings` for high-signal graph contradictions, starting with opposing use policies and competing browser-test-runner alternatives. Ingestion persists those same high-signal graph contradictions as relation conflicts using `primary_relation_id`, `conflicting_relation_id`, and `entity_id`; graph retrieval returns relation IDs, warnings preserve relation IDs, and compose looks up active conflicts for both claim IDs and relation IDs before deciding autonomy. It also emits a compact retrieval trace with per-stage status, durations, query counts, result counts, and parallelism markers so operators can diagnose latency growth without external tracing infrastructure. When OpenTelemetry is enabled, the HTTP/MCP span surrounds the same working-memory operation and records bounded aggregate counts, verdict, and agent decision for cross-service correlation. Compose results include `/memory/health`-compatible `memory_health.signals`; critical signals block autonomous work, review signals require operator review, and the context window includes health status in the task gate. To keep repeated agent calls from amplifying aggregate health-query load, compose uses a short-lived scoped health cache and coalesces concurrent cold lookups while direct `/memory/health` remains the uncached operator view. This keeps agents from hand-rolling retrieval loops and context packing, and makes memory use auditable.

Active conflicts are not approval problems. When active claim or relation conflicts are present, the agent decision gate removes approval-bypass next actions and returns conflict-review actions such as `list_conflicts`, `resolve_active_conflicts`, and `review_relation_conflicts`. This keeps an approval from accidentally overriding contradictory memory.

Agents may recall automatically within their authorized scopes. Auto-write is narrower:

- Source-backed observations may be ingested automatically when the source, scope, and authority are known.
- Unsourced agent memories remain `unverified`.
- Source-backed claim writes run deterministic contradiction detection for supported policy assertions. Detected contradictions create first-class conflict records and make working-memory packets unsafe until reviewed.
- Agents may propose challenges when evidence conflicts, but destructive or broad actions should require approval.
- Agents should not silently use `unverified`, `challenged`, `deprecated`, or `expired` claims as final truth.
- Scope expansion, source authority changes, ACL changes, and forget operations are operator-controlled.

## AI Provider Boundary

Abra depends on an embedding provider, not a generation model. The v0.1 provider interface is compatible with common embedding API shapes and configured with:

```text
EMBEDDING_PROVIDER=compatible
EMBEDDING_BASE_URL=https://...
EMBEDDING_API_KEY=...
EMBEDDING_MODEL=...
EMBEDDING_DIMENSIONS=1536
```

Any provider can be used if it implements the configured embeddings API shape and returns vectors matching the configured dimensions. Changing dimensions requires a database migration because the current schema uses `vector(1536)`.

Embedding migrations are operationally sensitive because chunks, claims, and entities all store vectors. Same-dimension model changes should be treated as a controlled re-ingestion plus eval comparison. Dimension changes require schema migration, vector index rebuilds, and full source re-ingestion.

## Operational Boundary

Abra v1 intentionally keeps the required data plane small:

- Go API, worker, and migration binaries
- Go CLI for install, bootstrap, status, ingestion, recall, compose, think, and MCP config
- Postgres with `pgvector`
- a compatible embedding provider
- HTTP MCP over the API service

The v1 architecture does not require Neo4j, Redis, Kafka, or a web UI. Those services should be introduced only when measured production workload proves the need. Operational completeness is instead handled by the CLI, Postgres backups, restore drills, reindex procedures, ingestion job history, metrics, and eval gates.

## Smart Memory Path

Abra's smart path is intentionally small:

```text
task
    -> working-memory composer
    -> policy planner
    -> hierarchical summaries
    -> parallel hybrid lexical/vector recall over claims/chunks and query-scoped summaries
    -> parallel graph context over entities, relations, and bounded expansion seeds
    -> retrieval warnings for degraded non-safety branches
    -> graph warnings for high-signal competing or opposing graph relations
    -> compact retrieval trace over stage status, duration, query count, result count, and parallelism
    -> deterministic impact map over files, packages, routes, symbols, and entities
    -> deterministic validation plan over inferred commands and review gates
    -> evidence and risk compiler
    -> verifier and agent decision gate
    -> bounded context-window packing
    -> agent-ready packet
```

This is not answer generation. Abra compiles verified working memory so any agent or model can act with better context while still citing the source records.

The context-window packer is deliberately deterministic. A caller can pass `token_budget` and receive selected blocks, dropped lower-priority blocks, token estimates, warnings, and a rendered prompt string. This gives small models enough verified context to act and prevents large-context models from wasting attention on unranked memory dumps.

## Dogfood Gate

Abra must be able to ingest and explain itself. The `eval:dogfood` gate registers the running Abra repository as `repo:abra`, queues worker ingestion, rebuilds summaries, and calls `working_memory_compose` for architecture and production-readiness context. The gate fails if the packet lacks summaries, source-backed facts or documents, graph relations, an impact map, a validation plan, and a budgeted context window. This keeps the product honest: a memory control plane that cannot build useful memory about its own Go CLI, service, worker, docs, and policy paths is not ready to claim agent-grade code intelligence.

## V1 Roadmap

1. Harden the Go API, worker, migration, and HTTP MCP roles against production client matrices.
2. Keep Postgres plus `pgvector` as the only required persistence layer.
3. Expand deterministic conflict detection from policy assertions into richer claim and graph contradictions.
4. Extend first-class policy enforcement across agent auto-write, operator approvals, and deployment-specific ACL gates.
5. Add private connector hooks and source ACL filters.
6. Automate recall, graph, policy, and embedding migration evals from `EVALS.md`.

## No CLI

Abra is intentionally service-first. It does not expose a product CLI. Local development uses normal package scripts to run API, MCP, worker, and migrations.
