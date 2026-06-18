# CLI Guide

Abra is terminal-first: install the `abra` command, start the service from the terminal, and operate it through the CLI, HTTP, or MCP.

The quickstart path uses local deterministic embeddings for evaluation. Production deployments should use compatible external embeddings, scoped credentials, and approval enforcement.

## 3-Minute Local Flow

Install the CLI from this checkout:

```sh
./scripts/install.sh
```

Install from GitHub releases:

```sh
curl -fsSL https://raw.githubusercontent.com/hermawan22/abra/main/scripts/install.sh | sh
```

Release downloads are verified against `SHA256SUMS` before the binary is installed. Set `ABRA_VERSION=vX.Y.Z` to install a specific release.

Run the guided first-run setup:

```sh
abra setup
```

`abra setup` checks prerequisites, creates an env file, asks which embedding provider to use, and can start Postgres, migrations, API, and worker. From a source checkout it uses `.tmp/quickstart.env`; from a global CLI install it stores runtime files under your Abra config directory and can be run from any folder. `abra install` is kept as a compatibility alias for `abra setup`; the curl script installs the CLI binary.

View or change the important runtime config without opening the env file:

```sh
abra config show
abra config path
abra config model local
abra config model openai --api-key-stdin
abra config model compatible --base-url https://api.example.com/v1 --api-key-stdin --model embedding-model-1536
```

For non-interactive local setup, use `abra setup --yes`. For OpenAI-compatible embeddings during onboarding, use `printf '%s' "$OPENAI_API_KEY" | abra setup --openai --api-key-stdin`.

After changing model config, restart the stack:

```sh
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

Ingest local docs or repo files immediately from the CLI:

```sh
abra ingest . --code
```

Queue a remote Git repo through the worker:

```sh
abra ingest --git https://github.com/owner/repo.git --ref main --code --wait
```

Ask Abra to think with governed memory:

```sh
abra think "What should agents use before autonomous code changes?"
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
x-api-key: dev-token
```

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
| start local stack | `abra up` |
| init env only | `abra init` |
| compatibility setup alias | `abra install` |
| ingest one document | `abra ingest --text "source-backed content"` |
| ingest local repo | `abra ingest . --code` |
| ingest remote git | `abra ingest --git https://github.com/owner/repo.git --ref main --code --wait` |
| list sources | `abra sources` |
| list jobs | `abra jobs` |
| think | `abra think "question"` |
| status | `abra status` |
| doctor | `abra doctor` |
| version | `abra version` |
| upgrade | `abra upgrade` |
| uninstall CLI | `abra uninstall --yes` |
| mcp | `abra mcp > .tmp/abra.mcp.json` |
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

For worker-based source refreshes, use `abra watch local --path . --wait`
or `abra watch git --git https://github.com/owner/repo.git --wait`.
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

The remote MCP endpoint is `http://localhost:18080/mcp`.

Stop Compose services:

```sh
docker compose --env-file .env.production down
```
