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
	NodeEnv                          string
	DatabaseURL                      string
	Port                             string
	APIKeys                          []string
	WebhookSecrets                   []string
	AllowLocalEmbeddingsInProduction bool
	RedactPII                        bool
	ApprovalMode                     string
	AuditSink                        AuditSinkConfig
	Tracing                          TracingConfig
	Embedding                        AIProviderConfig
	Extractor                        AIProviderConfig
	RateLimitMax                     int
	RateLimitWindow                  time.Duration
	WorkerInterval                   time.Duration
	ComposeHealthCacheTTL            time.Duration
	GitCacheDir                      string
	GitCloneDepth                    int
}

func Load() (Config, error) {
	_ = loadDotEnv(".env")

	cfg := Config{
		NodeEnv:                          env("NODE_ENV", "development"),
		DatabaseURL:                      env("DATABASE_URL", "postgres://abra:abra@localhost:5433/abra"),
		Port:                             env("PORT", "18080"),
		APIKeys:                          csvEnv("ABRA_API_KEYS"),
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
			Provider:   env("EMBEDDING_PROVIDER", "local"),
			BaseURL:    os.Getenv("EMBEDDING_BASE_URL"),
			APIKey:     os.Getenv("EMBEDDING_API_KEY"),
			Model:      env("EMBEDDING_MODEL", "embedding-model-1536"),
			Dimensions: intEnv("EMBEDDING_DIMENSIONS", 1536),
		},
		Extractor: AIProviderConfig{
			Provider: env("EXTRACTOR_PROVIDER", env("LLM_PROVIDER", "local")),
			BaseURL:  env("EXTRACTOR_BASE_URL", os.Getenv("LLM_BASE_URL")),
			APIKey:   env("EXTRACTOR_API_KEY", os.Getenv("LLM_API_KEY")),
			Model:    env("EXTRACTOR_MODEL", env("LLM_MODEL", "local-extractor")),
		},
		RateLimitMax:          intEnv("RATE_LIMIT_MAX", 120),
		RateLimitWindow:       durationEnv("RATE_LIMIT_WINDOW", time.Minute),
		WorkerInterval:        durationEnv("WORKER_INTERVAL", 5*time.Minute),
		ComposeHealthCacheTTL: durationEnv("ABRA_COMPOSE_HEALTH_CACHE_TTL", 2*time.Second),
		GitCacheDir:           env("ABRA_GIT_CACHE_DIR", "/tmp/abra-git-cache"),
		GitCloneDepth:         intEnv("ABRA_GIT_CLONE_DEPTH", 1),
	}

	if cfg.NodeEnv == "production" && len(cfg.APIKeys) == 0 {
		return Config{}, errors.New("ABRA_API_KEYS is required when NODE_ENV=production")
	}
	if cfg.NodeEnv == "production" && strings.EqualFold(cfg.Embedding.Provider, "local") && !cfg.AllowLocalEmbeddingsInProduction {
		return Config{}, errors.New("EMBEDDING_PROVIDER=local is not allowed when NODE_ENV=production unless ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=true")
	}
	if cfg.NodeEnv == "production" && (strings.EqualFold(cfg.Embedding.Provider, "compatible") || strings.EqualFold(cfg.Embedding.Provider, "openai-compatible")) {
		if strings.TrimSpace(cfg.Embedding.BaseURL) == "" {
			return Config{}, errors.New("EMBEDDING_BASE_URL is required when NODE_ENV=production and EMBEDDING_PROVIDER=compatible")
		}
		if strings.TrimSpace(cfg.Embedding.APIKey) == "" {
			return Config{}, errors.New("EMBEDDING_API_KEY is required when NODE_ENV=production and EMBEDDING_PROVIDER=compatible")
		}
	}
	if cfg.Embedding.Dimensions != 1536 {
		return Config{}, errors.New("Abra v1 default migrations use vector(1536); set EMBEDDING_DIMENSIONS=1536")
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
