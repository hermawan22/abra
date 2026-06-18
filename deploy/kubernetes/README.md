# Kubernetes Deployment Example

These manifests are generic production examples. Replace image, secrets, namespace, and ingress according to your platform.

This path is for operators who want explicit YAML. The Helm chart in `deploy/helm` packages the same roles for chart-based installs.

Apply order:

1. Provision Postgres with `pgvector`; managed Postgres is recommended.
2. Build and push the Abra image, then replace `abra:local` in the manifests.
3. Create `abra-secrets` with database URL, API keys, and optional embedding/reranker provider credentials.
4. Apply `configmap.yaml`.
5. Delete any previous `abra-migrate` Job, run `job-migrate.yaml`, and confirm it completes.
6. Deploy `deployment-api.yaml`.
7. Deploy `deployment-worker.yaml`.
8. Apply `service.yaml`.
9. Expose the service only on an internal network.

Example migration flow:

```sh
kubectl delete job abra-migrate --ignore-not-found
kubectl apply -f deploy/kubernetes/job-migrate.yaml
kubectl wait --for=condition=complete job/abra-migrate --timeout=120s
```

Required secret keys:

```text
DATABASE_URL
ABRA_API_KEYS
ABRA_WEBHOOK_SECRETS
ABRA_AUDIT_SINK_TOKEN
ABRA_AUDIT_SINK_SECRET
EMBEDDING_BASE_URL
EMBEDDING_API_KEY
RERANKER_BASE_URL
RERANKER_API_KEY
```

The default config uses `EMBEDDING_PROVIDER=compatible`, `EMBEDDING_MODEL=embedding-model`, `EMBEDDING_DIMENSIONS=1024`, `RERANKER_PROVIDER=""`, `ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false`, `ABRA_APPROVAL_MODE=enforce`, `RATE_LIMIT_MAX=120`, `RATE_LIMIT_WINDOW=1 minute`, `ABRA_COMPOSE_HEALTH_CACHE_TTL=2s`, `ABRA_GIT_CACHE_DIR=/tmp/abra-git-cache`, `ABRA_GIT_CLONE_DEPTH=1`, and tracing disabled. Keep `ABRA_APPROVAL_MODE=enforce` before exposing write-capable credentials to autonomous agents. Keep the built-in Postgres-backed rate limit enabled; after migrations are applied it is shared across replicated API pods. Add ingress or gateway rate limits for defense in depth, and tune limits for expected agent concurrency and ingestion traffic. Set `ABRA_COMPOSE_HEALTH_CACHE_TTL=0s` only when every working-memory compose call must run a fresh scoped health aggregate. Set `OTEL_EXPORTER_OTLP_ENDPOINT` and `ABRA_TRACING_SAMPLE_RATIO` to enable optional OpenTelemetry tracing. Set `ABRA_WEBHOOK_SECRETS` when connector overlays use `POST /ingest/webhooks`. Set `ABRA_AUDIT_SINK_URL`, `ABRA_AUDIT_SINK_TOKEN`, and `ABRA_AUDIT_SINK_SECRET` when the worker should push signed audit NDJSON to a SIEM endpoint. Use any compatible embedding provider if it returns vectors with the configured dimensions. Use `EMBEDDING_PROVIDER=local` only when your cluster can reach self-hosted Qwen-compatible local embedding and reranker endpoints. For `git_repo` source configs, mount Git credentials through the platform layer and keep repository tokens out of config maps and prompts.

Operational notes:

- Run exactly one migration job per release. The example uses a fixed Job name, so delete the old Job before applying it for the next release.
- Run one or more API replicas.
- Run one worker replica unless the worker implementation gains lease coordination.
- Keep `/mcp`, recall, ingestion, graph, and source-config endpoints behind API-key auth.
- Abra does not ship a browser UI; operate it through the CLI, API, MCP, metrics, and runbooks.
- Put network policy, TLS termination, and ingress/gateway rate limits in the platform layer.
