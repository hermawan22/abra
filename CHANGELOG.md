# Changelog

All notable changes to Abra are documented here.

Abra uses semantic versioning for public releases. Before `v1.0.0`, minor
versions may include breaking changes when they are documented in this file and
in the release notes.

## 0.5.1 - 2026-06-30

### Changed

- Improved local setup UX so generic compatible embedding providers no longer
  inherit OpenAI defaults, while OpenAI remains an explicit provider choice.
- Made `abra up --no-models` report that local embeddings are skipped and avoid
  deep model readiness checks for API/MCP bootstrap-only runs.
- Hid the removed browser UI invariant from normal `abra doctor` output so the
  operator surface stays CLI-first.
- Clarified installer provenance modes and added an npm publish guard because
  Abra release artifacts are GitHub CLI archives and GHCR images, not npm
  packages.

### Security

- MCP connector ingestion now rejects documents outside the configured source
  scope by default; reviewed multi-scope exporters must opt in with
  `allow_scope_expansion`.

## 0.5.0 - 2026-06-30

### Added

- Brain operator CLI commands for source-backed review, scorecards,
  maintenance dry-runs/proposals, anchor backfill, and entity dossiers.
- Governed learning proposal review commands for listing, accepting, rejecting,
  canceling, and applying proposals through MCP-backed CLI flows.
- Date-only CLI `--as-of YYYY-MM-DD` support for temporal recall; the CLI
  normalizes it to RFC3339 before calling MCP.
- A GIN index migration for memory summary source URL containment lookups.

### Changed

- Local model setup defaults remain self-hosted Qwen3 embedding and optional
  Qwen3 reranker, while custom compatible providers fully replace the local
  runner path.
- Local model startup now waits for embedding and reranker readiness
  concurrently and prioritizes container exit/OOM diagnostics over generic HTTP
  readiness errors.
- `abra up` now fails fast when production config selects local embeddings
  without explicit operator approval, matching runtime config validation.
- Worker source ingestion now splits changed documents into bounded sub-batches
  to reduce timeout and payload risk on large repositories.
- Release gate now runs migration and maintainability checks as explicit
  release evidence.

### Security

- Runtime source hydration now rejects mutable `main` source downloads unless
  an explicit source URL/checksum or local developer opt-in is provided.

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
