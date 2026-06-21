# CLI Guide

Abra is terminal-first: install the `abra` command, start the service from the terminal, and operate it through the CLI, HTTP, or MCP.

The quickstart path defaults to local neural embeddings: Qwen/Qwen3-Embedding-0.6B-GGUF served by a local llama.cpp OpenAI-compatible endpoint started automatically by `abra up` and managed directly with `abra models up/status` when troubleshooting. Qwen/Qwen3-Reranker-0.6B can be configured when a compatible rerank endpoint is available. Custom providers are supported and replace the local defaults when configured.

Local embedding calls default to a 10-minute provider timeout because CPU-backed model requests can be slower than normal API calls on large files. Custom providers default to 30 seconds and can be changed with `EMBEDDING_TIMEOUT`. Local neural setup writes `ABRA_AI_PROVIDER_CONCURRENCY=1`; compatible providers write `ABRA_AI_PROVIDER_CONCURRENCY=4`. `abra doctor` warns when this value is invalid or when a single local model runner is configured for multiple concurrent provider calls.

## 3-Minute Local Flow

For OSS users, install the latest published release binary from GitHub releases:

```sh
curl -fsSL https://github.com/hermawan22/abra/releases/latest/download/install.sh | sh
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
abra config model openai --api-key-stdin
abra config model compatible --base-url https://api.example.com/v1 --model embedding-model --dimensions 1024
```

For non-interactive local setup, use `abra setup --yes`. For authenticated compatible providers during onboarding, use `printf '%s' "$PROVIDER_API_KEY" | abra setup --compatible --embedding-base-url https://api.example.com/v1 --embedding-model embedding-model --api-key-stdin`. For OpenAI, non-interactive setup requires `--api-key-stdin` or `OPENAI_API_KEY`.
Use `abra setup --yes --no-models` only when you intentionally manage the embedding endpoint yourself; otherwise the default local provider is started for you by setup or `abra up`.

Generate repo-local AI agent instruction files after setup:

```sh
abra agents init --agent codex
```

This writes `AGENTS.md` with the exact Abra scope and `CLAUDE.md` as an import for tools that read Claude Code instructions.
After ingesting the project with the exact scope printed by `abra scope`, `abra agents verify . --scope <scope-from-abra-scope> --agent <agent>` checks both files, validates the MCP endpoint, calls `discover_scopes` with that exact project scope, and confirms `working_memory_compose` returns source-backed context for the selected agent profile. `abra agents ready` is a non-mutating alias for the same check. Both commands print a ready prompt and next steps for the AI client; `--json` returns `server_ready`, `client_ready`, `ready_prompt`, and `next_steps` for automation. The compose call runs in diagnostic mode, so verification does not write compose audit events or automatic learning proposals. If a coding agent says Abra has no context, run this before changing prompts or env files. If verification is ready but the agent still says there is no context, fully restart that AI client so it reloads MCP config and token environment, then rerun the same `abra agents verify ... --scope ... --agent <agent>` command.
For CI or release checks that should not contact a live MCP server, run
`abra agents verify --files-only --strict`.

After changing model config, restart the stack:

```sh
abra down
abra up
abra status
```

For local-runner troubleshooting, use `abra models status` and `abra models up` directly. These commands manage only the built-in local Qwen runner, publish it on `127.0.0.1` by default, use Docker pull policy `missing`, and recreate the container when runner-relevant model, dimension, port, cache, publish, image, pull, pooling, or context settings change. Production local embeddings require `ABRA_LOCAL_EMBEDDING_IMAGE` to be an operator-verified `@sha256` image reference; otherwise use `EMBEDDING_PROVIDER=compatible`. When `EMBEDDING_PROVIDER=compatible`, model commands report the local runner as inactive unless `--force` is passed.

Use these defaults for the remaining commands:

```sh
export ABRA_BASE_URL=http://localhost:18080
export ABRA_API_TOKEN=dev-token
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

The manual recovery path is:

```sh
abra scope
abra ingest . --code --scope <scope-from-abra-scope>
abra agents verify . --scope <scope-from-abra-scope>
```

Then tell the agent: `Use Abra MCP first. Exact scope: repo:<project>. Call
discover_scopes with expected_scope="repo:<project>", then call
working_memory_compose with task=<current task>, scope="repo:<project>", and
agent="codex" before answering or changing code. If discover_scopes does not
show repo:<project> or working_memory_compose returns no source-backed context,
run abra scope, ingest the project with that exact scope, rerun abra agents
verify, then retry with this exact scope.`

`abra scope` also prints the exact `abra agents bootstrap`, `abra agents init`,
`abra ingest`, and `abra agents verify` commands for the current project. Use
those printed commands when Codex or another AI client says Abra has no context;
the usual cause is that the agent queried a different scope than the one used
during ingestion.

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
| ingest one document | `abra ingest --text "source-backed content"` |
| ingest local repo directly from the CLI | `abra ingest . --code --scope <scope-from-abra-scope>` |
| ingest local repo and keep going after per-file failures | `abra ingest . --code --continue-on-error --scope <scope-from-abra-scope>` |
| suppress direct local ingest progress | `abra ingest . --code --quiet --scope <scope-from-abra-scope>` |
| ingest remote git | `abra ingest --git https://github.com/owner/repo.git --ref main --code --scope repo:owner-repo --wait --wait-timeout 10m` |
| list sources | `abra sources` |
| list jobs | `abra jobs` |
| capture raw observation | `abra observe "Agents should rerun release checks" --scope repo:demo` |
| capture and propose observation | `abra observe "Agents should rerun release checks" --scope repo:demo --propose --source-url file://release-runbook.md` |
| list observations | `abra observations --scope repo:demo --query release` |
| propose existing observation | `abra observations propose <observation-id> --scope repo:demo --claim "Agents should rerun release checks." --source-url file://release-runbook.md` |
| think | `abra think "question" --scope <scope-from-abra-scope>` |
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
abra observations --scope repo:demo --query release
abra observations propose <observation-id> --scope repo:demo --claim "Agents should rerun release checks before tagging." --source-url file://release-runbook.md
```

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
  --wait
```

The configured MCP tool must return normalized Abra documents as JSON, either as
`structuredContent.documents` or as a JSON text content item containing
`{"documents":[...]}`.
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

Start Postgres, run migrations, then start API and worker:

```sh
docker compose --env-file .env.production up -d postgres
docker compose --env-file .env.production run --rm migrate
docker compose --env-file .env.production up -d api worker
```

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
