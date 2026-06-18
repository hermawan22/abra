# CLI Guide

Abra is CLI-only: install the `abra` command, start the service from the terminal, and operate it through CLI, HTTP, or MCP.

The quickstart path uses local deterministic embeddings for evaluation. Production deployments should use compatible external embeddings, scoped credentials, and approval enforcement.

## 3-Minute Local Flow

Install the CLI from this checkout:

```sh
./scripts/install.sh
```

After the public GitHub repository exists, the remote installer is:

```sh
curl -fsSL https://raw.githubusercontent.com/hermawan22/abra/main/scripts/install.sh | sh
```

The remote URL only works after `hermawan22/abra` is published with a `main` branch. Release downloads are verified against `SHA256SUMS` before the binary is installed. Set `ABRA_VERSION=v0.1.0` to install a specific release.

Bootstrap the local stack:

```sh
abra install
```

`abra install` creates `.tmp/quickstart.env`, starts Postgres, runs migrations, and starts the API and worker.

Use these defaults for the remaining commands:

```sh
export ABRA_BASE_URL=http://localhost:18080
export ABRA_API_TOKEN=dev-token
export ABRA_SCOPE=repo:demo
```

Ingest a demo document:

```sh
abra ingest --scope "$ABRA_SCOPE" \
  --title Intro \
  --source-url file://intro.md \
  --text "Agents should use Abra before autonomous code changes."
```

Ask Abra to think with governed memory:

```sh
abra think --scope "$ABRA_SCOPE" "What should agents use before autonomous code changes?"
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
abra upgrade --version v0.1.0
abra uninstall --yes
```

## From Source

From source, run the Go CLI directly:

```sh
go run ./cmd/abra install
```

For repeated local use, build a binary:

```sh
go build -o .tmp/abra ./cmd/abra
.tmp/abra install
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
| bootstrap stack | `abra install` |
| init env only | `abra init` |
| up | `abra up` |
| ingest | `abra ingest --scope "$ABRA_SCOPE" --text "source-backed content"` |
| think | `abra think --scope "$ABRA_SCOPE" "question"` |
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
