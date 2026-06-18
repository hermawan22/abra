# Supply Chain

Abra's public release path should make artifacts traceable from source to deployment.

## Expected Controls

- CI runs Go tests, script checks, Helm render checks, Compose render checks, and container build checks.
- Security automation runs Go vulnerability checks and npm vulnerability checks.
- Dependency review blocks vulnerable dependency changes on pull requests.
- Dependabot keeps Go modules, npm packages, GitHub Actions, and Docker base images current.
- Release builds publish checksums for binaries.
- Container builds should publish SBOM and provenance metadata when supported by the registry.

## Verifying Binaries

Download `SHA256SUMS` from the release and run:

```sh
sha256sum -c SHA256SUMS
```

Only deploy artifacts that match published checksums.

## Verifying Images

Prefer immutable image digests:

```sh
docker pull ghcr.io/<owner>/<repo>/abra@sha256:<digest>
```

If the release workflow publishes provenance or SBOM attestations, verify them with the registry's supported tooling before promoting the image.

## Secrets

Do not commit `.env`, database dumps, embeddings, audit records, source-system exports, private connector credentials, SSH keys, or production tokens. The CI hygiene scan rejects known private project terms and business-strategy files, but it is not a substitute for secret scanning in the hosting platform.
