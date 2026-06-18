# Changelog

All notable changes to Abra are documented here.

This project uses semantic versioning for public releases. Until v1.0.0, minor versions may include breaking changes when they are documented in this file and in the release notes.

## 0.1.0 - Unreleased

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
