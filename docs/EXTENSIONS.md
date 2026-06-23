# Abra Extensions

Abra core stays provider-neutral. Extensions are adapters that bring external
systems into Abra without adding source-specific logic to the main binary.

## Boundary

Core owns:

- normalized document validation;
- transform, chunking, embedding, reranking, and extraction;
- citations, graph relations, conflict checks, approvals, and decision gates;
- MCP brain contracts, operator CLI contracts, and internal HTTP transport.

Extensions own:

- source authentication and token refresh;
- source-specific cursors, pagination, retries, and rate limits;
- ACL or group mapping before content reaches Abra;
- event subscriptions and webhook delivery;
- organization-specific policies and deployment overlays.

If a feature names a source system, workspace policy, private approval process, or
custom business model, it belongs outside core unless it can be expressed as a
generic contract.

## Normalized Document Contract

Every extension must emit source-backed documents:

```json
{
  "source_type": "markdown",
  "source_url": "https://kb.example.com/pages/123",
  "title": "Deploying service-a",
  "scope": "team:platform",
  "content": "The source-backed document body.",
  "source_authority": "kb",
  "source_updated_at": "2026-06-22T10:00:00Z",
  "metadata": {
    "connector": "example-kb",
    "workspace": "platform"
  }
}
```

Required fields are `source_type`, `source_url`, `title`, `scope`, and
`content`. Secrets, bearer tokens, private API keys, and raw source-system
exports must not be stored in documents.

Inspect the contract:

```sh
abra plugin contract
```

## MCP Exporter

Use this when an existing internal service or agent platform can expose an MCP
tool that returns normalized Abra documents.

Validate first:

```sh
abra connect mcp https://mcp.example.com/mcp \
  --tool export_documents \
  --scope team:docs \
  --dry-run
```

Register only after the dry run validates:

```sh
abra connect mcp https://mcp.example.com/mcp \
  --tool export_documents \
  --scope team:docs \
  --schedule "@every 10m" \
  --freshness-seconds 600
```

The MCP exporter should return documents in `structuredContent` or a JSON text
content item. Abra stores env variable names for credentials, not literal
source credentials.

## Signed Webhook Producer

Use this when a source system can push changes to Abra.

```sh
abra connect webhook sample --scope team:docs --connector example-kb
abra connect webhook sign --secret-env ABRA_WEBHOOK_SECRET --payload-json @payload.json
abra connect webhook test --secret-env ABRA_WEBHOOK_SECRET --payload-json @payload.json
```

Production webhook producers must include stable source identity, event type,
source update time, and an HMAC signature. Set webhook secrets in deployment
environment variables.

## Internal HTTP Batch Job

Use this only for scheduled private connectors, gateways, or deployment
automation. The connector fetches from the source API, converts records to
normalized documents, and calls Abra HTTP ingestion with an operator-approved
token or policy.

This is not the agent brain UX. Agents should talk to Abra through MCP. Internal
HTTP is a transport option for internal knowledge bases and private source
systems when MCP export or webhook push is not the better fit.

## Local Or Git Source

Use built-in source paths when the source is just files or git:

```sh
abra connect local /srv/docs --scope team:docs --schedule "@every 10m"
abra connect git https://github.com/owner/repo.git --scope repo:owner-repo
```

For a one-time direct local sync:

```sh
abra sync . --code --scope repo:project
```

## Plugin Rules

- Keep plugins optional.
- Keep plugin output provider-neutral.
- Do not move governance or memory quality decisions into plugins.
- Do not commit secrets, source exports, embeddings, or private customer data.
- Prefer env references for credentials.
- Add tests for normalized document shape and failure handling.

Core should grow only when a capability improves Abra for every source and every
agent. Everything else should be a plugin, MCP exporter, webhook producer, or
private overlay.
