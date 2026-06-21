# CLI Guide

Abra is terminal-first: install the `abra` command, start the service from the terminal, and operate it through the CLI, HTTP, or MCP.

The quickstart path defaults to local neural embeddings: Qwen/Qwen3-Embedding-0.6B-GGUF served by a local llama.cpp OpenAI-compatible endpoint started automatically by `abra up` and managed directly with `abra models up/status` when troubleshooting. Qwen/Qwen3-Reranker-0.6B can be configured when a compatible rerank endpoint is available. Custom providers are supported and replace the local defaults when configured.

Local embedding calls default to a 10-minute provider timeout because CPU-backed model requests can be slower than normal API calls on large files. Custom providers default to 30 seconds and can be changed with `EMBEDDING_TIMEOUT`. Local neural setup writes `ABRA_AI_PROVIDER_CONCURRENCY=1`, `ABRA_EMBEDDING_BATCH_MAX_ITEMS=6`, and `ABRA_EMBEDDING_BATCH_MAX_TOKENS=3000` so the default Qwen runner stays under its context window on large files. Compatible providers write `ABRA_AI_PROVIDER_CONCURRENCY=4`, `ABRA_EMBEDDING_BATCH_MAX_ITEMS=16`, and `ABRA_EMBEDDING_BATCH_MAX_TOKENS=6000`; raise those only after measuring provider capacity. `abra doctor` warns when these values are invalid or likely too aggressive for a single local model runner.

## 3-Minute Local Flow

For OSS users, this convenience command installs the latest published release
binary from GitHub releases:

```sh
curl -fsSL https://github.com/hermawan22/abra/releases/latest/download/install.sh | sh
```

For production workstations or repeatable automation, pin the release and
require GitHub Artifact Attestation verification:

```sh
curl -fsSL https://github.com/hermawan22/abra/releases/download/vX.Y.Z/install.sh \
  | ABRA_VERSION=vX.Y.Z ABRA_VERIFY_ATTESTATION=1 sh
```

If you already cloned the repo, this checkout-local installer does the same
release install. It does not install untagged local source changes:

```sh
./scripts/install.sh
```

Release downloads are verified against `SHA256SUMS` before the binary is installed. If GitHub CLI is available, the installer also verifies GitHub Artifact Attestations automatically. Set `ABRA_VERIFY_ATTESTATION=1` to require provenance verification, `ABRA_VERSION=vX.Y.Z` to install a specific release, or `ABRA_ALLOW_SOURCE_BUILD=1` to intentionally build from the release source tag when no platform asset exists.

Run the guided first-run setup:

```sh
abra setup
```

`abra setup` checks prerequisites, creates an env file, asks which embedding provider to use, can start the built-in local Qwen embedding runner, and can start Postgres, migrations, API, and worker. From a source checkout it uses `.tmp/quickstart.env`; from a global CLI install it stores runtime files under your Abra config directory and can be run from any folder. `abra install` is kept as a compatibility alias for `abra setup`; the curl script installs the CLI binary. If setup writes local config but you start later, `abra up` starts the default Qwen embedding runner automatically before checking API readiness.

If setup finishes but ingest or Codex still cannot use Abra, run `abra doctor` before changing config by hand. Doctor separates runtime env issues, worker interval problems, API/MCP readiness, Codex token-env visibility, model config, and local model readiness. With the default local provider, `abra up` starts the embedding runner automatically; `abra models status` shows whether the embedding endpoint is serving requests, and `abra models up` starts or repairs it directly.

View or change the important runtime config without opening the env file:

```sh
abra config show
abra config path
abra config model local
abra config model qwen3
abra config model openai --api-key-stdin
abra config model compatible --base-url https://api.example.com/v1 --model embedding-model --dimensions 1024
```

For non-interactive local setup, use `abra setup --yes`. `qwen3` and `local-smart` are local neural aliases; they use the same built-in Qwen runner lifecycle as `local`, and `abra models up` normalizes the stored provider back to `local` after syncing the env. For authenticated compatible providers during onboarding, use `printf '%s' "$PROVIDER_API_KEY" | abra setup --compatible --embedding-base-url https://api.example.com/v1 --embedding-model embedding-model --dimensions 1024 --api-key-stdin`. The CLI infers dimensions for known OpenAI, Qwen, BGE, Nomic, and Gemini embedding model names; pass `--dimensions` for unknown compatible models. If a local ingest still reports an embedding context-window error, reduce `--embedding-batch-max-items` or `--embedding-batch-max-tokens`; if a scaled compatible provider is underused, raise them deliberately. For OpenAI, non-interactive setup requires `--api-key-stdin` or `OPENAI_API_KEY`.
Use `abra setup --yes --no-models` only when you intentionally manage the embedding endpoint yourself; otherwise the default local provider is started for you by setup or `abra up`.

Make the current repo Codex-ready after setup:

```sh
abra agents bootstrap --agent codex
```

This writes `AGENTS.md` with the exact Abra scope, adds `CLAUDE.md` as an
import for tools that read Claude Code instructions, ingests the repo with
`--code`, installs Abra MCP into Codex without writing the token literally to
disk, and verifies `working_memory_compose` returns source-backed context.
Fully quit and reopen Codex Desktop after bootstrap so the active app process
reloads the MCP config and token environment.

Use `abra agents init --agent codex`, `abra ingest . --code --scope
<scope-from-abra-scope>`, and `abra agents verify . --scope
<scope-from-abra-scope> --agent codex` separately only when you need the manual
steps. `abra agents ready` is a non-mutating alias for verify. Both commands
print a ready prompt and next steps for the AI client; `--json` returns
`ok`, `server_ready`, `client_ready`, `agent_ready`, `ready_prompt`, and
`next_steps` for automation. Use `agent_ready`, not `ok` alone, when deciding
whether the active AI client can rely on Abra MCP.
The compose call runs in diagnostic mode, so verification does not write compose
audit events or learning proposals. If `abra doctor` says MCP is ok
but Codex has no Abra tools or no context, run `abra mcp install-codex`, fully
quit and reopen Codex Desktop, then rerun `abra agents verify . --scope
<scope-from-abra-scope>`; re-ingest only if verify says the exact scope or
source-backed memory is missing.
For CI or release checks that should not contact a live MCP server, run
`abra agents verify --files-only --strict`.

After changing model config, restart the stack:

```sh
abra down
abra up
abra status
```

For local-runner troubleshooting, use `abra models status` and `abra models up` directly. These commands manage only the built-in local Qwen runner, publish it on `127.0.0.1` by default, use Docker pull policy `missing`, and recreate the container when runner-relevant model, dimension, port, cache, publish, image, pull, pooling, or context settings change. Production local embeddings require `ABRA_LOCAL_EMBEDDING_IMAGE` to be an operator-verified `@sha256` image reference; otherwise use `EMBEDDING_PROVIDER=compatible`. When `EMBEDDING_PROVIDER` is not `local`, `qwen3`, or `local-smart`, model commands report the local runner as inactive unless `--force` is passed.

Use these defaults for the remaining commands:

```sh
export ABRA_BASE_URL=http://localhost:18080
export ABRA_API_TOKEN=demo-only-dev-token
```

Ingest a demo document:

```sh
abra ingest --scope repo:demo \
  --title Intro \
  --source-url file://intro.md \
  --text "Agents should use Abra before autonomous code changes."
```

Ingest local docs or repo files directly from the CLI:

```sh
abra scope
abra ingest . --code --scope <scope-from-abra-scope>
abra agents verify . --scope <scope-from-abra-scope>
```

`abra ingest .` reads the checkout from the CLI process, so it works even when
the API and worker run in Docker and cannot see your local path. Use `--tracked`
only when the worker can read the same path and you want a durable source config
plus ingestion job.

In human output, direct local ingestion prints the total file count and current
file before each embedding request so slow local model calls are visible. Use
`--quiet` to suppress per-file progress, or `--json` for clean machine-readable
output without progress lines.

Queue a remote Git repo through the worker:

```sh
abra ingest --git https://github.com/owner/repo.git --ref main --code --scope repo:owner-repo --wait --wait-timeout 10m
```

Ask Abra to think with governed memory:

```sh
abra think "What should agents use before autonomous code changes?" --scope <scope-from-abra-scope>
```

Check runtime status:

```sh
abra status
abra doctor
```

Use `abra doctor --strict` for CI or preflight scripts that should exit non-zero when any runtime check is not ok.

Connect MCP:

```sh
abra mcp > .tmp/abra.mcp.json
```

By default this writes `bearer_token_env_var: ABRA_API_TOKEN`, not a literal
token. Use `--token-env NAME` for a different env var. Use `--literal-token`
only for legacy clients that cannot read bearer-token env vars.

Connect Codex directly:

```sh
abra mcp install-codex
```

The installer writes the Codex MCP entry and validates that `/mcp` exposes
`discover_scopes` and `working_memory_compose`. If validation fails, start Abra
with `abra up`, check `abra doctor`, confirm local model readiness with
`abra models status` when using the default local provider, and rerun
`abra mcp install-codex`.

Fully quit and reopen Codex Desktop after installing or changing the token env.
Opening a new thread is enough only when the env var was already available to
the Codex process. `abra mcp install-codex` sets the macOS launch environment
when available; terminal-launched Codex still needs `ABRA_API_TOKEN` exported in
the launching shell. In each project, the one-command Codex-ready path is:

```sh
abra agents bootstrap --agent codex
```

This writes agent instructions, ingests the repo with the exact scope, verifies
source-backed working memory, and installs the Abra MCP endpoint into Codex.
For Claude Code or another MCP-capable client, use the same agent workflow with
an explicit profile and the generic MCP JSON instead of Codex auto-install:

```sh
abra agents bootstrap --agent claude
abra mcp > .tmp/abra.mcp.json
abra agents verify . --agent claude --scope <scope-from-abra-scope>
```

The manual setup path is:

```sh
abra scope
abra agents verify . --scope <scope-from-abra-scope>
abra ingest . --code --scope <scope-from-abra-scope>   # only if verify reports missing scope or empty memory
abra agents verify . --scope <scope-from-abra-scope>
```

Then tell the agent: `Use Abra MCP first. Exact scope: repo:<project>. Call
discover_scopes with expected_scope="repo:<project>", then call
working_memory_compose with task=<current task>, scope="repo:<project>", and
agent="codex" before answering or changing code. If Abra MCP tools are
unavailable or the AI client says Abra has no context, run abra agents verify .
--scope repo:<project> --agent codex --json first. Run abra doctor and repair
MCP/API/token/model readiness when verify reports readiness errors; when
server_ready=true but agent_ready=false, reinstall/restart the AI client MCP
integration. Re-ingest only when verify proves the exact scope is missing or
source-backed memory is empty, then rerun verify with this exact scope.`

`abra scope` also prints the exact `abra agents bootstrap`, `abra agents init`,
`abra ingest`, and `abra agents verify` commands for the current project. When
Codex or another AI client says Abra has no context, run the printed verify
command first. If `server_ready=true` but `agent_ready=false`, repair MCP/token
visibility and fully restart that client. Re-ingest only when verify proves the
exact scope or source-backed memory is missing.

Stop the local stack and default local embedding runner:

```sh
abra down
```

Keep the local embedding runner warm when stopping only the API stack:

```sh
abra down --keep-models
```

Reset demo data:

```sh
abra down --reset
```

Upgrade or remove the CLI binary:

```sh
abra upgrade
abra upgrade --version vX.Y.Z
abra uninstall --yes
```

## From Source

From source, run the Go CLI directly:

```sh
go run ./cmd/abra up
```

For repeated local use, build a binary:

```sh
go build -o .tmp/abra ./cmd/abra
.tmp/abra up
```

To replace the `abra` on your PATH with this checkout for development, build
explicitly to that path and confirm the binary before setup:

```sh
go build -o "$HOME/.local/bin/abra" ./cmd/abra
abra version
abra scope
```

The generated config uses:

```text
url: http://127.0.0.1:18080/mcp
bearer_token_env_var: ABRA_API_TOKEN
```

Raw HTTP endpoints also accept `x-api-key: <token>`.

Stop the local stack:

```sh
go run ./cmd/abra down
```

Reset demo data:

```sh
go run ./cmd/abra down --reset
```

## Command Map

From a source checkout, run the CLI as `go run ./cmd/abra <command>`. In a release install, the binary is `abra`.

| Task | Command |
| --- | --- |
| install CLI from checkout | `./scripts/install.sh` |
| install CLI from published release | `curl -fsSL https://github.com/hermawan22/abra/releases/latest/download/install.sh \| sh` |
| check installed CLI version | `abra --version` |
| guided first-run setup | `abra setup` |
| make Codex ready for the current repo | `abra agents bootstrap --agent codex` |
| generate agent instruction files | `abra agents init --agent codex` |
| verify agent context setup | `abra scope && abra agents verify . --scope <scope-from-abra-scope>` |
| print machine-readable agent readiness | `abra scope && abra agents ready . --scope <scope-from-abra-scope> --json` |
| verify agent instruction files in CI | `abra agents verify --files-only --strict` |
| start local Qwen embedding runner | `abra models up` |
| check local embedding runner | `abra models status` |
| inspect local embedding runner logs | `abra models logs` |
| stop local Qwen embedding runner | `abra models down` |
| start local stack and default local embedding runner | `abra up` |
| init env only | `abra init` |
| compatibility setup alias | `abra install` |
| ingest one document | `abra ingest --text "source-backed content" --approval-id <approval-id>` |
| ingest local repo directly from the CLI | `abra ingest . --code --scope <scope-from-abra-scope>` |
| ingest local repo and keep going after per-file failures | `abra ingest . --code --continue-on-error --scope <scope-from-abra-scope>` |
| suppress direct local ingest progress | `abra ingest . --code --quiet --scope <scope-from-abra-scope>` |
| ingest remote git | `abra ingest --git https://github.com/owner/repo.git --ref main --code --scope repo:owner-repo --wait --wait-timeout 10m` |
| list sources | `abra sources` |
| refresh an existing source config | `abra sources sync <source-config-id> --scope <scope> --wait --wait-timeout 10m` |
| backfill an existing source config | `abra sources backfill <source-config-id> --scope <scope> --approval-id <approval-id> --wait --wait-timeout 10m` |
| inspect one source config | `abra sources status <source-config-id>` |
| inspect one source job history | `abra sources logs <source-config-id> --limit 20` |
| pause a source config | `abra sources pause <source-config-id>` |
| resume a source config | `abra sources resume <source-config-id> --approval-id <approval-id>` |
| validate an MCP source before registering it | `abra source mcp --scope team:platform --mcp-url https://mcp.example/mcp --tool export_documents --dry-run` |
| register an auto-refreshing MCP source | `abra source mcp --scope team:platform --mcp-url https://mcp.example/mcp --tool export_documents --schedule "@every 10m" --freshness-seconds 600` |
| list jobs | `abra jobs` |
| capture raw observation | `abra observe "Agents should rerun release checks" --scope repo:demo` |
| capture and propose observation | `abra observe "Agents should rerun release checks" --scope repo:demo --propose --source-url file://release-runbook.md` |
| capture preferences from a transcript | `abra observe conversation --file transcript.md --scope repo:demo --propose` |
| list observations | `abra observations --scope repo:demo --query release` |
| propose existing observation | `abra observations propose <observation-id> --scope repo:demo --claim "Agents should rerun release checks." --source-url file://release-runbook.md` |
| think | `abra think "question" --scope <scope-from-abra-scope>` |
| compose prompt-ready agent context | `abra compose "ship a change" --scope <scope-from-abra-scope> --prompt` |
| compose and queue learning proposals | `abra compose "ship a change" --scope <scope-from-abra-scope> --persist-learning` |
| print project scope for agents | `abra scope` |
| status | `abra status` |
| doctor | `abra doctor` |
| strict doctor for CI/preflight | `abra doctor --strict --json` |
| version | `abra version` |
| upgrade | `abra upgrade` |
| uninstall CLI | `abra uninstall --yes` |
| mcp config JSON | `abra mcp > .tmp/abra.mcp.json` |
| install MCP into Codex | `abra mcp install-codex` |
| down | `abra down` |

For explicit HTTP ingestion, call the underlying endpoint directly:

```sh
auth_header="x-api-key: $ABRA_API_TOKEN"
curl -sS -H "$auth_header" \
  -H "content-type: application/json" \
  -d '{
    "source_type": "markdown",
    "source_url": "file://intro.md",
    "title": "Intro",
    "scope": "repo:demo",
    "content": "Agents should use Abra before autonomous code changes.",
    "authority": "official-doc"
  }' \
  "$ABRA_BASE_URL/ingest/documents"
```

Capture raw episodic memory without promoting it to a trusted claim:

```sh
abra observe "Agents should rerun release checks before tagging" --scope repo:demo
abra observe "Agents should rerun release checks before tagging" --scope repo:demo --propose --source-url file://release-runbook.md
abra observe conversation --file transcript.md --scope repo:demo --propose
abra observations --scope repo:demo --query release
abra observations propose <observation-id> --scope repo:demo --claim "Agents should rerun release checks before tagging." --source-url file://release-runbook.md
```

`observe conversation` accepts plain transcripts with `User:` / `Assistant:`
lines or JSON arrays/objects with `messages`. By default it captures only
preference-like user turns as raw `preference` observations, then `--propose`
queues review items without trusting them. Use `--all-turns` when a gateway
intentionally wants full episodic transcript capture.

When `ABRA_APPROVAL_MODE=enforce` or stored agent policy requires review,
direct ingestion uses the `agent_write` approval action. Pass
`--approval-id <approval-id>` to `abra ingest` after the request is approved.

The equivalent HTTP surface is:

```sh
curl -sS -H "$auth_header" \
  -H "content-type: application/json" \
  -d '{
    "scope": "repo:demo",
    "observation_text": "Agents should rerun release checks before tagging",
    "observation_type": "episode",
    "status": "raw",
    "created_by": "abra-cli"
  }' \
  "$ABRA_BASE_URL/observations"

curl -sS -H "$auth_header" \
  "$ABRA_BASE_URL/observations?scope=repo:demo&query=release"
```

To send a raw observation into review without trusting it, use the existing
learning proposal endpoint with `target_type: "observation"`:

```sh
curl -sS -H "$auth_header" \
  -H "content-type: application/json" \
  -d '{
    "scope": "repo:demo",
    "proposal_type": "claim",
    "title": "Promote release-check observation",
    "rationale": "Review raw observation as a trusted claim candidate.",
    "target_type": "observation",
    "target_id": "observation-id",
    "source_url": "file://release-runbook.md",
    "confidence": 0.7,
    "payload": {
      "observation_id": "observation-id",
      "claim": "Agents should rerun release checks before tagging.",
      "promotion_flow": "observation_to_claim"
    },
    "created_by": "abra-cli"
  }' \
  "$ABRA_BASE_URL/learning/proposals"
```

For worker-based source refreshes, use `abra ingest . --code --tracked`, `abra watch local --path . --wait --wait-timeout 10m`,
`abra watch git --git https://github.com/owner/repo.git --wait --wait-timeout 10m`, or an MCP-backed source such as:

```sh
abra source mcp \
  --scope team:platform \
  --mcp-url https://mcp.example.internal/mcp \
  --tool export_documents \
  --arguments-json '{"space":"ENG"}' \
  --document-source-type confluence \
  --bearer-token-env CONFLUENCE_MCP_TOKEN \
  --header-env X-API-Key=CONFLUENCE_API_KEY \
  --dry-run

abra source mcp \
  --scope team:platform \
  --mcp-url https://mcp.example.internal/mcp \
  --tool export_documents \
  --arguments-json '{"space":"ENG"}' \
  --document-source-type confluence \
  --bearer-token-env CONFLUENCE_MCP_TOKEN \
  --header-env X-API-Key=CONFLUENCE_API_KEY \
  --freshness-seconds 3600 \
  --schedule "@every 1h" \
  --wait
```

The configured MCP tool must return normalized Abra documents as JSON, either as
`structuredContent.documents` or as a JSON text content item containing
`{"documents":[...]}`. `--dry-run` or `--validate` calls that upstream MCP tool
and validates the returned documents without creating a source config or queueing
an ingestion job. Use `--bearer-token-env` and `--header-env HEADER=ENV_NAME`
to reference credentials from environment variables instead of writing secrets
into source configs.
To run an existing source again without changing its config:

```sh
abra sources sync <source-config-id> --scope team:platform --wait --wait-timeout 10m
```

For explicit historical reprocessing, use a backfill trigger. This still queues
the existing source config, but records the operator intent separately in the
job trigger and metadata:

```sh
abra sources backfill <source-config-id> --scope team:platform --approval-id <approval-id> --wait --wait-timeout 10m
```

Inspect a source and its latest job, then drill into recent job history:

```sh
abra sources status <source-config-id>
abra sources logs <source-config-id> --limit 20
```

Pause and resume connector-backed sources without rewriting their config:

```sh
abra sources pause <source-config-id>
abra sources resume <source-config-id> --approval-id <approval-id>
```

Resume is connector enablement and may require an approved request when
`ABRA_APPROVAL_MODE=enforce`.

Manual `sources sync` bypasses due checks. Scheduled worker refresh only queues
sources whose `freshness_policy` or `schedule_cron` says they are due, and skips
sources with active queued, retrying, or running jobs. Supported schedules are
`@hourly`, `@daily`, and `@every <N><s|m|h|d>`.

Tracked local sources require the worker process to see the same filesystem path;
use direct `abra ingest . --code` for ordinary Docker-backed local setup.
Use `WORKER_CONCURRENCY` to run multiple queued ingestion jobs in one worker process; keep the default `1` for local Qwen and raise it only after `abra doctor`, provider latency, and database usage show headroom.
When `--code` is enabled and no `--code-include` is supplied, Abra includes supported
Go, JavaScript, TypeScript, and React code files repo-wide while skipping common
dependency, build, cache, and vendor directories.
Matched files larger than `--max-file-bytes` are skipped before their content is
read; the default is `1048576` bytes. Binary-looking files and generated,
minified, protobuf, and lock files are skipped by default. Use
`--include-generated` only for trusted sources where generated artifacts are the
actual source of truth.
For event-based ingestion, send normalized documents to `POST /ingest/webhooks`
from your connector or automation. The core OSS worker schedules `local_repo`,
`git_repo`, markdown, and `mcp` source configs. Other source systems should use
a thin connector overlay that listens to source events and posts documents into
Abra, or expose an MCP document-export tool that Abra can call.

## Self-Host Commands

Create a Compose environment:

```sh
cp examples/env/production.env.example .env.production
$EDITOR .env.production
```

Set `ABRA_IMAGE` and `POSTGRES_IMAGE` in `.env.production` to digest-pinned
release/operator-approved images. The example file contains valid placeholder
digests for config validation; replace them before boot.

Pull images, start Postgres, run migrations, then start API and worker:

```sh
docker compose --env-file .env.production pull
docker compose --env-file .env.production up -d postgres
docker compose --env-file .env.production run --rm migrate
docker compose --env-file .env.production up -d api worker
```

For curl-installed CLI users, `abra up` uses the downloaded runtime Compose file
and pulls the published `ghcr.io/hermawan22/abra:<version>` image instead of
building locally. For local source-checkout development, prefer `abra up`; it
applies `docker-compose.dev.yml` and builds `abra:local` without making
production Compose depend on a local checkout.

Check readiness:

```sh
auth_header="x-api-key: replace-with-generated-token"
curl -sS -H "$auth_header" \
  http://localhost:18080/readyz
```

The remote MCP endpoint is `http://localhost:18080/mcp`. `abra doctor` validates
that the endpoint exposes the MCP tools needed by agents. MCP clients that read
the working-memory resource should use
`abra://working-memory?scope={scope}&task={task}`, which preserves scopes that
contain slashes.

Stop Compose services:

```sh
docker compose --env-file .env.production down
```
