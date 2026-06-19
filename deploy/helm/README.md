# Helm Chart

This directory contains the v1 Helm chart for self-hosting Abra on Kubernetes.

The chart packages:

- migration job running `/app/abra-migrate`
- API deployment running `/app/abra-api`
- worker deployment running `/app/abra-worker`
- ClusterIP service
- config map
- references to an existing secret
- optional ingress

## Install

Create a secret first. Use your platform secret manager in production; this direct command is only the raw Kubernetes shape:

```sh
kubectl create secret generic abra-secrets \
  --from-literal=DATABASE_URL='postgres://...' \
  --from-literal=ABRA_API_KEYS='replace-with-generated-token' \
  --from-literal=ABRA_WEBHOOK_SECRETS='replace-with-webhook-signing-secret' \
  --from-literal=ABRA_AUDIT_SINK_TOKEN='replace-with-siem-bearer-token' \
  --from-literal=ABRA_AUDIT_SINK_SECRET='replace-with-siem-signing-secret' \
  --from-literal=EMBEDDING_BASE_URL='https://embedding-provider.example/v1' \
  --from-literal=EMBEDDING_API_KEY='' \
  --from-literal=RERANKER_BASE_URL='' \
  --from-literal=RERANKER_API_KEY=''
```

Render and inspect:

```sh
helm template abra ./deploy/helm
```

Install or upgrade:

```sh
helm upgrade --install abra ./deploy/helm \
  --set image.repository=ghcr.io/your-org/abra \
  --set image.tag=0.3.7
```

## Values

Important values:

```yaml
image:
  repository: ghcr.io/your-org/abra
  tag: 0.3.7

secrets:
  existingSecret: abra-secrets
  keys:
    webhookSecrets: ABRA_WEBHOOK_SECRETS
    auditSinkToken: ABRA_AUDIT_SINK_TOKEN
    auditSinkSecret: ABRA_AUDIT_SINK_SECRET

config:
  bindAddress: "0.0.0.0"
  embeddingProvider: compatible
  embeddingModel: embedding-model
  embeddingDimensions: "1024"
  apiReadTimeout: 2m
  maxRequestBodyBytes: "26214400"
  rerankerProvider: ""
  rerankerModel: ""
  allowUnsignedWebhooksInProduction: "false"
  allowLocalEmbeddingsInProduction: "false"
  approvalMode: enforce
  auditSinkUrl: ""
  auditSinkScope: ""
  auditSinkBatchSize: "100"
  rateLimitMax: "120"
  rateLimitWindow: 1 minute
  aiProviderConcurrency: "4"
  composeHealthCacheTtl: 2s
  composeRecallConcurrency: "1"
  composeGraphConcurrency: "4"
  otelExporterOtlpEndpoint: ""
  tracingEnabled: "false"
  tracingSampleRatio: "1"
  tracingInsecure: "false"
  serviceName: abra
  deploymentEnvironment: production

worker:
  interval: 5m
  maxSourcesPerRun: "25"
  concurrency: "1"
  gitCacheDir: /tmp/abra-git-cache
  gitCloneDepth: "1"

migrate:
  enabled: true
  activeDeadlineSeconds: 600
  ttlSecondsAfterFinished: 86400
  hookDeletePolicy: before-hook-creation,hook-succeeded
```

## Rules

- Do not create a database by default; production should use managed Postgres with `pgvector`.
- Do not embed secret literals in values files.
- Run migrations as Helm pre-install/pre-upgrade hooks with a delete policy or unique job names so migrations run once on every release.
- Keep Abra internal-only by default.
- Keep `config.bindAddress="0.0.0.0"` for containerized API pods and restrict exposure through the Service, Ingress, gateway, or network policy layers.
- Keep `config.approvalMode=enforce` before exposing write-capable credentials to autonomous agents.
- Keep `ABRA_WEBHOOK_SECRETS` present in the existing secret. The chart requires it by default for the migration, API, and worker pods; set `config.allowUnsignedWebhooksInProduction="true"` only when webhook ingestion is disabled or an upstream gateway verifies webhook signatures.
- `config.embeddingProvider=local` means self-hosted Qwen-compatible neural retrieval. Set `config.embeddingProvider=compatible` plus the embedding secret values to replace it with any custom provider. Set `config.rerankerProvider` only when a reranker endpoint is available.
- Keep `config.aiProviderConcurrency=1` for a single local model runner. Raise it only when the embedding or reranker provider is horizontally scaled and latency/error metrics show headroom.
- Keep `worker.concurrency=1` for the default local Qwen runner. Raise it up to `32` only after provider capacity and database pool usage show headroom; same-source jobs are serialized within one worker process.
- Keep Abra's built-in Postgres-backed rate limit enabled with `config.rateLimitMax` and `config.rateLimitWindow`; it applies across replicated API pods after migrations are applied. Add ingress or gateway rate limits for defense in depth on exposed deployments.
- Set `config.composeHealthCacheTtl=0s` only when every working-memory compose call must run a fresh scoped health aggregate.
- Keep `config.composeRecallConcurrency` and `config.composeGraphConcurrency` at conservative values until database pool usage and memory-compose p95 have been measured under expected agent traffic. Values must be between `1` and `32`.
- Set `config.otelExporterOtlpEndpoint` to enable optional OpenTelemetry tracing. Keep `config.tracingSampleRatio` below `1` in high-throughput deployments unless you are debugging a short window.
- Set `worker.gitCacheDir` and `worker.gitCloneDepth` for `git_repo` source configs. Keep credentials outside values files and provide them through platform-managed SSH keys or credential helpers.
- Preserve the generic embedding provider contract.
- Keep graph relationships in Postgres; do not add Neo4j dependencies to the chart.
