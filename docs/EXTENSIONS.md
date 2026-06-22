# Extension Guide

Abra's core runtime is intentionally provider-neutral. Extensions should adapt source systems, identity systems, and compliance workflows into Abra's public contracts instead of adding organization-specific behavior to the core service.

## Connector Pattern

Abra uses a lightweight connector model:

- Abra owns source configs, ingestion jobs, source authority, approval gates,
  audit events, and the durable normalized-memory contract.
- A user-owned MCP exporter or private overlay owns vendor OAuth, refresh
  tokens, cursors, source webhooks, vendor retries, source-specific ACL
  normalization, and any system-specific discovery logic.
- `abra source mcp`, HTTP source-config endpoints, and MCP source-config tools
  register, validate, enable, refresh, pause, resume, and inspect ingestion after
  the exporter can return normalized Abra documents.

End-to-end connector onboarding:

1. Build an MCP tool or overlay job that can read the vendor system with
   user-owned credentials.
2. Normalize each record into Abra document fields, including stable
   `source_url`, `source_type`, `scope`, title, content, authority, update time,
   metadata, and ACL hints needed by the deployment gateway.
3. Inspect an MCP exporter when the tool name is unknown:
   `abra connectors mcp inspect --scope <scope> --mcp-url <url>` or MCP
   `inspect_connector_source`.
4. Validate the export without creating a source config:
   `abra connectors mcp validate --scope <scope> --mcp-url <url> --tool <tool>`,
   `POST /sources/configs/validate`, or MCP `validate_connector_source`.
5. Use `abra connectors mcp template --scope <scope> --output connector.json`
   to create a repeatable CLI-only onboarding manifest.
6. Add the connector with `abra connectors mcp add --manifest connector.json --wait --verify`
   when you want the guided headless flow: inspect, validate, then register.
7. Register and enable the source directly with `abra connectors mcp register ... --schedule ... --wait --verify`,
   HTTP `POST /sources/configs`, or MCP `upsert_connector_source`.
8. For repeatable onboarding, store the URL, tool, arguments, env refs,
   schedule, authority, and verification query in a manifest such as
   `examples/connectors/mcp-knowledge-base.connector.json`.
9. Watch `abra connectors status`, `abra connectors logs`, `abra jobs`, or
   `GET /ingestion/jobs` until jobs succeed, then verify recall or
   `working_memory_compose` returns source-backed context.

Connector implementation checklist:

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

The `mcp` source type lets Abra call an existing HTTP MCP server as a source adapter. Configure `base_url` or `config.server_url`, `config.tool`, optional `config.arguments`, and optional secret references such as `config.bearer_token_env` and `config.header_env`. Those secret references point to runtime environment variables; Abra should store names, not vendor tokens. Validate the export contract before enabling a source with CLI `abra source mcp --dry-run`, HTTP `POST /sources/configs/validate`, or MCP `validate_mcp_source`. The configured tool must return normalized Abra documents as JSON, either in `structuredContent` or a text content item:

```json
{
  "documents": [
    {
      "source_type": "markdown",
      "source_url": "https://kb.example.com/pages/123",
      "source_id": "123",
      "title": "Architecture Decision",
      "scope": "team:docs",
      "content": "Decision text in markdown or plain text",
      "source_updated_at": "2026-06-21T10:00:00Z",
      "metadata": {
        "collection": "docs",
        "acl_groups": ["docs"],
        "allowed_principals": ["group:docs"],
        "owner": "team:docs"
      }
    }
  ]
}
```

Abra preserves document metadata from MCP exports, including ACL hints such as
`acl_groups`, `allowed_principals`, `denied_groups`, and `owner`. Treat this as
minimal passthrough: the source connector or deployment gateway still owns vendor
group resolution and deny-by-default enforcement before showing recall output to a
principal.

Deployment overlays may still store other source configs, such as `jira`, `confluence`, or `drive`, through HTTP or MCP. The core worker does not schedule those vendor-specific source types directly; use `mcp` when an internal MCP server can export normalized documents, or let the overlay own polling, webhooks, OAuth, cursors, ACL normalization, and retry behavior before pushing into Abra. In enforced approval mode, active source config writes and resume use `connector_enable` unless the authority is trusted, in which case the gate is `source_authority_change`; explicit source backfills use `backfill`.

### Native Vs Overlay Responsibilities

| Responsibility | Native Abra | User-owned MCP/exporter or overlay |
| --- | --- | --- |
| Source config lifecycle | `abra source mcp`, HTTP `/sources/configs`, MCP `upsert_source_config`, pause/resume, status, logs | Chooses which vendor collections, projects, spaces, or repositories should be exposed |
| Validation | Calls the exporter and validates normalized document shape with dry-run, HTTP validation, or MCP `validate_mcp_source` | Returns only normalized Abra documents and actionable errors |
| Scheduling and jobs | Schedules due `markdown`, `local_repo`, `git_repo`, and `mcp` sources; records `ingestion_jobs` | Maintains vendor cursors, delta windows, webhook delivery state, and source-specific retries |
| Governance | Approval gates, source authority, audit events, job history, recall, working memory | Vendor OAuth, tenant policy, ACL/group mapping, and gateway-side deny-by-default filtering |
| Vendor behavior | Provider-neutral ingestion and memory contracts | Jira, Confluence, Slack, Drive, Git provider, or internal-system API details |

## Webhook Pattern

Use signed webhook ingestion when a connector can push batches:

```http
POST /ingest/webhooks
x-abra-signature: sha256=<hmac>
x-api-key: <api-key>
```

Set `ABRA_WEBHOOK_SECRETS` and include connector metadata such as `connector_kind`, event type, source authority, and source updated time. Use `abra connectors webhook sample`, `abra connectors webhook sign`, and `abra connectors webhook test` to generate a normalized payload, compute the HMAC SHA-256 signature, and verify an overlay can push to Abra before wiring the real vendor webhook.

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
