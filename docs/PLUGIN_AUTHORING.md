# Plugin Authoring

Abra plugins are source adapters. They bring external knowledge into Abra while
leaving trust, ranking, memory health, and governance inside core.

The shortest path for community plugins is an MCP exporter. A scheduled private
job or signed webhook is also acceptable when MCP is not the right transport.

## Contract

Every plugin emits at least one normalized document:

```json
{
  "source_type": "markdown",
  "source_url": "https://kb.example.com/pages/123",
  "source_id": "123",
  "title": "Deploying service-a",
  "scope": "team:platform",
  "content": "The source-backed document body.",
  "source_updated_at": "2026-06-22T10:00:00Z",
  "metadata": {
    "connector": "example-kb",
    "owner": "team:platform",
    "allowed_principals": ["group:platform"]
  }
}
```

Required fields are `source_type`, `source_url`, `title`, `scope`, and
`content`. The plugin must not include secrets, bearer tokens, raw private
exports, local API keys, or credentials in `content` or `metadata`.

## MCP Exporter

Use an MCP exporter when your source system can expose a tool returning
normalized documents.

1. Implement an MCP tool such as `export_documents`.
2. Return documents in `structuredContent` or JSON text content.
3. Validate without registering:

```sh
abra plugin mcp validate \
  --scope team:docs \
  --mcp-url https://mcp.example.com/mcp \
  --tool export_documents
```

4. Register after validation:

```sh
abra plugin mcp register \
  --scope team:docs \
  --mcp-url https://mcp.example.com/mcp \
  --tool export_documents \
  --schedule "@every 10m" \
  --verify \
  --verify-query "runbook"
```

Use `--bearer-token-env` and `--header-env Header=ENV_NAME` for credentials.
Abra stores env variable names, not literal secret values.

## Signed Webhook

Use a signed webhook when the source system can push updates.

```sh
abra plugin webhook sample --scope team:docs --connector example-kb
abra connect webhook sign --secret-env ABRA_WEBHOOK_SECRET --payload-json @payload.json
abra connect webhook test --secret-env ABRA_WEBHOOK_SECRET --payload-json @payload.json
```

Production webhook plugins must include stable source identity, event type,
source update time, and an HMAC signature.

## Internal HTTP Batch Job

Use HTTP ingestion for scheduled private jobs that cannot expose MCP or send
webhooks. The job fetches source records, normalizes them, and calls Abra with
operator-approved credentials.

This is not the agent UX. Agents talk to Abra through MCP after ingestion.

## Quality Bar

A community plugin should include:

- a manifest example under `examples/connectors`;
- dry-run validation instructions;
- failure behavior for auth, pagination, rate limits, and malformed documents;
- ACL or principal mapping when the source has access controls;
- tests for normalized document shape and error cases.

Do not move governance decisions, trusted-memory promotion, citation policy, or
ranking overrides into the plugin. If a plugin finds a useful fact, it supplies
source-backed evidence and lets Abra core decide how the agent may use it.
