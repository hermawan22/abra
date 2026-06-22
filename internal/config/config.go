package config

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"net/url"
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
	APIReadTimeout                     time.Duration
	MaxRequestBodyBytes                int64
	AllowUnauthenticatedDev            bool
	WebhookSecrets                     []string
	AllowUnsignedWebhooksInProduction  bool
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
	WorkerMaxSourcesPerRun             int
	WorkerConcurrency                  int
	WorkerSourceTimeout                time.Duration
	WorkerLeaseTimeout                 time.Duration
	WorkerMaxChangedDocumentsPerSource int
	AIProviderConcurrency              int
	EmbeddingBatchMaxItems             int
	EmbeddingBatchMaxTokens            int
	ComposeHealthCacheTTL              time.Duration
	ComposeRecallConcurrency           int
	ComposeGraphConcurrency            int
	GitCacheDir                        string
	GitCloneDepth                      int
}

func Load() (Config, error) {
	_ = loadDotEnv(".env")
	if err := validateEnvSyntax(); err != nil {
		return Config{}, err
	}
	embeddingProvider := env("EMBEDDING_PROVIDER", "local")
	defaultEmbeddingBaseURL := ""
	defaultEmbeddingTimeout := 30 * time.Second
	defaultRerankerProvider := ""
	defaultRerankerBaseURL := ""
	defaultRerankerModel := ""
	defaultRerankerTimeout := 30 * time.Second
	defaultWorkerSourceTimeout := 2 * time.Minute
	defaultWorkerLeaseTimeout := 5 * time.Minute
	defaultAPIReadTimeout := 2 * time.Minute
	defaultAIProviderConcurrency := 4
	defaultEmbeddingBatchMaxItems := 16
	defaultEmbeddingBatchMaxTokens := 6000
	if isLocalNeuralProvider(embeddingProvider) {
		defaultEmbeddingBaseURL = "http://host.docker.internal:8080/v1"
		defaultEmbeddingTimeout = 10 * time.Minute
		defaultRerankerTimeout = 10 * time.Minute
		defaultWorkerSourceTimeout = 30 * time.Minute
		defaultWorkerLeaseTimeout = 35 * time.Minute
		defaultAPIReadTimeout = 10 * time.Minute
		defaultAIProviderConcurrency = 1
		defaultEmbeddingBatchMaxItems = 6
		defaultEmbeddingBatchMaxTokens = 3000
	}

	nodeEnv := env("NODE_ENV", "development")
	defaultBindAddress := "127.0.0.1"
	defaultApprovalMode := "advisory"
	if nodeEnv == "production" {
		defaultBindAddress = "0.0.0.0"
		defaultApprovalMode = "enforce"
	}
	cfg := Config{
		NodeEnv:                           nodeEnv,
		BindAddress:                       env("ABRA_BIND_ADDR", defaultBindAddress),
		DatabaseURL:                       env("DATABASE_URL", "postgres://abra:abra@localhost:5433/abra"),
		Port:                              env("PORT", "18080"),
		APIKeys:                           csvEnv("ABRA_API_KEYS"),
		APIReadTimeout:                    durationEnv("ABRA_API_READ_TIMEOUT", defaultAPIReadTimeout),
		MaxRequestBodyBytes:               int64(intEnv("ABRA_MAX_REQUEST_BODY_BYTES", 25<<20)),
		AllowUnauthenticatedDev:           boolEnv("ABRA_UNAUTHENTICATED_DEV", false),
		WebhookSecrets:                    csvEnv("ABRA_WEBHOOK_SECRETS"),
		AllowUnsignedWebhooksInProduction: boolEnv("ABRA_ALLOW_UNSIGNED_WEBHOOKS_IN_PRODUCTION", false),
		AllowLocalEmbeddingsInProduction:  boolEnv("ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION", false),
		RedactPII:                         boolEnv("REDACT_PII", true),
		ApprovalMode:                      strings.ToLower(env("ABRA_APPROVAL_MODE", defaultApprovalMode)),
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
			Provider: env("RERANKER_PROVIDER", defaultRerankerProvider),
			BaseURL:  env("RERANKER_BASE_URL", defaultRerankerBaseURL),
			APIKey:   os.Getenv("RERANKER_API_KEY"),
			Model:    env("RERANKER_MODEL", defaultRerankerModel),
			Timeout:  durationEnv("RERANKER_TIMEOUT", defaultRerankerTimeout),
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
		WorkerMaxSourcesPerRun:             intEnv("WORKER_MAX_SOURCES_PER_RUN", 25),
		WorkerConcurrency:                  intEnv("WORKER_CONCURRENCY", 1),
		WorkerSourceTimeout:                durationEnv("WORKER_SOURCE_TIMEOUT", defaultWorkerSourceTimeout),
		WorkerLeaseTimeout:                 durationEnv("WORKER_LEASE_TIMEOUT", defaultWorkerLeaseTimeout),
		WorkerMaxChangedDocumentsPerSource: intEnv("WORKER_MAX_CHANGED_DOCUMENTS_PER_SOURCE", 100),
		AIProviderConcurrency:              intEnv("ABRA_AI_PROVIDER_CONCURRENCY", defaultAIProviderConcurrency),
		EmbeddingBatchMaxItems:             intEnv("ABRA_EMBEDDING_BATCH_MAX_ITEMS", defaultEmbeddingBatchMaxItems),
		EmbeddingBatchMaxTokens:            intEnv("ABRA_EMBEDDING_BATCH_MAX_TOKENS", defaultEmbeddingBatchMaxTokens),
		ComposeHealthCacheTTL:              durationEnv("ABRA_COMPOSE_HEALTH_CACHE_TTL", 2*time.Second),
		ComposeRecallConcurrency:           intEnv("ABRA_COMPOSE_RECALL_CONCURRENCY", 1),
		ComposeGraphConcurrency:            intEnv("ABRA_COMPOSE_GRAPH_CONCURRENCY", 4),
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
		if len(cfg.WebhookSecrets) == 0 && !cfg.AllowUnsignedWebhooksInProduction {
			return Config{}, errors.New("ABRA_WEBHOOK_SECRETS is required when NODE_ENV=production; set ABRA_ALLOW_UNSIGNED_WEBHOOKS_IN_PRODUCTION=true only when webhook ingestion is disabled or protected elsewhere")
		}
		if err := validateProductionSecrets("ABRA_WEBHOOK_SECRETS", cfg.WebhookSecrets); err != nil {
			return Config{}, err
		}
	}
	if cfg.NodeEnv == "production" && isRemoteCompatibleProvider(cfg.Embedding.Provider) && !isLocalNeuralProvider(cfg.Embedding.Provider) {
		if strings.TrimSpace(cfg.Embedding.BaseURL) == "" {
			return Config{}, errors.New("EMBEDDING_BASE_URL is required when NODE_ENV=production and EMBEDDING_PROVIDER=compatible")
		}
		if err := validateProductionAIProviderURL("EMBEDDING_BASE_URL", cfg.Embedding.BaseURL); err != nil {
			return Config{}, err
		}
	}
	if cfg.NodeEnv == "production" && isLocalNeuralProvider(cfg.Embedding.Provider) && !cfg.AllowLocalEmbeddingsInProduction {
		return Config{}, errors.New("ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=true is required when NODE_ENV=production and EMBEDDING_PROVIDER=local")
	}
	if port, err := strconv.Atoi(cfg.Port); err != nil || port < 1 || port > 65535 {
		return Config{}, errors.New("PORT must be an integer between 1 and 65535")
	}
	if strings.TrimSpace(cfg.BindAddress) == "" {
		return Config{}, errors.New("ABRA_BIND_ADDR must not be empty")
	}
	if _, err := netip.ParseAddr(cfg.BindAddress); err != nil {
		return Config{}, errors.New("ABRA_BIND_ADDR must be an IP address such as 127.0.0.1 or 0.0.0.0")
	}
	if cfg.APIReadTimeout <= 0 || cfg.APIReadTimeout > 30*time.Minute {
		return Config{}, errors.New("ABRA_API_READ_TIMEOUT must be between 1ns and 30m")
	}
	if cfg.MaxRequestBodyBytes < 1 || cfg.MaxRequestBodyBytes > 100<<20 {
		return Config{}, errors.New("ABRA_MAX_REQUEST_BODY_BYTES must be between 1 and 104857600")
	}
	if strings.TrimSpace(cfg.Reranker.Provider) != "" && isRemoteCompatibleProvider(cfg.Reranker.Provider) && strings.TrimSpace(cfg.Reranker.BaseURL) == "" {
		return Config{}, errors.New("RERANKER_BASE_URL is required when RERANKER_PROVIDER is configured")
	}
	if cfg.NodeEnv == "production" && strings.TrimSpace(cfg.Reranker.Provider) != "" && isRemoteCompatibleProvider(cfg.Reranker.Provider) && !isLocalNeuralProvider(cfg.Reranker.Provider) {
		if err := validateProductionAIProviderURL("RERANKER_BASE_URL", cfg.Reranker.BaseURL); err != nil {
			return Config{}, err
		}
	}
	if cfg.Embedding.Dimensions < 1 {
		return Config{}, errors.New("EMBEDDING_DIMENSIONS must be positive")
	}
	if cfg.WorkerLeaseTimeout <= cfg.WorkerSourceTimeout {
		return Config{}, errors.New("WORKER_LEASE_TIMEOUT must be greater than WORKER_SOURCE_TIMEOUT")
	}
	if cfg.WorkerMaxSourcesPerRun < 1 || cfg.WorkerMaxSourcesPerRun > 1000 {
		return Config{}, errors.New("WORKER_MAX_SOURCES_PER_RUN must be between 1 and 1000")
	}
	if cfg.WorkerConcurrency < 1 || cfg.WorkerConcurrency > 32 {
		return Config{}, errors.New("WORKER_CONCURRENCY must be between 1 and 32")
	}
	if cfg.WorkerMaxChangedDocumentsPerSource < 1 {
		return Config{}, errors.New("WORKER_MAX_CHANGED_DOCUMENTS_PER_SOURCE must be positive")
	}
	if cfg.AIProviderConcurrency < 1 || cfg.AIProviderConcurrency > 32 {
		return Config{}, errors.New("ABRA_AI_PROVIDER_CONCURRENCY must be between 1 and 32")
	}
	if cfg.EmbeddingBatchMaxItems < 1 || cfg.EmbeddingBatchMaxItems > 128 {
		return Config{}, errors.New("ABRA_EMBEDDING_BATCH_MAX_ITEMS must be between 1 and 128")
	}
	if cfg.EmbeddingBatchMaxTokens < 1 || cfg.EmbeddingBatchMaxTokens > 200000 {
		return Config{}, errors.New("ABRA_EMBEDDING_BATCH_MAX_TOKENS must be between 1 and 200000")
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
	if cfg.ComposeRecallConcurrency < 1 || cfg.ComposeRecallConcurrency > 32 {
		return Config{}, errors.New("ABRA_COMPOSE_RECALL_CONCURRENCY must be between 1 and 32")
	}
	if cfg.ComposeGraphConcurrency < 1 || cfg.ComposeGraphConcurrency > 32 {
		return Config{}, errors.New("ABRA_COMPOSE_GRAPH_CONCURRENCY must be between 1 and 32")
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
		token, rawOptions, hasOptions := strings.Cut(strings.TrimSpace(spec), "|")
		token = strings.TrimSpace(token)
		if err := validateProductionSecret("ABRA_API_KEYS", token); err != nil {
			return err
		}
		if hasOptions {
			if err := validateProductionAPIKeyOptions(rawOptions); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateProductionAPIKeyOptions(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("ABRA_API_KEYS production key options must include explicit roles and scopes")
	}
	seenRoles := false
	seenScopes := false
	for _, option := range strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == '|' }) {
		option = strings.TrimSpace(option)
		if option == "" {
			continue
		}
		key, value, ok := strings.Cut(option, "=")
		if !ok {
			return fmt.Errorf("ABRA_API_KEYS production key option %q must use key=value", option)
		}
		key = strings.ToLower(strings.TrimSpace(key))
		values := splitAuthOptionValues(value)
		if len(values) == 0 {
			return fmt.Errorf("ABRA_API_KEYS production key option %q must not be empty", key)
		}
		switch key {
		case "role", "roles":
			seenRoles = true
			for _, role := range values {
				if !isKnownAPIKeyRole(role) {
					return fmt.Errorf("ABRA_API_KEYS contains unknown production role %q", role)
				}
			}
		case "scope", "scopes":
			seenScopes = true
		default:
			return fmt.Errorf("ABRA_API_KEYS contains unknown production key option %q", key)
		}
	}
	if !seenRoles {
		return errors.New("ABRA_API_KEYS production key options must include explicit roles")
	}
	if !seenScopes {
		return errors.New("ABRA_API_KEYS production key options must include explicit scopes; use scopes=* only when all-scope access is intentional")
	}
	return nil
}

func splitAuthOptionValues(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ' ' || r == '+' || r == ','
	})
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.ToLower(strings.TrimSpace(part)); value != "" {
			values = append(values, value)
		}
	}
	return values
}

func isKnownAPIKeyRole(role string) bool {
	switch role {
	case "admin", "writer", "reader", "ops":
		return true
	default:
		return false
	}
}

func validateProductionSecrets(name string, values []string) error {
	for _, value := range values {
		if err := validateProductionSecret(name, value); err != nil {
			return err
		}
	}
	return nil
}

func validateProductionSecret(name, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s contains an empty value", name)
	}
	lower := strings.ToLower(value)
	if strings.Contains(lower, "replace") || strings.Contains(lower, "example") || strings.Contains(lower, "changeme") || strings.Contains(lower, "dev-token") || strings.Contains(lower, "dev-webhook-secret") || strings.Contains(lower, "test-key") {
		return fmt.Errorf("%s contains a placeholder value; generate a unique production secret", name)
	}
	if len(value) < 16 {
		return fmt.Errorf("%s production values must be at least 16 characters", name)
	}
	return nil
}

func validateProductionAIProviderURL(name, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("%s must be a valid http or https URL", name)
	}
	if parsed.Scheme == "https" {
		return nil
	}
	if parsed.Scheme != "http" {
		return fmt.Errorf("%s must use http or https", name)
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" {
		return nil
	}
	if addr, err := netip.ParseAddr(host); err == nil && addr.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%s must use https for non-loopback production providers", name)
}

func validateEnvSyntax() error {
	for _, name := range []string{
		"EMBEDDING_DIMENSIONS",
		"RATE_LIMIT_MAX",
		"ABRA_AUDIT_SINK_BATCH_SIZE",
		"WORKER_MAX_SOURCES_PER_RUN",
		"WORKER_CONCURRENCY",
		"WORKER_MAX_CHANGED_DOCUMENTS_PER_SOURCE",
		"ABRA_GIT_CLONE_DEPTH",
		"ABRA_MAX_REQUEST_BODY_BYTES",
		"ABRA_EMBEDDING_BATCH_MAX_ITEMS",
		"ABRA_EMBEDDING_BATCH_MAX_TOKENS",
	} {
		if raw, ok := os.LookupEnv(name); ok && strings.TrimSpace(raw) != "" {
			if _, err := strconv.Atoi(strings.TrimSpace(raw)); err != nil {
				return fmt.Errorf("%s must be an integer", name)
			}
		}
	}
	for _, name := range []string{"ABRA_TRACING_SAMPLE_RATIO"} {
		if raw, ok := os.LookupEnv(name); ok && strings.TrimSpace(raw) != "" {
			value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
			if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
				return fmt.Errorf("%s must be a number", name)
			}
		}
	}
	for _, name := range []string{
		"ABRA_UNAUTHENTICATED_DEV",
		"ABRA_ALLOW_UNSIGNED_WEBHOOKS_IN_PRODUCTION",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION",
		"REDACT_PII",
		"ABRA_TRACING_ENABLED",
		"ABRA_TRACING_INSECURE",
		"OTEL_EXPORTER_OTLP_INSECURE",
	} {
		if raw, ok := os.LookupEnv(name); ok && strings.TrimSpace(raw) != "" {
			if _, ok := parseBool(strings.TrimSpace(raw)); !ok {
				return fmt.Errorf("%s must be a boolean", name)
			}
		}
	}
	for _, name := range []string{
		"EMBEDDING_TIMEOUT",
		"RERANKER_TIMEOUT",
		"EXTRACTOR_TIMEOUT",
		"LLM_TIMEOUT",
		"RATE_LIMIT_WINDOW",
		"WORKER_INTERVAL",
		"WORKER_SOURCE_TIMEOUT",
		"WORKER_LEASE_TIMEOUT",
		"ABRA_COMPOSE_HEALTH_CACHE_TTL",
		"ABRA_API_READ_TIMEOUT",
	} {
		if raw, ok := os.LookupEnv(name); ok && strings.TrimSpace(raw) != "" {
			if _, err := parseDuration(strings.TrimSpace(raw)); err != nil {
				return fmt.Errorf("%s must be a duration: %w", name, err)
			}
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
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return fallback
	}
	return value
}

func boolEnv(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	if parsed, ok := parseBool(raw); ok {
		return parsed
	}
	return fallback
}

func parseBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "y", "on":
		return true, true
	case "0", "false", "no", "n", "off":
		return false, true
	default:
		return false, false
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
	if parsed, err := parseDuration(raw); err == nil {
		return parsed
	}
	return fallback
}

func parseDuration(raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if parsed, err := time.ParseDuration(raw); err == nil {
		return parsed, nil
	}
	fields := strings.Fields(raw)
	if len(fields) == 2 {
		value, err := strconv.Atoi(fields[0])
		if err == nil && value >= 0 {
			switch strings.ToLower(fields[1]) {
			case "second", "seconds":
				return time.Duration(value) * time.Second, nil
			case "minute", "minutes":
				return time.Duration(value) * time.Minute, nil
			case "hour", "hours":
				return time.Duration(value) * time.Hour, nil
			}
		}
	}
	return 0, fmt.Errorf("invalid duration %q", raw)
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
