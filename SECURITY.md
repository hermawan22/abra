# Security

## Supported Versions

Security fixes are applied to the latest published release line and the current `main` branch.

## Supported Controls

- API key authentication for every non-health endpoint.
- Production startup fails without `ABRA_API_KEYS`.
- Production startup fails without signed webhook secrets unless `ABRA_ALLOW_UNSIGNED_WEBHOOKS_IN_PRODUCTION=true` is explicitly set for deployments that do not accept webhook ingestion or verify it upstream.
- Production startup fails when local neural embeddings are selected without `ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=true`.
- Invalid numeric, boolean, duration, and tracing sample config values fail startup instead of falling back silently.
- Local mode uses self-hosted Qwen-compatible embedding and reranker endpoints by default; custom providers replace those endpoints through env/CLI config.
- PII redaction is enabled by default.
- Claims retain source, scope, status, confidence, and freshness metadata.
- `forget` deprecates claims instead of deleting audit history.
- Audit events record write-side memory changes.

## Sensitive Data

Abra stores source-derived text snippets and embeddings. Treat the database as sensitive. Do not publish database dumps, embeddings, logs, or audit records from company deployments.

## Installer and Release Verification

For production hosts, install from a pinned release and require both checksum and
GitHub Artifact Attestation verification:

```sh
curl -fsSL https://github.com/hermawan22/abra/releases/download/vX.Y.Z/install.sh \
  | ABRA_VERSION=vX.Y.Z ABRA_VERIFY_ATTESTATION=1 sh
```

The installer verifies release archives against `SHA256SUMS` before installing.
With `ABRA_VERIFY_ATTESTATION=1`, it also requires GitHub CLI (`gh`) and
verifies artifact provenance for both the platform archive and `SHA256SUMS`.
The release also publishes and attests `install.sh`; operators who need
installer-script provenance before execution should download it first, run
`gh attestation verify --repo hermawan22/abra install.sh`, then execute it with
`ABRA_VERIFY_ATTESTATION=1`. Do not use `ABRA_ALLOW_SOURCE_BUILD=1` for
production installation or release verification; source builds are a developer
fallback and are not release artifacts.

## Reporting Issues

Report security issues privately through GitHub private vulnerability reporting for this repository: `https://github.com/hermawan22/abra/security/advisories/new`. If that flow is unavailable in a fork, contact the fork maintainer privately before sharing details. Do not file public issues for vulnerabilities, leaked secrets, database dumps, embeddings, or audit records.

Deployment forks should also follow their own incident and security process.

Maintainers should acknowledge a valid report within 5 business days, provide an initial severity assessment when enough detail is available, and coordinate disclosure after a fix or mitigation is ready.
