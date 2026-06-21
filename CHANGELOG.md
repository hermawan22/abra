# Changelog

All notable changes to Abra are documented here.

This project uses semantic versioning for public releases. Until v1.0.0, minor versions may include breaking changes when they are documented in this file and in the release notes.

## Unreleased

### Added

- Add structured AI provider errors for embedding, reranker, and extraction calls, including bounded provider code, HTTP status, retryability, attempts, model/provider identity, and batch metadata.
- Add structured HTTP, MCP, CLI, metrics, and ingestion-job diagnostics for AI provider failures so agent setup problems surface as `auth_failed`, `provider_unreachable`, `provider_timeout`, `rate_limited`, or `invalid_response` instead of opaque no-context errors.
- Add agent-aware `abra agents verify` / `ready` / `bootstrap` flows so Claude and other MCP clients can verify source-backed context with their own agent profile instead of being forced through Codex defaults.
- Add first-class rerank warnings and per-result rerank metadata so working-memory packets expose reranker failures, bounded rerank scores, and base-vs-final ranking.
- Add direct local `abra ingest` per-file progress in human output, with `--quiet` and `--json` keeping automation output clean.
- Add explicit `server_ready`, `client_ready`, and `client_warnings` fields to `abra agents verify --json` so client MCP/token issues do not look like missing memory context.
- Add fail-fast MCP `ingest_documents` batching that validates every document first, embeds chunks and extracted claims across the whole request, then persists only after the embedding provider succeeds.
- Add top-level `abra --version` and `abra -v` aliases for standard CLI install and troubleshooting checks.
- Apply the configured request body limit to stateless MCP requests so MCP batch ingestion cannot bypass the HTTP ingest/webhook body guardrail.

### Changed

- Redact and bound provider error bodies and transport causes before exposing them through API, CLI, MCP, logs, or job metadata.
- Make batched embedding ingestion preserve batch range and token estimates on provider failures for easier local model and custom provider troubleshooting.
- Keep `ingest_documents(continue_on_error=true)` on per-document ingestion so partial connector overlays still receive stable success/error entries while the default fail-fast path gets cross-document embedding efficiency.
- Keep Codex MCP installation automatic only for Codex while guiding other agents to the generic MCP config.
- Treat a working directory as an Abra source checkout only when it matches the Abra repo fingerprint, so user projects with their own Compose files still use the global CLI runtime and env paths.
- Make agent ready prompts distinguish unavailable MCP/token setup from missing source-backed memory, and make `abra compose` ignore generic gate blocks when deciding whether source-backed context exists.
- Make generated `AGENTS.md` recovery guidance use the same MCP/token-before-reingest order as the ready prompt.
- Persist fail-fast batch ingestion inside one database transaction after validation and embedding, rolling back the batch on the first persistence error.
- Bound reranker rank boosts instead of adding raw provider scores directly to recall ranking.
- Omit raw rerank query text from retrieval warnings, keep rerank metadata JSON stable, and only mark recall as reranked when a returned candidate index was actually applied.
- Default `base_rank_score` to `rank_score` for non-reranked recall results so public ranking metadata remains internally consistent.

## 0.3.8 - 2026-06-21

### Added

- Add repo-local `AGENTS.md` guidance so Codex-style agents use Abra MCP with the exact `repo:abra` scope before code changes.
- Add repo-local `CLAUDE.md` compatibility shim so Claude Code reads the same source of truth as `AGENTS.md`.
- Add deep readiness checks through `/readyz?deep=1` and have the CLI use them for local embedding setups.
- Add structured deep-readiness embedding diagnostics for timeout vs provider error, including check and provider timeout values.
- Add black-box installer fail-closed tests for checksum, attestation, archive, platform, tool, and source-fallback failures.
- Add fake-Docker lifecycle tests for the built-in local Qwen runner create, start, recreate, status, logs, and shutdown paths.
- Add release-gate dry-run reporting and named installer/OSS hygiene checks so release evidence can be audited before expensive stack execution.
- Add configurable API read timeout and request-body limits for large local ingestion workloads.
- Add optional MCP `ingest_documents` partial batch results with `continue_on_error` for connector overlays that need per-document status.
- Add `abra ingest . --continue-on-error` for direct local repo ingestion so one failed file does not hide successful files or the remaining per-file failures.
- Add an OSS hygiene self-test that proves mutable GitHub Actions refs are rejected before release.
- Add an OSS hygiene guardrail that rejects committed raw branch installer URLs so official install and upgrade paths stay pinned to GitHub Release assets.
- Add retrieval reason explainability to recall, working-memory composition, context windows, and governed think results.
- Add retrieval source-diversity scoring so working-memory verification can flag packets dominated by one source.
- Add structured verifier `required_actions` so agents can respond to weak, partial, or unsafe memory packets without parsing recommendation text.
- Add bounded Prometheus counters for verifier `required_actions` so operators can see recurring agent-blocking causes without exposing scopes, tasks, queries, or recommendation text.
- Add configurable `--wait-timeout` / `ABRA_CLI_WAIT_TIMEOUT` for queued source ingestion waits.
- Add release preflight checks for `package-lock.json` version alignment.
- Add first-party GHCR image release documentation for digest pinning, image provenance, SBOM expectations, and Helm deployment usage.
- Add first-class raw observations through CLI, HTTP, and MCP so agents can capture scoped episodic memory without promoting it to trusted claims.
- Add observation-target learning proposals so raw observations can move into review while staying outside trusted recall until explicitly applied.
- Add smoke and Tier 2/3 coverage for HTTP and MCP observation-to-learning proposal review, dedupe, audit, and no trusted auto-promotion.
- Add a local OSS hygiene scanner to block real secret patterns, developer-local paths, and private organization context before CI or release gates.
- Add Prometheus gauges for ingestion queue pressure in working-memory health metrics so operators can alert on queued, retrying, failed, running, and stale jobs.
- Add configurable working-memory recall and graph fan-out caps for predictable compose load under concurrent agents.
- Add webhook ingestion job lineage and idempotent delivery handling so connector events are visible in ingestion job history.
- Add `abra agents bootstrap` as a one-command Codex-ready path that writes agent instructions, ingests the repo with the exact scope, verifies source-backed working memory, and installs Abra MCP into Codex.
- Add `abra agents init` to generate AGENTS.md and CLAUDE.md instructions that point coding agents at the exact Abra scope.
- Add `abra agents verify` to check repo instruction files, MCP readiness, required agent tools, and exact-scope discovery before using an AI coding agent.
- Add `abra agents verify --files-only --strict` and run it in the release gate so agent instruction files cannot regress without a live MCP server.
- Add `ABRA_AI_PROVIDER_CONCURRENCY` to bound service-wide embedding and reranker calls across ingest, recall, readiness checks, and working-memory paths.
- Add CLI setup/config/doctor visibility for `ABRA_AI_PROVIDER_CONCURRENCY`, including local-model overload warnings.
- Add Prometheus metrics for AI provider calls, wait time, in-flight calls, and queued calls so operators can diagnose embedding and reranker saturation.
- Add a queue-pressure eval gate that verifies signed webhook ingestion jobs drain, leave no queued/retry/stale residue, and become recallable.
- Add the full managed release gate, vulnerability checks, runtime version alignment, and main-branch ancestry checks to the tag release workflow before CLI archives are built and published.
- Add hot query embedding caching for recall and working-memory paths, and make full release dogfood/performance gates stable on the default local Qwen embedding runner.
- Add staged install-script verification, installer asset publishing, and pre-upload attestation verification to the release workflow so CLI archives must pass the same installer path users run with `curl | sh`.
- Add an npm pack allowlist gate so the developer npm package can only contain `LICENSE`, `README.md`, and `package.json`.
- Add local repo ingestion file guardrails that skip oversized, binary-looking, generated, minified, protobuf, and lock files before reading content.

### Changed

- Default `ABRA_APPROVAL_MODE` to `enforce` when `NODE_ENV=production` while keeping local development on `advisory` unless explicitly overridden.
- Validate production `ABRA_API_KEYS` role/scope options instead of allowing malformed option strings to fall back to admin all-scope access.
- Reject plaintext non-loopback production embedding and reranker provider URLs while still allowing loopback self-hosted endpoints.
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
- Make setup next steps lead with `abra agents bootstrap --agent codex` and include exact-scope no-context recovery guidance.
- Make `abra up` start the default local Qwen embedding runner automatically when the env uses `EMBEDDING_PROVIDER=local`.
- Make `abra scope` print agent init, agent verification, MCP install, and exact-scope recovery commands when AI clients say Abra has no context.
- Make CLI docs and generated agent instructions treat `abra scope` as the source of truth and recover empty agent context by ingesting and verifying the exact scope.
- Make `abra agents verify` call `working_memory_compose` and fail when the exact scope returns no source-backed context.
- Make `abra agents verify` use diagnostic working-memory compose so context checks do not write compose audit events or automatic learning proposals.
- Make accepted claim-promotion apply plans target scoped `memory_write` even when the proposal originated from a raw observation.
- Make `abra setup --no-start`, `abra mcp install-codex`, CLI help, and docs surface model logs and exact-scope recovery steps for Codex no-context cases.
- Print `abra ingest` before `abra agents verify` in `abra scope` guidance now that verification requires source-backed working memory.
- Make `abra agents ready` a non-mutating readiness-check alias instead of bootstrapping, ingesting, or installing MCP.
- Make `abra agents verify` and `abra agents ready` print recovery next steps in terminal output, and keep returning `ready_prompt` plus `next_steps` for JSON-based AI-client launchers.
- Lower default working-memory recall fan-out to one to reduce local embedding oversubscription and stabilize compose p95 under concurrent agents.
- Make the self-host smoke gate require AI provider call, wait, and gauge metrics for embedding paths so provider observability cannot silently regress.
- Make managed release-gate stacks use a non-placeholder local API token so production secret validation runs during bootstrap.
- Point managed release-gate local embeddings at the host Qwen endpoint so containerized smoke, eval, and perf checks exercise the built-in model path.
- Use one managed release-gate webhook secret for both the API stack and signed smoke webhook requests.
- Give the quick release profile local-Qwen Tier 1 and perf latency thresholds while keeping the full gate's default thresholds unchanged.
- Run managed release-gate Compose stacks under an isolated project and cleanup them afterward so local Codex MCP does not inherit release-gate credentials.
- Align runtime build version reporting across MCP server info, Prometheus metrics, and tracing resources.
- Pin GitHub Actions workflow dependencies to immutable commit SHAs across CI, security, release-gate, and release workflows.
- Make the OSS hygiene gate reject mutable GitHub Actions refs so workflow pinning cannot regress silently.
- Prefer query-form working-memory MCP resources so scopes containing slashes are preserved.
- Make `abra upgrade` download the install script before executing it so wrong installer URLs produce actionable recovery guidance instead of a raw curl pipe failure.
- Make `abra upgrade` use published release installer URLs by default, with `--version` resolving to that pinned release's installer instead of branch `main`.
- Raise local demo/setup and managed release-gate worker intervals to reduce background ingestion contention during recall and working-memory latency gates.
- Warn on overly aggressive `WORKER_INTERVAL` values in `abra doctor` and normalize stale local setup env files back to the safer default.
- Make `abra doctor` warn when local-Qwen compose recall fan-out exceeds configured AI provider concurrency.
- Make source-built CLIs report the tracked runtime version by default and make installers warn when `PATH` resolves to a different `abra` binary.
- Make `abra setup` defer project-scoped ingest, verify, and think commands to the scope printed after `cd /path/to/project`.
- Make `abra agents init` default to Codex instructions and make agent context verification compose as `codex`.
- Make direct and tracked local ingestion expose configurable `max_file_bytes` / `--max-file-bytes` and generated-file override controls.
- Improve chunk splitting and embedding batch token estimation for oversized paragraphs, minified JSON, and dense code.
- Expand default `--code` ingestion includes to supported code files repo-wide instead of only `src` JavaScript/TypeScript paths.
- Gate low-confidence retrieval on lexical and semantic relevance signal instead of allowing boosted rank alone to make weak matches look strong, while preserving moderate rank-only compatibility paths.
- Make `abra setup --openai/--compatible --no-start` print provider-appropriate next steps instead of telling users to start local models.
- Rewrite loopback custom embedding provider URLs to `host.docker.internal` in setup/config flows so Dockerized Abra services can reach host-served models.
- Make production setup recommend a compatible/self-hosted embedding endpoint instead of the development local-Qwen shortcut unless operators explicitly allow local embeddings in production.
- Make `abra models status/up/down/logs` report inactive local-runner state when the active embedding provider is compatible/custom, avoiding repair commands for a runner Abra will not use.
- Make `abra models up` bind the local Qwen runner to `127.0.0.1` by default and recreate the container when runner-relevant model, dimension, port, publish, cache, image, pooling, or context settings change.
- Make the local Qwen runner use Docker pull policy `missing` by default, expose image/pull/readiness env controls, require digest-pinned runner images for production local embeddings, and have `abra down` stop the owned local runner unless `--keep-models` is set.
- Require an OpenAI API key for non-interactive `abra setup --openai` via `--api-key-stdin` or `OPENAI_API_KEY`.
- Harden production Compose and Helm defaults around compatible embeddings, loopback publish defaults, webhook signing, bind address, and request sizing.
- Make the release gate provide production-valid placeholder embedding and webhook settings for Docker Compose config validation.

### Fixed

- Keep worker ingestion jobs heartbeated during document processing and only allow the owning worker lease to finish a running job.
- Keep unmanaged release gates on the target stack's token while still generating non-placeholder secrets for managed release-gate stacks.
- Return webhook ingestion job IDs and detect duplicate signed deliveries in the smoke gate.
- Make the Tier 1 working-memory eval seed corroborating evidence so its strong-verification expectation matches source-diversity gates.
- Align the self-host smoke test with the query-form working-memory MCP resource template used to preserve scopes containing slashes.
- Validate the Abra MCP endpoint before mutating Codex MCP config during `abra mcp install-codex`.
- Treat summary-only and graph/context-only packets as usable source-backed context in CLI and governed think output.
- Align stale public release metadata in lockfile, Helm examples, and supported-version docs.
- Mark npm metadata as private developer tooling while adding standard repository, license, issue, and homepage fields.
- Keep npm packaging intentionally minimal so private developer scripts cannot become an accidental npm distribution artifact.
- Pin `govulncheck` to a reviewed module version in release documentation and GitHub workflows.
- Use digest-pinned image placeholders in raw Kubernetes examples instead of mutable version tags.
- Make Helm's default render use a digest placeholder and add an OSS hygiene guard so chart defaults do not regress to mutable image tags.
- Include `LICENSE` and `README.md` in CLI release tarballs alongside the `abra` binary.
- Make `abra doctor` validate that Codex can read MCP config and has an `abra` MCP entry before reporting token-env readiness.

### Security

- Add release workflow gates for verified signed tags, version alignment, checksum verification, and GitHub Artifact Attestations for CLI release assets.
- Replace public CI hygiene denylist wording with generic secret-pattern checks.
- Harden the curl installer to fail closed for missing checksums, checksum mismatches, invalid archives, and missing executables; source builds now require explicit `ABRA_ALLOW_SOURCE_BUILD=1`.
- Add optional installer-side GitHub Artifact Attestation verification for release archives and `SHA256SUMS`.
- Document production image promotion around GHCR digests, release-attested `IMAGE_DIGEST`, registry image provenance, and Kubernetes pod hardening.
- Omit example env files from the production runtime image so fixed demo credentials stay in source documentation and release archives, not deployed containers.
- Reject unsigned production webhooks by default unless explicitly overridden for deployments that disable webhook ingestion or verify signatures upstream.
- Fail startup on malformed numeric, boolean, duration, tracing sample, port, and bind-address configuration.
- Remove committed private-context literals from the OSS hygiene scanner and allow local/private denylist patterns through `ABRA_OSS_PRIVATE_CONTEXT_PATTERNS`.
- Scan package lockfiles in OSS hygiene checks so private registry metadata cannot hide in dependency locks.

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
