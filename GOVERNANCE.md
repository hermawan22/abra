# Governance

Abra is a small OSS infrastructure project. Governance should stay lightweight until the contributor base grows.

## Decision Making

Maintainers make final decisions for:

- Public API and MCP contract changes.
- Database migrations and compatibility policy.
- Production defaults and security posture.
- Release timing.
- Project scope and extension boundaries.

Design discussions should happen in issues or pull requests so decisions are visible to future contributors.

## Compatibility

Before v1.0.0, breaking changes may happen when they are documented in `CHANGELOG.md`, `RELEASE.md`, and migration notes. After v1.0.0, public HTTP/MCP contracts and migration behavior should follow semantic versioning.

## Extensions

Organization-specific connectors, identity integration, source ACL sync, and compliance workflows should remain deployment extensions unless they can be generalized without weakening the core runtime.
