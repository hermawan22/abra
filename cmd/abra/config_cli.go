package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func configCommand(args cliArgs) error {
	action := "show"
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch action {
	case "", "show", "get":
		return configShow(args)
	case "path", "where":
		fmt.Println(envPath(args))
		return nil
	case "model", "embedding":
		return configModel(args)
	default:
		return fmt.Errorf("unknown config command %q\n\n%s", action, commandUsage("config"))
	}
}

func configShow(args cliArgs) error {
	path := envPath(args)
	if !fileExists(path) {
		return fmt.Errorf("config file not found: %s; run `abra setup` to create one", path)
	}
	values, err := readEnvValues(path)
	if err != nil {
		return err
	}
	view := map[string]any{
		"env_file":             path,
		"api_token":            maskSecret(firstNonEmpty(values["ABRA_API_TOKEN"], firstCSV(values["ABRA_API_KEYS"], ""))),
		"approval_mode":        values["ABRA_APPROVAL_MODE"],
		"port":                 firstNonEmpty(values["ABRA_PORT"], "18080"),
		"embedding_provider":   values["EMBEDDING_PROVIDER"],
		"embedding_base_url":   values["EMBEDDING_BASE_URL"],
		"embedding_api_key":    maskSecret(values["EMBEDDING_API_KEY"]),
		"embedding_model":      values["EMBEDDING_MODEL"],
		"embedding_dimensions": values["EMBEDDING_DIMENSIONS"],
		"embedding_timeout":    values["EMBEDDING_TIMEOUT"],
		"provider_concurrency": firstNonEmpty(values["ABRA_AI_PROVIDER_CONCURRENCY"], defaultProviderConcurrency(values["EMBEDDING_PROVIDER"])),
		"batch_max_items":      firstNonEmpty(values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"], defaultBatchMaxItems(values["EMBEDDING_PROVIDER"])),
		"batch_max_tokens":     firstNonEmpty(values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"], defaultBatchMaxTokens(values["EMBEDDING_PROVIDER"])),
		"worker_concurrency":   firstNonEmpty(values["WORKER_CONCURRENCY"], "1"),
		"worker_max_sources":   firstNonEmpty(values["WORKER_MAX_SOURCES_PER_RUN"], "25"),
		"reranker_provider":    values["RERANKER_PROVIDER"],
		"reranker_base_url":    values["RERANKER_BASE_URL"],
		"reranker_api_key":     maskSecret(values["RERANKER_API_KEY"]),
		"reranker_model":       values["RERANKER_MODEL"],
		"reranker_timeout":     values["RERANKER_TIMEOUT"],
		"local_runner_image":   values["ABRA_LOCAL_EMBEDDING_IMAGE"],
		"local_runner_pull":    firstNonEmpty(values["ABRA_LOCAL_EMBEDDING_PULL_POLICY"], "missing"),
		"local_runner_timeout": firstNonEmpty(values["ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT"], "10s"),
		"redaction_enabled":    firstNonEmpty(values["REDACT_PII"], "true"),
	}
	if boolFlag(args, "json") {
		return printJSON(view)
	}
	fmt.Println("Config: " + envPath(args))
	fmt.Println("token:     " + stringValue(view["api_token"], ""))
	fmt.Println("approval:  " + stringValue(view["approval_mode"], ""))
	fmt.Println("port:      " + stringValue(view["port"], ""))
	fmt.Println("embedding: " + stringValue(view["embedding_provider"], ""))
	if baseURL := stringValue(view["embedding_base_url"], ""); baseURL != "" {
		fmt.Println("base_url:  " + baseURL)
	}
	fmt.Println("model:     " + stringValue(view["embedding_model"], ""))
	fmt.Println("dims:      " + stringValue(view["embedding_dimensions"], ""))
	if timeout := stringValue(view["embedding_timeout"], ""); timeout != "" {
		fmt.Println("timeout:   " + timeout)
	}
	fmt.Println("provider_concurrency: " + stringValue(view["provider_concurrency"], ""))
	fmt.Println("batch_max_items:      " + stringValue(view["batch_max_items"], ""))
	fmt.Println("batch_max_tokens:     " + stringValue(view["batch_max_tokens"], ""))
	fmt.Println("worker_concurrency:   " + stringValue(view["worker_concurrency"], ""))
	fmt.Println("worker_max_sources:   " + stringValue(view["worker_max_sources"], ""))
	fmt.Println("api_key:   " + stringValue(view["embedding_api_key"], ""))
	if rerankerProvider := stringValue(view["reranker_provider"], ""); rerankerProvider != "" {
		fmt.Println("reranker:  " + rerankerProvider)
		fmt.Println("rerank_url: " + stringValue(view["reranker_base_url"], ""))
		fmt.Println("rerank_model: " + stringValue(view["reranker_model"], ""))
		if timeout := stringValue(view["reranker_timeout"], ""); timeout != "" {
			fmt.Println("rerank_timeout: " + timeout)
		}
		fmt.Println("rerank_key:   " + stringValue(view["reranker_api_key"], ""))
	}
	if isLocalProviderName(stringValue(view["embedding_provider"], "")) {
		if image := stringValue(view["local_runner_image"], ""); image != "" {
			fmt.Println("local_image: " + image)
		}
		fmt.Println("local_pull:  " + stringValue(view["local_runner_pull"], "missing"))
		fmt.Println("local_ready_timeout: " + stringValue(view["local_runner_timeout"], "10s"))
	}
	return nil
}

func configModel(args cliArgs) error {
	mode := "show"
	if len(args.Rest) > 0 {
		mode = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch mode {
	case "", "show":
		return configShow(args)
	case "local":
		return configModelLocalNeural(args, "local neural embeddings")
	case "qwen3", "local-smart":
		return configModelLocalNeural(args, "local neural embeddings")
	case "openai":
		if flag(args, "base-url", "") == "" {
			args.Flags["base-url"] = "https://api.openai.com/v1"
		}
		if flag(args, "model", "") == "" {
			args.Flags["model"] = "text-embedding-3-small"
		}
		return configModelCompatible(args, "OpenAI embeddings")
	case "compatible", "openai-compatible":
		return configModelCompatible(args, "compatible embeddings")
	default:
		return fmt.Errorf("unknown model config %q\n\n%s", mode, commandUsage("config"))
	}
}

func configModelLocalNeural(args cliArgs, label string) error {
	apiKey := flag(args, "api-key", "")
	if apiKey == "" && boolFlag(args, "api-key-stdin") {
		bytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		apiKey = strings.TrimSpace(string(bytes))
	}
	reranker, err := compatibleRerankerConfig(args, apiKey, "none")
	if err != nil {
		return err
	}
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                   "local",
		"EMBEDDING_BASE_URL":                   containerReachableBaseURL(flag(args, "base-url", defaultEmbeddingBaseURL)),
		"EMBEDDING_API_KEY":                    apiKey,
		"EMBEDDING_MODEL":                      flag(args, "model", defaultServedModelName),
		"EMBEDDING_DIMENSIONS":                 flag(args, "dimensions", "1024"),
		"EMBEDDING_TIMEOUT":                    flag(args, "embedding-timeout", "10m"),
		"ABRA_EMBEDDING_BATCH_MAX_ITEMS":       flag(args, "embedding-batch-max-items", "6"),
		"ABRA_EMBEDDING_BATCH_MAX_TOKENS":      flag(args, "embedding-batch-max-tokens", "3000"),
		"ABRA_AI_PROVIDER_CONCURRENCY":         flag(args, "provider-concurrency", "1"),
		"RERANKER_PROVIDER":                    reranker.Provider,
		"RERANKER_BASE_URL":                    reranker.BaseURL,
		"RERANKER_API_KEY":                     reranker.APIKey,
		"RERANKER_MODEL":                       reranker.Model,
		"RERANKER_TIMEOUT":                     reranker.Timeout,
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "false",
		"ABRA_LOCAL_EMBEDDING_IMAGE":           flag(args, "runner-image", ""),
		"ABRA_LOCAL_EMBEDDING_PULL_POLICY":     localRunnerPullPolicy(flag(args, "pull-policy", "missing")),
		"ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT": firstNonEmpty(
			flag(args, "readiness-timeout", ""),
			"10s",
		),
	}); err != nil {
		return err
	}
	fmt.Println("Model config updated: " + label)
	if reranker.Provider != "" && !isDisabledProviderName(reranker.Provider) {
		fmt.Println("Reranker config updated: " + reranker.Provider + " " + reranker.Model)
	}
	fmt.Println("Run `abra up` to start the default local embedding runner and stack, or use `abra model up` only to manage the runner directly.")
	printRestartHint(args)
	return nil
}

func configModelCompatible(args cliArgs, label string) error {
	baseURL := flag(args, "base-url", "")
	apiKey := flag(args, "api-key", "")
	model := flag(args, "model", "")
	if apiKey == "" && boolFlag(args, "api-key-stdin") {
		bytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		apiKey = strings.TrimSpace(string(bytes))
	}
	if baseURL == "" || model == "" {
		return errors.New("config model compatible requires --base-url and --model; add --api-key or --api-key-stdin when the provider requires auth")
	}
	dimensions, err := resolveCompatibleEmbeddingDimensions(args, model)
	if err != nil {
		return err
	}
	baseURL = containerReachableBaseURL(strings.TrimSpace(baseURL))
	reranker, err := compatibleRerankerConfig(args, apiKey, "")
	if err != nil {
		return err
	}
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                     "compatible",
		"EMBEDDING_BASE_URL":                     baseURL,
		"EMBEDDING_API_KEY":                      apiKey,
		"EMBEDDING_MODEL":                        model,
		"EMBEDDING_DIMENSIONS":                   dimensions,
		"EMBEDDING_TIMEOUT":                      flag(args, "embedding-timeout", "30s"),
		"ABRA_EMBEDDING_BATCH_MAX_ITEMS":         flag(args, "embedding-batch-max-items", "16"),
		"ABRA_EMBEDDING_BATCH_MAX_TOKENS":        flag(args, "embedding-batch-max-tokens", "6000"),
		"ABRA_AI_PROVIDER_CONCURRENCY":           flag(args, "provider-concurrency", "4"),
		"RERANKER_PROVIDER":                      reranker.Provider,
		"RERANKER_BASE_URL":                      reranker.BaseURL,
		"RERANKER_API_KEY":                       reranker.APIKey,
		"RERANKER_MODEL":                         reranker.Model,
		"RERANKER_TIMEOUT":                       reranker.Timeout,
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION":   "false",
		"ABRA_LOCAL_EMBEDDING_IMAGE":             "",
		"ABRA_LOCAL_EMBEDDING_PULL_POLICY":       "missing",
		"ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT": "10s",
	}); err != nil {
		return err
	}
	fmt.Println("Model config updated: " + label)
	if reranker.Provider != "" && !isDisabledProviderName(reranker.Provider) {
		fmt.Println("Reranker config updated: " + reranker.Provider + " " + reranker.Model)
	}
	printRestartHint(args)
	return nil
}

func compatibleRerankerConfig(args cliArgs, embeddingAPIKey, defaultProvider string) (rerankerCLIConfig, error) {
	provider := strings.ToLower(strings.TrimSpace(flag(args, "reranker-provider", "")))
	if boolFlag(args, "no-reranker") || boolFlag(args, "disable-reranker") {
		provider = "none"
	}
	rerankerFlagUsed := provider != "" || rerankerFlagProvided(args)
	if provider == "" && rerankerFlagUsed {
		provider = "compatible"
	}
	if provider == "" {
		provider = strings.ToLower(strings.TrimSpace(defaultProvider))
	}
	if provider == "" {
		return rerankerCLIConfig{}, nil
	}
	if isDisabledProviderName(provider) {
		return rerankerCLIConfig{Provider: provider}, nil
	}

	baseURL := firstNonEmpty(flag(args, "reranker-base-url", ""), defaultRerankerBaseURLForProvider(provider))
	model := firstNonEmpty(flag(args, "reranker-model", ""), defaultRerankerModelForProvider(provider))
	if !isLocalProviderName(provider) && (strings.TrimSpace(baseURL) == "" || strings.TrimSpace(model) == "") {
		return rerankerCLIConfig{}, errors.New("compatible reranker config requires --reranker-base-url and --reranker-model; use --no-reranker to disable reranking")
	}
	apiKey := flag(args, "reranker-api-key", "")
	if apiKey == "" && boolFlag(args, "reranker-api-key-stdin") {
		bytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			return rerankerCLIConfig{}, err
		}
		apiKey = strings.TrimSpace(string(bytes))
	}
	if apiKey == "" && !isLocalProviderName(provider) {
		apiKey = embeddingAPIKey
	}
	timeout := flag(args, "reranker-timeout", "")
	if timeout == "" {
		if isLocalProviderName(provider) {
			timeout = "10m"
		} else {
			timeout = "30s"
		}
	}
	return rerankerCLIConfig{
		Provider: provider,
		BaseURL:  strings.TrimRight(containerReachableBaseURL(strings.TrimSpace(baseURL)), "/"),
		APIKey:   strings.TrimSpace(apiKey),
		Model:    strings.TrimSpace(model),
		Timeout:  timeout,
	}, nil
}

func rerankerFlagProvided(args cliArgs) bool {
	return flag(args, "reranker-base-url", "") != "" ||
		flag(args, "reranker-model", "") != "" ||
		flag(args, "reranker-api-key", "") != "" ||
		boolFlag(args, "reranker-api-key-stdin")
}

func resolveCompatibleEmbeddingDimensions(args cliArgs, model string) (string, error) {
	if dimensions := strings.TrimSpace(flag(args, "dimensions", "")); dimensions != "" {
		return dimensions, nil
	}
	if dimensions := inferEmbeddingDimensions(model); dimensions != "" {
		return dimensions, nil
	}
	return "", fmt.Errorf("embedding dimensions are required for compatible model %q; pass --dimensions <size> so Abra can validate vector storage correctly", strings.TrimSpace(model))
}

func inferEmbeddingDimensions(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	switch {
	case normalized == "":
		return ""
	case strings.Contains(normalized, "text-embedding-3-small"):
		return "1536"
	case strings.Contains(normalized, "text-embedding-3-large"):
		return "3072"
	case strings.Contains(normalized, "qwen3-embedding-0.6b"):
		return "1024"
	case strings.Contains(normalized, "qwen3-embedding-4b"):
		return "2560"
	case strings.Contains(normalized, "qwen3-embedding-8b"):
		return "4096"
	case strings.Contains(normalized, "bge-m3"):
		return "1024"
	case strings.Contains(normalized, "nomic-embed-text"):
		return "768"
	case strings.Contains(normalized, "embedding-001") || strings.Contains(normalized, "text-embedding-004"):
		return "768"
	default:
		return ""
	}
}

func updateEnvValues(args cliArgs, updates map[string]string) error {
	if err := ensureEnv(args); err != nil {
		return err
	}
	path := envPath(args)
	lines, err := readEnvLines(path)
	if err != nil {
		return err
	}
	applied := map[string]bool{}
	for i, line := range lines {
		key, _, ok := parseEnvLine(line)
		if !ok {
			continue
		}
		if value, exists := updates[key]; exists {
			lines[i] = key + "=" + value
			applied[key] = true
		}
	}
	for _, key := range []string{
		"EMBEDDING_PROVIDER",
		"EMBEDDING_BASE_URL",
		"EMBEDDING_API_KEY",
		"EMBEDDING_MODEL",
		"EMBEDDING_DIMENSIONS",
		"EMBEDDING_TIMEOUT",
		"ABRA_EMBEDDING_BATCH_MAX_ITEMS",
		"ABRA_EMBEDDING_BATCH_MAX_TOKENS",
		"ABRA_AI_PROVIDER_CONCURRENCY",
		"RERANKER_PROVIDER",
		"RERANKER_BASE_URL",
		"RERANKER_API_KEY",
		"RERANKER_MODEL",
		"RERANKER_TIMEOUT",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION",
		"ABRA_LOCAL_EMBEDDING_IMAGE",
		"ABRA_LOCAL_EMBEDDING_PULL_POLICY",
		"ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT",
		"ABRA_LOCAL_EMBEDDING_PUBLISH_ADDR",
		"WORKER_INTERVAL",
		"WORKER_MAX_SOURCES_PER_RUN",
		"WORKER_CONCURRENCY",
	} {
		if value, exists := updates[key]; exists && !applied[key] {
			lines = append(lines, key+"="+value)
		}
	}
	content := strings.Join(lines, "\n")
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return os.WriteFile(path, []byte(content), 0o600)
}

func readEnvValues(path string) (map[string]string, error) {
	lines, err := readEnvLines(path)
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	for _, line := range lines {
		key, value, ok := parseEnvLine(line)
		if ok {
			values[key] = value
		}
	}
	return values, nil
}

func readEnvLines(path string) ([]string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	content := strings.ReplaceAll(string(bytes), "\r\n", "\n")
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return []string{}, nil
	}
	return strings.Split(content, "\n"), nil
}

func parseEnvLine(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", "", false
	}
	key, value, ok := strings.Cut(trimmed, "=")
	if !ok {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}
	return key, strings.TrimSpace(value), true
}

func maskSecret(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "****"
	}
	return value[:4] + "..." + value[len(value)-4:]
}

func printRestartHint(args cliArgs) {
	fmt.Println("Config: " + envPath(args))
	fmt.Println("Restart: abra down && abra up")
	fmt.Println("Check:   abra status")
	fmt.Println("After changing embedding providers, sync important sources again so vector recall uses the new embedding space.")
}
