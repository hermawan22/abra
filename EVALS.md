# Abra Evaluation Plan

Abra needs two kinds of verification: runtime smoke tests that prove endpoints work, and quality evaluations that prove the brain is useful and safe for agents. This plan defines the v1 eval suite target without pretending every check is automated today.

## Evaluation Goals

- Recall returns the right sourced claims for a task.
- Recall avoids unsafe claims when evidence is missing, stale, challenged, or outside scope.
- Ingestion preserves source lineage from documents through chunks, claims, entities, and relations.
- Code ingestion does not promote raw code, comments, or syntax-shaped text into trusted claims; code knowledge is represented through graph context and deterministic summaries.
- Ingestion detects supported deterministic claim contradictions and opens reviewable conflicts before agents can treat the memory as safe.
- Graph extraction adds useful context without inventing unsupported relationships.
- Policy planning asks agents to consult memory at the right time.
- Retrieval planning selects the memory strategy, stages, and query budget before hybrid recall work runs.
- Working-memory composition returns a compact, source-backed task packet with summaries, facts, graph context, impact map, validation plan, risks, verification, stored agent-policy decisions, agent decision, budgeted context window, and next steps.
- Evidence verification marks packets as strong, partial, weak, or unsafe before agents use them, including retrieval-quality scoring from rank, text, and vector signals.
- Self-learning automatically queues actionable reviewable proposals from working-memory composition, deduplicates repeated and concurrent pending proposals, and never promotes them to trusted memory without a follow-up workflow.
- Embedding provider changes do not silently degrade answer quality.

## Eval Tiers

### Tier 0: Smoke

Purpose: prove the deployed service is alive and the main contracts work.

Current command:

```sh
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run smoke:selfhost
```

Required coverage:

- auth rejection for protected endpoints
- readiness
- metrics endpoint shape
- source config write through HTTP and MCP
- document ingestion through HTTP and MCP, including MCP batch ingestion
- signed webhook ingestion
- recall
- policy planning
- working-memory composition
- memory health scorecard
- hierarchical summaries and approved summary rebuild
- ACL policy write/list/audit and decision checks through HTTP and MCP
- stored agent-action policy write/list/audit and decision checks through HTTP and MCP
- configurable agent profile write/list/audit checks through HTTP and MCP, including profile-applied working-memory defaults
- MCP `initialize`, `tools/list`, `tools/call`, resource discovery/read, and prompt discovery/get
- MCP normalized document ingestion writes source-backed memory with chunks and claims
- MCP source config upsert/list and ingestion job enqueue/list checks, including source-config audit events
- MCP learning proposal/decision returns an apply plan and writes auditable `learning.proposed`/`learning.decided` events
- readiness exposes tracing status, and the API/worker boot with tracing disabled by default
- ingestion jobs response shape through HTTP and MCP

### Tier 1: Deterministic Local Quality

Purpose: use the deterministic local embedding provider and fixture documents to catch regressions without external services.

Current command:

```sh
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run eval:tier1
```

The target deployment must run with `EMBEDDING_PROVIDER=local` for deterministic recall checks. The command fails fast through a JSON report when the runtime is not using local embeddings. For non-deterministic staging probes only, set `ABRA_TIER1_ALLOW_NONLOCAL=1`.

Dataset shape:

- small markdown fixtures seeded through the public HTTP API at run time
- unique source URLs and scopes per run
- expected claims and citations
- negative queries that must not return cross-scope facts

Assertions:

- top hybrid recall result contains the expected claim, reports `retrieval_mode: hybrid`, and includes text/vector score components for ranking explainability
- every result has a source URL when status is `verified`
- manually forgotten claims do not return as trusted memory
- policy planning returns scoped recall queries for a coding hook
- graph entities and relations contain the expected fixture relationship
- working-memory packets include summaries, facts, supporting documents, graph context, impact map, validation plan, grouped evidence, risks, budgeted prompt-ready context, suggested steps, and stats
- working-memory packets include retrieval planner output with intent-specific coverage targets, retrieval trace timing/status, retrieval-coverage verification, retrieval-quality verification, degraded retrieval warning accounting, graph warning accounting with relation IDs, deterministic verification reports, relation IDs in graph context, persisted relation-conflict checks, relation-filtered conflict lookup, active relation-conflict surfacing for high-signal graph contradictions, conflict-review routing that avoids approval bypass, stored agent-policy decisions, and deterministic agent decision gates
- working-memory packets include scoped memory health signals, health-signal stats, and prompt-ready health gate context; critical health blocks autonomous agent work
- repeated and concurrent working-memory composition reuses/coalesces a short-lived scoped health snapshot without sharing mutable response state
- working-memory packets include learning suggestions, including low-confidence retrieval repair suggestions, and proposal lifecycle create/list/decide works
- memory health reports zero duplicate pending learning-proposal groups after repeated or concurrent compose calls
- memory health returns structured signals with stable code, category, severity, message, action, count, and score impact for agent/control-plane gating
- metrics expose bounded working-memory health status and signal counters for operator alerting without scope, task, query, or principal labels
- automatic claim-conflict detection flags contradictory policy assertions and the conflict lifecycle can resolve or suppress them
- source re-ingestion deprecates removed claims and reactivates claims still present in the refreshed source
- source re-ingestion deprecates removed graph relations, reactivates still-present relations, and replaces source summaries
- code documents create chunks, graph context, and summaries without creating verified natural-language claims from code text
- memory health reports zero trusted claims from code documents after code ingestion and cleanup
- memory health reports zero stale running ingestion jobs and zero retrying ingestion jobs after dogfood ingestion
- summary rebuild backfills existing documents behind approval enforcement
- source config pause/resume changes are auditable through `source_config.upserted` audit events
- cross-scope recall does not leak the positive fixture claim
- cross-scope working-memory composition does not leak the positive fixture context
- working-memory latency remains under `ABRA_TIER1_MEMORY_MAX_MS`, defaulting to `2500ms`

Current output:

- prints a JSON summary to stdout
- returns exit code `0` when every check passes
- returns nonzero when any check fails

Current limitations:

- graph conflict quality is limited to deterministic fixtures and should be expanded with sampled production-like sources
- graph quality is checked with exact fixture expectations, not sampled precision
- the command assumes an already running self-hosted API and does not start Docker Compose itself

### Tier 2: Provider Quality Baseline

Purpose: compare embedding providers before production rollout with the same JSONL fixtures used by golden evals plus staging-specific representative documents.

```sh
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run eval:provider
ABRA_PROVIDER_EXPECT=compatible ABRA_PROVIDER_DATASET=/path/to/staging-provider.jsonl npm run eval:provider
```

The default dataset is deterministic and can run on local embeddings. For production provider promotion, run `eval:provider` against a staging deployment configured with the target compatible embedding provider and a representative staging dataset. Use `ABRA_PROVIDER_EXPECT` to fail fast when the runtime is not using the intended provider.

The provider benchmark JSON report tracks:

- hybrid recall hit rate at 1, 3, and 5
- citation coverage
- scope leakage rate
- verified claims without citation
- recall p50/p95/p99 latency
- working-memory p50/p95/p99 latency
- working-memory retrieval coverage completeness
- working-memory verification verdict distribution
- agent decision distribution

Thresholds:

- `ABRA_PROVIDER_MIN_HIT_RATE_AT_3=1`
- `ABRA_PROVIDER_MIN_CITATION_COVERAGE=1`
- `ABRA_PROVIDER_MAX_LEAKAGE_COUNT=0`
- `ABRA_PROVIDER_RECALL_P95_MS=750`
- `ABRA_PROVIDER_MEMORY_P95_MS=3500`

Promotion rule:

- no critical scope leaks
- no verified claim without citation
- recall hit rate does not regress beyond the agreed threshold
- latency remains within the operator SLO
- working-memory decisions remain inside the accepted release distribution

### Performance Gate

Purpose: catch recall and working-memory latency regressions on the deployed API with a repeatable fixture workload.

Current command:

```sh
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run perf:local
```

Run this against an isolated candidate deployment or raise `RATE_LIMIT_MAX` for the eval window; this gate measures recall and working-memory backend latency, not public-edge throttling behavior.

Defaults:

- `ABRA_PERF_DOCS=40`
- `ABRA_PERF_ITERATIONS=30`
- `ABRA_PERF_CONCURRENCY=4`
- `ABRA_PERF_CAPACITY_ITERATIONS=60`
- `ABRA_PERF_CAPACITY_CONCURRENCY=8`
- `ABRA_PERF_SOAK_SECONDS=0`
- `ABRA_PERF_SOAK_CONCURRENCY=8`
- `ABRA_PERF_RECALL_P95_MS=750`
- `ABRA_PERF_MEMORY_P95_MS=2500`
- `ABRA_PERF_MEMORY_CAPACITY_P95_MS=5000`
- `ABRA_PERF_MEMORY_SOAK_P95_MS=5000`
- `ABRA_PERF_MAX_FAILURE_RATE=0`

Assertions:

- fixture ingestion creates enough claims and chunks
- hybrid recall returns claims and supporting documents under the p95 threshold
- working-memory composition returns summaries, facts, supporting documents, evidence, prompt-ready context, and stats under the p95 threshold
- working-memory composition returns retrieval plans, retrieval traces with memory-health cache status, retrieval and graph warning accounting, verification reports, and agent decisions under the p95 threshold
- working-memory composition returns learning suggestions and persists actionable suggestions under the p95 threshold
- working-memory capacity probe reports p50/p95/p99 latency, failure rate, and memory-health cache status distribution under higher concurrency
- optional working-memory soak probe, enabled with `ABRA_PERF_SOAK_SECONDS`, reports sustained throughput, p50/p95/p99 latency, failure rate, and memory-health cache status distribution
- benchmark output is JSON so CI or release tooling can archive it

### Dogfood Gate

Purpose: prove Abra can build a useful brain about itself before claiming general agent memory quality.

Current command:

```sh
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run eval:dogfood
```

The eval process defaults to the current checkout path. The API worker must be able to read the same source root. For direct binary runs this usually works as-is; for container layouts, mount the checkout read-only and set `ABRA_DOGFOOD_SOURCE_ROOT` to the mounted path visible to the worker. Set `ABRA_DOGFOOD_REPO_PATH` only when the eval process itself is launched outside the checkout. The gate pauses its source config after success by default; set `ABRA_DOGFOOD_KEEP_SOURCE_ACTIVE=1` only when that worker can continuously read the configured source root.

Assertions:

- source config registration works through the same approval path as production source authority changes
- worker local-repo ingestion sees the Abra docs and code source set
- summary rebuild works through the same approved backfill path
- working-memory composition for `repo:abra` returns summaries, source-backed facts or documents, graph relations, an impact map, and a validation plan
- graph relation listing includes Go code intelligence from package declarations, imports, functions, methods, constants, variables, or types

This gate is intentionally stricter than endpoint smoke tests. A green dogfood run proves the self-hosted service can ingest its own architecture, code, policy, graph, and working-memory paths into a usable agent packet.

### Tier 3: Agent Workflow Eval

Purpose: prove an agent can use Abra before, during, and after work.

The current `npm run eval:tier23` command verifies policy-plan coverage, working-memory packet quality, working-memory scope isolation, approval request state transitions, stored agent-action policy review gates, scoped risky-action probes, and positive-path approved risky operations. Approval enforcement probes are skipped in advisory deployments and become hard failures when enforcement is expected. Run production gates against a deployment running `ABRA_APPROVAL_MODE=enforce` with:

```sh
ABRA_TIER23_EXPECT_APPROVAL_ENFORCEMENT=1 ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run eval:tier23
```

Scenarios:

- before-task recall for feature work
- before-code recall for repo conventions
- working-memory composition before agent work
- after-task memory proposal
- reviewable learning proposal lifecycle
- challenge a stale claim with newer source evidence
- reject broad-scope writes without approval
- answer with uncertainty when only unverified memory exists

Pass criteria:

- agent calls `policy_plan` before task or code hooks
- agent runs the suggested recall queries
- agent receives an evidence-backed working-memory packet before implementation
- agent checks the working-memory `agent_decision` before using the packet for autonomous changes
- agent workflow trace proves `policy_plan -> planned recall -> working_memory_compose -> agent_decision -> after_task policy_plan`
- working-memory `agent_policy_decisions` expose stored policy outcomes for standard risky actions
- MCP `acl_decision` exposes the same fail-closed ACL policy outcome as the HTTP decision endpoint
- MCP `agent_policy_decision` exposes the same stored policy outcome as the HTTP decision endpoint
- MCP source and ingestion tools expose the same source lifecycle and job queue controls as the HTTP control plane
- stored `require_review` policies block matching agent writes until an approved request is supplied
- final answer cites relevant source-backed claims
- risky write or forget operations are proposed, not silently applied
- enforced deployments reject unapproved agent writes and forgets without skipped checks
- actionable learning proposals are automatically queued for review, repeated or concurrent compose calls reuse the same pending proposal, and they do not automatically become trusted claims

## Golden Dataset Runner

Use `npm run eval:golden` to run a plain JSONL dataset against a deployed Abra API:

```sh
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run eval:golden
ABRA_GOLDEN_DATASET=examples/evals/golden.jsonl npm run eval:golden
```

The default dataset is `examples/evals/golden.jsonl`. Each run replaces `{{run_id}}` placeholders with a unique suffix so fixture scopes and source URLs do not collide.

The runner accepts `document` records for self-contained fixtures and `case` records for recall and working-memory assertions:

```json
{"type":"document","id":"service-fixture","scope":"team:example-{{run_id}}","source_type":"markdown","source_url":"file://service-{{run_id}}.md","title":"Service Fixture","content":"- Services should run contract tests before release."}
{"type":"case","id":"service-contract-tests","scope":"team:example-{{run_id}}","query":"what should services run before release?","expected_claim_contains":"contract tests","expected_source_url":"file://service-{{run_id}}.md","min_rank":3,"memory_task":"implement service contract test coverage","expected_agent_decision":["proceed","caution"]}
{"type":"case","id":"no-cross-scope","scope":"team:isolated-{{run_id}}","query":"what should services run before release?","must_not_contain":"contract tests"}
```

Each `case` should include:

- `id`
- `scope`
- `query`
- expected claim text or forbidden text
- expected source URL when positive
- accepted rank threshold
- optional `memory_task`, hook/files/language, expected memory text, and accepted `agent_decision`

The JSON report includes recall hit rate at 1/3/5, citation failures, leakage count, memory case count, and agent decision distribution.

## Metrics To Track

- Recall hit rate at 1, 3, and 5.
- Citation precision.
- Cross-scope leakage count.
- Unverified claim usage count.
- Stale or deprecated claim usage count.
- Policy-plan query usefulness.
- Working-memory packet completeness.
- Working-memory impact-map coverage for files, packages, routes, symbols, and graph entities.
- Working-memory validation-plan coverage for inferred commands and review gates.
- Working-memory stored-policy decision coverage.
- Working-memory verification verdict distribution.
- Working-memory agent decision distribution.
- Learning proposal creation, decision, and applied/rejected rates.
- Working-memory latency.
- Graph relation precision on sampled fixtures.
- Source refresh graph and summary reconciliation counts.
- Ingestion success rate.
- Ingestion latency and recall latency.
- Cost per source refresh for external embedding providers.

## Release Gate

For every production release:

```sh
ABRA_BASE_URL=http://localhost:18080 ABRA_API_TOKEN=replace-with-token npm run release:gate
```

The default `full` release gate runs script checks, Go tests, Docker build, Compose render, Helm render, Tier 0 smoke, Tier 1 deterministic quality, golden evals, provider-quality benchmark, Tier 2/3 agent workflow evals, enforced approval-mode Tier 2/3 probes when the gate manages the local stack, dogfood, and local performance/capacity. Use `ABRA_RELEASE_PROFILE=quick` only for developer sanity checks; it skips provider/Tier 2/3, enforced approval-mode probes, dogfood, and golden evals and uses a reduced performance fixture. The full gate uses an isolated dogfood release scope by default; set `ABRA_DOGFOOD_SCOPE` only when intentionally validating an existing scope. Set `ABRA_RELEASE_MANAGE_STACK=1` for local Docker Compose releases; this raises the local rate limit, shortens the worker interval for eval responsiveness, prepares a worker-visible dogfood source copy, and runs a second Tier 2/3 pass under `ABRA_APPROVAL_MODE=enforce`. When the stack is already running in Docker Compose, set `ABRA_RELEASE_PREPARE_DOGFOOD_SOURCE=1` to copy the checkout into the worker container before dogfood. Set `ABRA_RELEASE_APPROVAL_ENFORCEMENT_GATE=1` only when the target stack is already running in enforced mode or the release runner is allowed to recreate it.

For releases that change embedding configuration or agent policy behavior, also run the targeted exploratory gates and attach their JSON summaries to the release notes:

1. If embedding config changed, run `eval:provider` against a representative staging dataset for the target provider.
2. If policy behavior changed, run Tier 3 agent workflow eval.
3. Attach `release:gate`, Tier 2, and Tier 3 result summaries to release notes.

## Current Automation Gap

The repository currently has the Tier 0 smoke suite, the Tier 1 deterministic local quality command, a provider-quality benchmark command, a focused Tier 2/Tier 3 command, an enforced approval-mode Tier 2/Tier 3 gate, a dogfood gate, a local performance/capacity gate with optional soak mode, and a release-gate runner that aggregates the main checks into one JSON report. The provider benchmark materially verifies provider identity, hit rate, citation coverage, leakage count, recall latency, working-memory latency, verdict distribution, and agent decision distribution against a JSONL dataset. The Tier 2/Tier 3 command materially verifies fixture recall, citation, scope isolation, policy-plan workflow coverage, explicit agent workflow trace order, working-memory packet completeness and latency, approval request lifecycle, stored agent-action policy review gates, risky-action rejection in enforced deployments, and approved write/forget positive paths. The dogfood gate verifies that Abra can ingest and compose useful working memory about its own source tree. The performance gate verifies p95/p99 recall and working-memory latency, failure rate, memory-health cache behavior, and opt-in sustained throughput on seeded fixtures. It still needs real staging datasets to prove provider quality for a specific production corpus.

Do not market a deployment as quality-validated only because endpoint smoke tests pass. Smoke proves availability. Eval proves usefulness and safety.
