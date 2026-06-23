# Changelog

All notable changes to Abra are documented here.

Abra uses semantic versioning for public releases. Before `v1.0.0`, minor
versions may include breaking changes when they are documented in this file and
in the release notes.

## 0.4.0 - 2026-06-23

OSS release of Abra as an MCP-first governed agent brain.

### Added

- Source-cited working memory for AI agents through MCP tools such as
  `working_memory_compose`, `brain_think`, `brain_review`, `brain_scorecard`,
  `brain_anchor_backfill`, and `brain_maintain`.
- Deterministic no-LLM default recall with hybrid text/vector retrieval,
  pgvector storage, source snippets, evidence anchors, and token-budgeted
  context windows.
- Governed learning loop where agents can observe, challenge, and propose
  memory changes without silently promoting trusted facts.
- Conflict-aware and temporal memory signals for stale, superseded, expired, or
  disputed claims and graph relations.
- Core, archival, procedural, and episodic memory records using the existing
  Postgres schema, without an external graph database service.
- Agent handoff packets with answer, evidence, trust, gaps, conflicts,
  next-action guidance, validation plans, impact maps, and allowed actions.
- Provider-neutral embedding and reranker configuration through compatible
  endpoints, plus a self-hosted local development path.
- Lean operator CLI for setup, source sync, diagnostics, governance, eval, and
  release validation while keeping MCP as the canonical agent interface.
- Local, Git, MCP, signed webhook, and internal HTTP ingestion paths that all
  normalize into source-backed Abra documents.
- Community plugin and connector contracts for adapters, MCP exporters, signed
  webhook producers, and private overlays.
- Production deployment assets for Docker Compose, Kubernetes manifests, and a
  Helm chart with digest-pinned image guidance.
- OSS documentation for architecture, cognitive model, repository layout,
  extension boundaries, plugin authoring, benchmarks, operations, and release
  verification.
- Release, security, OSS hygiene, migration, npm pack allowlist, installer,
  eval, dogfood, queue-pressure, and performance gates.

### Changed

- Public release baseline is a single fresh-install migration,
  `migrations/001_init.sql`. After public release, schema changes must be added
  as append-only migrations.
- Large CLI, brain, memory, server, and store surfaces are split into focused
  files with maintainability budgets enforced by repository checks.
- Benchmark reporting is explicitly limited to capability scoring unless a
  reproducible cross-system harness, dataset, model/provider configuration, and
  hardware profile are published.

## 0.1.0 - 2026-06-18

Initial tagged release.

### Security

- Production mode requires explicit API keys and approval enforcement.
- Write-side risky operations are approval-gated in enforced mode.
- Installer tests fail closed for missing checksums, invalid archives, missing
  platform assets, and missing attestations.
- Public hygiene checks block committed secrets, developer-local paths,
  mutable GitHub Action refs, raw branch installer URLs, and private-context
  patterns configured by maintainers.
