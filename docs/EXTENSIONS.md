# Extension Guide

Abra's core runtime is intentionally provider-neutral. Extensions should adapt source systems, identity systems, and compliance workflows into Abra's public contracts instead of adding organization-specific behavior to the core service.

## Connector Pattern

1. Discover source records in the external system.
2. Normalize each record into a stable `source_url`, `source_type`, `scope`, `title`, `content`, authority, and metadata.
3. Preserve source ACL and ownership metadata for the deployment's gateway or overlay.
4. Push normalized documents through `POST /ingest/documents`, `POST /ingest/webhooks`, or MCP `ingest_documents`.
5. Re-ingest idempotently when records change.
6. Keep connector cursors, credentials, webhook state, and retry policy outside Abra core.

## Source Config Pattern

Core scheduled source types are:

- `markdown`
- `local_repo`
- `git_repo`

Deployment overlays may still store other source configs, such as `jira`, `confluence`, or `drive`, through HTTP or MCP. The core worker does not schedule those source types; the overlay owns polling, webhooks, ACL normalization, and retry behavior.

## Webhook Pattern

Use signed webhook ingestion when a connector can push batches:

```http
POST /ingest/webhooks
x-abra-signature: sha256=<hmac>
x-api-key: <api-key>
```

Set `ABRA_WEBHOOK_SECRETS` and include connector metadata such as `connector_kind`, event type, source authority, and source updated time.

## ACL And Policy Pattern

Identity gateways can call:

- `POST /acl/decision`
- MCP `acl_decision`
- `POST /agent/policy/decision`
- MCP `agent_policy_decision`

Treat missing matches as deny in the gateway. Use approval enforcement for ACL changes, source authority changes, broad memory writes, forget operations, and connector enablement.

## Provider Boundary

Abra needs an embedding provider, not a generation model. Any provider may be used when it implements the configured embedding request/response shape and returns vectors matching `EMBEDDING_DIMENSIONS`.

Do not hardcode provider-specific prompts, credentials, or source-system behavior into the OSS runtime.
