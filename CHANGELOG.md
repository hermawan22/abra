# Changelog

All notable changes to Abra are documented here.

This project uses semantic versioning for public releases. Until v1.0.0, minor versions may include breaking changes when they are documented in this file and in the release notes.

## 0.1.3 - 2026-06-18

### Fixed

- Allow `abra up` and `abra down` to run from any directory after global CLI installation by downloading and caching the matching runtime source bundle automatically.
- Store the quickstart env under the Abra config directory for global installs instead of creating `.tmp/quickstart.env` in the caller's current directory.

## 0.1.2 - 2026-06-18

### Changed

- Update installer next-step output to point users at `abra up`.

## 0.1.1 - 2026-06-18

### Changed

- Make `abra up` the primary command for starting the local stack; `abra install` remains a compatibility alias.
- Add `abra ingest . --code` and positional file/directory ingestion shortcuts.
- Default CLI scope to `repo:<current-git-root-or-folder>` when `--scope` is omitted.

## 0.1.0 - 2026-06-18

### Added

- Go API, worker, and migration roles.
- Postgres plus `pgvector` storage for documents, chunks, claims, evidence, graph records, approvals, audit events, and rate-limit buckets.
- HTTP and MCP interfaces for ingestion, recall, memory composition, policies, approvals, conflicts, source configs, ingestion jobs, and learning proposals.
- Go CLI for install, local bootstrap, status, ingestion, recall, compose, think, and MCP config.
- Docker Compose, Kubernetes manifests, and Helm chart.
- Production runbooks, eval gates, dogfood gate, and local performance gate.

### Security

- Production startup requires API keys.
- Production startup blocks local deterministic embeddings unless explicitly overridden for isolated smoke tests.
- Risky memory operations can require approval enforcement.
- API rate limiting is shared through Postgres for replicated API deployments.
