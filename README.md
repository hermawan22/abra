# Abra

Abra is a CLI-only, source-cited memory control plane and governed brain layer for AI agents.

It is not a generic RAG box, vector database UI, browser UI, or chatbot. Abra is meant to be installed, operated, and connected to agents from the terminal through the `abra` CLI, HTTP, and MCP contracts.

Abra stores organizational knowledge as claims backed by evidence, scope, freshness, and trust status. Agents can recall the best-supported claims through hybrid lexical/vector retrieval, compose task-specific working memory, or ask `brain_think` for a governed answer with citations, gap analysis, graph context, memory-health status, and an explicit agent decision gate.

## Why

Agent memory becomes dangerous when every observation is treated as truth. Abra uses a safer loop:

```text
observe -> propose -> verify -> promote -> use
```

Claims without a source are stored as `unverified`. Source-backed claims can be `verified`. Challenged or stale claims lose ranking and should not be used silently.

## Architecture

```text
Agents / agent runtimes
  -> Abra MCP HTTP
    -> Abra API
      -> Postgres + pgvector
      -> Auto-ingestion Worker
```

Source systems are ingested through the API, signed webhooks, or `source_configs` consumed by the worker. The OSS build supports generic document ingestion, local repo ingestion, and provider-neutral remote Git ingestion. Deployment extensions can add event connectors for Confluence, Jira, Slack decisions, Drive, or other systems by pushing normalized documents into Abra.

## 3-Minute CLI Install

The fastest path from this checkout puts the `abra` binary on your machine:

```sh
./scripts/install.sh
```

Install from GitHub releases:

```sh
curl -fsSL https://raw.githubusercontent.com/hermawan22/abra/main/scripts/install.sh | sh
```

The installer downloads a platform release binary when available and verifies it against `SHA256SUMS` before installing. If no release asset exists for your platform yet, it falls back to `go install`.

Then run the guided CLI onboarding:

```sh
abra setup
```

`abra setup` checks required commands, creates the runtime env file, asks which embedding provider to use, and can start the local stack. From a source checkout it uses `.tmp/quickstart.env`; from a global CLI install it stores runtime files under your Abra config directory and can be run from any folder. `abra install` is a compatibility alias for `abra setup`; the curl script is what installs the CLI binary.

For non-interactive local setup:

```sh
abra setup --yes
```

Connect a custom compatible embedding provider during setup without editing env files:

```sh
printf '%s' "$PROVIDER_API_KEY" | abra setup --compatible --base-url https://api.example.com/v1 --embedding-model embedding-model --api-key-stdin
```

Try the governed brain:

```sh
abra ingest --scope repo:demo \
  --title Intro \
  --source-url file://intro.md \
  --text "Agents should use Abra before autonomous code changes."

abra think "What should agents use before autonomous code changes?" --scope repo:demo
```

Ingest local docs or repo files immediately from the CLI:

```sh
abra ingest . --code
```

Queue a remote Git repo through the worker:

```sh
abra ingest --scope repo:my-app --git https://github.com/owner/repo.git --ref main --code --wait
```

Generate MCP client config:

```sh
abra mcp > .tmp/abra.mcp.json
```

Stop the stack:

```sh
abra down
```

Reset demo data:

```sh
abra down --reset
```

Upgrade or remove the CLI binary:

```sh
abra upgrade
abra uninstall --yes
```

### From Source

From source, run the Go CLI directly:

```sh
go run ./cmd/abra up
```

For repeated use, build one local binary:

```sh
go build -o .tmp/abra ./cmd/abra
.tmp/abra up
```

The `demo` command is still available when you want seed data and an immediate `brain/think` probe:

```sh
abra demo
```

Step-by-step equivalent:

```sh
go run ./cmd/abra init
go run ./cmd/abra up
```

When it finishes:

- MCP endpoint: `http://localhost:18080/mcp`
- Demo token: `dev-token`

Run the guided CLI onboarding:

```sh
go run ./cmd/abra setup
```

Ingest one source-backed demo document:

```sh
go run ./cmd/abra ingest --scope repo:demo \
  --title Intro \
  --source-url file://intro.md \
  --text "Agents should use Abra before autonomous code changes."
```

Ingest local repo docs and code intelligence immediately from the CLI:

```sh
go run ./cmd/abra ingest . --code
```

Think with governed memory:

```sh
go run ./cmd/abra think "What should agents use before autonomous code changes?" --scope repo:demo
```

Check service and memory status:

```sh
go run ./cmd/abra status
go run ./cmd/abra doctor
```

Connect an MCP client:

```sh
go run ./cmd/abra mcp > .tmp/abra.mcp.json
```

The generated config points at `http://127.0.0.1:18080/mcp` with the quickstart token. A static example is available at [examples/mcp/remote-http.json](./examples/mcp/remote-http.json).

Stop the stack:

```sh
go run ./cmd/abra down
```

Reset demo data:

```sh
go run ./cmd/abra down --reset
```

The demo uses the default local neural embedding path. Run Qwen/Qwen3-Embedding-0.6B and Qwen/Qwen3-Reranker-0.6B on the configured local endpoints before ingesting documents. For production, keep approval enforcement on and either self-host those local models or configure a compatible custom embedding provider.

For command-by-command local and self-host usage, read [docs/CLI.md](./docs/CLI.md).

## Self-Host Install

Abra has two supported self-host paths:

- Docker Compose for a single VM or small internal deployment.
- Kubernetes, either through raw manifests in `deploy/kubernetes` or the Helm chart in `deploy/helm`.

The current image is a Go runtime. It runs API by default and uses command overrides for migration and worker roles.

### Docker Compose

Create a production env file for Compose:

```sh
cp examples/env/production.env.example .env.production
$EDITOR .env.production
```

By default this uses the Compose-managed Postgres service. To use an external Postgres with `pgvector`, add `ABRA_DATABASE_URL=postgres://...` to `.env.production`; Compose maps that value to the container's `DATABASE_URL`.

Start Postgres, run migrations, then start the API and worker:

```sh
docker compose --env-file .env.production up -d postgres
docker compose --env-file .env.production run --rm migrate
docker compose --env-file .env.production up -d api worker
```

Check the service:

```sh
auth_header="x-api-key: replace-with-generated-token"
curl -H "$auth_header" http://localhost:18080/readyz
```

Use `POST /mcp` on the API service for remote MCP clients.
Prometheus-compatible metrics are available at `GET /metrics` with the same API authentication policy as other non-health endpoints.
In addition to HTTP route metrics, Abra exposes smart-path metrics for recall and working-memory composition: `abra_smart_path_requests_total`, duration sums, returned facts/documents/graph relations, learning suggestions, review-required decisions, autonomous-allowed decisions, recall retrieval modes, working-memory retrieval-quality counters/scores, and working-memory memory-health gates. Health-aware compose metrics include `abra_working_memory_health_status_total`, `abra_working_memory_health_lookup_total`, returned signal counts, critical/warning signal totals, last health score, and bounded per-signal counters. Stored agent-action policy decisions are counted with `abra_agent_policy_decisions_total`. These labels intentionally avoid scope, principal IDs, and query text; policy action labels, quality labels, health status labels, health lookup labels, and health signal labels are normalized to bounded sets.
Optional OpenTelemetry tracing is available through OTLP HTTP by setting `OTEL_EXPORTER_OTLP_ENDPOINT` or `ABRA_OTEL_EXPORTER_OTLP_ENDPOINT`. Traces cover HTTP routes, recall, working-memory composition, MCP tool calls, and worker ingestion cycles with bounded attributes; raw scope names, query text, task text, principals, and tokens are not attached as span attributes.

Production operators should read [PRODUCTION.md](./PRODUCTION.md), [docs/EXTENSIONS.md](./docs/EXTENSIONS.md), [RELEASE.md](./RELEASE.md), and [SECURITY.md](./SECURITY.md) before exposing Abra to internal agents.

Bundled ops helpers and eval gates use the repository scripts. They are developer/operator tooling, not required for the Go CLI first-run path:

```sh
bash scripts/abra-backup.sh
bash scripts/abra-restore-drill.sh
bash scripts/abra-reindex.sh
```

Restore drills and reindexing default to dry-run; set `ABRA_DRY_RUN=0` only for an isolated restore target or approved maintenance window.

Quality and performance gates:

```sh
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run smoke:selfhost
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run eval:tier1
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run eval:golden
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run eval:provider
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run eval:dogfood
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run perf:local
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run release:gate
```

`eval:golden` runs a JSONL dataset, defaulting to `examples/evals/golden.jsonl`. Set `ABRA_GOLDEN_DATASET=/path/to/dataset.jsonl` to run team-specific recall, graph, and working-memory cases without editing the eval runner.

`eval:provider` runs the provider-quality benchmark on the same JSONL format and reports embedding provider identity, hit rate at 1/3/5, citation coverage, leakage count, recall p95/p99, working-memory p95/p99, verification verdict distribution, and agent decision distribution. Set `ABRA_PROVIDER_DATASET=/path/to/provider.jsonl` for staging datasets and `ABRA_PROVIDER_EXPECT=compatible` when a deployment must use a compatible external embedding provider.

`eval:dogfood` registers the Abra repo itself as `repo:abra`, queues a local-repo ingestion job, rebuilds summaries, and fails if `working_memory_compose` still returns an empty packet. The eval process defaults to the current checkout path, but the worker must be able to read the same path. Set `ABRA_DOGFOOD_SOURCE_ROOT=/path/visible/to/worker` when the API and worker run in containers or another filesystem layout. The gate pauses its source config after success unless `ABRA_DOGFOOD_KEEP_SOURCE_ACTIVE=1` is set.

`perf:local` seeds a scoped fixture workload, then checks p95/p99 recall and working-memory latency, failure rate, and a higher-concurrency working-memory capacity probe with memory-health cache-status accounting. Set `ABRA_PERF_SOAK_SECONDS` for an opt-in sustained working-memory soak profile that reports throughput, p95/p99, failure rate, and health-cache distribution. Tune release thresholds with `ABRA_PERF_RECALL_P95_MS`, `ABRA_PERF_MEMORY_P95_MS`, `ABRA_PERF_MEMORY_CAPACITY_P95_MS`, `ABRA_PERF_MEMORY_SOAK_P95_MS`, and `ABRA_PERF_MAX_FAILURE_RATE`.

`release:gate` emits one JSON report that combines script checks, Go tests, Compose/Helm render checks, smoke, quality evals, provider-quality benchmark, Tier 2/3 agent workflow traces, enforced approval-mode probes, dogfood, and performance/capacity gates. Use `ABRA_RELEASE_PROFILE=quick` for a short developer gate that skips provider/Tier 2/3, enforced approval-mode probes, dogfood, and golden evals and reduces the perf fixture; use the default `full` profile before a release. The full gate gives dogfood an isolated release scope by default so old local ingestion failures do not contaminate release evidence; set `ABRA_DOGFOOD_SCOPE` only when you intentionally want to validate a specific existing scope. Set `ABRA_RELEASE_MANAGE_STACK=1` when the runner should build the local Docker Compose image, start Postgres, run migrations, and start the API and worker itself; managed mode raises the local rate limit, uses a short worker interval for eval responsiveness, and runs a second Tier 2/3 pass under `ABRA_APPROVAL_MODE=enforce`. For containerized dogfood, set `ABRA_RELEASE_PREPARE_DOGFOOD_SOURCE=1` to copy the checkout into the worker container before running `eval:dogfood`; this is enabled automatically when `ABRA_RELEASE_MANAGE_STACK=1`. Set `ABRA_RELEASE_APPROVAL_ENFORCEMENT_GATE=1` only when the target stack is already running in enforced mode or the runner may recreate it.

### Kubernetes

Generic manifests live in `deploy/kubernetes`. Apply them with your own image, namespace, secrets, network policy, and internal ingress. The required order is:

1. Provision Postgres with `pgvector`.
2. Create `abra-secrets`.
3. Apply `configmap.yaml`.
4. Delete any previous `abra-migrate` Job, apply `job-migrate.yaml`, and wait for it to complete.
5. Deploy `deployment-api.yaml`, `deployment-worker.yaml`, and `service.yaml`.

The Helm chart lives in `deploy/helm`; render it with `helm template abra ./deploy/helm` and install it with your image, secret, namespace, and ingress settings.

## Services

- `cmd/abra` is the Go-native CLI for local bootstrap, ingestion, thinking, recall, compose, status, and MCP config.
- `cmd/abra-api` exposes HTTP ingestion, recall, graph, source-config, and MCP endpoints.
- `cmd/abra-worker` expires stale claims, records ingestion jobs, and runs configured source ingestion.
- `cmd/abra-migrate` applies SQL migrations.
- `migrations/001_init.sql` creates the Postgres + pgvector schema.
- `migrations/002_abra_v1_graph_ingestion_policy.sql` adds source configs, graph, policy, job, observation, and conflict tables.

Abra is a service with a CLI operator workflow: use the Go `abra` binary, or `go run ./cmd/abra ...` from source, to bring the stack up or down, ingest memory, ask governed questions, inspect status, and generate MCP client configuration.

## Container

The Docker image defaults to the API service:

```text
/app/abra-api
```

Override the command for other service roles:

```text
/app/abra
/app/abra-worker
/app/abra-migrate
```

## Data Model

- `documents`: source records such as markdown pages, Confluence pages, Jira tickets, or repo docs.
- `chunks`: searchable document chunks.
- `claims`: atomic facts with scope, status, authority, confidence, and freshness.
- `evidence`: source snippets backing claims.
- `feedback`: corrections and usefulness signals.
- `conflicts`: active, reviewing, resolved, or suppressed contradictions between claims or graph records.
- `source_configs`: approved ingestion sources consumed by the worker.
- `entities` and `relations`: evidence-backed graph records in Postgres.
- `memory_summaries`: precomputed source, repo, module, file, route, component, symbol, and package summaries used by working-memory composition.
- `learning_proposals`: reviewable suggestions from agents or verification signals; these do not become trusted memory until an operator or approved workflow applies them. Pending proposals are deduplicated by scope, type, title, target, and source at the database layer so repeated or concurrent agents share one review item.
- `agent_profiles`: configurable agent runtime records with principal reference, default scope, allowed and denied scopes, permissions, and memory preferences.
- `policies`: configurable ACL and agent-action policy records.

Source refresh is idempotent across the memory layers. Re-ingesting the same `scope` and `source_url` temporarily deprecates that source's active claims and relations, deletes summaries tied to the source, then reactivates claims and relations that still appear in the refreshed content.

Claim statuses:

- `verified`
- `unverified`
- `inferred`
- `challenged`
- `deprecated`
- `expired`

## MCP Interface

`initialize` declares `tools`, `resources`, and `prompts` capabilities. Tools perform requested operations, resources expose bounded read-only context, and prompts provide reusable agent workflows for clients that support MCP prompt discovery.

MCP resources:

- `abra://guide/agent-workflow`
- `abra://memory/health/{scope}`
- `abra://working-memory/{scope}/{task}`

MCP prompts:

- `abra-before-code(task, scope, agent?)`
- `abra-review-memory(scope)`

MCP tools:

- `recall(query, scope, limit?, include_unverified?)`
- `ingest_document(source_type, source_url, title, scope, content, source_id?, source_updated_at?, authority?, authority_score?, metadata?)`
- `ingest_documents(documents, scope?, source_type?, source_updated_at?, authority?, authority_score?, metadata?)`
- `remember_claim(claim, scope, source_url?, source_type?, authority?)`
- `challenge(claim_id, reason, source_url?, verdict?, conflicting_claim_id?, severity?)`
- `forget(claim_id, reason?)`
- `brain_sources(query, scope, limit?)`
- `brain_summaries(scope, query?, limit?)`
- `brain_think(question, scope, agent?, limit?, max_queries?, token_budget?, include_unverified?)`
- `memory_health(scope)`
- `rebuild_summaries(scope, limit?, approval_id?)`
- `policy_plan(hook, task, scope?, files?, changed_files?, language?, agent?, limit?, max_queries?)`
- `working_memory_compose(task, scope, hook?, agent?, files?, changed_files?, language?, limit?, max_queries?, token_budget?, include_unverified?)`
- `list_conflicts(scope, status?, severity?, claim_id?, relation_id?, limit?)`
- `resolve_conflict(conflict_id, status, resolved_by?, resolution?, metadata?)`
- `upsert_acl_policy(scope, name, subject_type, subject_id, effect, rule, status?, priority?, created_by?, metadata?, approval_id?)`
- `list_acl_policies(scope, subject_type?, subject_id?, limit?)`
- `acl_decision(scope, principal_type, principal_id, action, resource_type?, resource_id?, context?)`
- `upsert_agent_policy(scope, name, effect, rule, status?, priority?, subject_type?, subject_id?, created_by?, metadata?, approval_id?)`
- `list_agent_policies(scope, limit?)`
- `agent_policy_decision(scope, action, target_type?, target_id?, principal_type?, principal_id?, context?)`
- `upsert_agent_profile(scope, profile_key, display_name, agent_type?, status?, principal_ref?, default_scope?, allowed_scopes?, denied_scopes?, permissions?, memory_preferences?, created_by?, metadata?, approval_id?)`
- `list_agent_profiles(scope, status?, limit?)`
- `upsert_source_config(scope, source_type, name, id?, base_url?, connector_kind?, status?, authority?, authority_score?, config?, metadata?, created_by?, approval_id?)`
- `list_source_configs(scope, limit?)`
- `enqueue_ingestion_job(source_config_id, trigger_type?, created_by?, max_attempts?, metadata?)`
- `list_ingestion_jobs(scope, source_config_id?, limit?)`
- `retry_ingestion_job(job_id, created_by?, max_attempts?, metadata?)`
- `cancel_ingestion_job(job_id, reason?, created_by?, metadata?)`
- `propose_learning(scope, proposal_type, title, rationale, target_type?, target_id?, source_url?, confidence?, payload?, created_by?)`
- `list_learning_proposals(scope, status?, limit?)`
- `decide_learning_proposal(proposal_id, status, reviewed_by?, review_reason?, approval_id?, metadata?)`
- `request_approval(action, scope, reason, target_type?, target_id?, requested_by?, payload?, metadata?, expires_at?)`

## HTTP API

- `GET /healthz`
- `GET /readyz`
- `POST /ingest/documents`
- `POST /ingest/webhooks`
- `POST /recall`
- `POST /claims`
- `POST /claims/:claimId/challenge`
- `POST /claims/:claimId/forget`
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
- `GET /sources/configs`
- `POST /sources/configs`
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
- `GET /agent/profiles`
- `POST /agent/profiles`
- `GET /agent/policies`
- `POST /agent/policies`
- `POST /agent/policy/decision`
- `GET /graph/entities`
- `GET /graph/relations`
- `POST /policy/plan`
- `POST /mcp` for stateless MCP Streamable HTTP
- `GET /metrics` for Prometheus-compatible API metrics
- `GET /audit/events` for authenticated audit export as JSON or NDJSON
- worker-driven audit delivery to HTTP/SIEM sinks as signed NDJSON

Abra opens claim conflicts from explicit `challenge(..., verdict="conflict")` calls and from deterministic ingestion checks. The first automatic claim detector handles high-signal policy assertions such as “services must run contract tests before release” conflicting with “services should skip contract tests before release.” Graph ingestion also opens relation conflicts for high-signal opposing or competing relations, starting with opposing use policies and competing release-readiness alternatives. This keeps the OSS core explainable and cheap while still catching the kind of contradictory team memory that can mislead agents.

`POST /brain/think` is the answer-facing brain API. It asks the same governed memory stack used by `POST /memory/compose`, then returns a concise answer, citation map, graph paths, explicit gaps, memory-health status, active conflicts, verification report, next actions, and the agent decision gate. Unlike a plain synthesis layer, it will not silently turn weak, stale, challenged, or conflict-heavy retrieval into confident prose; the response names those gaps and keeps autonomous use blocked or review-gated when the underlying memory is unsafe.

`POST /memory/compose` is the agent-facing smart-memory API. It runs policy planning, retrieval planning, multiple hybrid lexical/vector recall passes, graph-aware retrieval, active conflict lookup across recalled claims and graph relation IDs, impact-map compilation, validation-plan compilation, evidence grouping, deterministic evidence verification, stored agent-action policy checks, risk detection, relevant-file extraction, bounded context-window packing, and an agent decision gate, then returns one working-memory packet for the task. The retrieval plan includes intent-specific `coverage_targets` for summaries, facts, supporting documents, graph relations, and evidence sources; verification compares the packet against that contract before an agent uses it. This lets code and architecture packets be strong with summaries, source chunks, and graph context even when code ingestion intentionally creates no trusted natural-language claims, while migration/debugging packets can still require claim facts. Independent planned summary/recall lookups and graph seed expansions run in parallel and are merged deterministically, keeping larger query and graph budgets from turning into linear request latency. Non-safety retrieval branch failures are returned as a degraded packet with `retrieval_warnings` instead of hard-failing the whole request; safety gates such as conflict lookup and agent-action policy evaluation still fail closed. The composer also emits deterministic `graph_warnings` for high-signal opposing or competing graph relations, such as a service having competing release-readiness recommendations. Warning relation snapshots include relation IDs, so operators and agents can jump from a warning to filtered conflict review. Ingestion persists those high-signal graph contradictions as relation conflicts so operators can inspect and resolve them through the same conflict workflow, and active relation conflicts are returned in the packet's `conflicts` list. Deterministic code intelligence currently extracts JavaScript/TypeScript package imports, exports, routes, React-style components, plus Go package declarations, imports, functions, methods, constants, variables, and types. The packet includes a compact `retrieval_trace` with per-stage status, duration, query counts, result counts, and parallelism markers so operators can see which retrieval or verification stage dominates latency as memory grows.

Example working-memory request:

```json
{
  "task": "rotate API authentication middleware",
  "scope": "repo:example/service",
  "agent": "service-agent",
  "hook": "before_task",
  "files": ["cmd/api/main.go", "internal/auth.go"],
  "language": "go",
  "limit": 6,
  "max_queries": 6,
  "token_budget": 1600
}
```

The response includes `intent`, `strategy`, the recall `plan`, `retrieval_plan`, `retrieval_trace`, optional `retrieval_warnings`, hierarchical `summaries`, source-backed `facts`, `supporting_documents`, `graph_context` with relation IDs, optional `graph_warnings`, active `conflicts`, scoped `memory_health`, `relevant_files`, `impact_map`, `validation_plan`, `risks`, grouped `evidence`, deterministic `verification`, `agent_policy_decisions`, deterministic `agent_decision`, `context_window`, `learning_suggestions`, `suggested_steps`, and packet `stats`. The `impact_map` is a deterministic priority list of files, packages, routes, symbols, and entities with confidence, reasons, relation counts, summary counts, fact counts, and source evidence. The `validation_plan` is a deterministic list of required or optional checks inferred from intent, language, impacted files, policy gates, memory health, and verification state; it can include commands such as `go test ./...`, `npm test`, `docker compose config`, or `helm template abra deploy/helm` when those areas are relevant.

`context_window` is the prompt-ready slice of the packet. It packs task gate, risks, validation, summaries, facts, graph relations, impact items, and source excerpts by priority until `token_budget` is reached. It returns selected `blocks`, dropped lower-priority blocks, warnings, token estimates, and a rendered `prompt` string. This is how small and large models can consume the same verified memory contract without hand-rolling their own context packing.

`memory_health` is included directly in `POST /memory/compose` and MCP `working_memory_compose`. Critical health signals block autonomous agent work even when retrieval is otherwise strong; review-level health signals disable autonomous work until the operator or agent resolves the recommended actions. The context window includes the memory health status and signal codes in the task gate, so small models see readiness without calling a second endpoint. Compose uses a short-lived per-scope health cache and coalesces concurrent cold lookups to avoid repeatedly running aggregate health queries under agent load; direct `GET /memory/health` calls remain the real-time operator view.

`GET /memory/health` includes `claims.trusted_from_code_documents`. This value must stay at zero: code files may create chunks, graph context, and summaries, but they must not become trusted natural-language claims. A nonzero value makes the scope critical until the source is cleaned up and re-ingested.

`GET /memory/health` also includes `learning.duplicate_pending_groups`. This value must stay at zero: repeated or concurrent agent suggestions should share a single pending learning proposal for the same scope, type, title, target, and source. A nonzero value makes the scope critical because the review queue can no longer be trusted as one item per proposed learning action.

`GET /memory/health` also reports ingestion liveness. `ingestion.stale_running_jobs` must stay at zero; a nonzero value makes the scope critical because a worker lease may be stuck and memory freshness cannot be trusted. `ingestion.retry_jobs` makes the scope need review because one or more sources are waiting for another ingestion attempt.

`GET /memory/health` returns structured `signals` in addition to the human-readable `reasons`. Each signal has a stable `code`, `category`, `severity`, `count`, `score_impact`, `message`, and recommended `action`. Agents, CLI tooling, and alerting integrations should use these fields instead of parsing reason strings when deciding whether to proceed, request review, retry ingestion, or clean up unsafe memory.

The verifier returns a `verdict` (`strong`, `partial`, `weak`, or `unsafe`), `score`, intent coverage, claim source coverage, retrieval quality, active conflict records, graph warnings, unsafe claim IDs, and recommendations. `retrieval_coverage` reports the coverage targets, actual counts, whether the packet is complete, and which layers are missing. Missing required coverage makes the packet action-required and weakens autonomy even when some source-backed evidence exists. `retrieval_quality` summarizes result count, top and average rank score, top text/vector signals, lexical and semantic hit counts, zero-score results, and whether the packet is low-confidence. Low-confidence retrieval makes the packet action-required so agents rerun with a better query or rebuild embeddings before using weak context autonomously. Active conflicts make the packet `unsafe` and block autonomous action until the contradiction is resolved. They are routed to conflict review actions such as `list_conflicts`, not approval-bypass actions. Graph warnings make the packet action-required and reduce autonomy until the graph evidence is reviewed. Operators can inspect conflicts with `GET /conflicts`, optionally filtered by `claim_id` or `relation_id`, and mark them `resolved`, `suppressed`, `reviewing`, or `open` with `POST /conflicts/:conflictId/resolve`; resolved and suppressed conflicts no longer block working-memory composition. The challenged claims remain challenged until a later claim review, correction, or deprecation decides which memory should be trusted. `agent_policy_decisions` evaluates the current agent against stored policies for standard risky actions such as `agent_write`, `challenge_claim`, `forget_claim`, `backfill`, `source_authority_change`, and `acl_change`. `agent_decision` combines evidence verification and stored policy decisions into an explicit gate: `proceed`, `caution`, `needs_review`, or `blocked`, plus whether autonomous action is allowed and which follow-up actions are permitted. Agents should obey this gate before autonomous writes or code changes.

`learning_suggestions` are not writes to trusted memory. They are candidate actions such as source refresh, challenge, summary rebuild, graph extraction, low-confidence retrieval repair, or claim promotion. `POST /memory/compose` and MCP `working_memory_compose` automatically persist actionable suggestions into the pending learning-proposal queue with application and database deduplication; no-op suggestions such as `No learning action required` are returned in the packet but are not queued. Persisted suggestions include `proposal_id`, `persisted`, and `persisted_new` so agents can link the packet to operator review. When an operator accepts a proposal through `POST /learning/proposals/:proposalId/decide` or MCP `decide_learning_proposal`, the response includes an `apply_plan` with the deterministic next operation, endpoint, target, payload, and whether approval is required. HTTP and MCP proposal and decision calls also write `learning.proposed` and `learning.decided` audit events with channel metadata. Acceptance still does not auto-promote memory; the apply plan is the controlled handoff to an operator or gateway. Agents and operators can still create explicit proposals with `POST /learning/proposals` or MCP `propose_learning`:

```json
{
  "scope": "repo:example/service",
  "proposal_type": "source_refresh",
  "title": "Refresh stale API authentication source",
  "rationale": "Working-memory verification found stale claims used by an authentication task.",
  "target_type": "claim",
  "target_id": "claim-id",
  "confidence": 0.75,
  "created_by": "service-agent"
}
```

`POST /memory/summaries` returns the precomputed hierarchy layer directly:

```json
{
  "scope": "repo:example/service",
  "query": "API authentication middleware dependencies",
  "limit": 10
}
```

`POST /memory/summaries/rebuild` rebuilds deterministic summaries from existing documents and chunks in a scope. It is a backfill operation and requires an approved `approval_id` when `ABRA_APPROVAL_MODE=enforce`.

Example document ingestion payload:

```json
{
  "source_type": "markdown",
  "source_url": "file://examples/knowledge/service.md",
  "title": "Service engineering conventions",
  "scope": "team:example",
  "content": "## Testing\nServices should run contract tests before release when API behavior changes."
}
```

Generic connector overlays can also push signed webhook documents or batches:

```sh
body='{
  "connector_kind": "jira",
  "event_type": "issue.updated",
  "delivery_id": "delivery-123",
  "scope": "team:platform",
  "source_type": "jira",
  "source_url": "https://jira.example/browse/PLAT-1",
  "source_id": "PLAT-1",
  "title": "PLAT-1",
  "content": "PLAT-1 should use Abra for source-cited memory.",
  "authority": "jira-project",
  "authority_score": 0.8
}'
sig="$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$ABRA_WEBHOOK_SECRET" -hex | awk '{print $2}')"
curl -X POST "$ABRA_BASE_URL/ingest/webhooks" \
  -H "x-api-key: $ABRA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -H "x-abra-signature: sha256=$sig" \
  -d "$body"
```

`POST /ingest/webhooks` accepts either one document at the top level or a `documents` array for batches up to 50 items. It still requires API authentication. When `ABRA_WEBHOOK_SECRETS` is configured, the request body must match `x-abra-signature` or `x-hub-signature-256` using HMAC SHA-256.

Example recall payload:

```json
{
  "query": "what should services use before release?",
  "scope": "team:example",
  "limit": 5
}
```

Recall responses include `retrieval_mode`, plus `text_score` and `vector_score` on returned claims and supporting documents. Normal agent paths should return `hybrid`, meaning Abra embedded the query and ranked candidates from both full-text matches and pgvector nearest-neighbor matches. If query embedding fails, Abra can fall back to full-text recall and reports that fallback mode explicitly. Operators can watch `abra_recall_retrieval_mode_total` to detect provider outages or unexpected fallback behavior.

## Local Runtime

1. Copy `examples/env/production.env.example` to `.env.production`.
2. Start Postgres with Docker Compose.
3. Install dependencies.
4. Run the migration.
5. Start the API, MCP server, or worker process.

The default embedding provider is `local`, meaning self-hosted Qwen-compatible neural retrieval: Qwen/Qwen3-Embedding-0.6B for first-stage vectors and Qwen/Qwen3-Reranker-0.6B for optional reranking. Custom providers replace the local defaults by setting `EMBEDDING_PROVIDER=compatible`, `EMBEDDING_BASE_URL`, `EMBEDDING_MODEL`, and `EMBEDDING_DIMENSIONS`; set `RERANKER_PROVIDER` only when the custom provider also exposes a rerank endpoint.

Forgetting a claim marks it `deprecated`. Source re-ingestion will not reactivate a manually forgotten claim; only claims and relations temporarily deprecated by source refresh can be reactivated.

## Environment

```text
DATABASE_URL=postgres://abra:abra@localhost:5433/abra
PORT=18080
NODE_ENV=development
ABRA_API_KEYS=replace-me
ABRA_WEBHOOK_SECRETS=replace-webhook-secret
EMBEDDING_PROVIDER=local
EMBEDDING_BASE_URL=http://host.docker.internal:8080/v1
EMBEDDING_MODEL=text-embeddings-inference
EMBEDDING_DIMENSIONS=1024
RERANKER_PROVIDER=local
RERANKER_BASE_URL=http://host.docker.internal:8081
RERANKER_MODEL=text-embeddings-inference
ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false
REDACT_PII=true
RATE_LIMIT_MAX=120
RATE_LIMIT_WINDOW=1 minute
# OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318
# ABRA_TRACING_SAMPLE_RATIO=0.25
```

`ABRA_API_KEYS` is required when `NODE_ENV=production`. Clients can pass either `Authorization: Bearer <key>` or `x-api-key: <key>`.
Every HTTP response includes `x-request-id` for request correlation.
Keep `RATE_LIMIT_MAX` and `RATE_LIMIT_WINDOW` configured in production. Abra records rate-limit buckets in Postgres when the API is running normally, so limits apply across replicated API pods. A gateway or ingress rate limit is still recommended as defense in depth for public or agent-facing traffic.
Tracing is disabled by default and enabled automatically when an OTLP endpoint is configured. `ABRA_TRACING_SAMPLE_RATIO` must be between `0` and `1`; use `ABRA_TRACING_ENABLED=true` only when you want startup to fail if the endpoint is missing.

## ACL Policy

Abra has a generic ACL decision surface for identity gateways and connector overlays. Operators can write ACL policies with `POST /acl/policies` or MCP `upsert_acl_policy`; in `ABRA_APPROVAL_MODE=enforce`, ACL changes require an approved `acl_change` request and `approval_id`. Policy writes require ops access and record `acl_policy.upserted` audit events with `channel` metadata.

```json
{
  "scope": "team:example",
  "name": "allow-agent-alpha-recall",
  "subject_type": "agent",
  "subject_id": "agent-alpha",
  "effect": "allow",
  "priority": 10,
  "rule": {
    "actions": ["recall"],
    "resource_types": ["claim", "document"],
    "resource_ids": ["*"]
  }
}
```

Gateways can call `POST /acl/decision` or MCP `acl_decision` with `principal_type`, `principal_id`, `action`, `scope`, and optional resource fields. Deny policies win, allow policies permit access, and no match returns `deny` so external identity layers can fail closed.

Simple keys stay backwards compatible and act as admin keys:

```text
ABRA_API_KEYS=replace-me
```

Scoped keys can limit roles and scopes:

```text
ABRA_API_KEYS=reader-token|roles=reader;scopes=team:example,ops-token|roles=ops;scopes=*
```

Supported roles are `admin`, `writer`, `reader`, and `ops`. Scoped keys must provide a `scope` query or payload field on list/read endpoints to avoid cross-scope enumeration.

Audit export is an ops endpoint:

```sh
auth_header="x-api-key: ops-token"
curl -H "$auth_header" \
  "http://localhost:18080/audit/events?scope=team:example&format=ndjson&since=2026-06-16T00:00:00Z"
```

Filters: `scope`, `event_type` or `type`, `target_type`, `since`, `until`, `limit`, and `format=json|ndjson`. All-scope export requires an all-scope `ops` or `admin` key; scoped ops keys must pass an allowed `scope`.

The worker can also push audit events to an HTTP/SIEM sink. It sends `application/x-ndjson`, optionally signs the body with `x-abra-signature: sha256=<hmac>`, and advances a durable Postgres cursor only after the sink returns 2xx:

```text
ABRA_AUDIT_SINK_URL=https://siem.example.internal/abra/audit
ABRA_AUDIT_SINK_TOKEN=replace-with-sink-token
ABRA_AUDIT_SINK_SECRET=replace-with-hmac-secret
ABRA_AUDIT_SINK_SCOPE=team:example
ABRA_AUDIT_SINK_BATCH_SIZE=100
```

Leave `ABRA_AUDIT_SINK_URL` empty to disable push delivery.

Abra stores variable-dimension pgvector embeddings and records the provider, model, and returned dimensions with each chunk and claim. Built-in partial vector indexes cover common dimensions including 768, 1024, 1280, and 1536.

Optional external embeddings:

```text
EMBEDDING_PROVIDER=compatible
EMBEDDING_BASE_URL=https://embedding-provider.example/v1
EMBEDDING_API_KEY=...
EMBEDDING_MODEL=embedding-model
EMBEDDING_DIMENSIONS=1024
RERANKER_PROVIDER=
```

The provider contract is generic: any embedding endpoint that implements the configured embeddings API shape can be used by setting `EMBEDDING_BASE_URL`, `EMBEDDING_API_KEY`, `EMBEDDING_MODEL`, and `EMBEDDING_DIMENSIONS`. Empty API keys are allowed for self-hosted endpoints. Abra does not use an LLM for answer generation; the provider is used to embed chunks, claims, and recall queries for hybrid retrieval. The optional reranker uses `RERANKER_PROVIDER`, `RERANKER_BASE_URL`, `RERANKER_API_KEY`, and `RERANKER_MODEL`. If reranking fails, recall keeps the hybrid retrieval result instead of failing the user query.

## V1 Direction

The v1 production line uses Go for the core service while keeping stable external contracts:

- HTTP API and stateless MCP over `POST /mcp`.
- Postgres with `pgvector` as the system of record and vector index.
- Structured claim, evidence, feedback, and audit records.
- Generic embedding provider configuration.
- Prometheus-compatible API metrics and ingestion job history for operators.
- Optional OpenTelemetry traces for HTTP, MCP, smart-memory, recall, and worker ingestion latency analysis.

Go is the preferred v1 stack because Abra is a long-running self-hosted service with simple HTTP, database, worker, and policy surfaces. A static binary, predictable memory profile, straightforward concurrency, and mature Postgres/observability libraries are a better operational fit than keeping the service runtime tied to a Node dependency tree.

V1 should not add Neo4j by default. The claim graph is evidence-backed and can be represented with relational edges in Postgres. Keeping graph edges, vectors, audit events, and transactional updates in one database reduces the deployment surface and avoids consistency drift. A graph database should only be introduced later if real workloads need deep, high-volume path traversal that Postgres cannot satisfy.

## Agent Policy

Default agent behavior should be conservative:

- Agents may call `policy_plan` to compute the recall queries they should run before a task, before code changes, or after a task.
- Agents may call `recall` automatically before answering questions inside an allowed scope.
- Agents may ingest or remember claims automatically only when durable source metadata is present.
- Claims without a source remain `unverified` and must not be presented as settled fact.
- Source-backed ingestion and claim writes run deterministic contradiction checks for supported policy assertions and open conflicts when incompatible memory is found.
- Agents may challenge claims when a newer or stronger source conflicts with memory.
- Forgetting, broad-scope writes, source authority changes, ACL changes, connector enables, and backfills are risky operations. Abra records approval requests through HTTP and MCP, and `ABRA_APPROVAL_MODE=enforce` makes first-class risky memory endpoints reject requests without a matching approved `approval_id`.
- Keep `ABRA_APPROVAL_MODE=advisory` for permissive local development. Use `enforce` for production agent credentials, and still handle deployment-specific ACL, connector, and backfill gates in an agent gateway or private overlay.
- Recall responses should include citations and uncertainty when claims are inferred, stale, or unverified.

Abra also has stored agent-action policies for runtime decisions that should be durable, auditable, and independent from prompt text. Operators can write them with `POST /agent/policies` or MCP `upsert_agent_policy`; policy writes require ops access, write `agent_policy.upserted` audit events with channel metadata, and require an approved `acl_change` request when approval enforcement is active. Agents or gateways can ask `POST /agent/policy/decision` or MCP `agent_policy_decision` before attempting a risky action.

Operators can register agent runtime profiles with `POST /agent/profiles`. A profile binds a stable `profile_key` and optional `principal_ref` to a default scope, allowed scopes, denied scopes, permissions, and memory preferences. When `policy_plan` or `working_memory_compose` is called with an `agent` matching an active profile key in the requested scope, Abra enforces the profile scope guard and applies memory preferences such as default `limit`, `max_queries`, `token_budget`, and `include_unverified` when the request did not provide an explicit value. Compose responses include the applied `agent_profile` so agents and gateways can audit which runtime defaults shaped the packet. Profile changes require ops access, write `agent_profile.upserted` audit events with channel metadata, and require `acl_change` approval when approval enforcement is active. Denied scopes are durable guardrails for gateways and private overlays, while memory preferences give agents a generic place to store defaults without hardcoding them into prompts.

```json
{
  "scope": "team:example",
  "name": "require-service-agent-review",
  "subject_type": "agent",
  "subject_id": "service-*",
  "effect": "require_review",
  "priority": 10,
  "rule": {
    "actions": ["agent_write", "challenge_claim"],
    "target_types": ["memory_write", "claim"],
    "target_ids": ["team:example*"]
  }
}
```

Effects are `allow`, `deny`, and `require_review`. Deny wins immediately, allow bypasses the global approval-mode fallback for the exact match, and require-review forces an approved request even when the deployment is otherwise running in advisory mode. Use `allow` sparingly; most production agent policies should be `require_review` or `deny`.

## Approval Workflow

Approval records are an operator review surface. In advisory mode they only make risky intent visible and auditable; in enforce mode the supported risky endpoints require the approved request ID:

1. The agent calls `request_approval` or `POST /approvals` with `action`, `scope`, `reason`, optional target fields, and a payload describing the proposed change.
2. The operator lists pending requests with `GET /approvals?scope=...&status=pending`, verifies source evidence, scope, blast radius, and requester identity, then approves or rejects the request.
3. If approved and `ABRA_APPROVAL_MODE=enforce` or a stored agent-action policy returns `require_review`, the caller retries the risky operation with `approval_id`. Supported gates are `agent_write` for `POST /claims`, `forget_claim`, `challenge_claim`, `backfill`, `acl_change`, and `source_authority_change` for trusted source config writes.
4. The operator records the approval ID, operation request ID, and result in incident or change notes.

Do not give autonomous agents all-scope `admin` credentials. Give them scoped writer credentials with approval enforcement enabled, or request-only wrapper tools when a private overlay owns the final operation.

## Auto Ingestion

The OSS surface is generic document ingestion through `POST /ingest/documents` or MCP `ingest_document`, batch ingestion through MCP `ingest_documents`, and signed connector batches through `POST /ingest/webhooks`. Production deployments should automate ingestion outside the request path:

- Poll or subscribe to approved systems such as Confluence, Jira, Git repositories, runbooks, or decision logs.
- Map every source to a stable `source_url`, `source_type`, `scope`, title, and authority level.
- Re-ingest idempotently. When a source changes, missing claims and graph relations are deprecated, still-present claims and relations are reactivated, and source summaries are replaced.
- Preserve ACL and scope metadata before records become available to agents.
- Keep connector-specific auth and normalization in a private overlay or fork.

Worker runs are written to `ingestion_jobs` and exposed through `GET /ingestion/jobs`. Operators and agent gateways can manually enqueue a source with `POST /ingestion/jobs` or MCP `enqueue_ingestion_job`, list jobs with `list_ingestion_jobs`, retry failed/canceled jobs with `POST /ingestion/jobs/:jobId/retry` or MCP `retry_ingestion_job`, and cancel queued/retry jobs with `POST /ingestion/jobs/:jobId/cancel` or MCP `cancel_ingestion_job`. Source configs can be written and listed through both HTTP and MCP (`upsert_source_config`, `list_source_configs`). Source configs also keep the latest success/error timestamps for quick operator checks over HTTP or MCP. Source config changes, including pause/resume status changes, write `source_config.upserted` audit events so operators can review lifecycle changes through `GET /audit/events`.

### Git/local-repo source configs

OSS repo ingestion supports mounted paths through `local_repo` and provider-neutral shallow clone through `git_repo`. Local repo ingestion reads markdown from a mounted path and adds git source identity when `.git` metadata is available. Remote Git ingestion clones or updates the configured repository into `ABRA_GIT_CACHE_DIR` with `ABRA_GIT_CLONE_DEPTH` as the default depth. The runtime image includes `git` and disables interactive credential prompts; private deployments should provide SSH keys, credential helpers, network policy, or secret-mounted remote URLs through the platform layer rather than embedding credentials in prompts.

Set `include_code=true` to add deterministic structural code graph extraction. Code ingestion is opt-in so large repositories do not get indexed accidentally. Code files create chunks, graph relations, and deterministic summaries; they do not create verified natural-language claims from raw source text or comments. The extractor records file, route, package, component, symbol, import, export, dependency, and Go package/symbol relations without calling an LLM.

Core scheduled sources are `markdown`, `local_repo`, and `git_repo`. `markdown` and `local_repo` must point at a local path or `file://` URL visible to the worker. `git_repo` accepts `base_url` or `config.repository_url` / `config.remote_url` / `config.git_remote_url`, plus optional `branch`, `git_depth`, `provider`, and `project_path`. Private connector overlays are still the right place for Confluence, Jira, Slack, provider-specific webhook diffing, ACL normalization, and token rotation; after normalization they can push documents through `POST /ingest/documents` / `POST /ingest/webhooks`.

Non-core source types such as `jira`, `confluence`, or deployment-specific names may still be stored as overlay source configs, but the OSS worker will not schedule them. The overlay owns discovery, credentials, ACL normalization, diffing, and retries; Abra owns the durable memory contract after normalized documents arrive.

```json
{
  "name": "Frontend docs",
  "source_type": "local_repo",
  "scope": "team:example",
  "base_url": "file:///mnt/repos/service",
  "connector_kind": "generic",
  "authority": "team-convention",
  "authority_score": 0.8,
  "config": {
    "root": "/mnt/repos/service",
    "include": ["README.md", "docs/**/*.md"],
    "exclude": ["docs/private/**"],
    "include_code": true,
    "code_include": ["package.json", "src/**/*.ts", "src/**/*.tsx"],
    "code_exclude": ["src/**/*.test.ts", "src/**/*.test.tsx"],
    "repository_url": "git@github.com:example-org/service-app.git",
    "branch": "main",
    "commit": "0123456789abcdef",
    "provider": "github",
    "project_path": "example-org/service-app"
  },
  "metadata": {
    "owner": "example-team",
    "acl_source": "github-team:example"
  }
}
```

For GitLab, use the same shape with `provider: "gitlab"` and `repository_url` such as `git@gitlab.example.com:platform/service.git`. The OSS worker handles clone/fetch when the runtime environment can authenticate non-interactively. Private overlays are still responsible for webhook handling, token rotation, ACL normalization, and provider-specific repository discovery.

Remote Git example:

```json
{
  "name": "Frontend remote repo",
  "source_type": "git_repo",
  "scope": "team:example",
  "base_url": "https://bitbucket.org/example-org/service-app.git",
  "connector_kind": "git",
  "authority": "source-repo",
  "authority_score": 0.8,
  "config": {
    "branch": "main",
    "git_depth": 1,
    "include": ["README.md", "docs/**/*.md"],
    "include_code": true,
    "code_include": ["package.json", "src/**/*.ts", "src/**/*.tsx"],
    "provider": "bitbucket",
    "project_path": "example-org/service-app"
  },
  "metadata": {
    "owner": "example-team"
  }
}
```

Abra stores normalized fields as document metadata (`git_remote_url`, `git_ref`, `git_revision`, `git_path`, `git_provider`, `git_project_path`, `git_cache_key`) and builds stable source URLs for GitHub, GitLab, and Bitbucket files when enough identity is present.

## Scope

Abra is open source and self-hostable. The core project stays generic: CLI, API, worker, Postgres, pgvector, MCP, ingestion, source configs, metrics, approval requests and enforcement for core memory operations, audit export, and release gates.

Deployment-specific identity, ACL sync, private connector automation, SIEM routing, and managed operations should be added through extensions or overlays without making the OSS runtime unusable.

## V1 Roadmap

1. Harden the Go HTTP MCP endpoint against a full MCP client compatibility matrix.
2. Add connector extension hooks for external source systems.
3. Add richer graph relation types for supersedes, contradicts, derives-from, and duplicates relationships.
4. Extend stored agent policy enforcement across auto-write, source authority, ACL filtering, and approval gates.
5. Keep the full release gate aligned with recall, graph, policy, dogfood, and performance checks.
6. Improve CLI-first operator workflows for restore drills, backfills, and approval history.

## Extension Path

Keep OSS Abra generic. Add deployment-specific behavior in an extension, private connector, or overlay:

- internal agent UI auth
- Confluence/Jira/source-system connector automation
- Slack thread source URLs
- team ACL mapping
- source authority rules
- Helm/Vault deployment

## License

Apache-2.0
