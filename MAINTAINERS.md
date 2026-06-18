# Maintainers

Abra is maintained by the project owners in the `hermawan22/abra` repository.

Maintainer responsibilities:

- Review changes to public API, MCP tools, migrations, deployment manifests, and security-sensitive behavior.
- Keep release notes, changelog entries, and migration notes current.
- Triage security reports privately.
- Keep examples generic and free of organization-specific data.
- Reject changes that commit secrets, private source exports, embeddings, database dumps, audit records, or business-sensitive material.

Before public launch, repository owners should enable:

- GitHub private vulnerability reporting.
- Branch protection for `main`.
- Required CI, security, dependency review, and release-gate checks.
- Protected path ownership after the public GitHub organization/team exists.
