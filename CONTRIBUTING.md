# Contributing

Thanks for considering a contribution to Abra.

## Development

Prerequisites:

- Go 1.25.11 or newer.
- Node.js 24 or newer.
- Docker or a compatible container runtime for full release-gate checks.
- Postgres with `pgvector` for local integration testing.

Run the fast checks:

```sh
go test ./...
npm test
```

Run the full local release gate when Docker is available:

```sh
ABRA_RELEASE_PROFILE=full ABRA_RELEASE_MANAGE_STACK=1 npm run release:gate
```

## Pull Request Guidelines

- Keep changes scoped to one behavior or documentation topic.
- Add or update tests when changing runtime behavior.
- Keep public APIs, MCP tools, migrations, and deployment manifests backward-compatible unless the change is explicitly documented.
- Do not commit secrets, database dumps, embeddings, source-system exports, audit logs, or organization-specific policies.
- Use generic examples in docs. Avoid company names, real repository URLs, tokens, private domains, incident details, or customer data.

## Design Principles

- Claims should remain source-cited, scoped, auditable, and status-aware.
- Provider-specific logic belongs behind generic interfaces.
- Deployment-specific connector, identity, and compliance behavior belongs in extensions or overlays.
- Production behavior should fail closed for auth, approvals, rate limits, and unsafe configuration.
