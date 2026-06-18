# Release Process

Abra releases should be reproducible from a signed Git tag and backed by CI evidence.

## Versioning

- Use semantic versions: `vMAJOR.MINOR.PATCH`.
- Keep `package.json`, `deploy/helm/Chart.yaml`, chart `appVersion`, and `CHANGELOG.md` aligned before tagging.
- Before v1.0.0, document any breaking change in `CHANGELOG.md` and the GitHub release notes.

## Pre-Release Checklist

Run locally before creating a tag:

```sh
go test ./...
npm test
helm lint ./deploy/helm
helm template abra ./deploy/helm >/tmp/abra-helm-template.yaml
ABRA_RELEASE_PROFILE=full ABRA_RELEASE_MANAGE_STACK=1 npm run release:gate
```

Run security checks:

```sh
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
npm audit --audit-level=high
```

## Artifacts

Each release should publish:

- `abra` CLI archives for supported platforms
- `SHA256SUMS`

Container images, SBOMs, and provenance should only be documented in release
notes after the workflow publishes them.

## Tagging

```sh
git tag -s vX.Y.Z -m "Abra vX.Y.Z"
git push origin vX.Y.Z
```

The release workflow builds CLI archives and uploads checksums.

## Verification

Download release artifacts and verify checksums:

```sh
sha256sum -c SHA256SUMS
```

For container images published by downstream deployment workflows, prefer digests over mutable tags in production deploy manifests.

## Rollback

Rollback should use a previously published release artifact or image digest, not a locally rebuilt image. After rollback, run smoke checks and inspect `/readyz`, `/metrics`, and recent ingestion jobs.
