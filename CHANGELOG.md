# Changelog

All notable changes to Abra are documented here.

This project uses semantic versioning for public releases. Until v1.0.0, minor versions may include breaking changes when they are documented in this file and in the release notes.

## Unreleased

### Added

- Add repo-local `AGENTS.md` guidance so Codex-style agents use Abra MCP with the exact `repo:abra` scope before code changes.
- Add repo-local `CLAUDE.md` compatibility shim so Claude Code reads the same source of truth as `AGENTS.md`.
- Add deep readiness checks through `/readyz?deep=1` and have the CLI use them for local embedding setups.
- Add configurable API read timeout and request-body limits for large local ingestion workloads.
- Add optional MCP `ingest_documents` partial batch results with `continue_on_error` for connector overlays that need per-document status.
- Add retrieval reason explainability to recall, working-memory composition, context windows, and governed think results.
- Add retrieval source-diversity scoring so working-memory verification can flag packets dominated by one source.
- Add structured verifier `required_actions` so agents can respond to weak, partial, or unsafe memory packets without parsing recommendation text.
- Add bounded Prometheus counters for verifier `required_actions` so operators can see recurring agent-blocking causes without exposing scopes, tasks, queries, or recommendation text.
- Add configurable `--wait-timeout` / `ABRA_CLI_WAIT_TIMEOUT` for queued source ingestion waits.
- Add release preflight checks for `package-lock.json` version alignment.
- Add configurable working-memory recall and graph fan-out caps for predictable compose load under concurrent agents.
- Add webhook ingestion job lineage and idempotent delivery handling so connector events are visible in ingestion job history.
- Add `abra agents init` to generate AGENTS.md and CLAUDE.md instructions that point coding agents at the exact Abra scope.
- Add `abra agents verify` to check repo instruction files, MCP readiness, required agent tools, and exact-scope discovery before using an AI coding agent.
- Add `abra agents verify --files-only --strict` and run it in the release gate so agent instruction files cannot regress without a live MCP server.
- Add `ABRA_AI_PROVIDER_CONCURRENCY` to bound service-wide embedding and reranker calls across ingest, recall, readiness checks, and working-memory paths.
- Add CLI setup/config/doctor visibility for `ABRA_AI_PROVIDER_CONCURRENCY`, including local-model overload warnings.
- Add Prometheus metrics for AI provider calls, wait time, in-flight calls, and queued calls so operators can diagnose embedding and reranker saturation.

### Changed

- Default local neural providers to one in-flight AI provider call and compatible remote providers to four, reducing single-runner Qwen overload while preserving tuneable provider scaling.
- Make `discover_scopes` accept `expected_scope` and `query` hints so agents can find the exact project scope even when release or perf scopes crowd the first page.
- Make `abra mcp` generate bearer-token environment variable config by default, with literal token output only behind `--literal-token`.
- Derive default scopes for remote Git ingestion from the repository URL instead of the caller's current directory.
- Make `abra doctor` and `abra mcp install-codex` validate that the MCP endpoint exposes `discover_scopes` and `working_memory_compose`.
- Improve `abra doctor`, `abra scope`, and `abra mcp install-codex` guidance for Codex token env, exact scope matching, model config, and local model readiness.
- Make `abra doctor` check macOS launch-environment token visibility for Codex Desktop separately from the current shell.
- Add `--tracked` local path ingestion for worker-visible paths while keeping direct local `abra ingest <path>` as the Docker-safe default.
- Make setup next steps print the exact project scope for ingest and think commands.
- Make setup and ready banners include `abra agents init` and `abra agents verify` so agent context readiness is part of the default CLI onboarding path.
- Make `abra up` start the default local Qwen embedding runner automatically when the env uses `EMBEDDING_PROVIDER=local`.
- Make `abra scope` print agent init, agent verification, MCP install, and exact-scope recovery commands when AI clients say Abra has no context.
- Make CLI docs and generated agent instructions treat `abra scope` as the source of truth and recover empty agent context by ingesting and verifying the exact scope.
- Make `abra agents verify` call `working_memory_compose` and fail when the exact scope returns no source-backed context.
- Make `abra agents verify` use diagnostic working-memory compose so context checks do not write compose audit events or automatic learning proposals.
- Print `abra ingest` before `abra agents verify` in `abra scope` guidance now that verification requires source-backed working memory.
- Lower default working-memory recall fan-out to one to reduce local embedding oversubscription and stabilize compose p95 under concurrent agents.
- Make the self-host smoke gate require AI provider call, wait, and gauge metrics for embedding paths so provider observability cannot silently regress.
- Make managed release-gate stacks use a non-placeholder local API token so production secret validation runs during bootstrap.
- Point managed release-gate local embeddings at the host Qwen endpoint so containerized smoke, eval, and perf checks exercise the built-in model path.
- Use one managed release-gate webhook secret for both the API stack and signed smoke webhook requests.
- Give the quick release profile local-Qwen Tier 1 and perf latency thresholds while keeping the full gate's default thresholds unchanged.
- Run managed release-gate Compose stacks under an isolated project and cleanup them afterward so local Codex MCP does not inherit release-gate credentials.
- Align runtime build version reporting across MCP server info, Prometheus metrics, and tracing resources.
- Prefer query-form working-memory MCP resources so scopes containing slashes are preserved.
- Make `abra upgrade` download the install script before executing it so wrong installer URLs produce actionable recovery guidance instead of a raw curl pipe failure.
- Raise local demo/setup and managed release-gate worker intervals to reduce background ingestion contention during recall and working-memory latency gates.
- Warn on overly aggressive `WORKER_INTERVAL` values in `abra doctor` and normalize stale local setup env files back to the safer default.
- Improve chunk splitting and embedding batch token estimation for oversized paragraphs, minified JSON, and dense code.
- Expand default `--code` ingestion includes to supported code files repo-wide instead of only `src` JavaScript/TypeScript paths.
- Gate low-confidence retrieval on lexical and semantic relevance signal instead of allowing boosted rank alone to make weak matches look strong, while preserving moderate rank-only compatibility paths.
- Make `abra setup --openai/--compatible --no-start` print provider-appropriate next steps instead of telling users to start local models.
- Rewrite loopback custom embedding provider URLs to `host.docker.internal` in setup/config flows so Dockerized Abra services can reach host-served models.
- Harden production Compose and Helm defaults around compatible embeddings, loopback publish defaults, webhook signing, bind address, and request sizing.
- Make the release gate provide production-valid placeholder embedding and webhook settings for Docker Compose config validation.

### Fixed

- Keep worker ingestion jobs heartbeated during document processing and only allow the owning worker lease to finish a running job.
- Return webhook ingestion job IDs and detect duplicate signed deliveries in the smoke gate.
- Make the Tier 1 working-memory eval seed corroborating evidence so its strong-verification expectation matches source-diversity gates.
- Align the self-host smoke test with the query-form working-memory MCP resource template used to preserve scopes containing slashes.
- Validate the Abra MCP endpoint before mutating Codex MCP config during `abra mcp install-codex`.
- Treat summary-only and graph/context-only packets as usable source-backed context in CLI and governed think output.
- Align stale public release metadata in lockfile, Helm examples, and supported-version docs.

### Security

- Add release workflow gates for verified signed tags, version alignment, checksum verification, and GitHub Artifact Attestations for CLI release assets.
- Replace public CI hygiene denylist wording with generic secret-pattern checks.
- Harden the curl installer to fail closed for missing checksums, checksum mismatches, invalid archives, and missing executables; source builds now require explicit `ABRA_ALLOW_SOURCE_BUILD=1`.
- Add optional installer-side GitHub Artifact Attestation verification for release archives and `SHA256SUMS`.
- Reject unsigned production webhooks by default unless explicitly overridden for deployments that disable webhook ingestion or verify signatures upstream.
- Fail startup on malformed numeric, boolean, duration, tracing sample, port, and bind-address configuration.

## 0.3.7 - 2026-06-19

### Added

- Add MCP `discover_scopes` so AI clients can list visible memory scopes before choosing `brain_think`, `policy_plan`, or `working_memory_compose` scope values.
- Add configurable worker source and lease timeouts for slower local Qwen ingestion paths.

### Changed

- Require `scope` for MCP `policy_plan` to prevent agents from planning against a broad default scope.
- Treat code-backed packets with source documents, summaries, or graph context as usable evidence even when no claim facts are present.
- Make `abra mcp install-codex` warn when macOS launch environment setup fails and instruct users to fully reopen Codex Desktop after token-env changes.
- Bind development API servers to loopback by default and bind Compose Postgres to `127.0.0.1` by default.

### Fixed

- Make `abra models up --port <port>` update the runtime embedding base URL used by `abra up`.
- Remove a static demo bearer token from the MCP example config.

### Security

- Require explicit `ABRA_UNAUTHENTICATED_DEV=1` before allowing unauthenticated local development mode.
- Reject placeholder or too-short API tokens when `NODE_ENV=production`.

## 0.3.6 - 2026-06-19

### Added

- Add `abra scope` to print the exact project memory scope, ingest command, think command, and Codex prompt to prevent AI clients from querying the wrong scope.
- Add `abra mcp install-codex` to install Abra into Codex as a streamable HTTP MCP server without manually editing Codex config.

## 0.3.5 - 2026-06-19

### Fixed

- Increase the API-side local embedding provider timeout to 10 minutes so direct CLI ingestion does not fail while the local Qwen runner is still processing.
- Increase the API write timeout for synchronous ingestion requests.
- Align Docker Compose local defaults with the built-in Qwen embedding runner and leave reranking disabled unless explicitly configured.

## 0.3.4 - 2026-06-19

### Fixed

- Increase direct CLI ingest timeout to 10 minutes for local embedding backends and add `--timeout` / `ABRA_CLI_TIMEOUT` overrides for large repositories or slower machines.

## 0.3.3 - 2026-06-19

### Fixed

- Batch embedding requests during ingestion so large documents and repositories do not exceed local model context limits.
- Start the built-in llama.cpp Qwen embedding runner with a 32768-token context and recreate older local model containers that used the previous smaller context.
- Default local config loading to the Qwen GGUF embedding model with no implicit reranker endpoint.

## 0.3.2 - 2026-06-19

### Fixed

- Switch the built-in local Qwen embedding runner from Hugging Face TEI to llama.cpp GGUF serving, because the TEI ARM64 image path does not serve Qwen/Qwen3-Embedding-0.6B reliably without ONNX artifacts.
- Reset local embedding defaults to `Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0` and remove the non-started reranker endpoint from default local config.
- Recreate the local model container automatically when an older runner image is present.

## 0.3.1 - 2026-06-19

### Added

- Add `abra models up`, `abra models status`, `abra models logs`, and `abra models down` to manage the default local Qwen embedding runner from the CLI.
- Add local embedding readiness checks to `abra doctor`.

### Changed

- Let `abra setup` start the built-in local Qwen embedding runner before the Abra stack when local embeddings are selected.
- Replace raw embedding-provider ingest failures with a CLI hint to run `abra models up`.
- Document the CLI-owned local model path instead of asking users to manually run embedding servers.

## 0.3.0 - 2026-06-18

### Added

- Make `local` the default self-hosted neural provider path for Qwen/Qwen3-Embedding-0.6B and Qwen/Qwen3-Reranker-0.6B served through local compatible endpoints.
- Add optional reranker provider support with `RERANKER_PROVIDER`, `RERANKER_BASE_URL`, `RERANKER_API_KEY`, and `RERANKER_MODEL`.
- Add variable-dimension vector storage and partial pgvector indexes for common embedding dimensions.

### Changed

- Remove the deterministic local hash embedding provider from the product surface and internal provider registry.
- Allow self-hosted compatible embedding endpoints without an API key.
- Make custom embedding providers replace the local Qwen defaults, including disabling the local reranker unless explicitly configured.

## 0.2.0 - 2026-06-18

### Changed

- Breaking: remove the experimental interactive UI from the active product surface.
- Add `abra setup` guided CLI onboarding for prerequisite checks, runtime env creation, embedding provider selection, and optional stack startup.
- Point the public installer next step at `abra setup`.

## 0.1.9 - 2026-06-18

### Changed

- Replace an internal-looking redaction test fixture with generic OSS-safe registry placeholders.

## 0.1.8 - 2026-06-18

### Added

- Expand `abra ui` into a terminal cockpit with setup, restart, embedding config, local ingest, remote Git ingest, think, jobs, MCP, and doctor workflows.
- Add `abra config model openai --api-key-stdin` as a safer OpenAI embedding shortcut with 1536-dimension defaults.

### Changed

- Improve OSS release/security documentation consistency and remove private-term denylist entries from the public CI workflow.
- Warn users to re-ingest important sources after changing embedding providers.

## 0.1.7 - 2026-06-18

### Added

- Add `abra ui`, a native terminal cockpit for runtime health, model configuration, local repo ingestion, governed think, and MCP config without shipping a browser UI.

## 0.1.6 - 2026-06-18

### Added

- Add `abra config` commands for viewing runtime config and switching embedding model settings without manually editing the env file.

## 0.1.5 - 2026-06-18

### Fixed

- Skip empty matched files during local CLI ingest instead of aborting the repository ingest.
- Redact credential variable names and secret-handling context while preserving normal domain terms such as UI tokens.

## 0.1.4 - 2026-06-18

### Changed

- Make `abra up` next-step output point at the simple project ingest flow.
- Use a stable Docker Compose project name for global runtime installs.

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
- Production startup requires explicit model endpoint configuration before ingestion can use neural recall.
- Risky memory operations can require approval enforcement.
- API rate limiting is shared through Postgres for replicated API deployments.
