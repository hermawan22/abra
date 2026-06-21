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
- baseline NetworkPolicy
- optional ServiceMonitor and PrometheusRule

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
image_ref="$(sed -n '1p' IMAGE_DIGEST)"
helm upgrade --install abra ./deploy/helm \
  --set image.repository=ghcr.io/hermawan22/abra \
  --set image.digest="${image_ref#*@}"
```

`IMAGE_DIGEST` is published with each GitHub release. The first line is the
digest-pinned GHCR image reference, such as
`ghcr.io/hermawan22/abra@sha256:...`. Verify it before promotion:

```sh
gh attestation verify --repo hermawan22/abra IMAGE_DIGEST
docker buildx imagetools inspect "$(sed -n '1p' IMAGE_DIGEST)"
gh attestation verify "oci://$(sed -n '1p' IMAGE_DIGEST)" --repo hermawan22/abra
```

## Values

Important values:

```yaml
image:
  repository: ghcr.io/hermawan22/abra
  tag: ""
  digest: sha256:...
  pullPolicy: IfNotPresent

api:
  resources:
    requests:
      cpu: 250m
      memory: 512Mi
      ephemeral-storage: 128Mi
    limits:
      cpu: "1"
      memory: 1Gi
      ephemeral-storage: 512Mi

worker:
  interval: 5m
  maxSourcesPerRun: "25"
  concurrency: "1"
  gitCacheDir: /var/cache/abra/git
  gitCacheSizeLimit: 1Gi
  gitCloneDepth: "1"
  resources:
    requests:
      cpu: 100m
      memory: 256Mi
      ephemeral-storage: 256Mi
    limits:
      cpu: 500m
      memory: 512Mi
      ephemeral-storage: 2Gi

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
  embeddingBatchMaxItems: "16"
  embeddingBatchMaxTokens: "6000"
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

migrate:
  enabled: true
  backoffLimit: 1
  activeDeadlineSeconds: 600
  ttlSecondsAfterFinished: 86400
  hookDeletePolicy: before-hook-creation,hook-succeeded
  resources:
    requests:
      cpu: 50m
      memory: 128Mi
      ephemeral-storage: 64Mi
    limits:
      cpu: 250m
      memory: 256Mi
      ephemeral-storage: 256Mi

networkPolicy:
  enabled: true
  apiIngress:
    - podSelector:
        matchLabels:
          app.kubernetes.io/part-of: abra
          abra.dev/api-client: "true"
  extraApiIngress: []

serviceMonitor:
  enabled: false
  interval: 30s
  scrapeTimeout: 10s
  path: /metrics
  bearerTokenSecret:
    name: abra-metrics-token
    key: token
  labels: {}

prometheusRule:
  enabled: false
  labels: {}
  rules:
    apiDown:
      enabled: true
      for: 5m
      severity: critical
    high5xxRate:
      enabled: true
      for: 10m
      severity: warning
      threshold: 0.05
    memoryComposeFailures:
      enabled: true
      for: 10m
      severity: warning
    aiProviderQueueing:
      enabled: true
      for: 10m
      severity: warning
      minWaitRate: 0.05
    ingestionStaleRunningJobs:
      enabled: true
      for: 15m
      severity: critical
    ingestionFailedJobs:
      enabled: true
      for: 15m
      severity: warning
```

## Rules

- Do not create a database by default; production should use managed Postgres with `pgvector`.
- Do not embed secret literals in values files.
- Use the first-party image `ghcr.io/hermawan22/abra` and set `image.digest` from the release `IMAGE_DIGEST` asset for production. Leave `image.tag` empty when a digest is set.
- Verify `IMAGE_DIGEST` and image provenance with GitHub Artifact Attestations before promotion. Treat missing SBOM/provenance or unsupported platforms as release blockers.
- Run migrations as Helm pre-install/pre-upgrade hooks with a delete policy or unique job names so migrations run once on every release.
- Keep Abra internal-only by default.
- Keep the rendered pod hardening controls: non-root UID/GID, `RuntimeDefault` seccomp, disabled service-account token automount, read-only root filesystem, dropped capabilities, resource requests/limits, and bounded writable `emptyDir` mounts.
- Keep `networkPolicy.enabled=true` for the baseline internal-only posture. Add `networkPolicy.extraApiIngress` entries for ingress gateways, Prometheus, or agent runtimes outside the release namespace, or replace it with an equivalent service-mesh policy.
- Enable `serviceMonitor.enabled` and `prometheusRule.enabled` only when the Prometheus Operator CRDs are installed in the cluster. Create `serviceMonitor.bearerTokenSecret.name` with a `token` value that is authorized for ops access to `GET /metrics`; the chart does not copy API keys into monitoring secrets.
- Add namespace Pod Security, internal ingress, gateway rate limits, and admission rules that require digest-pinned GHCR images in the target cluster.
- Keep `config.bindAddress="0.0.0.0"` for containerized API pods and restrict exposure through the Service, Ingress, gateway, or network policy layers.
- Keep `config.approvalMode=enforce` before exposing write-capable credentials to autonomous agents.
- Keep `ABRA_WEBHOOK_SECRETS` present in the existing secret. The chart requires it by default for the migration, API, and worker pods; set `config.allowUnsignedWebhooksInProduction="true"` only when webhook ingestion is disabled or an upstream gateway verifies webhook signatures.
- `config.embeddingProvider=local` means self-hosted Qwen-compatible neural retrieval. Set `config.embeddingProvider=compatible` plus the embedding secret values to replace it with any custom provider. Set `config.rerankerProvider` only when a reranker endpoint is available.
- Keep `config.aiProviderConcurrency=1` for a single local model runner. Raise it only when the embedding or reranker provider is horizontally scaled and latency/error metrics show headroom.
- Tune `config.embeddingBatchMaxItems` and `config.embeddingBatchMaxTokens` for provider request size. Use smaller values for local Qwen context-window reliability; raise them only for compatible providers with measured capacity.
- Keep `worker.concurrency=1` for the default local Qwen runner. Raise it up to `32` only after provider capacity and database pool usage show headroom; same-source jobs are serialized within one worker process.
- Keep Abra's built-in Postgres-backed rate limit enabled with `config.rateLimitMax` and `config.rateLimitWindow`; it applies across replicated API pods after migrations are applied. Add ingress or gateway rate limits for defense in depth on exposed deployments.
- Set `config.composeHealthCacheTtl=0s` only when every working-memory compose call must run a fresh scoped health aggregate.
- Keep `config.composeRecallConcurrency` and `config.composeGraphConcurrency` at conservative values until database pool usage and memory-compose p95 have been measured under expected agent traffic. Values must be between `1` and `32`.
- Set `config.otelExporterOtlpEndpoint` to enable optional OpenTelemetry tracing. Keep `config.tracingSampleRatio` below `1` in high-throughput deployments unless you are debugging a short window.
- Set `worker.gitCacheDir` and `worker.gitCloneDepth` for `git_repo` source configs. Keep credentials outside values files and provide them through platform-managed SSH keys or credential helpers.
- Preserve the generic embedding provider contract.
- Keep graph relationships in Postgres; do not add Neo4j dependencies to the chart.
