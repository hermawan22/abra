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
npm run release:gate:dry-run
ABRA_RELEASE_PROFILE=full ABRA_RELEASE_MANAGE_STACK=1 npm run release:gate
```

The dry-run report must list every release check without executing external
commands. It is a cheap way to review the release evidence contract before
running the managed full gate.

Run security checks:

```sh
go run golang.org/x/vuln/cmd/govulncheck@v1.4.0 ./...
npm audit --audit-level=high
```

## Artifacts

Each release should publish:

- `abra_linux_amd64.tar.gz`
- `abra_linux_arm64.tar.gz`
- `abra_darwin_amd64.tar.gz`
- `abra_darwin_arm64.tar.gz`
- `abra_runtime_vX.Y.Z.tar.gz`
- `install.sh`
- `SHA256SUMS`
- `IMAGE_DIGEST`
- `abra-release-gate.json`
- A multi-architecture image at `ghcr.io/hermawan22/abra` for `linux/amd64`
  and `linux/arm64`
- GitHub Artifact Attestations for the CLI archives, runtime bundle, `install.sh`, and
  `SHA256SUMS`
- GitHub Artifact Attestations for `IMAGE_DIGEST` and `abra-release-gate.json`
- Registry-attached image provenance and SBOM attestations for the GHCR image

The npm package metadata is private developer tooling only. Do not publish Abra
to npm as a release artifact; the Go CLI archives and the GHCR image are the
published runtime artifacts.

Do not document a platform as supported until the release contains its archive,
the archive is listed in `SHA256SUMS`, and both files have published
attestations. The install script fails closed for missing platform assets,
missing checksums, checksum mismatches, and invalid archives. Source builds are
developer fallback installs only; they are not release artifacts.

Do not document `abra up` from a release-installed CLI as production-hardened
unless the release contains `abra_runtime_vX.Y.Z.tar.gz`, the runtime bundle is
listed in `SHA256SUMS`, and the bundle plus `SHA256SUMS` have published
attestations. The runtime bundle should contain only the Compose material needed
to run published images and the release `IMAGE_DIGEST` file.

Do not document a container image as supported until `IMAGE_DIGEST` contains the
image digest, the digest points at `ghcr.io/hermawan22/abra`, the image is
available for both supported Linux platforms, and image provenance plus SBOM
attestations are present in GHCR. Production deployment examples must pin the
digest rather than relying only on mutable tags.

All external GitHub Actions used by CI, security, release-gate, and release
workflows must be pinned to full commit SHAs. The OSS hygiene gate rejects
mutable action tags or branches before release.

Set `ABRA_OSS_PRIVATE_CONTEXT_PATTERNS` to a comma-separated or newline-separated
list of local regex patterns when preparing a public release from a private
working copy. Keep that local denylist out of git; the committed scanner only
contains generic secret, local-path, registry-credential, workflow-ref, and
installer-URL checks.

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
Attestations, verifying those attestations, verifying the staged install script
path against the archive that will be uploaded, publishing the
multi-architecture GHCR image with provenance and SBOM attestations, and
uploading the release assets.

## Verification

Download release artifacts and verify checksums:

```sh
sha256sum -c SHA256SUMS
```

Verify artifact provenance with GitHub CLI for every archive, the runtime bundle, `install.sh`,
`SHA256SUMS`, `IMAGE_DIGEST`, and `abra-release-gate.json`:

```sh
gh attestation verify --repo OWNER/REPO abra_linux_amd64.tar.gz
gh attestation verify --repo OWNER/REPO abra_runtime_vX.Y.Z.tar.gz
gh attestation verify --repo OWNER/REPO install.sh
gh attestation verify --repo OWNER/REPO SHA256SUMS
gh attestation verify --repo OWNER/REPO IMAGE_DIGEST
gh attestation verify --repo OWNER/REPO abra-release-gate.json
```

Hardened install-script verification:

```sh
curl -fsSLO https://github.com/OWNER/REPO/releases/download/vX.Y.Z/install.sh
gh attestation verify --repo OWNER/REPO install.sh
ABRA_VERSION=vX.Y.Z ABRA_VERIFY_ATTESTATION=1 sh install.sh
```

The hardened installer path must install from the release archive for the
detected platform. Do not set `ABRA_ALLOW_SOURCE_BUILD=1` when verifying a
published release.

For immutable installer provenance, download the release-pinned installer asset
and verify its attestation before executing it:

```sh
curl -fsSLO https://github.com/OWNER/REPO/releases/download/vX.Y.Z/install.sh
gh attestation verify --repo OWNER/REPO install.sh
ABRA_VERSION=vX.Y.Z ABRA_VERIFY_ATTESTATION=1 sh install.sh
```

The release workflow also runs the installer against the staged `dist`
directory before uploading assets by setting `ABRA_RELEASE_BASE_URL` to a local
file URL. This variable is for release verification only; normal users should
install from the published GitHub release URL above.

Release-installed `abra up` downloads `abra_runtime_vX.Y.Z.tar.gz` from the same
release base URL, verifies the bundle checksum against `SHA256SUMS`, and uses
the bundle's `IMAGE_DIGEST` first line as the default `ABRA_IMAGE`. Set
`ABRA_VERIFY_RUNTIME_ATTESTATION=1` to require runtime bundle and `SHA256SUMS`
attestation verification during runtime download.

For the first-party GHCR image, prefer digests over mutable tags in production
deploy manifests.

Verify and promote the first-party GHCR image by digest:

```sh
image_ref="$(sed -n '1p' IMAGE_DIGEST)"
docker buildx imagetools inspect "$image_ref"
gh attestation verify "oci://${image_ref}" --repo OWNER/REPO
```

The first line of `IMAGE_DIGEST` is the digest-pinned image reference to use in
production, for example `ghcr.io/hermawan22/abra@sha256:...`. The other lines
list human-friendly tags for traceability only. Do not deploy from `latest` or
from an unpinned semantic version tag.

## Rollback

Rollback should use a previously published release artifact or image digest, not a locally rebuilt image. After rollback, run smoke checks and inspect `/readyz`, `/metrics`, and recent ingestion jobs.
