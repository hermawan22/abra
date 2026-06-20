# Release Process

Abra releases should be reproducible from a signed Git tag and backed by CI evidence.

## Versioning

- Use semantic versions: `vMAJOR.MINOR.PATCH`.
- Keep `package.json`, `package-lock.json`, `deploy/helm/Chart.yaml`, chart `appVersion`, and `CHANGELOG.md` aligned before tagging.
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

- `abra_linux_amd64.tar.gz`
- `abra_linux_arm64.tar.gz`
- `abra_darwin_amd64.tar.gz`
- `abra_darwin_arm64.tar.gz`
- `SHA256SUMS`
- GitHub Artifact Attestations for the CLI archives and `SHA256SUMS`

Container images and SBOMs should only be documented in release notes after the
workflow publishes them.

Do not document a platform as supported until the release contains its archive,
the archive is listed in `SHA256SUMS`, and both files have published
attestations. The install script fails closed for missing platform assets,
missing checksums, checksum mismatches, and invalid archives. Source builds are
developer fallback installs only; they are not release artifacts.

## Tagging

```sh
git tag -s vX.Y.Z -m "Abra vX.Y.Z"
git push origin vX.Y.Z
```

The release workflow rejects lightweight tags, unsigned or unverified tag
signatures, and release tags whose version does not match `package.json`,
`deploy/helm/Chart.yaml`, chart `appVersion`, and the latest numbered
`CHANGELOG.md` entry, and a commit reachable from `origin/main`. It then runs
Go and npm vulnerability checks and the full managed release gate before
building CLI archives, verifying `SHA256SUMS`, creating GitHub Artifact
Attestations, and uploading the release assets.

## Verification

Download release artifacts and verify checksums:

```sh
sha256sum -c SHA256SUMS
```

Verify artifact provenance with GitHub CLI for every archive and for
`SHA256SUMS`:

```sh
gh attestation verify --repo OWNER/REPO abra_linux_amd64.tar.gz
gh attestation verify --repo OWNER/REPO SHA256SUMS
```

Hardened install-script verification:

```sh
curl -fsSL https://raw.githubusercontent.com/OWNER/REPO/main/scripts/install.sh \
  | ABRA_VERSION=vX.Y.Z ABRA_VERIFY_ATTESTATION=1 sh
```

The hardened installer path must install from the release archive for the
detected platform. Do not set `ABRA_ALLOW_SOURCE_BUILD=1` when verifying a
published release.

For container images published by downstream deployment workflows, prefer digests over mutable tags in production deploy manifests.

## Rollback

Rollback should use a previously published release artifact or image digest, not a locally rebuilt image. After rollback, run smoke checks and inspect `/readyz`, `/metrics`, and recent ingestion jobs.
