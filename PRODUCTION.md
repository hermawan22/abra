# Production Readiness

Abra is production-oriented when deployed with the controls below. Do not run the API publicly without them.

## Supported Self-Host Paths

Use Docker Compose for a single-host production install or pilot. Use Kubernetes for replicated production deployments, either through the raw manifests or the Helm chart in `deploy/helm`.

## Docker Compose Install

Prerequisites:

- Docker Engine with Compose.
- A generated `ABRA_API_KEYS` value.
- Local Qwen-compatible embedding and reranker endpoints, or a custom compatible embedding provider. The default local path expects Qwen/Qwen3-Embedding-0.6B and Qwen/Qwen3-Reranker-0.6B served outside the Abra containers.
- Enough disk for Postgres source snippets, claims, audit events, and vectors.

Create `.env.production`:

```text
ABRA_API_KEYS=replace-with-generated-token
ABRA_WEBHOOK_SECRETS=replace-with-webhook-signing-secret
ABRA_ALLOW_UNSIGNED_WEBHOOKS_IN_PRODUCTION=false
ABRA_APPROVAL_MODE=enforce
EMBEDDING_PROVIDER=compatible
EMBEDDING_BASE_URL=https://embedding-provider.example/v1
EMBEDDING_API_KEY=replace-with-embedding-key
EMBEDDING_MODEL=embedding-model
EMBEDDING_DIMENSIONS=1024
RERANKER_PROVIDER=
RERANKER_BASE_URL=
RERANKER_API_KEY=
RERANKER_MODEL=
ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false
REDACT_PII=true
RATE_LIMIT_MAX=120
RATE_LIMIT_WINDOW=1 minute
ABRA_BIND_ADDR=0.0.0.0
ABRA_API_READ_TIMEOUT=2m
ABRA_MAX_REQUEST_BODY_BYTES=26214400
ABRA_PUBLISH_ADDR=127.0.0.1
ABRA_PORT=18080
```

The Compose file uses its bundled Postgres service unless `ABRA_DATABASE_URL` is set. To point Compose at managed Postgres with `pgvector`, add:

```text
ABRA_DATABASE_URL=postgres://user:password@postgres.example.internal:5432/abra
```

Install:

```sh
docker compose --env-file .env.production up -d postgres
docker compose --env-file .env.production run --rm migrate
docker compose --env-file .env.production up -d api worker
```

Always pass `--env-file .env.production`; the development `.env` uses host-oriented values such as `localhost` that are not valid inside Compose service containers.

Upgrade:

```sh
docker compose --env-file .env.production build api worker migrate
docker compose --env-file .env.production run --rm migrate
docker compose --env-file .env.production up -d api worker
```

If `ABRA_IMAGE` points at a pushed registry image, use `docker compose --env-file .env.production pull` instead of the build command.

Back up the `abra-postgres` volume or, for serious production use, point `ABRA_DATABASE_URL` at managed Postgres with `pgvector` and an existing backup policy. The bundled backup, restore-drill, and reindex scripts are in `scripts/`.

## Kubernetes Install

Prerequisites:

- Managed Postgres with `pgvector`.
- A private image registry containing the Abra image.
- Kubernetes Secrets management for `DATABASE_URL`, `ABRA_API_KEYS`, and embedding credentials.
- Internal ingress or service mesh routing; do not publish Abra directly to the internet.

Apply flow:

```sh
kubectl apply -f deploy/kubernetes/configmap.yaml
kubectl apply -f path/to/your-abra-secret.yaml
kubectl delete job abra-migrate --ignore-not-found
kubectl apply -f deploy/kubernetes/job-migrate.yaml
kubectl wait --for=condition=complete job/abra-migrate --timeout=120s
kubectl apply -f deploy/kubernetes/deployment-api.yaml
kubectl apply -f deploy/kubernetes/deployment-worker.yaml
kubectl apply -f deploy/kubernetes/service.yaml
```

Before using the example secret, replace all values and preferably manage it through your platform secret store. Run the migration job once per deploy and inspect completion before rolling the API and worker. The fixed-name example Job must be deleted before each run; otherwise Kubernetes will keep the completed Job and will not rerun migrations for a later deploy.

The Helm chart is available in `deploy/helm`; render it with `helm template abra ./deploy/helm` and install it with your registry image and existing secret.

## Required Configuration

```text
NODE_ENV=production
DATABASE_URL=postgres://...
ABRA_API_KEYS=<comma-separated service tokens>
ABRA_WEBHOOK_SECRETS=<comma-separated webhook signing secrets>
ABRA_ALLOW_UNSIGNED_WEBHOOKS_IN_PRODUCTION=false
ABRA_APPROVAL_MODE=enforce
EMBEDDING_PROVIDER=compatible
EMBEDDING_BASE_URL=https://...
EMBEDDING_API_KEY=...
EMBEDDING_MODEL=<embedding model>
EMBEDDING_DIMENSIONS=1024
RERANKER_PROVIDER=
RERANKER_BASE_URL=
RERANKER_API_KEY=
RERANKER_MODEL=
ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false
REDACT_PII=true
RATE_LIMIT_MAX=120
RATE_LIMIT_WINDOW=1 minute
ABRA_COMPOSE_HEALTH_CACHE_TTL=2s
ABRA_COMPOSE_RECALL_CONCURRENCY=4
ABRA_COMPOSE_GRAPH_CONCURRENCY=4
ABRA_BIND_ADDR=0.0.0.0
ABRA_API_READ_TIMEOUT=2m
ABRA_MAX_REQUEST_BODY_BYTES=26214400
# OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318
# ABRA_TRACING_SAMPLE_RATIO=0.25
```

For Docker Compose, set `ABRA_DATABASE_URL` in the Compose env file when overriding the bundled database. Raw Kubernetes secrets and direct binary/container runs use `DATABASE_URL`.

`ABRA_API_KEYS` accepts simple admin tokens for compatibility, or scoped role tokens for production:

```text
ABRA_API_KEYS=abra_admin_generated_32_chars,abra_reader_generated_32_chars|roles=reader;scopes=team:example,abra_ops_generated_32_chars|roles=ops;scopes=*
```

Production tokens must be unique, non-placeholder values of at least 16 characters. Use scoped keys for agents and automation. Reserve all-scope `admin` keys for operators and automation that needs write access across scopes.

Production startup fails without `ABRA_WEBHOOK_SECRETS` unless `ABRA_ALLOW_UNSIGNED_WEBHOOKS_IN_PRODUCTION=true` is explicitly set. Keep the override false for deployments that expose `POST /ingest/webhooks`; use it only when webhook ingestion is disabled or an upstream gateway verifies signatures before requests reach Abra.

`EMBEDDING_PROVIDER=local` is the default self-hosted neural path. It does not need an external API key, but it does require local model endpoints reachable from the Abra containers. With Docker Compose, the default URLs use `host.docker.internal` so models running on the host can be reached from the API and worker containers. Set `EMBEDDING_PROVIDER=compatible` to replace the local defaults with a custom provider.

The provider contract is intentionally provider-neutral. The provider must expose the configured embeddings request/response shape and must return vectors with the configured `EMBEDDING_DIMENSIONS`. API keys may be empty for self-hosted endpoints. The optional reranker is controlled separately with `RERANKER_PROVIDER`, `RERANKER_BASE_URL`, `RERANKER_API_KEY`, and `RERANKER_MODEL`; when unset, custom embedding providers do not keep the local Qwen reranker enabled. Abra stores embeddings and source-derived snippets; it does not send prompts for generation.

`ABRA_COMPOSE_HEALTH_CACHE_TTL` controls the short-lived per-scope health snapshot cache used by `POST /memory/compose`; set it to `0s` to disable caching. Direct `GET /memory/health` remains uncached for operator checks. Track `abra_working_memory_health_lookup_total` to see whether compose traffic is using fresh lookups, cache hits, coalesced waits, disabled cache mode, or unknown/error paths.

`ABRA_COMPOSE_RECALL_CONCURRENCY` and `ABRA_COMPOSE_GRAPH_CONCURRENCY` cap per-request fan-out inside `POST /memory/compose` and MCP `working_memory_compose`. Keep the defaults at `4` for small installs; raise them only after watching database pool usage, recall latency, and memory-compose p95 under realistic agent traffic. Values must be between `1` and `32`.

OpenTelemetry tracing is optional and disabled unless `OTEL_EXPORTER_OTLP_ENDPOINT` or `ABRA_OTEL_EXPORTER_OTLP_ENDPOINT` is configured. Set `ABRA_TRACING_SAMPLE_RATIO` to a value from `0` to `1` to control head sampling. Keep sampled traces out of user/task payloads: Abra spans intentionally record bounded operation metadata such as route, status, retrieval mode, counts, verdict, agent decision, and worker ingestion counts, not raw scope names, queries, task text, principals, or tokens.

## Deployment Roles

One image supports multiple process roles:

```text
/app/abra-migrate
/app/abra-api
/app/abra-worker
```

Recommended deployment:

- Run `/app/abra-migrate` as a pre-deploy job.
- Run at least one API replica behind an internal load balancer.
- Run one worker replica for TTL expiry/revalidation.
- Use `POST /mcp` on the API service for remote MCP clients.
- Operate Abra through the CLI, API, MCP, metrics, and runbooks.

## Network

- Expose Abra only on an internal network.
- Keep `ABRA_BIND_ADDR=0.0.0.0` inside containers and restrict exposure at the platform edge.
- Docker Compose publishes the API on `ABRA_PUBLISH_ADDR:ABRA_PORT`, defaulting to `127.0.0.1:18080`; set `ABRA_PUBLISH_ADDR` to a private interface or put a gateway on the same host before remote clients use it.
- Require `Authorization: Bearer <key>` or `x-api-key`.
- Terminate TLS at the ingress/gateway.
- Keep Abra's built-in Postgres-backed rate limit enabled with `RATE_LIMIT_MAX` and `RATE_LIMIT_WINDOW`; it applies across replicated API pods after migrations are applied. Put an additional rate limit at the gateway layer for defense in depth before exposing write-capable credentials.

## Database

- Use managed Postgres with `pgvector`.
- Run migrations once per deploy.
- Back up the database; it contains memory and source-derived snippets.
- Restore into a non-production database at least once per release cycle or before risky upgrades.
- Monitor connection usage, storage growth, and slow queries.
- Keep `EMBEDDING_DIMENSIONS` aligned with the provider output. Abra stores returned dimensions per row and includes partial vector indexes for common dimensions such as 768, 1024, 1280, and 1536.
- Keep graph relationships in Postgres for v1. Do not add Neo4j unless production evidence shows Postgres cannot handle required traversal workloads.
- Keep policy indexes healthy. Working-memory composition evaluates stored agent-action policies on the hot path, so include `policies_agent_action_*` indexes in reindex maintenance.
- Tune `token_budget` per agent class. Small local models can request tighter `context_window` prompts, while large hosted models can request larger budgets without changing the retrieval, verification, policy, or evidence contract.
- For `git_repo` source configs, mount or provision `ABRA_GIT_CACHE_DIR` on worker pods and tune `ABRA_GIT_CLONE_DEPTH` to balance freshness, network use, and source URL revision precision. Keep Git authentication non-interactive through platform secrets, SSH deploy keys, or credential helpers; do not put repository tokens in prompts or agent-provided task text.

## Safety

- Leave `REDACT_PII=true` unless the deployment has a stronger upstream redaction layer.
- Claims without `source_url` are `unverified`.
- Manually forgotten claims are `deprecated` and are not reactivated by source re-ingestion.
- Source re-ingestion only reactivates claims and graph relations deprecated by source refresh.
- Use scoped memories: `company`, `team:<name>`, `agent:<slug>`, or `user:<id>`.
- Treat agent-initiated forgets, broad-scope writes, stored agent-action policy changes, ACL changes, source authority changes, connector enables, and backfills as approval-required risky operations.
- Set `ABRA_APPROVAL_MODE=enforce` for production agent-facing deployments. This makes core risky memory endpoints reject requests without a matching approved `approval_id`.
- Agents can create approval requests with `POST /approvals` or the `request_approval` MCP tool. Operators approve or reject with `POST /approvals/:approvalId/approve` and `POST /approvals/:approvalId/reject`.
- Do not give autonomous agents direct `admin` or all-scope write credentials. Route connector, ACL, and backfill operations through request-only tools or a private overlay when those operations live outside OSS Abra.
- Use `POST /acl/decision` from the identity gateway to combine external group membership with Abra scope/resource policy. Treat no-match decisions as deny.
- Use `POST /agent/policy/decision` before risky agent actions when prompt-level rules are not enough. Stored agent-action policies can return `allow`, `deny`, or `require_review`; require-review forces an approved request even if the deployment is otherwise in advisory mode.
- Require agents that use `POST /memory/compose` to read both `agent_policy_decisions` and `agent_decision` before calling write, challenge, forget, backfill, source authority, or policy mutation tools.

## Production Approval Workflow

Use this workflow whenever an agent proposes a risky memory or ingestion operation:

1. Agent creates an approval request with `action`, `scope`, `reason`, `target_type`, `target_id`, and enough `payload` detail for review.
2. Operator lists pending requests:

   ```sh
   auth_header="x-api-key: $ABRA_OPS_TOKEN"
   curl -H "$auth_header" \
     "$ABRA_URL/approvals?scope=team:example&status=pending"
   ```

3. Operator verifies requester identity, scope, source evidence, blast radius, rollback path, and whether the action belongs in OSS Abra or a private overlay such as ACL or connector management.
4. Operator approves or rejects:

   ```sh
   auth_header="x-api-key: $ABRA_OPS_TOKEN"
   curl -X POST -H "$auth_header" \
     -H "Content-Type: application/json" \
     -d '{"decided_by":"oncall","decision_reason":"source owner confirmed"}' \
     "$ABRA_URL/approvals/$APPROVAL_ID/approve"
   ```

5. If approved, retry supported OSS risky endpoints with `approval_id`; for overlay-owned operations, let privileged automation perform the actual operation and record both the approval ID and operation `x-request-id`.

Stored agent-action policies are written through `POST /agent/policies` with ops credentials and an approved `acl_change` request. Prefer `require_review` or `deny` effects for production agents; use `allow` only for narrow, well-understood low-risk actions because it can bypass the global approval-mode fallback for exact matches.

For rejection, call `POST /approvals/:approvalId/reject` with a decision reason. Do not execute the proposed operation after rejection or expiry.

## Auto Ingestion Policy

Production ingestion should be automated but bounded:

- Prefer source webhooks or scheduled connector jobs over manual uploads.
- Ingest only approved sources and map each source to a stable scope and authority.
- Keep private connector credentials outside the Abra OSS image.
- Use `POST /ingest/webhooks` for connector overlays that can push normalized documents. Configure `ABRA_WEBHOOK_SECRETS` and send `x-abra-signature: sha256=<hmac>` so webhook bodies are tamper-evident in addition to API-key auth.
- Treat source refresh as idempotent. Re-ingestion deprecates missing claims and graph relations, reactivates still-present claims and relations from the same source, and replaces source-scoped summaries.
- Store connector state outside the request path so ingestion spikes do not affect recall latency.

## Observability

Endpoints:

- `GET /healthz`
- `GET /readyz`
- `POST /ingest/documents`
- `POST /ingest/webhooks`
- `POST /mcp`
- `GET /sources/configs`
- `GET /ingestion/jobs`
- `POST /ingestion/jobs`
- `GET /approvals`
- `POST /approvals`
- `POST /approvals/:approvalId/approve`
- `POST /approvals/:approvalId/reject`
- `GET /acl/policies`
- `POST /acl/policies`
- `POST /acl/decision`
- `GET /agent/policies`
- `POST /agent/policies`
- `POST /agent/policy/decision`
- `GET /graph/entities`
- `GET /graph/relations`
- `GET /memory/health`
- `POST /policy/plan`
- `GET /audit/events`

Every HTTP response includes `x-request-id`; propagate it into gateway logs and support traces.

Prometheus scraping should include the smart-path metrics, `abra_recall_retrieval_mode_total`, and `abra_working_memory_retrieval_quality_total`. Alert when production recall stops reporting `mode="hybrid"` or when `mode="full_text_embedding_error"` / `mode="full_text_empty_embedding"` rises above the expected maintenance window, because those fallback modes usually indicate an embedding provider or configuration problem. Alert separately when `abra_working_memory_retrieval_quality_total{quality="low_confidence"}` increases outside known sparse scopes; low-confidence packets mean agents should rerun with better queries, ingest stronger sources, or rebuild embeddings before autonomous work.
When tracing is enabled, use spans for latency diagnosis rather than high-cardinality metrics. The useful path is `HTTP route -> abra.memory.compose -> retrieval_trace in response` or `HTTP /mcp -> abra.mcp.tool -> abra.recall/abra.memory.compose`.

Audit events are recorded for:

- document ingestion
- claim remembering
- claim challenge
- claim forget
- claim expiry

Approval decisions are stored in `approval_requests`. Include approval IDs in change records and incident notes, and pair them with the `x-request-id` from the later risky operation. The current audit export covers memory mutation events; do not rely on it as the only approval history source.

Operators can export audit events for SIEM pulls:

```sh
auth_header="x-api-key: $ABRA_OPS_TOKEN"
curl -H "$auth_header" \
  "$ABRA_URL/audit/events?scope=team:example&event_type=claim.remembered&format=ndjson&since=2026-06-16T00:00:00Z"
```

Supported filters are `scope`, `event_type` or `type`, `target_type`, `since`, `until`, `limit`, and `format=json|ndjson`. All-scope export requires an all-scope `ops` or `admin` key. Scoped ops keys must pass an allowed `scope`, which prevents accidental cross-scope enumeration from SIEM jobs.

The worker can also push audit events to an HTTP/SIEM sink without adding another service:

```text
ABRA_AUDIT_SINK_URL=https://siem.example.internal/abra/audit
ABRA_AUDIT_SINK_TOKEN=replace-with-sink-token
ABRA_AUDIT_SINK_SECRET=replace-with-hmac-secret
ABRA_AUDIT_SINK_SCOPE=team:example
ABRA_AUDIT_SINK_BATCH_SIZE=100
```

Delivery uses `application/x-ndjson`, `authorization: Bearer ...` when a token is set, and `x-abra-signature: sha256=<hmac>` when a secret is set. The cursor is stored in Postgres and only advances after a 2xx sink response. Leave `ABRA_AUDIT_SINK_URL` empty when the deployment uses pull-based SIEM collection.

At minimum, on-call operators should know how to:

- distinguish API, Postgres, embedding provider, and worker failures
- inspect ingestion job failures
- trace a request by `x-request-id`
- pause or stop worker ingestion during a data incident
- restore a backup into an isolated database
- validate recall quality after an embedding provider change

## Release Gate

Run before deploy:

```text
npm test
go test ./...
docker build -t abra:local .
helm lint deploy/helm
helm template abra deploy/helm >/tmp/abra-rendered.yaml
```

Then run a database smoke test against a disposable Postgres:

1. Start Postgres with pgvector.
2. Run `/app/abra-migrate` twice; second run must be a no-op.
3. Start API with `NODE_ENV=production`, `ABRA_API_KEYS`, and production embedding config.
4. Confirm unauthenticated `/recall` returns `401`.
5. Confirm `/readyz` reports `ok: true`; readiness checks Postgres, `pgvector`, and core migrated tables, including the shared rate-limit bucket table.
6. Confirm `POST /mcp` initialize succeeds.
7. Ingest a document, recall a claim, forget it, and confirm it no longer appears as trusted recall.

For the self-hosted API surface, run the bundled smoke suite after API and worker are up:

```sh
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-generated-token npm run smoke:selfhost
```

The suite verifies auth, readiness, metrics, source config writes, document ingestion, recall, policy planning, agent-action policy decisions, MCP, approval request/decision endpoints, and ingestion job response shape. Use `ABRA_APPROVAL_MODE=enforce ABRA_TIER23_EXPECT_APPROVAL_ENFORCEMENT=1 npm run eval:tier23` to verify approval enforcement and stored agent-action policy review gates before exposing agent credentials.

Run the dogfood gate against the candidate API and worker before promoting the build:

```sh
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-generated-token npm run eval:dogfood
```

The eval process defaults to the current checkout path, but the worker must be able to read that path. For container layouts, mount the checkout read-only and set `ABRA_DOGFOOD_SOURCE_ROOT` to the mounted path visible to the worker. The gate pauses its source config after success unless `ABRA_DOGFOOD_KEEP_SOURCE_ACTIVE=1` is set. This gate proves Abra can ingest its own docs and Go source, rebuild summaries, return graph-aware working memory for `repo:abra`, and expose Go code intelligence in graph relations.

Smoke tests are not quality evaluation. The full release gate covers recall quality, citation precision, scope leakage, graph quality, policy planning, dogfood ingestion, and embedding provider checks.

## Backup, Restore, Reindex, and Embedding Changes

Operational maintenance is part of the v1 bar:

- Backups must include all Postgres tables, vector indexes, source configs, ingestion jobs, audit events, stored policies, and graph records.
- Restores must be drilled into isolated databases before production cutover.
- Reindexing should be done during a maintenance window and followed by smoke plus recall quality checks.
- Same-dimension embedding model changes require re-ingestion and eval comparison.
- Embedding dimension changes require a database migration and full re-ingestion.

The bundled helper commands are:

```sh
DATABASE_URL=postgres://... npm run ops:backup
ABRA_RESTORE_DUMP=backups/abra_YYYYMMDD_HHMMSS.dump ABRA_RESTORE_DATABASE_URL=postgres://... npm run ops:restore-drill
DATABASE_URL=postgres://... npm run ops:reindex
```

`ops:restore-drill` and `ops:reindex` are dry-run by default; set `ABRA_DRY_RUN=0` only after confirming the isolated target or maintenance window.

## V1 Production Target

The v1 runtime is Go for the API, worker, and migration roles, preserving the current HTTP and MCP contracts. Keep Postgres plus `pgvector` as the only required data service. Use Helm as the primary Kubernetes install path and keep Docker Compose as the small self-host path.

## V1 Completeness Boundary

The OSS v1 target is complete only when these operator-owned surfaces are present and documented:

- self-host install path
- migrations and upgrade flow
- authenticated API and MCP
- source-backed ingestion and recall
- metrics, traces, and ingestion job visibility, including smart-path recall and working-memory counters for verdicts, decisions, graph context, returned evidence volume, retrieval-mode fallback counts, bounded stored agent-action policy decision counts, and optional OpenTelemetry spans for API/MCP/worker latency diagnosis
- backup and restore runbooks
- reindex and embedding migration runbooks
- scheduled audit push or authenticated audit pull for SIEM
- smoke and quality eval gates
- clear extension boundary for private connectors, ACLs, SSO, and deployment-specific approval gates

Features outside that boundary can be deployment overlay work, but they must not be required for the OSS service to run safely.
