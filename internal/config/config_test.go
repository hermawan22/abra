package config

import (
	"testing"
	"time"
)

func TestLoadAcceptsApprovalModeEnforce(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("ABRA_APPROVAL_MODE", "enforce")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ApprovalMode != "enforce" {
		t.Fatalf("ApprovalMode = %q, want enforce", cfg.ApprovalMode)
	}
}

func TestLoadRejectsInvalidApprovalMode(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("ABRA_APPROVAL_MODE", "strict")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid approval mode error")
	}
}

func TestLoadRejectsLocalNeuralEmbeddingsInProductionWithoutOverride(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	t.Setenv("ABRA_API_KEYS", "unit-production-secret-alpha")
	t.Setenv("ABRA_WEBHOOK_SECRETS", "webhook-secret-production-123")
	t.Setenv("EMBEDDING_PROVIDER", "local")

	if _, err := Load(); err == nil {
		t.Fatal("expected local production embeddings to require explicit override")
	}
}

func TestLoadAllowsLocalNeuralEmbeddingsInProductionWithOverride(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	t.Setenv("ABRA_API_KEYS", "unit-production-secret-alpha")
	t.Setenv("ABRA_WEBHOOK_SECRETS", "webhook-secret-production-123")
	t.Setenv("EMBEDDING_PROVIDER", "local")
	t.Setenv("ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Embedding.Provider != "local" {
		t.Fatalf("Embedding.Provider = %q, want local", cfg.Embedding.Provider)
	}
	if cfg.Embedding.Dimensions != 1024 {
		t.Fatalf("Embedding.Dimensions = %d, want 1024", cfg.Embedding.Dimensions)
	}
}

func TestLoadDefaultsToLocalQwenCompatibleEndpoints(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	t.Setenv("ABRA_API_KEYS", "unit-production-secret-alpha")
	t.Setenv("ABRA_WEBHOOK_SECRETS", "webhook-secret-production-123")
	t.Setenv("ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Embedding.Provider != "local" {
		t.Fatalf("Embedding.Provider = %q, want local", cfg.Embedding.Provider)
	}
	if cfg.Embedding.BaseURL != "http://host.docker.internal:8080/v1" {
		t.Fatalf("Embedding.BaseURL = %q", cfg.Embedding.BaseURL)
	}
	if cfg.Embedding.Model != "Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0" {
		t.Fatalf("Embedding.Model = %q", cfg.Embedding.Model)
	}
	if cfg.Embedding.Timeout != 10*time.Minute {
		t.Fatalf("Embedding.Timeout = %s, want 10m", cfg.Embedding.Timeout)
	}
	if cfg.Reranker.Provider != "" {
		t.Fatalf("Reranker.Provider = %q, want empty", cfg.Reranker.Provider)
	}
	if cfg.Reranker.BaseURL != "" {
		t.Fatalf("Reranker.BaseURL = %q", cfg.Reranker.BaseURL)
	}
	if cfg.WorkerSourceTimeout != 30*time.Minute {
		t.Fatalf("WorkerSourceTimeout = %s, want 30m", cfg.WorkerSourceTimeout)
	}
	if cfg.WorkerLeaseTimeout != 35*time.Minute {
		t.Fatalf("WorkerLeaseTimeout = %s, want 35m", cfg.WorkerLeaseTimeout)
	}
}

func TestLoadAllowsLocalEmbeddingsOutsideProductionByDefault(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("NODE_ENV", "development")
	t.Setenv("EMBEDDING_PROVIDER", "local")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NodeEnv != "development" {
		t.Fatalf("NodeEnv = %q, want development", cfg.NodeEnv)
	}
	if cfg.Embedding.Provider != "local" {
		t.Fatalf("Embedding.Provider = %q, want local", cfg.Embedding.Provider)
	}
	if cfg.AllowLocalEmbeddingsInProduction {
		t.Fatal("AllowLocalEmbeddingsInProduction should default to false")
	}
}

func TestLoadAllowsExternalEmbeddingsInProductionWithoutOverride(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	t.Setenv("ABRA_API_KEYS", "unit-production-secret-alpha")
	t.Setenv("ABRA_WEBHOOK_SECRETS", "webhook-secret-production-123")
	t.Setenv("EMBEDDING_PROVIDER", "compatible")
	t.Setenv("EMBEDDING_BASE_URL", "https://embedding.example/v1")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Reranker.Provider != "" {
		t.Fatalf("Reranker.Provider = %q, want empty for custom embedding provider", cfg.Reranker.Provider)
	}
	if cfg.Embedding.Timeout != 30*time.Second {
		t.Fatalf("Embedding.Timeout = %s, want 30s", cfg.Embedding.Timeout)
	}
}

func TestLoadEmbeddingTimeoutOverride(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("EMBEDDING_TIMEOUT", "3m")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Embedding.Timeout != 3*time.Minute {
		t.Fatalf("Embedding.Timeout = %s, want 3m", cfg.Embedding.Timeout)
	}
}

func TestLoadRejectsInvalidEmbeddingTimeout(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("EMBEDDING_TIMEOUT", "45m")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid embedding timeout error")
	}
}

func TestLoadRejectsInvalidEmbeddingDimensions(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("EMBEDDING_DIMENSIONS", "abc")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid embedding dimensions error")
	}
}

func TestLoadRejectsInvalidBoolean(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("ABRA_TRACING_ENABLED", "sometimes")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid boolean error")
	}
}

func TestLoadRejectsInvalidRedactPIIBoolean(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("REDACT_PII", "sometimes")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid REDACT_PII boolean error")
	}
}

func TestLoadRejectsInvalidPort(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("PORT", "70000")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid port error")
	}
}

func TestLoadRejectsIncompleteExternalEmbeddingsInProduction(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	t.Setenv("ABRA_API_KEYS", "unit-production-secret-alpha")
	t.Setenv("ABRA_WEBHOOK_SECRETS", "webhook-secret-production-123")
	t.Setenv("EMBEDDING_PROVIDER", "compatible")

	if _, err := Load(); err == nil {
		t.Fatal("expected missing production embedding config to be rejected")
	}
}

func TestLoadComposeHealthCacheTTL(t *testing.T) {
	allowUnauthenticatedDev(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ComposeHealthCacheTTL != 2*time.Second {
		t.Fatalf("default ComposeHealthCacheTTL = %s, want 2s", cfg.ComposeHealthCacheTTL)
	}

	t.Setenv("ABRA_COMPOSE_HEALTH_CACHE_TTL", "750ms")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ComposeHealthCacheTTL != 750*time.Millisecond {
		t.Fatalf("ComposeHealthCacheTTL = %s, want 750ms", cfg.ComposeHealthCacheTTL)
	}

	t.Setenv("ABRA_COMPOSE_HEALTH_CACHE_TTL", "0s")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ComposeHealthCacheTTL != 0 {
		t.Fatalf("ComposeHealthCacheTTL = %s, want disabled", cfg.ComposeHealthCacheTTL)
	}
}

func TestLoadRejectsInvalidComposeHealthCacheTTL(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("ABRA_COMPOSE_HEALTH_CACHE_TTL", "2m")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid compose health cache ttl error")
	}
}

func TestLoadComposeConcurrencyLimits(t *testing.T) {
	allowUnauthenticatedDev(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ComposeRecallConcurrency != 1 || cfg.ComposeGraphConcurrency != 4 {
		t.Fatalf("default compose concurrency = recall:%d graph:%d", cfg.ComposeRecallConcurrency, cfg.ComposeGraphConcurrency)
	}

	t.Setenv("ABRA_COMPOSE_RECALL_CONCURRENCY", "2")
	t.Setenv("ABRA_COMPOSE_GRAPH_CONCURRENCY", "1")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ComposeRecallConcurrency != 2 || cfg.ComposeGraphConcurrency != 1 {
		t.Fatalf("compose concurrency = recall:%d graph:%d", cfg.ComposeRecallConcurrency, cfg.ComposeGraphConcurrency)
	}
}

func TestLoadRejectsInvalidComposeConcurrencyLimits(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("ABRA_COMPOSE_RECALL_CONCURRENCY", "0")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid recall concurrency error")
	}
	t.Setenv("ABRA_COMPOSE_RECALL_CONCURRENCY", "4")
	t.Setenv("ABRA_COMPOSE_GRAPH_CONCURRENCY", "33")
	if _, err := Load(); err == nil {
		t.Fatal("expected invalid graph concurrency error")
	}
}

func TestLoadGitSourceConfig(t *testing.T) {
	allowUnauthenticatedDev(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitCacheDir != "/tmp/abra-git-cache" {
		t.Fatalf("GitCacheDir = %q", cfg.GitCacheDir)
	}
	if cfg.GitCloneDepth != 1 {
		t.Fatalf("GitCloneDepth = %d, want 1", cfg.GitCloneDepth)
	}

	t.Setenv("ABRA_GIT_CACHE_DIR", "/var/cache/abra/git")
	t.Setenv("ABRA_GIT_CLONE_DEPTH", "5")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GitCacheDir != "/var/cache/abra/git" || cfg.GitCloneDepth != 5 {
		t.Fatalf("git source config = dir:%q depth:%d", cfg.GitCacheDir, cfg.GitCloneDepth)
	}
}

func TestLoadRejectsInvalidGitCloneDepth(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("ABRA_GIT_CLONE_DEPTH", "0")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid git clone depth error")
	}
}

func TestLoadRejectsLeaseTimeoutNotGreaterThanSourceTimeout(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("WORKER_SOURCE_TIMEOUT", "10m")
	t.Setenv("WORKER_LEASE_TIMEOUT", "10m")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid worker lease timeout error")
	}
}

func TestLoadTracingDefaultsDisabled(t *testing.T) {
	allowUnauthenticatedDev(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tracing.Enabled {
		t.Fatal("tracing should be disabled without an OTLP endpoint")
	}
	if cfg.Tracing.SampleRatio != 1 {
		t.Fatalf("Tracing.SampleRatio = %v, want 1", cfg.Tracing.SampleRatio)
	}
}

func TestLoadTracingFromEndpoint(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
	t.Setenv("ABRA_SERVICE_NAME", "abra-test")
	t.Setenv("ABRA_TRACING_SAMPLE_RATIO", "0.25")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Tracing.Enabled {
		t.Fatal("tracing should be enabled when an OTLP endpoint is configured")
	}
	if cfg.Tracing.Endpoint != "http://collector:4318" {
		t.Fatalf("Tracing.Endpoint = %q", cfg.Tracing.Endpoint)
	}
	if cfg.Tracing.ServiceName != "abra-test" {
		t.Fatalf("Tracing.ServiceName = %q", cfg.Tracing.ServiceName)
	}
	if cfg.Tracing.SampleRatio != 0.25 {
		t.Fatalf("Tracing.SampleRatio = %v, want 0.25", cfg.Tracing.SampleRatio)
	}
}

func TestLoadRejectsInvalidTracingSampleRatio(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("ABRA_TRACING_ENABLED", "true")
	t.Setenv("ABRA_OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
	t.Setenv("ABRA_TRACING_SAMPLE_RATIO", "2")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid tracing sample ratio error")
	}
}

func TestLoadRejectsNaNTracingSampleRatio(t *testing.T) {
	allowUnauthenticatedDev(t)
	t.Setenv("ABRA_TRACING_SAMPLE_RATIO", "NaN")

	if _, err := Load(); err == nil {
		t.Fatal("expected NaN tracing sample ratio error")
	}
}

func TestLoadRejectsMissingAPIKeysWithoutExplicitDevBypass(t *testing.T) {
	if _, err := Load(); err == nil {
		t.Fatal("expected missing API keys to be rejected")
	}
}

func TestLoadRejectsPlaceholderProductionAPIKeys(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	t.Setenv("ABRA_API_KEYS", "replace-with-generated-token")

	if _, err := Load(); err == nil {
		t.Fatal("expected placeholder production API key to be rejected")
	}
}

func TestLoadRejectsMissingProductionWebhookSecrets(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	t.Setenv("ABRA_API_KEYS", "unit-production-secret-alpha")
	t.Setenv("EMBEDDING_PROVIDER", "compatible")
	t.Setenv("EMBEDDING_BASE_URL", "https://embedding.example/v1")

	if _, err := Load(); err == nil {
		t.Fatal("expected missing production webhook secret to be rejected")
	}
}

func TestLoadRejectsPlaceholderProductionWebhookSecrets(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	t.Setenv("ABRA_API_KEYS", "unit-production-secret-alpha")
	t.Setenv("ABRA_WEBHOOK_SECRETS", "replace-with-webhook-signing-secret")
	t.Setenv("EMBEDDING_PROVIDER", "compatible")
	t.Setenv("EMBEDDING_BASE_URL", "https://embedding.example/v1")

	if _, err := Load(); err == nil {
		t.Fatal("expected placeholder production webhook secret to be rejected")
	}
}

func TestLoadAllowsExplicitUnsignedProductionWebhooks(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	t.Setenv("ABRA_API_KEYS", "unit-production-secret-alpha")
	t.Setenv("ABRA_ALLOW_UNSIGNED_WEBHOOKS_IN_PRODUCTION", "true")
	t.Setenv("EMBEDDING_PROVIDER", "compatible")
	t.Setenv("EMBEDDING_BASE_URL", "https://embedding.example/v1")

	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDefaultBindAddressDependsOnEnvironment(t *testing.T) {
	allowUnauthenticatedDev(t)
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BindAddress != "127.0.0.1" {
		t.Fatalf("development BindAddress = %q", cfg.BindAddress)
	}

	t.Setenv("NODE_ENV", "production")
	t.Setenv("ABRA_UNAUTHENTICATED_DEV", "")
	t.Setenv("ABRA_API_KEYS", "unit-production-secret-alpha")
	t.Setenv("ABRA_WEBHOOK_SECRETS", "webhook-secret-production-123")
	t.Setenv("EMBEDDING_PROVIDER", "compatible")
	t.Setenv("EMBEDDING_BASE_URL", "https://embedding.example/v1")
	cfg, err = Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BindAddress != "0.0.0.0" {
		t.Fatalf("production BindAddress = %q", cfg.BindAddress)
	}
}

func allowUnauthenticatedDev(t *testing.T) {
	t.Helper()
	t.Setenv("ABRA_UNAUTHENTICATED_DEV", "1")
}
