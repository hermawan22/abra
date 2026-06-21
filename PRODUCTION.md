# Production Readiness

Abra is production-oriented when deployed with the controls below. Do not run the API publicly without them.

## Supported Self-Host Paths

Use Docker Compose for a single-host production install or pilot. Use Kubernetes for replicated production deployments, either through the raw manifests or the Helm chart in `deploy/helm`.

## Docker Compose Install

Prerequisites:

- Docker Engine with Compose.
- A generated `ABRA_API_KEYS` value.
- A local Qwen-compatible embedding endpoint, or a custom compatible embedding provider. The built-in CLI lifecycle manages the Qwen/Qwen3-Embedding-0.6B runner only; Qwen/Qwen3-Reranker-0.6B is optional and must be configured separately when you provide a compatible rerank endpoint.
- Enough disk for Postgres source snippets, claims, audit events, and vectors.

Create `.env.production`:

```text
ABRA_API_KEYS=replace-with-generated-token
ABRA_WEBHOOK_SECRETS=replace-with-webhook-signing-secret
ABRA_ALLOW_UNSIGNED_WEBHOOKS_IN_PRODUCTION=false
ABRA_APPROVAL_MODE=enforce
POSTGRES_USER=abra
POSTGRES_PASSWORD=replace-with-generated-database-password
POSTGRES_DB=abra
ABRA_DATABASE_URL=postgres://abra:replace-with-generated-database-password@postgres:5432/abra
EMBEDDING_PROVIDER=compatible
EMBEDDING_BASE_URL=https://embedding-provider.example/v1
EMBEDDING_API_KEY=replace-with-embedding-key
EMBEDDING_MODEL=embedding-model
EMBEDDING_DIMENSIONS=1024
ABRA_EMBEDDING_BATCH_MAX_ITEMS=16
ABRA_EMBEDDING_BATCH_MAX_TOKENS=6000
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

The Compose file uses its bundled Postgres service with the `POSTGRES_*` and
`ABRA_DATABASE_URL` values above. Replace the database password before first
boot. To point Compose at managed Postgres with `pgvector`, set:

```text
ABRA_DATABASE_URL=postgres://user:password@postgres.example.invalid:5432/abra
```

Production Compose intentionally has no local source-build fallback. Set
`ABRA_IMAGE` and `POSTGRES_IMAGE` to digest-pinned references in
`.env.production`; the example file uses explicit placeholder digests that must
be replaced with real release/operator-approved digests before validation or boot.

Install:

```sh
docker compose --env-file .env.production pull
docker compose --env-file .env.production up -d postgres
docker compose --env-file .env.production run --rm migrate
docker compose --env-file .env.production up -d api worker
```

Always pass `--env-file .env.production`; the development `.env` uses host-oriented values such as `localhost` that are not valid inside Compose service containers.

Upgrade:

```sh
docker compose --env-file .env.production pull
docker compose --env-file .env.production run --rm migrate
docker compose --env-file .env.production up -d api worker
```

Use `docker-compose.dev.yml` only for local source-checkout development or
release-gate stack builds. Do not deploy production with `build: .` or
`ABRA_IMAGE=abra:local`.

Back up the `abra-postgres` volume or, for serious production use, point `ABRA_DATABASE_URL` at managed Postgres with `pgvector` and an existing backup policy. The bundled backup, restore-drill, and reindex scripts are in `scripts/`.

## Kubernetes Install

Prerequisites:

- Managed Postgres with `pgvector`.
- Access to the first-party GHCR image `ghcr.io/hermawan22/abra`, pinned by
  digest from the release `IMAGE_DIGEST` asset.
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

The Helm chart is available in `deploy/helm`; render it with
`helm template abra ./deploy/helm` and install it with your existing secret plus
the first-party GHCR image digest.

## Image Provenance and Pinning

Release images are published to `ghcr.io/hermawan22/abra` for `linux/amd64` and
`linux/arm64`. Each GitHub release includes an `IMAGE_DIGEST` asset. The first
line is the digest-pinned image reference and the remaining lines are tag aliases
for traceability.

Before promoting a release, verify the release-attested `IMAGE_DIGEST` file and
the registry image provenance:

```sh
gh attestation verify --repo hermawan22/abra IMAGE_DIGEST
image_ref="$(sed -n '1p' IMAGE_DIGEST)"
docker buildx imagetools inspect "$image_ref"
gh attestation verify "oci://${image_ref}" --repo hermawan22/abra
```

BuildKit SBOM and provenance attestations are attached to the GHCR image during
release. Treat missing SBOM/provenance, a missing platform, or a digest that
does not start with `ghcr.io/hermawan22/abra@sha256:` as a release-blocking
condition. Promote and roll back by digest, not by `latest`, semantic-version
tags, or locally rebuilt images.

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
ABRA_COMPOSE_RECALL_CONCURRENCY=1
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

`EMBEDDING_PROVIDER=local` is the default self-hosted neural path. It does not need an external API key, but it does require a local embedding endpoint reachable from the Abra containers. With Docker Compose, the default URLs use `host.docker.internal` so models running on the host can be reached from the API and worker containers. If you intentionally run local embeddings in production, set `ABRA_LOCAL_EMBEDDING_IMAGE` to an operator-verified `@sha256` runner image or provide your own compatible endpoint. Set `EMBEDDING_PROVIDER=compatible` to replace the local defaults with a custom provider.

The provider contract is intentionally provider-neutral. The provider must expose the configured embeddings request/response shape and must return vectors with the configured `EMBEDDING_DIMENSIONS`. API keys may be empty for self-hosted endpoints. `ABRA_EMBEDDING_BATCH_MAX_ITEMS` and `ABRA_EMBEDDING_BATCH_MAX_TOKENS` bound each embedding provider request; use smaller batches for local Qwen context-window reliability and raise them only after a compatible provider has measured capacity. The optional reranker is controlled separately with `RERANKER_PROVIDER`, `RERANKER_BASE_URL`, `RERANKER_API_KEY`, and `RERANKER_MODEL`; when unset, custom embedding providers do not keep the local Qwen reranker enabled. Abra stores embeddings and source-derived snippets; it does not send prompts for generation.

`ABRA_COMPOSE_HEALTH_CACHE_TTL` controls the short-lived per-scope health snapshot cache used by `POST /memory/compose`; set it to `0s` to disable caching. Direct `GET /memory/health` remains uncached for operator checks. Track `abra_working_memory_health_lookup_total` to see whether compose traffic is using fresh lookups, cache hits, coalesced waits, disabled cache mode, or unknown/error paths.

`ABRA_COMPOSE_RECALL_CONCURRENCY` and `ABRA_COMPOSE_GRAPH_CONCURRENCY` cap per-request fan-out inside `POST /memory/compose` and MCP `working_memory_compose`. Keep the conservative defaults (`1` for recall and `4` for graph) for small installs and local embedding runners; raise them only after watching database pool usage, recall latency, embedding-provider saturation, and memory-compose p95 under realistic agent traffic. Values must be between `1` and `32`.

`WORKER_MAX_SOURCES_PER_RUN` caps how many queued ingestion jobs one worker cycle claims. `WORKER_CONCURRENCY` caps how many claimed jobs run at once inside one worker process. Keep `WORKER_CONCURRENCY=1` with the default local Qwen runner; raise it only after the embedding provider and database pool have measured headroom. Same-source jobs are serialized inside one worker process to avoid git-cache and source-refresh races.

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
- Run one worker replica by default; scale `WORKER_CONCURRENCY` first for job-level ingestion parallelism.
- Use `POST /mcp` on the API service for remote MCP clients.
- Operate Abra through the CLI, API, MCP, metrics, and runbooks.

## Kubernetes Hardening

The Helm chart is the primary Kubernetes install path and renders the runtime
pods with non-root users, `RuntimeDefault` seccomp, disabled service-account
token automount, read-only root filesystems, dropped Linux capabilities,
resource requests and limits, and bounded writable `emptyDir` mounts for `/tmp`
and the worker git cache. Keep those controls enabled when translating the chart
to another deployment system.

Cluster operators should keep the included baseline NetworkPolicy enabled, or
replace it with an equivalent service-mesh policy, and add namespace-level Pod
Security admission, platform secret management, image-pull policy controls,
internal-only ingress, gateway rate limits, and admission policy that requires
`ghcr.io/hermawan22/abra@sha256:...` image references. Enable the packaged
ServiceMonitor and PrometheusRule only in clusters that install the Prometheus
Operator CRDs. Do not grant the API or worker pods broad Kubernetes API access;
the chart does not require a mounted service account token for normal operation.

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

- Prefer scheduled source configs, signed source webhooks, or connector batch
  jobs over manual uploads.
- Ingest only approved sources and map each source to a stable scope and authority.
- Keep connector credentials outside the Abra OSS image.
- Use `mcp` source configs when a user-owned or internal MCP server can export
  normalized Abra documents on a schedule, `POST /ingest/webhooks` for event
  pushes, or `POST /ingest/documents/batch` for connector-owned batch jobs.
  Configure `ABRA_WEBHOOK_SECRETS` and send `x-abra-signature: sha256=<hmac>`
  for webhooks so bodies are tamper-evident in addition to API-key auth.
- Treat source refresh as idempotent. Re-ingestion deprecates missing claims and graph relations, reactivates still-present claims and relations from the same source, and replaces source-scoped summaries.
- Store connector state outside the request path so ingestion spikes do not affect recall latency.

When approval enforcement is active, direct `POST /ingest/documents`, MCP
`ingest_document(s)`, and CLI `abra ingest` use the `agent_write` approval gate.
Automated connectors should either carry an approved `approval_id` for planned
writes or run behind stored agent-action policies that explicitly allow the
source scope.

Before registering a user-owned MCP export tool as a production source, validate
it from the operator CLI with `--dry-run` or `--validate`:

```sh
abra source mcp \
  --scope team:platform \
  --mcp-url https://mcp.example/mcp \
  --tool export_documents \
  --header-env X-API-Key=CONFLUENCE_API_KEY \
  --dry-run
```

The dry run calls the upstream MCP tool and validates the returned normalized
documents without creating a source config or queueing ingestion. The same
validation contract is available to gateways through `POST /sources/configs/validate`
and MCP `validate_mcp_source`. Server-side validation that reads
`bearer_token_env` or `header_env` can require an approved `connector_enable`
`approval_id` before Abra sends those env-backed credentials to the upstream
MCP server. Register the source only after the exported documents have stable
`source_url`, title, content, scope, and authority semantics. Keep bearer tokens
and custom headers in env references such as `bearer_token_env` and
`header_env`; do not store literal vendor credentials in source configs.

Operate registered sources through the source lifecycle commands:

```sh
abra sources status <source-config-id>
abra sources logs <source-config-id> --limit 20
abra sources sync <source-config-id> --scope team:platform --wait --wait-timeout 10m
abra sources backfill <source-config-id> --scope team:platform --approval-id <approval-id> --wait --wait-timeout 10m
abra sources pause <source-config-id>
abra sources resume <source-config-id> --approval-id <approval-id>
```

Manual sync and backfill are operator actions and bypass normal due checks, so
use them for incident recovery, source migration, credential rotation, or
normalization changes. Backfill uses the `backfill` approval action when
enforcement or stored policy requires review. Pause stops future scheduled
refresh without rewriting the connector config. Active source config writes and
resume are connector enablement; in enforced approval mode they can require an
approved `connector_enable` or `source_authority_change` request, especially
when the source authority, authority score, scope, or connector identity
changed. Source config upserts and pause/resume changes write audit events, so
record the approval ID and the later operation `x-request-id` in change records.

Private connector overlays still own vendor credentials, token rotation,
source-system ACL and group normalization, event diffing, and provider-specific
retries for systems such as Confluence, Jira, Slack, or Drive. Abra owns the
durable memory contract after the overlay or MCP source emits normalized
documents.

## Observability

Endpoints:

- `GET /healthz`
- `GET /readyz`
- `GET /metrics`
- `GET /audit/events`
- `POST /ingest/documents`
- `POST /ingest/documents/batch`
- `POST /ingest/webhooks`
- `POST /recall`
- `POST /claims`
- `POST /claims/:claimId/challenge`
- `POST /claims/:claimId/forget`
- `GET /observations`
- `POST /observations`
- `GET /conflicts`
- `POST /conflicts/:conflictId/resolve`
- `POST /sources`
- `POST /brain/think`
- `POST /memory/compose`
- `GET /memory/health`
- `POST /memory/summaries`
- `POST /memory/summaries/rebuild`
- `GET /learning/proposals`
- `POST /learning/proposals`
- `POST /learning/proposals/:proposalId/decide`
- `POST /learning/proposals/:proposalId/apply`
- `GET /sources/configs`
- `POST /sources/configs`
- `POST /sources/configs/validate`
- `GET /sources/configs/:sourceConfigId`
- `POST /sources/configs/:sourceConfigId/pause`
- `POST /sources/configs/:sourceConfigId/resume`
- `GET /ingestion/jobs`
- `POST /ingestion/jobs`
- `POST /ingestion/jobs/:jobId/retry`
- `POST /ingestion/jobs/:jobId/cancel`
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
- `GET /agent/profiles`
- `POST /agent/profiles`
- `GET /graph/entities`
- `GET /graph/relations`
- `POST /policy/plan`
- `POST /mcp`

Every HTTP response includes `x-request-id`; propagate it into gateway logs and support traces.

Prometheus scraping should include the smart-path metrics, `abra_recall_retrieval_mode_total`, `abra_working_memory_retrieval_quality_total`, and `abra_ai_provider_*` metrics. Alert when production recall stops reporting `mode="hybrid"` or when `mode="full_text_embedding_error"` / `mode="full_text_empty_embedding"` rises above the expected maintenance window, because those fallback modes usually indicate an embedding provider or configuration problem. Alert when `abra_ai_provider_waiting` or `abra_ai_provider_wait_duration_milliseconds_sum` grows while `abra_ai_provider_in_flight` sits at the configured concurrency limit; that usually means the embedding or reranker provider is saturated and `ABRA_AI_PROVIDER_CONCURRENCY`, provider replicas, or ingestion concurrency need tuning. Alert separately when `abra_working_memory_retrieval_quality_total{quality="low_confidence"}` increases outside known sparse scopes; low-confidence packets mean agents should rerun with better queries, ingest stronger sources, or rebuild embeddings before autonomous work.
When tracing is enabled, use spans for latency diagnosis rather than high-cardinality metrics. The useful path is `HTTP route -> abra.memory.compose -> retrieval_trace in response` or `HTTP /mcp -> abra.mcp.tool -> abra.recall/abra.memory.compose`.

Audit events are recorded for memory and governance mutations, including:

- document ingestion and batch ingestion
- claim remembering, challenge, and forget
- observation capture and observation-to-learning proposal linkage
- summary rebuilds
- memory compose events outside diagnostic mode
- conflict resolution
- learning proposal create, decide, and apply
- source config upsert and status changes
- ACL policy upserts
- agent action policy upserts and decisions
- agent profile upserts

Approval decisions are stored in `approval_requests`. Include approval IDs in change records and incident notes, and pair them with the `x-request-id` from the later risky operation. The audit export is the operational mutation log; approval requests remain the source of truth for approval history.

Operators can export audit events for SIEM pulls:

```sh
auth_header="x-api-key: $ABRA_OPS_TOKEN"
curl -H "$auth_header" \
  "$ABRA_URL/audit/events?scope=team:example&event_type=claim.remembered&format=ndjson&since=2026-06-16T00:00:00Z"
```

Supported filters are `scope`, `event_type` or `type`, `target_type`, `since`, `until`, `limit`, and `format=json|ndjson`. All-scope export requires an all-scope `ops` or `admin` key. Scoped ops keys must pass an allowed `scope`, which prevents accidental cross-scope enumeration from SIEM jobs.

The worker can also push audit events to an HTTP/SIEM sink without adding another service:

```text
ABRA_AUDIT_SINK_URL=https://siem.example.invalid/abra/audit
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
npm test   # maintainer docs/scripts gate; npm is not the runtime
go test ./...
docker build -t abra:local .
helm lint deploy/helm
helm template abra deploy/helm >/tmp/abra-rendered.yaml
```

For a release candidate, also verify the published artifacts and render Helm
with the promoted digest:

```sh
sha256sum -c SHA256SUMS
gh attestation verify --repo hermawan22/abra IMAGE_DIGEST
image_ref="$(sed -n '1p' IMAGE_DIGEST)"
gh attestation verify "oci://${image_ref}" --repo hermawan22/abra
helm template abra deploy/helm \
  --set image.repository=ghcr.io/hermawan22/abra \
  --set image.digest="${image_ref#*@}" \
  >/tmp/abra-rendered.yaml
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

Smoke tests are not quality evaluation. The full release gate covers recall quality, citation precision, scope leakage, graph quality, policy planning, webhook queue-pressure drain, dogfood ingestion, and embedding provider checks.

## Backup, Restore, Reindex, and Embedding Changes

Operational maintenance is part of the v1 bar:

- Backups must include all Postgres tables, vector indexes, source configs, ingestion jobs, audit events, stored policies, and graph records.
- Restores must be drilled into isolated databases before production cutover.
- Reindexing should be done during a maintenance window and followed by smoke plus recall quality checks.
- Same-dimension embedding model changes require re-ingestion and eval comparison.
- Embedding dimension changes require a database migration and full re-ingestion.

The bundled helper commands run directly as shell scripts. The `npm run ops:*`
aliases are source-checkout convenience wrappers only, not the production
runtime path.

```sh
DATABASE_URL=postgres://... bash scripts/abra-backup.sh
ABRA_RESTORE_DUMP=backups/abra_YYYYMMDD_HHMMSS.dump ABRA_RESTORE_DATABASE_URL=postgres://... bash scripts/abra-restore-drill.sh
DATABASE_URL=postgres://... bash scripts/abra-reindex.sh
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
- clear extension boundary for connector adapters, ACLs, SSO, and deployment-specific approval gates

Features outside that boundary can be deployment overlay work, but they must not be required for the OSS service to run safely.
