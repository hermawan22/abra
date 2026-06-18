# Abra CLI And Onboarding Benchmark

Date: June 18, 2026.

This document is a product-direction QA benchmark for Abra's CLI-first onboarding path. Abra remains service-backed: the CLI is the primary user experience, while the API, worker, Postgres store, and MCP endpoint remain the authoritative runtime.

## Current Verdict

Abra should ship a real CLI, but it must stay a client over the existing HTTP and MCP contracts. It should not become a second control plane, a hidden ingestion runtime, or a place where source authority, ACL, approval, and memory-trust rules diverge from the API.

The strongest competitor pattern is not "ship a CLI because terminal tools are fashionable." The useful pattern is:

- one-command or two-command first success
- agent-readable JSON for every command
- explicit scope and source authority on writes
- explainable retrieval and graph traces
- doctor/status commands that validate installation and memory quality
- dry-run and approval-aware destructive actions
- benchmark evidence before any product claim

## Benchmarked Ideas

### GBrain

GBrain is the most relevant onboarding benchmark because it is explicitly agent-operated. Its install path tells users to have an agent follow an install protocol, and its quick start for coding agents uses a local brain plus MCP connection in a couple of commands. It also stresses a brain-first loop: signal, search, respond, write, auto-link, sync.

Useful ideas for Abra:

- Agent-first installation can reduce human setup friction, but only if the protocol is auditable and smoke-tested at the end.
- Local-first quick starts are compelling when they avoid Docker and tokens for the first experience.
- Retrieval explainability belongs in the CLI surface: users should be able to see ranking stages, boosts, graph signals, and freshness warnings.
- The CLI should warn when the memory does not know something or has not seen a source recently.
- "Doctor" checks should inspect graph signal health, search quality, stale ingestion, and batch/retry incidents.

Abra-specific caution:

- GBrain's agent-write loop is too permissive for Abra unless every write preserves scope, source URL, authority, trust status, and audit linkage.
- Abra should not copy an opaque "brain writes itself" posture. Candidate memory improvements must stay as reviewable learning proposals unless policy allows promotion.

Sources: [GBrain README](https://github.com/garrytan/gbrain).

### Mem0

Mem0 has the clearest agent-safe CLI contract. Its CLI supports add, search, list, get, update, delete, import, config, entity, event, status, and version. Its `--agent` and `--json` modes return consistent structured envelopes, suppress colors and spinners, and return JSON errors with nonzero exit codes.

Useful ideas for Abra:

- Every command needs a machine-readable mode from day one.
- Human output and agent output should be separate contracts.
- Commands should expose entity/scope filters instead of relying on implicit defaults.
- Async work needs event IDs and status inspection.
- Destructive commands need dry-run previews and force flags, not hidden prompts only.
- Help should be introspectable as JSON so coding agents can discover valid commands without scraping prose.

Abra-specific caution:

- Mem0 optimizes for broad memory CRUD. Abra must make trust-state and source evidence visible in the command grammar, not hide them behind generic add/update/delete verbs.

Sources: [Mem0 CLI docs](https://docs.mem0.ai/platform/cli), [Mem0 API overview](https://docs.mem0.ai/api-reference), [Mem0 memory types](https://docs.mem0.ai/core-concepts/memory-types).

### Letta

Letta Code is useful because its CLI treats memory as a first-class developer artifact. It starts locally without login, offers `/init`, `/doctor`, `/remember`, `/memory`, `letta -p` headless mode, JSON and stream-JSON output, memory status/diff/pull/backup/restore/export commands, and a memory filesystem backed by local files and git-style workflows.

Useful ideas for Abra:

- Onboarding should bootstrap useful memory structure, not only create credentials.
- A doctor command should audit memory structure, token budget, source coverage, and corruption/conflict risks.
- Headless mode needs stable JSON and streaming events for CI, agent loops, and long-running ingestion.
- Memory diffs, backups, exports, and token estimates are powerful operator primitives.
- Progressive disclosure matters: agents should see a compact index first, then load only the relevant details.
- Multi-agent learning is safer when concurrent work has isolated workspaces and merge/conflict semantics.

Abra-specific caution:

- Abra should not expose mutable local memory files as the source of truth unless those files are only an export/import format. The authoritative state remains Postgres plus audited API writes.
- A `/remember`-style shortcut must create unverified or reviewable memory unless backed by approved source evidence.

Sources: [Letta Code CLI](https://docs.letta.com/letta-code/cli), [Letta CLI reference](https://docs.letta.com/letta-code/cli-reference), [Letta headless mode](https://docs.letta.com/letta-code/headless), [Letta context repositories](https://www.letta.com/blog/context-repositories/), [Letta ADE overview](https://docs.letta.com/guides/ade/overview/).

### Graphiti And Zep

Graphiti/Zep is the strongest graph-memory benchmark. The important product idea is temporal context, not a specific graph database. Graphiti models facts with validity windows, preserves episodes as provenance, integrates updates incrementally, and combines semantic, keyword, and graph traversal retrieval. Zep packages that into governed, low-latency context retrieval and assembly.

Useful ideas for Abra:

- CLI diagnostics should show "true now" versus "true before" when claims or relations have changed.
- Graph context should expose provenance, validity, supersession, and conflict state.
- Incremental ingestion should be inspectable by source, episode/document, and derived relation.
- Retrieval benchmarks should measure hybrid recall quality and graph contribution separately.
- Low latency is a product requirement only if measured; publish p95/p99 thresholds and eval reports.

Abra-specific caution:

- Abra should not add Neo4j, FalkorDB, Neptune, or another graph backend just to match Graphiti. The current Postgres graph model is the right default until measured workloads prove otherwise.

Sources: [Graphiti README](https://github.com/getzep/graphiti), [Zep graph overview](https://help.getzep.com/graph-overview), [Zep paper](https://arxiv.org/html/2501.13956v1).

### Terminal-Native Developer Tools

Codex CLI, Gemini CLI, Claude Code, and Aider show what developers now expect from terminal-native agent tools:

- Work should happen in the user's existing terminal and repo, with no forced IDE switch.
- Tooling should support MCP or equivalent extension points.
- Approval modes, sandboxing, and explicit command boundaries are part of the product, not advanced settings.
- Non-interactive runs must be scriptable.
- Long tasks need progress events, traceable command history, and resumable status.
- Agents should run tests, linters, and verification commands, then expose failures clearly.
- Local config should be overrideable per invocation and safe for CI.

Useful ideas for Abra:

- `abra status`, `abra doctor`, and `abra eval` matter more than generic CRUD.
- CLI onboarding should end with a real `brain_think` or `working_memory_compose` result containing citations, verification, memory health, and an agent decision gate.
- The CLI should never hide approval requirements. If approval enforcement blocks an action, the CLI should print the required next action and approval request ID.
- All risky commands should default to preview/dry-run and require explicit confirmation or flags.

Sources: [Codex CLI docs](https://developers.openai.com/codex/cli), [Codex command reference](https://developers.openai.com/codex/cli/reference), [Gemini CLI docs](https://developers.google.com/gemini-code-assist/docs/gemini-cli), [Claude Code product page](https://claude.com/product/claude-code), [Claude Code power user tips](https://support.claude.com/en/articles/14554000-claude-code-power-user-tips), [Aider linting and testing](https://aider.chat/docs/usage/lint-test.html).

## Product Principles For Abra CLI

1. Keep the API authoritative.
   The CLI is a client. It must call public HTTP/MCP contracts and must not write directly to Postgres or invent private state.

2. Make first success real.
   The onboarding path is not complete until the user sees a source-cited answer or working-memory packet with verification, citations, memory-health status, and agent decision.

3. Design for agents and humans separately.
   Human output can be readable. Agent output must be deterministic JSON with stable fields, no color codes, no spinners, nonzero exit codes on errors, and parseable remediation hints.

4. Preserve Abra's trust model.
   Every write path must require or derive scope, source URL, authority, trust status, and audit context. Unsourced memories are never silently promoted.

5. Prefer diagnostics over a web surface.
   A CLI should help install, connect, inspect, benchmark, repair, export, and explain. Abra should not need a bundled web UI to be usable.

6. Treat graph and recall as explainable systems.
   Users should be able to ask why a result ranked, what graph relations contributed, what was stale or conflicted, and which source records backed the answer.

7. Make dangerous actions boring.
   Forget, source authority changes, ACL changes, broad backfills, and destructive repairs need dry-run, approval awareness, audit IDs, and exact blast-radius summaries.

8. Benchmark before messaging.
   Do not claim "agent-grade memory," "production-ready onboarding," or "fast graph recall" without eval output and latency thresholds.

## Acceptance Criteria

### A. Onboarding

- AC-ONBOARD-01: A new user can run one installer command, then `abra install`, and end with MCP URL, auth mode, and next command.
- AC-ONBOARD-02: The guided path creates or reuses a scoped demo source and ends by calling `brain_think` or `working_memory_compose`.
- AC-ONBOARD-03: The final onboarding output includes at least one citation, verification verdict, memory-health status, and agent decision gate.
- AC-ONBOARD-04: The onboarding path fails closed when auth is missing, migrations are pending, embeddings are unavailable, or worker ingestion cannot run.
- AC-ONBOARD-05: The CLI prints exact remediation commands for common failures without masking the underlying API error.
- AC-ONBOARD-06: The first-run flow does not require external connectors, Neo4j, Redis, Kafka, or cloud-only services.
- AC-ONBOARD-07: The CLI makes clear that Abra is CLI-only but service-backed: the API remains authoritative and no bundled browser UI is shipped.

### B. Agent-Safe Command Contract

- AC-AGENT-01: Every command supports `--json` and returns a stable envelope with `status`, `command`, `duration_ms`, `request_id` when available, `scope` when relevant, `data`, and `error`.
- AC-AGENT-02: JSON mode emits no spinners, color escapes, progress prose, banners, or mixed stderr/stdout data that would break parsing.
- AC-AGENT-03: Errors in JSON mode include `code`, `message`, `retryable`, `required_action`, and any relevant `approval_id`, `job_id`, or `event_id`.
- AC-AGENT-04: `abra help --json` returns the command tree, arguments, defaults, environment variables, and risk class for every command.
- AC-AGENT-05: Commands accept explicit `--scope`, `--agent`, and `--token-budget` where applicable and do not silently use broad scopes.
- AC-AGENT-06: Non-interactive mode never prompts. It either uses explicit flags/environment or exits with a structured missing-input error.

### C. Trust, Scope, And Safety

- AC-SAFE-01: Any memory write command requires source evidence or stores the result as unverified/reviewable.
- AC-SAFE-02: Commands that change source authority, ACLs, agent-action policy, forget state, broad backfills, or destructive repair default to dry-run.
- AC-SAFE-03: Dry-run output lists affected scopes, sources, claim counts, relation counts, summaries, and audit intent before execution.
- AC-SAFE-04: When approval enforcement is enabled, risky commands create or reference approval requests and refuse to bypass required review.
- AC-SAFE-05: The CLI never accepts secrets as positional arguments in documented examples. It prefers environment variables, config files with redaction, or interactive secret entry.
- AC-SAFE-06: `abra config show` redacts API keys and connector credentials.
- AC-SAFE-07: Cross-scope recall and compose commands include leakage checks in test fixtures and benchmark output.

### D. Diagnostics And Explainability

- AC-DIAG-01: `abra status` verifies readiness, auth, embedding provider identity, database migration status, worker reachability, and MCP initialization.
- AC-DIAG-02: `abra doctor` checks memory health, stale ingestion jobs, retrying jobs, missing summaries, active conflicts, critical health signals, and pending learning-proposal duplication.
- AC-DIAG-03: `abra recall --explain` shows final rank, text score, vector score, source authority, freshness, status, graph contribution, and citation coverage.
- AC-DIAG-04: `abra compose --trace` shows retrieval plan, stage timings, warning counts, graph warnings, verification verdict, context-window packing, dropped blocks, and agent decision.
- AC-DIAG-05: Graph diagnostics expose relation IDs, source URLs, validity/freshness state, active conflicts, and superseded/deprecated records.
- AC-DIAG-06: CLI diagnostics include stable machine-readable signal codes matching `/memory/health` rather than prose-only warnings.

### E. Ingestion And Source Config

- AC-INGEST-01: The CLI can register, list, pause, resume, enqueue, retry, and cancel source configs only through the existing API/MCP contracts.
- AC-INGEST-02: Source config commands validate local path visibility and remote Git settings before enqueueing work.
- AC-INGEST-03: Ingestion job commands stream or poll job status with job ID, state, attempts, last error, source URL, and timestamps.
- AC-INGEST-04: Re-ingestion output reports deprecated/reactivated claims, deprecated/reactivated relations, replaced summaries, and new conflicts.
- AC-INGEST-05: Bulk import supports preview and requires explicit scope and source authority.

### F. Evaluation And Benchmarks

- AC-EVAL-01: `abra eval smoke` maps to the existing smoke gate and preserves its JSON output.
- AC-EVAL-02: `abra eval tier1`, `abra eval provider`, `abra eval dogfood`, `abra perf local`, and `abra release gate` are thin wrappers around existing npm/script contracts until a native runner exists.
- AC-EVAL-03: Benchmark output includes hit rate at 1/3/5, citation coverage, leakage count, recall p95/p99, working-memory p95/p99, verification verdict distribution, and agent decision distribution.
- AC-EVAL-04: CLI onboarding cannot be marked ready unless dogfood proves Abra can compose useful memory about its own docs, code, policy, graph, and validation paths.
- AC-EVAL-05: A release gate fails if the CLI JSON contract changes without a fixture update.
- AC-EVAL-06: Benchmark reports are archiveable JSON and include Abra version, embedding provider, dataset path, thresholds, and pass/fail summary.

### G. Terminal-Native UX

- AC-TERM-01: Commands work in macOS, Linux, and CI shells without requiring an interactive TUI.
- AC-TERM-02: Interactive niceties are optional. Every workflow has a documented non-interactive equivalent.
- AC-TERM-03: The CLI supports environment-variable overrides and per-invocation flags without mutating global config unless requested.
- AC-TERM-04: Long-running commands support progress events in stream-JSON mode.
- AC-TERM-05: Commands that trigger local verification can run configured test, smoke, or eval commands and surface nonzero exits with captured summaries.
- AC-TERM-06: The CLI records commands it ran or API operations it invoked in an audit-friendly local transcript when requested.

## Suggested Minimum Command Set

This is the target product surface. The current CLI implements the first slice: init/up/down/status/doctor/seed/ingest/think/recall/compose/mcp.

```text
abra status
abra doctor
abra quickstart
abra mcp inspect
abra source list|upsert|pause|resume|enqueue|jobs|retry|cancel
abra ingest document --scope ... --source-url ... --authority ...
abra recall --scope ... --query ... [--explain]
abra compose --scope ... --task ... [--trace] [--token-budget ...]
abra health --scope ...
abra conflicts list|show
abra learning list|propose|apply-plan
abra approvals list|request|show
abra eval smoke|tier1|provider|dogfood
abra perf local
abra release gate
```

Commands intentionally absent from the minimum set:

- no direct database shell
- no hidden connector credential manager
- no broad `abra remember` that creates trusted memory without evidence
- no bundled web UI
- no graph database administration

## QA Gate For Any CLI Proposal

Before implementation, a CLI proposal should answer these questions:

1. Which existing API or MCP contract does each command call?
2. What is the exact JSON schema for success and failure?
3. What is the risk class of each command?
4. Which commands can mutate memory, source configs, ACLs, policies, approvals, or learning proposals?
5. How does each risky command behave under `ABRA_APPROVAL_MODE=enforce`?
6. Which eval proves the command works?
7. Which benchmark proves the onboarding path improves first success without weakening trust?
8. What is explicitly still better handled by operator runbooks or platform tooling?

## Recommendation

Build the CLI around diagnostics, onboarding, eval wrappers, and agent-safe working-memory access. Defer broad memory CRUD, local mutable memory files, connector-specific setup, and graph backend changes until there is measured evidence that the existing governed service path cannot handle the workflow.
