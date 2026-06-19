# CLI Guide

Abra is terminal-first: install the `abra` command, start the service from the terminal, and operate it through the CLI, HTTP, or MCP.

The quickstart path defaults to local neural embeddings: Qwen/Qwen3-Embedding-0.6B-GGUF served by a local llama.cpp OpenAI-compatible endpoint managed by `abra models up`. Qwen/Qwen3-Reranker-0.6B can be configured when a compatible rerank endpoint is available. Custom providers are supported and replace the local defaults when configured.

Local embedding calls default to a 10-minute provider timeout because CPU-backed model requests can be slower than normal API calls on large files. Custom providers default to 30 seconds and can be changed with `EMBEDDING_TIMEOUT`.

## 3-Minute Local Flow

Install the CLI from this checkout:

```sh
./scripts/install.sh
```

Install from GitHub releases:

```sh
curl -fsSL https://raw.githubusercontent.com/hermawan22/abra/main/scripts/install.sh | sh
```

Release downloads are verified against `SHA256SUMS` before the binary is installed. If GitHub CLI is available, the installer also verifies GitHub Artifact Attestations automatically. Set `ABRA_VERIFY_ATTESTATION=1` to require provenance verification, `ABRA_VERSION=vX.Y.Z` to install a specific release, or `ABRA_ALLOW_SOURCE_BUILD=1` to intentionally build from the release source tag when no platform asset exists.

Run the guided first-run setup:

```sh
abra setup
```

`abra setup` checks prerequisites, creates an env file, asks which embedding provider to use, can start the built-in local Qwen embedding runner, and can start Postgres, migrations, API, and worker. From a source checkout it uses `.tmp/quickstart.env`; from a global CLI install it stores runtime files under your Abra config directory and can be run from any folder. `abra install` is kept as a compatibility alias for `abra setup`; the curl script installs the CLI binary.

If setup finishes but ingest or Codex still cannot use Abra, run `abra doctor` before changing config by hand. Doctor separates runtime env issues, worker interval problems, API/MCP readiness, Codex token-env visibility, model config, and local model readiness. With the default local provider, `abra models status` shows whether the embedding endpoint is serving requests, and `abra models up` starts or repairs it.

View or change the important runtime config without opening the env file:

```sh
abra config show
abra config path
abra config model local
abra config model openai --api-key-stdin
abra config model compatible --base-url https://api.example.com/v1 --model embedding-model --dimensions 1024
```

For non-interactive local setup, use `abra setup --yes`. For authenticated compatible providers during onboarding, use `printf '%s' "$PROVIDER_API_KEY" | abra setup --compatible --base-url https://api.example.com/v1 --embedding-model embedding-model --api-key-stdin`.

Generate repo-local AI agent instruction files after setup:

```sh
abra agents init --agent codex
abra agents verify
```

This writes `AGENTS.md` with the exact Abra scope and `CLAUDE.md` as an import for tools that read Claude Code instructions.
`abra agents verify` checks both files, validates the MCP endpoint, and calls `discover_scopes` with the exact project scope. If a coding agent says Abra has no context, run this before changing prompts or env files.

After changing model config, restart the stack:

```sh
abra models up
abra down
abra up
abra status
```

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
abra ingest . --code --scope repo:my-app
```

`abra ingest .` reads the checkout from the CLI process, so it works even when
the API and worker run in Docker and cannot see your local path. Use `--tracked`
only when the worker can read the same path and you want a durable source config
plus ingestion job.

Queue a remote Git repo through the worker:

```sh
abra ingest --git https://github.com/owner/repo.git --ref main --code --scope repo:owner-repo --wait --wait-timeout 10m
```

Ask Abra to think with governed memory:

```sh
abra think "What should agents use before autonomous code changes?" --scope repo:my-app
```

Check runtime status:

```sh
abra status
abra doctor
```

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
the launching shell. In each project, ask Abra for the exact scope before
prompting an AI agent:

```sh
abra scope
```

Then tell the agent: `Use Abra MCP first. Exact scope: repo:<project>. Call
discover_scopes with expected_scope="repo:<project>", then call
working_memory_compose with that exact scope before answering or changing code.
If discover_scopes does not show repo:<project>, run abra scope and ingest the
project with that exact scope.`

Stop the local stack:

```sh
abra down
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
| install CLI from published release | `curl -fsSL https://raw.githubusercontent.com/hermawan22/abra/main/scripts/install.sh \| sh` |
| guided first-run setup | `abra setup` |
| generate agent instruction files | `abra agents init --agent codex` |
| verify agent context setup | `abra agents verify` |
| start local Qwen embedding runner | `abra models up` |
| check local embedding runner | `abra models status` |
| start local stack | `abra up` |
| init env only | `abra init` |
| compatibility setup alias | `abra install` |
| ingest one document | `abra ingest --text "source-backed content"` |
| ingest local repo directly from the CLI | `abra ingest . --code --scope repo:my-app` |
| ingest remote git | `abra ingest --git https://github.com/owner/repo.git --ref main --code --scope repo:owner-repo --wait --wait-timeout 10m` |
| list sources | `abra sources` |
| list jobs | `abra jobs` |
| think | `abra think "question" --scope repo:my-app` |
| print project scope for agents | `abra scope` |
| status | `abra status` |
| doctor | `abra doctor` |
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

For worker-based source refreshes, use `abra ingest . --code --tracked`, `abra watch local --path . --wait --wait-timeout 10m`,
or `abra watch git --git https://github.com/owner/repo.git --wait --wait-timeout 10m`.
Tracked local sources require the worker process to see the same filesystem path;
use direct `abra ingest . --code` for ordinary Docker-backed local setup.
When `--code` is enabled and no `--code-include` is supplied, Abra includes supported
Go, JavaScript, TypeScript, and React code files repo-wide while skipping common
dependency, build, cache, and vendor directories.
For event-based ingestion, send normalized documents to `POST /ingest/webhooks`
from your connector or automation. The core OSS worker schedules `local_repo`,
`git_repo`, and markdown source configs. Other source systems should use a thin
connector overlay that listens to source events and posts documents into Abra.

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
