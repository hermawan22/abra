# Extension Guide

Abra's core runtime is intentionally provider-neutral. Extensions should adapt source systems, identity systems, and compliance workflows into Abra's public contracts instead of adding organization-specific behavior to the core service.

## Connector Pattern

1. Discover source records in the external system.
2. Normalize each record into a stable `source_url`, `source_type`, `scope`, `title`, `content`, authority, and metadata.
3. Preserve source ACL and ownership metadata for the deployment's gateway or overlay.
4. Push normalized documents through `POST /ingest/documents`, `POST /ingest/documents/batch`, `POST /ingest/webhooks`, or MCP `ingest_documents`.
5. Re-ingest idempotently when records change.
6. Keep connector cursors, credentials, webhook state, and retry policy outside Abra core.

Production connectors should run as scheduled sources, signed webhook producers,
or connector-owned batch jobs. Do not put source-system polling, credentials, or
vendor-specific retry loops in the OSS runtime.

## Source Config Pattern

Core scheduled source types are:

- `markdown`
- `local_repo`
- `git_repo`
- `mcp`

The `mcp` source type lets Abra call an existing HTTP MCP server as a source adapter. Configure `base_url` or `config.server_url`, `config.tool`, optional `config.arguments`, and optional secret references such as `config.bearer_token_env` and `config.header_env`. Validate the export contract before enabling a source with CLI `abra source mcp --dry-run`, HTTP `POST /sources/configs/validate`, or MCP `validate_mcp_source`. The configured tool must return normalized Abra documents as JSON, either in `structuredContent` or a text content item:

```json
{
  "documents": [
    {
      "source_type": "confluence",
      "source_url": "https://confluence.example/wiki/pages/123",
      "source_id": "123",
      "title": "Platform Architecture Decision",
      "scope": "team:platform",
      "content": "Decision text in markdown or plain text",
      "source_updated_at": "2026-06-21T10:00:00Z",
      "metadata": {
        "space": "ENG",
        "acl_groups": ["platform"]
      }
    }
  ]
}
```

Deployment overlays may still store other source configs, such as `jira`, `confluence`, or `drive`, through HTTP or MCP. The core worker does not schedule those vendor-specific source types directly; use `mcp` when an internal MCP server can export normalized documents, or let the overlay own polling, webhooks, ACL normalization, and retry behavior before pushing into Abra. In enforced approval mode, active source config writes and resume use `connector_enable` unless the authority is trusted, in which case the gate is `source_authority_change`; explicit source backfills use `backfill`.

## Webhook Pattern

Use signed webhook ingestion when a connector can push batches:

```http
POST /ingest/webhooks
x-abra-signature: sha256=<hmac>
x-api-key: <api-key>
```

Set `ABRA_WEBHOOK_SECRETS` and include connector metadata such as `connector_kind`, event type, source authority, and source updated time.

Webhook ingestion is asynchronous. A successful response means Abra has accepted durable ingestion jobs, not that embeddings are already written. Use the returned `ingestion_job_id` values, `abra jobs --scope <scope>`, or `GET /ingestion/jobs` to wait for `succeeded` before expecting recall or `working_memory_compose` to include the new content. For larger connector refreshes, prefer `POST /ingest/documents/batch` or MCP `ingest_documents` from the connector job instead of many single-document requests.

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
