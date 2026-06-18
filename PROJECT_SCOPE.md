# Project Scope

Abra is an open-source memory control plane and governed brain layer for AI agents.

The core project focuses on a small, self-hostable runtime that agents can use to retrieve source-cited, scoped, auditable working memory and governed brain answers. Deployment-specific integrations should extend the same public contracts rather than changing the core memory model.

## Included In Core

- Go CLI, API, worker, and migration binaries.
- Postgres plus `pgvector` persistence.
- Generic document ingestion.
- Local markdown/repo ingestion and provider-neutral Git ingestion.
- Source-cited claims, evidence, graph records, feedback, conflicts, approvals, and audit events.
- HTTP and MCP interfaces for agent runtimes.
- Policy planning, governed brain answers, working-memory composition, and approval gates for core memory operations.
- Metrics, optional tracing, ingestion job visibility, and eval gates.
- Docker Compose, Kubernetes manifests, Helm chart, production docs, and runbooks.

The core should remain complete enough for a small team to install, migrate, ingest, recall, evaluate, back up, restore, and operate safely.

## Extension Points

Use extensions or deployment overlays for behavior that depends on a specific organization or platform:

- Identity and access-control synchronization.
- Source-system connectors.
- Connector credentials, webhook state, and provider-specific retry logic.
- Compliance exports, retention workflows, and SIEM routing.
- Managed operations and upgrade automation.
- Organization-specific approval workflows.

Extensions should normalize data into Abra's ingestion, ACL, policy, approval, and audit contracts. The core service should stay provider-neutral and useful without private integrations.

See [docs/EXTENSIONS.md](./docs/EXTENSIONS.md) for connector and overlay patterns.

## Non-Goals

- Abra is not a chatbot or answer-generation model.
- Abra is not a generic vector database UI.
- Abra is not a replacement for source systems such as issue trackers, docs, chat, or repositories.
- Abra does not require Neo4j, Redis, Kafka, or a product CLI for v1.
- Abra should not store deployment-specific secrets, connector credentials, or organization policy in the public runtime.
