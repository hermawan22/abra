package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type AIProviderConfig struct {
	Provider   string
	BaseURL    string
	APIKey     string
	Model      string
	Dimensions int
	Timeout    time.Duration
}

type AuditSinkConfig struct {
	URL       string
	Token     string
	Secret    string
	Scope     string
	BatchSize int
}

type TracingConfig struct {
	Enabled     bool
	ServiceName string
	Endpoint    string
	Insecure    bool
	SampleRatio float64
	Environment string
}

type Config struct {
	NodeEnv                            string
	BindAddress                        string
	DatabaseURL                        string
	Port                               string
	APIKeys                            []string
	AllowUnauthenticatedDev            bool
	WebhookSecrets                     []string
	AllowLocalEmbeddingsInProduction   bool
	RedactPII                          bool
	ApprovalMode                       string
	AuditSink                          AuditSinkConfig
	Tracing                            TracingConfig
	Embedding                          AIProviderConfig
	Reranker                           AIProviderConfig
	Extractor                          AIProviderConfig
	RateLimitMax                       int
	RateLimitWindow                    time.Duration
	WorkerInterval                     time.Duration
	WorkerSourceTimeout                time.Duration
	WorkerLeaseTimeout                 time.Duration
	WorkerMaxChangedDocumentsPerSource int
	ComposeHealthCacheTTL              time.Duration
	GitCacheDir                        string
	GitCloneDepth                      int
}

func Load() (Config, error) {
	_ = loadDotEnv(".env")
	embeddingProvider := env("EMBEDDING_PROVIDER", "local")
	defaultEmbeddingBaseURL := ""
	defaultEmbeddingTimeout := 30 * time.Second
	defaultWorkerSourceTimeout := 2 * time.Minute
	defaultWorkerLeaseTimeout := 5 * time.Minute
	if isLocalNeuralProvider(embeddingProvider) {
		defaultEmbeddingBaseURL = "http://host.docker.internal:8080/v1"
		defaultEmbeddingTimeout = 10 * time.Minute
		defaultWorkerSourceTimeout = 30 * time.Minute
		defaultWorkerLeaseTimeout = 35 * time.Minute
	}

	nodeEnv := env("NODE_ENV", "development")
	defaultBindAddress := "127.0.0.1"
	if nodeEnv == "production" {
		defaultBindAddress = "0.0.0.0"
	}
	cfg := Config{
		NodeEnv:                          nodeEnv,
		BindAddress:                      env("ABRA_BIND_ADDR", defaultBindAddress),
		DatabaseURL:                      env("DATABASE_URL", "postgres://abra:abra@localhost:5433/abra"),
		Port:                             env("PORT", "18080"),
		APIKeys:                          csvEnv("ABRA_API_KEYS"),
		AllowUnauthenticatedDev:          boolEnv("ABRA_UNAUTHENTICATED_DEV", false),
		WebhookSecrets:                   csvEnv("ABRA_WEBHOOK_SECRETS"),
		AllowLocalEmbeddingsInProduction: boolEnv("ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION", false),
		RedactPII:                        env("REDACT_PII", "true") != "false",
		ApprovalMode:                     strings.ToLower(env("ABRA_APPROVAL_MODE", "advisory")),
		AuditSink: AuditSinkConfig{
			URL:       os.Getenv("ABRA_AUDIT_SINK_URL"),
			Token:     os.Getenv("ABRA_AUDIT_SINK_TOKEN"),
			Secret:    os.Getenv("ABRA_AUDIT_SINK_SECRET"),
			Scope:     os.Getenv("ABRA_AUDIT_SINK_SCOPE"),
			BatchSize: intEnv("ABRA_AUDIT_SINK_BATCH_SIZE", 100),
		},
		Tracing: TracingConfig{
			Enabled:     boolEnv("ABRA_TRACING_ENABLED", false) || tracingEndpoint() != "",
			ServiceName: env("ABRA_SERVICE_NAME", "abra"),
			Endpoint:    tracingEndpoint(),
			Insecure:    boolEnv("ABRA_TRACING_INSECURE", boolEnv("OTEL_EXPORTER_OTLP_INSECURE", false)),
			SampleRatio: floatEnv("ABRA_TRACING_SAMPLE_RATIO", 1),
			Environment: env("ABRA_DEPLOYMENT_ENVIRONMENT", env("NODE_ENV", "development")),
		},
		Embedding: AIProviderConfig{
			Provider:   embeddingProvider,
			BaseURL:    env("EMBEDDING_BASE_URL", defaultEmbeddingBaseURL),
			APIKey:     os.Getenv("EMBEDDING_API_KEY"),
			Model:      env("EMBEDDING_MODEL", "Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0"),
			Dimensions: intEnv("EMBEDDING_DIMENSIONS", 1024),
			Timeout:    durationEnv("EMBEDDING_TIMEOUT", defaultEmbeddingTimeout),
		},
		Reranker: AIProviderConfig{
			Provider: os.Getenv("RERANKER_PROVIDER"),
			BaseURL:  os.Getenv("RERANKER_BASE_URL"),
			APIKey:   os.Getenv("RERANKER_API_KEY"),
			Model:    os.Getenv("RERANKER_MODEL"),
			Timeout:  durationEnv("RERANKER_TIMEOUT", 30*time.Second),
		},
		Extractor: AIProviderConfig{
			Provider: env("EXTRACTOR_PROVIDER", env("LLM_PROVIDER", "local")),
			BaseURL:  env("EXTRACTOR_BASE_URL", os.Getenv("LLM_BASE_URL")),
			APIKey:   env("EXTRACTOR_API_KEY", os.Getenv("LLM_API_KEY")),
			Model:    env("EXTRACTOR_MODEL", env("LLM_MODEL", "local-extractor")),
			Timeout:  durationEnv("EXTRACTOR_TIMEOUT", durationEnv("LLM_TIMEOUT", 30*time.Second)),
		},
		RateLimitMax:                       intEnv("RATE_LIMIT_MAX", 120),
		RateLimitWindow:                    durationEnv("RATE_LIMIT_WINDOW", time.Minute),
		WorkerInterval:                     durationEnv("WORKER_INTERVAL", 5*time.Minute),
		WorkerSourceTimeout:                durationEnv("WORKER_SOURCE_TIMEOUT", defaultWorkerSourceTimeout),
		WorkerLeaseTimeout:                 durationEnv("WORKER_LEASE_TIMEOUT", defaultWorkerLeaseTimeout),
		WorkerMaxChangedDocumentsPerSource: intEnv("WORKER_MAX_CHANGED_DOCUMENTS_PER_SOURCE", 100),
		ComposeHealthCacheTTL:              durationEnv("ABRA_COMPOSE_HEALTH_CACHE_TTL", 2*time.Second),
		GitCacheDir:                        env("ABRA_GIT_CACHE_DIR", "/tmp/abra-git-cache"),
		GitCloneDepth:                      intEnv("ABRA_GIT_CLONE_DEPTH", 1),
	}

	if len(cfg.APIKeys) == 0 && !cfg.AllowUnauthenticatedDev {
		return Config{}, errors.New("ABRA_API_KEYS is required; set ABRA_UNAUTHENTICATED_DEV=1 only for isolated local development")
	}
	if cfg.AllowUnauthenticatedDev && cfg.NodeEnv == "production" {
		return Config{}, errors.New("ABRA_UNAUTHENTICATED_DEV is not allowed when NODE_ENV=production")
	}
	if cfg.NodeEnv == "production" {
		if err := validateProductionAPIKeys(cfg.APIKeys); err != nil {
			return Config{}, err
		}
	}
	if cfg.NodeEnv == "production" && isRemoteCompatibleProvider(cfg.Embedding.Provider) {
		if strings.TrimSpace(cfg.Embedding.BaseURL) == "" {
			return Config{}, errors.New("EMBEDDING_BASE_URL is required when NODE_ENV=production and EMBEDDING_PROVIDER=compatible")
		}
	}
	if strings.TrimSpace(cfg.Reranker.Provider) != "" && isRemoteCompatibleProvider(cfg.Reranker.Provider) && strings.TrimSpace(cfg.Reranker.BaseURL) == "" {
		return Config{}, errors.New("RERANKER_BASE_URL is required when RERANKER_PROVIDER is configured")
	}
	if cfg.Embedding.Dimensions < 1 {
		return Config{}, errors.New("EMBEDDING_DIMENSIONS must be positive")
	}
	if cfg.WorkerLeaseTimeout <= cfg.WorkerSourceTimeout {
		return Config{}, errors.New("WORKER_LEASE_TIMEOUT must be greater than WORKER_SOURCE_TIMEOUT")
	}
	if cfg.WorkerMaxChangedDocumentsPerSource < 1 {
		return Config{}, errors.New("WORKER_MAX_CHANGED_DOCUMENTS_PER_SOURCE must be positive")
	}
	if cfg.Embedding.Timeout <= 0 || cfg.Embedding.Timeout > 30*time.Minute {
		return Config{}, errors.New("EMBEDDING_TIMEOUT must be between 1ns and 30m")
	}
	if cfg.Reranker.Timeout <= 0 || cfg.Reranker.Timeout > 30*time.Minute {
		return Config{}, errors.New("RERANKER_TIMEOUT must be between 1ns and 30m")
	}
	if cfg.Extractor.Timeout <= 0 || cfg.Extractor.Timeout > 30*time.Minute {
		return Config{}, errors.New("EXTRACTOR_TIMEOUT must be between 1ns and 30m")
	}
	if cfg.RateLimitMax < 1 {
		return Config{}, errors.New("RATE_LIMIT_MAX must be at least 1")
	}
	if cfg.ApprovalMode != "advisory" && cfg.ApprovalMode != "enforce" {
		return Config{}, errors.New("ABRA_APPROVAL_MODE must be advisory or enforce")
	}
	if cfg.AuditSink.BatchSize < 1 || cfg.AuditSink.BatchSize > 1000 {
		return Config{}, errors.New("ABRA_AUDIT_SINK_BATCH_SIZE must be between 1 and 1000")
	}
	if cfg.Tracing.SampleRatio < 0 || cfg.Tracing.SampleRatio > 1 {
		return Config{}, errors.New("ABRA_TRACING_SAMPLE_RATIO must be between 0 and 1")
	}
	if cfg.Tracing.Enabled && cfg.Tracing.Endpoint == "" {
		return Config{}, errors.New("ABRA_TRACING_ENABLED requires ABRA_OTEL_EXPORTER_OTLP_ENDPOINT or OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if cfg.ComposeHealthCacheTTL < 0 || cfg.ComposeHealthCacheTTL > time.Minute {
		return Config{}, errors.New("ABRA_COMPOSE_HEALTH_CACHE_TTL must be between 0s and 1m")
	}
	if strings.TrimSpace(cfg.GitCacheDir) == "" {
		return Config{}, errors.New("ABRA_GIT_CACHE_DIR is required")
	}
	if cfg.GitCloneDepth < 1 || cfg.GitCloneDepth > 1000 {
		return Config{}, errors.New("ABRA_GIT_CLONE_DEPTH must be between 1 and 1000")
	}
	return cfg, nil
}

func validateProductionAPIKeys(keys []string) error {
	for _, spec := range keys {
		token, _, _ := strings.Cut(strings.TrimSpace(spec), "|")
		token = strings.TrimSpace(token)
		if token == "" {
			return errors.New("ABRA_API_KEYS contains an empty token")
		}
		lower := strings.ToLower(token)
		if strings.Contains(lower, "replace") || strings.Contains(lower, "example") || strings.Contains(lower, "changeme") || strings.Contains(lower, "dev-token") || lower == "test-key" {
			return errors.New("ABRA_API_KEYS contains a placeholder token; generate a unique production token")
		}
		if len(token) < 16 {
			return errors.New("ABRA_API_KEYS production tokens must be at least 16 characters")
		}
	}
	return nil
}

func isLocalNeuralProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "local", "qwen3", "local-smart":
		return true
	default:
		return false
	}
}

func isRemoteCompatibleProvider(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "local", "qwen3", "local-smart", "tei", "compatible", "openai-compatible", "openai", "embeddinggemma", "bge-m3", "voyage", "zeroentropy":
		return true
	default:
		return false
	}
}

func env(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func csvEnv(name string) []string {
	raw := os.Getenv(name)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}

func intEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func floatEnv(name string, fallback float64) float64 {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return value
}

func boolEnv(name string, fallback bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(name)))
	if raw == "" {
		return fallback
	}
	switch raw {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func tracingEndpoint() string {
	if value := strings.TrimSpace(os.Getenv("ABRA_OTEL_EXPORTER_OTLP_ENDPOINT")); value != "" {
		return value
	}
	return strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	if parsed, err := time.ParseDuration(raw); err == nil {
		return parsed
	}
	switch raw {
	case "1 minute":
		return time.Minute
	case "2 minutes":
		return 2 * time.Minute
	default:
		return fallback
	}
}

func loadDotEnv(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s from .env: %w", key, err)
		}
	}
	return scanner.Err()
}
