# Kubernetes Deployment Example

These manifests are generic production examples. Replace image, secrets, namespace, and ingress according to your platform.

This path is for operators who want explicit YAML. The Helm chart in `deploy/helm` packages the same roles for chart-based installs.

Apply order:

1. Provision Postgres with `pgvector`; managed Postgres is recommended.
2. Use a published Abra image, then replace the example tag in the manifests with a digest-pinned GHCR image such as `ghcr.io/hermawan22/abra@sha256:DIGEST`.
3. Create `abra-secrets` with database URL, API keys, and optional embedding/reranker provider credentials.
4. Apply `configmap.yaml`.
5. Delete any previous `abra-migrate` Job, run `job-migrate.yaml`, and confirm it completes.
6. Deploy `deployment-api.yaml`.
7. Deploy `deployment-worker.yaml`.
8. Apply `service.yaml`.
9. Apply `networkpolicy.yaml`; label approved gateway or agent pods with
   `app.kubernetes.io/part-of=abra` and `abra.dev/api-client=true`, or adjust
   the selector to your platform-owned gateway identity.
10. If the Prometheus Operator CRDs are installed, apply `servicemonitor.yaml`
    and `prometheusrule.yaml`.
11. Expose the service only on an internal network.

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

If you apply `servicemonitor.yaml`, also create an `abra-metrics-token` secret
with a `token` key containing an ops-authorized Abra API key. `/metrics` is
authenticated, and Prometheus will receive 401 responses without this token.

The default config uses `EMBEDDING_PROVIDER=compatible`, `EMBEDDING_MODEL=embedding-model`, `EMBEDDING_DIMENSIONS=1024`, `ABRA_EMBEDDING_BATCH_MAX_ITEMS=16`, `ABRA_EMBEDDING_BATCH_MAX_TOKENS=6000`, `RERANKER_PROVIDER=""`, `ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false`, `ABRA_APPROVAL_MODE=enforce`, `RATE_LIMIT_MAX=120`, `RATE_LIMIT_WINDOW=1m`, `ABRA_AI_PROVIDER_CONCURRENCY=4`, `ABRA_COMPOSE_HEALTH_CACHE_TTL=2s`, `ABRA_COMPOSE_RECALL_CONCURRENCY=1`, `ABRA_COMPOSE_GRAPH_CONCURRENCY=4`, `WORKER_MAX_SOURCES_PER_RUN=25`, `WORKER_CONCURRENCY=1`, `ABRA_GIT_CACHE_DIR=/var/cache/abra/git`, `ABRA_GIT_CLONE_DEPTH=1`, and tracing disabled. Keep `ABRA_APPROVAL_MODE=enforce` before exposing write-capable credentials to autonomous agents. Keep the built-in Postgres-backed rate limit enabled; after migrations are applied it is shared across replicated API pods. Add ingress or gateway rate limits for defense in depth, and tune limits for expected agent concurrency and ingestion traffic. Set `ABRA_COMPOSE_HEALTH_CACHE_TTL=0s` only when every working-memory compose call must run a fresh scoped health aggregate. Tune compose concurrency only after watching database pool usage and memory-compose p95 under expected agent traffic; values must stay between `1` and `32`. Tune `ABRA_AI_PROVIDER_CONCURRENCY` separately after watching embedding and reranker provider latency; keep it at `1` for a single local embedding runner and raise it only for scaled compatible provider endpoints. Tune `ABRA_EMBEDDING_BATCH_MAX_ITEMS` and `ABRA_EMBEDDING_BATCH_MAX_TOKENS` separately for provider request size; use smaller batches for local runner context-window reliability and larger batches only for measured compatible provider capacity. Tune `WORKER_CONCURRENCY` separately; same-source jobs are serialized within one worker process, and the default `1` is safest for a single local runner. Set `OTEL_EXPORTER_OTLP_ENDPOINT` and `ABRA_TRACING_SAMPLE_RATIO` to enable optional OpenTelemetry tracing. Set `ABRA_WEBHOOK_SECRETS` when connector overlays use `POST /ingest/webhooks`. Set `ABRA_AUDIT_SINK_URL`, `ABRA_AUDIT_SINK_TOKEN`, and `ABRA_AUDIT_SINK_SECRET` when the worker should push signed audit NDJSON to a SIEM endpoint. Use any compatible embedding provider if it returns vectors with the configured dimensions. Use `EMBEDDING_PROVIDER=local` only when your cluster can reach a self-hosted compatible local embedding endpoint; configure a reranker separately only when your deployment provides one. For `git_repo` source configs, mount Git credentials through the platform layer and keep repository tokens out of config maps and prompts.

Operational notes:

- Run exactly one migration job per release. The example uses a fixed Job name, so delete the old Job before applying it for the next release.
- Run one or more API replicas.
- Run one worker replica by default and raise `WORKER_CONCURRENCY` first for bounded job-level parallelism.
- Keep runtime hardening enabled: service account tokens are not mounted, pods run as UID/GID `10001`, containers use read-only root filesystems, `/tmp` is a bounded `emptyDir`, and the worker Git cache is a bounded `emptyDir` mounted at `/var/cache/abra/git`.
- Pin runtime images by digest, preferably from GHCR, and update the API, worker, and migration Job image fields together for each release.
- Keep `/mcp`, recall, ingestion, graph, and source-config endpoints behind API-key auth.
- Abra does not ship a browser UI; operate it through the CLI, API, MCP, metrics, and runbooks.
- Treat the included NetworkPolicy, ServiceMonitor, and PrometheusRule as a baseline. Keep TLS termination, gateway authentication/rate limits, namespace Pod Security, image-policy admission, and environment-specific network exceptions in the platform layer.
