# Security

## Supported Versions

Security fixes are applied to the current unreleased `main` branch until the first public release. After tagged releases begin, supported versions will be listed here by minor version.

## Supported Controls

- API key authentication for every non-health endpoint.
- Production startup fails without `ABRA_API_KEYS`.
- Production startup rejects local deterministic embeddings by default.
- PII redaction is enabled by default.
- Claims retain source, scope, status, confidence, and freshness metadata.
- `forget` deprecates claims instead of deleting audit history.
- Audit events record write-side memory changes.

## Sensitive Data

Abra stores source-derived text snippets and embeddings. Treat the database as sensitive. Do not publish database dumps, embeddings, logs, or audit records from company deployments.

## Reporting Issues

Report security issues privately to the maintainers. Use GitHub private vulnerability reporting when it is enabled for the repository; otherwise contact the repository owner privately through GitHub before sharing details. Do not file public issues for vulnerabilities, leaked secrets, database dumps, embeddings, or audit records.

Deployment forks should also follow their own incident and security process.

Maintainers should acknowledge a valid report within 5 business days, provide an initial severity assessment when enough detail is available, and coordinate disclosure after a fix or mitigation is ready.
