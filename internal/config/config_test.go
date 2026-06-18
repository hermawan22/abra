package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadAcceptsApprovalModeEnforce(t *testing.T) {
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
	t.Setenv("ABRA_APPROVAL_MODE", "strict")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid approval mode error")
	}
}

func TestLoadRejectsLocalEmbeddingsInProductionByDefault(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	t.Setenv("ABRA_API_KEYS", "test-key")
	t.Setenv("EMBEDDING_PROVIDER", "local")

	_, err := Load()
	if err == nil {
		t.Fatal("expected local embeddings to be rejected in production")
	}
	if !strings.Contains(err.Error(), "ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=true") {
		t.Fatalf("error = %q, want local embeddings production guard", err)
	}
}

func TestLoadAllowsLocalEmbeddingsInProductionWhenExplicit(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	t.Setenv("ABRA_API_KEYS", "test-key")
	t.Setenv("EMBEDDING_PROVIDER", "local")
	t.Setenv("ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION", "true")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AllowLocalEmbeddingsInProduction {
		t.Fatal("AllowLocalEmbeddingsInProduction = false, want true")
	}
}

func TestLoadAllowsLocalEmbeddingsOutsideProductionByDefault(t *testing.T) {
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
	t.Setenv("ABRA_API_KEYS", "test-key")
	t.Setenv("EMBEDDING_PROVIDER", "compatible")
	t.Setenv("EMBEDDING_BASE_URL", "https://embedding.example/v1")
	t.Setenv("EMBEDDING_API_KEY", "embedding-key")

	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsIncompleteExternalEmbeddingsInProduction(t *testing.T) {
	t.Setenv("NODE_ENV", "production")
	t.Setenv("ABRA_API_KEYS", "test-key")
	t.Setenv("EMBEDDING_PROVIDER", "compatible")

	if _, err := Load(); err == nil {
		t.Fatal("expected missing production embedding config to be rejected")
	}
}

func TestLoadComposeHealthCacheTTL(t *testing.T) {
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
	t.Setenv("ABRA_COMPOSE_HEALTH_CACHE_TTL", "2m")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid compose health cache ttl error")
	}
}

func TestLoadGitSourceConfig(t *testing.T) {
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
	t.Setenv("ABRA_GIT_CLONE_DEPTH", "0")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid git clone depth error")
	}
}

func TestLoadTracingDefaultsDisabled(t *testing.T) {
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
	t.Setenv("ABRA_TRACING_ENABLED", "true")
	t.Setenv("ABRA_OTEL_EXPORTER_OTLP_ENDPOINT", "http://collector:4318")
	t.Setenv("ABRA_TRACING_SAMPLE_RATIO", "2")

	if _, err := Load(); err == nil {
		t.Fatal("expected invalid tracing sample ratio error")
	}
}
