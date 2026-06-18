# Threat Model

Abra is a self-hosted memory control plane for AI agents. It stores source-derived text, embeddings, claims, graph context, approvals, policies, audit events, and ingestion state.

## Assets

- Source-derived documents and snippets.
- Embeddings and graph records.
- Claim status, confidence, source lineage, and scope metadata.
- API keys and webhook secrets.
- Approval requests and audit events.
- Connector configuration and ingestion job history.

## Trust Boundaries

- Agent runtimes call Abra through HTTP or MCP.
- Operators use the CLI, MCP, and API with scoped credentials.
- Workers read source configs and write ingestion results.
- External embedding providers receive text for embedding, not answer-generation prompts.
- Deployment overlays own private connector credentials, identity sync, ACL normalization, and organization-specific compliance workflows.

## Primary Risks

- Unauthorized memory reads or writes.
- Agents treating unverified, stale, challenged, or scoped-out memory as truth.
- Broad-scope writes, forget operations, source authority changes, ACL changes, and backfills without review.
- Connector leaks through source URLs, metadata, or prompts.
- Embedding provider outage or fallback degrading recall quality.
- Database exposure, because it contains source-derived snippets and embeddings.
- Audit or approval history loss during incident response.

## Built-In Controls

- Production requires API keys.
- Production blocks local deterministic embeddings by default.
- Core risky memory operations support approval enforcement.
- Claims retain source, evidence, status, confidence, scope, and freshness metadata.
- Recall and working-memory responses expose verification and health signals.
- Rate limits are shared through Postgres across API replicas.
- Readiness fails when required migrations are missing.
- Metrics and optional tracing avoid raw prompts, scopes, principals, and tokens as high-cardinality labels.
- Audit events record write-side memory changes.

## Deployment Responsibilities

- Keep Abra behind an internal load balancer or service mesh.
- Use managed Postgres with backups for serious production use.
- Store API keys, webhook secrets, embedding keys, and connector credentials in a platform secret store.
- Use scoped API keys for agents and reserve all-scope admin credentials for operators.
- Enforce organization-specific identity and source ACLs through gateways or overlays.
- Run restore drills, reindex procedures, smoke checks, and release gates before production rollout.
- Monitor retrieval fallback modes, low-confidence working-memory packets, failed ingestion jobs, and approval activity.
