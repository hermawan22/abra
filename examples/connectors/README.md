# Connector Examples

This directory contains community-plugin starting points. The examples are
provider-neutral and intentionally use fake hosts, scopes, and credentials.

## MCP Knowledge Base

`mcp-knowledge-base.connector.json` describes a user-owned MCP exporter that
returns normalized Abra documents.

Validate it before registration:

```sh
abra plugin mcp validate \
  --manifest examples/connectors/mcp-knowledge-base.connector.json \
  --scope team:docs
```

Register it after validation:

```sh
abra plugin mcp register \
  --manifest examples/connectors/mcp-knowledge-base.connector.json \
  --scope team:docs \
  --wait \
  --verify
```

Credentials are env references such as `MCP_EXPORT_TOKEN`; do not put literal
tokens in manifests.

## Adding An Example

New examples should include:

- a generic connector name and fake source URL;
- a narrow scope such as `team:docs`;
- a schedule and freshness policy when the source is durable;
- ACL metadata when the source has visibility rules;
- a `verify_query` that proves useful content was ingested.
