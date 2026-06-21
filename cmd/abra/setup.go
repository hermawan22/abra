package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"
)

func setup(ctx context.Context, args cliArgs) error {
	if boolFlag(args, "production") {
		args.Bools["production"] = true
		if err := initEnv(args); err != nil {
			return err
		}
		fmt.Println("Production env created.")
		fmt.Println("Configure embeddings with:")
		fmt.Println("  abra config model compatible --base-url https://models.example.com/v1 --model embedding-model --dimensions 1024 --env-file " + envPath(args))
		fmt.Println("For an intentionally self-hosted local Qwen endpoint in production, also set ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=true and ABRA_LOCAL_EMBEDDING_IMAGE to a digest-pinned runner image after reviewing capacity and security.")
		fmt.Println("Then start with:")
		fmt.Println("  abra up --env-file " + envPath(args))
		return nil
	}

	interactive := isInteractive()
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("Abra setup")
	fmt.Println("This will check prerequisites, create the runtime env, choose the embedding provider used for retrieval, and optionally start the local stack.")
	fmt.Println("Common paths write config through CLI commands; no manual env or Codex config editing is required.")
	fmt.Println()
	printSetupPrerequisites()
	fmt.Println("Env file: " + envPath(args))

	if !boolFlag(args, "no-start") && !boolFlag(args, "skip-up") && !isAbraSourceCheckout(".") {
		if _, err := ensureProjectDir(ctx, args); err != nil {
			return err
		}
	}
	if err := ensureEnv(args); err != nil {
		return err
	}
	if err := normalizeLocalRuntimeDefaults(args); err != nil {
		return err
	}
	if err := setupEmbeddingConfig(args, reader, interactive); err != nil {
		return err
	}

	if boolFlag(args, "no-start") || boolFlag(args, "skip-up") {
		fmt.Println("Skipped stack start.")
		printSetupNext(args)
		return nil
	}
	if setupUsesLocalEmbeddings(args) && !boolFlag(args, "no-models") && !boolFlag(args, "skip-models") {
		startModels := "yes"
		if interactive && !boolFlag(args, "yes") {
			var err error
			startModels, err = promptDefault(reader, "Start local Qwen embedding and reranker runners now?", "yes")
			if err != nil {
				return err
			}
		}
		if yesish(startModels) {
			if err := modelsUp(ctx, args); err != nil {
				return err
			}
			args.Bools["skip-models"] = true
		} else {
			fmt.Println("Skipped separate model start.")
			fmt.Println("abra up starts them automatically for provider=local; use abra models up only to repair them directly.")
		}
	}
	if interactive && !boolFlag(args, "yes") {
		start, err := promptDefault(reader, "Start local stack now?", "yes")
		if err != nil {
			return err
		}
		if !yesish(start) {
			fmt.Println("Skipped stack start.")
			printSetupNext(args)
			return nil
		}
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("missing required command: docker")
	}
	if err := up(ctx, setupStackArgs(args)); err != nil {
		return err
	}
	fmt.Println("Local stack is ready.")
	printSetupNext(args)
	return nil
}

func setupStackArgs(args cliArgs) cliArgs {
	out := cliArgs{
		Command: args.Command,
		Flags:   map[string]string{},
		Bools:   map[string]bool{},
		Rest:    append([]string(nil), args.Rest...),
	}
	for key, value := range args.Flags {
		out.Flags[key] = value
	}
	for key, value := range args.Bools {
		out.Bools[key] = value
	}
	delete(out.Flags, "base-url")
	return out
}

func setupUsesLocalEmbeddings(args cliArgs) bool {
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return false
	}
	return isLocalProviderName(values["EMBEDDING_PROVIDER"])
}

func normalizeLocalRuntimeDefaults(args cliArgs) error {
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return err
	}
	updates := map[string]string{}
	raw := strings.TrimSpace(values["WORKER_INTERVAL"])
	if raw == "" {
		updates["WORKER_INTERVAL"] = defaultWorkerInterval.String()
	} else {
		interval, err := time.ParseDuration(raw)
		if err != nil || interval < 10*time.Second {
			updates["WORKER_INTERVAL"] = defaultWorkerInterval.String()
		}
	}
	if !validIntRange(values["WORKER_MAX_SOURCES_PER_RUN"], 1, 1000) {
		updates["WORKER_MAX_SOURCES_PER_RUN"] = "25"
	}
	if !validIntRange(values["WORKER_CONCURRENCY"], 1, 32) {
		updates["WORKER_CONCURRENCY"] = "1"
	}
	if len(updates) == 0 {
		return nil
	}
	return updateEnvValues(args, updates)
}

func validIntRange(raw string, minValue, maxValue int) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return false
	}
	return value >= minValue && value <= maxValue
}

func printSetupPrerequisites() {
	fmt.Println("Prerequisites:")
	for _, name := range []string{"docker", "curl", "sh"} {
		if path, err := exec.LookPath(name); err == nil {
			fmt.Println("  ok   " + name + " " + path)
		} else {
			fmt.Println("  miss " + name)
		}
	}
	fmt.Println()
}

func setupEmbeddingConfig(args cliArgs, reader *bufio.Reader, interactive bool) error {
	mode, err := setupProvider(args)
	if err != nil {
		return err
	}
	switch {
	case boolFlag(args, "local"):
		mode = "local"
	case boolFlag(args, "qwen3"):
		mode = "qwen3"
	case boolFlag(args, "openai"):
		mode = "openai"
	case boolFlag(args, "compatible"):
		mode = "compatible"
	}
	if mode == "" {
		if interactive && !boolFlag(args, "yes") {
			fmt.Println("Embedding provider (used for retrieval, not chat/LLM answers):")
			fmt.Println("  1. local - built-in Qwen3 embedding and reranker runners")
			fmt.Println("  2. compatible - custom OpenAI-compatible embedding endpoint")
			fmt.Println("  3. openai - OpenAI text-embedding-3-small convenience alias")
			choice, err := promptDefault(reader, "Choose embedding provider [1/2/3]", "1")
			if err != nil {
				return err
			}
			switch strings.TrimSpace(strings.ToLower(choice)) {
			case "2", "compatible":
				mode = "compatible"
			case "3", "openai":
				mode = "openai"
			default:
				mode = "local"
			}
		} else {
			mode = "local"
		}
	}

	switch mode {
	case "local":
		return setupLocalNeuralEmbeddings(args, reader, interactive)
	case "qwen3", "local-smart":
		return setupLocalNeuralEmbeddings(args, reader, interactive)
	case "openai":
		return setupOpenAIEmbeddings(args, reader, interactive)
	case "compatible", "openai-compatible":
		return setupCompatibleEmbeddings(args, reader, interactive)
	default:
		return fmt.Errorf("unknown setup embedding provider %q; use local, compatible, or openai", mode)
	}
}

func setupProvider(args cliArgs) (string, error) {
	selectors := []string{}
	for _, name := range []string{"local", "qwen3", "openai", "compatible"} {
		if boolFlag(args, name) {
			selectors = append(selectors, "--"+name)
		}
	}
	for _, name := range []string{"provider", "mode"} {
		if flag(args, name, "") != "" {
			selectors = append(selectors, "--"+name)
		}
	}
	if legacyModel := strings.ToLower(strings.TrimSpace(flag(args, "model", ""))); legacyModel == "local" || legacyModel == "qwen3" || legacyModel == "local-smart" || legacyModel == "openai" || legacyModel == "compatible" || legacyModel == "openai-compatible" {
		selectors = append(selectors, "--model")
	}
	if len(selectors) > 1 {
		return "", fmt.Errorf("choose one embedding provider only; conflicting flags: %s", strings.Join(selectors, ", "))
	}

	mode := strings.ToLower(strings.TrimSpace(firstNonEmpty(flag(args, "provider", ""), flag(args, "mode", ""))))
	if mode != "" {
		return mode, nil
	}
	legacyModel := strings.ToLower(strings.TrimSpace(flag(args, "model", "")))
	switch legacyModel {
	case "local", "qwen3", "local-smart", "openai", "compatible", "openai-compatible":
		return legacyModel, nil
	default:
		return "", nil
	}
}

func setupEmbeddingModel(args cliArgs, fallback string) string {
	if value := firstNonEmpty(flag(args, "embedding-model", ""), flag(args, "model-name", "")); value != "" {
		return value
	}
	legacyModel := strings.TrimSpace(flag(args, "model", ""))
	switch strings.ToLower(legacyModel) {
	case "", "local", "qwen3", "local-smart", "openai", "compatible", "openai-compatible":
		return fallback
	default:
		return legacyModel
	}
}

func setupLocalNeuralEmbeddings(args cliArgs, reader *bufio.Reader, interactive bool) error {
	baseURL := containerReachableBaseURL(setupEmbeddingBaseURL(args, defaultEmbeddingBaseURL))
	model := setupEmbeddingModel(args, defaultServedModelName)
	dimensions := firstNonEmpty(flag(args, "dimensions", ""), "1024")
	rerankerProvider := firstNonEmpty(flag(args, "reranker-provider", ""), "local")
	rerankerBaseURL := firstNonEmpty(flag(args, "reranker-base-url", ""), defaultRerankerBaseURLForProvider(rerankerProvider))
	rerankerModel := firstNonEmpty(flag(args, "reranker-model", ""), defaultRerankerModelForProvider(rerankerProvider))
	apiKey := flag(args, "api-key", "")
	if apiKey == "" && boolFlag(args, "api-key-stdin") {
		bytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		apiKey = strings.TrimSpace(string(bytes))
	}
	if interactive && !boolFlag(args, "yes") {
		var err error
		baseURL, err = promptDefault(reader, "Qwen3 OpenAI-compatible base URL", baseURL)
		if err != nil {
			return err
		}
		model, err = promptDefault(reader, "Embedding request model name", model)
		if err != nil {
			return err
		}
		dimensions, err = promptDefault(reader, "Embedding dimensions", dimensions)
		if err != nil {
			return err
		}
		rerankerProvider, err = promptDefault(reader, "Reranker provider", rerankerProvider)
		if err != nil {
			return err
		}
		rerankerBaseURL, err = promptDefault(reader, "Reranker base URL", rerankerBaseURL)
		if err != nil {
			return err
		}
		rerankerModel, err = promptDefault(reader, "Reranker request model", rerankerModel)
		if err != nil {
			return err
		}
	}
	baseURL = containerReachableBaseURL(strings.TrimSpace(baseURL))
	rerankerBaseURL = containerReachableBaseURL(strings.TrimSpace(rerankerBaseURL))
	if isDisabledProviderName(rerankerProvider) {
		rerankerBaseURL = ""
		rerankerModel = ""
	}
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                   "local",
		"EMBEDDING_BASE_URL":                   strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		"EMBEDDING_API_KEY":                    strings.TrimSpace(apiKey),
		"EMBEDDING_MODEL":                      strings.TrimSpace(model),
		"EMBEDDING_DIMENSIONS":                 strings.TrimSpace(dimensions),
		"EMBEDDING_TIMEOUT":                    firstNonEmpty(flag(args, "embedding-timeout", ""), "10m"),
		"ABRA_EMBEDDING_BATCH_MAX_ITEMS":       firstNonEmpty(flag(args, "embedding-batch-max-items", ""), "6"),
		"ABRA_EMBEDDING_BATCH_MAX_TOKENS":      firstNonEmpty(flag(args, "embedding-batch-max-tokens", ""), "3000"),
		"ABRA_AI_PROVIDER_CONCURRENCY":         firstNonEmpty(flag(args, "provider-concurrency", ""), "1"),
		"RERANKER_PROVIDER":                    strings.TrimSpace(rerankerProvider),
		"RERANKER_BASE_URL":                    strings.TrimSpace(rerankerBaseURL),
		"RERANKER_API_KEY":                     strings.TrimSpace(apiKey),
		"RERANKER_MODEL":                       strings.TrimSpace(rerankerModel),
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "false",
	}); err != nil {
		return err
	}
	fmt.Println("Embedding: local neural default (Qwen3-compatible)")
	fmt.Println("Reranker: local neural default (Qwen3-compatible)")
	fmt.Println("Local model runners: started automatically by abra up; inspect with abra models status")
	fmt.Println("Host endpoint: embedding http://127.0.0.1:8080/v1")
	fmt.Println("Host endpoint: reranker  http://127.0.0.1:8081/v1")
	fmt.Println("Compose endpoints are written as host.docker.internal so Abra containers can reach those host services.")
	fmt.Println("After changing embedding providers, re-ingest important sources so vector recall uses the new embedding space.")
	return nil
}

func setupOpenAIEmbeddings(args cliArgs, reader *bufio.Reader, interactive bool) error {
	args.Bools["openai"] = true
	args.Flags["embedding-base-url"] = setupEmbeddingBaseURL(args, "https://api.openai.com/v1")
	args.Flags["embedding-model"] = setupEmbeddingModel(args, "text-embedding-3-small")
	if flag(args, "api-key", "") == "" && !boolFlag(args, "api-key-stdin") {
		if envKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); envKey != "" {
			args.Flags["api-key"] = envKey
		} else if !interactive || boolFlag(args, "yes") {
			return errors.New("setup --openai requires an API key in non-interactive mode; pass --api-key-stdin or set OPENAI_API_KEY")
		}
	}
	return setupCompatibleEmbeddings(args, reader, interactive)
}

func setupCompatibleEmbeddings(args cliArgs, reader *bufio.Reader, interactive bool) error {
	if (!interactive || boolFlag(args, "yes")) && !boolFlag(args, "openai") {
		if strings.TrimSpace(flag(args, "embedding-base-url", "")) == "" && strings.TrimSpace(flag(args, "base-url", "")) == "" {
			return errors.New("setup --compatible requires --embedding-base-url in non-interactive mode; use --openai for OpenAI")
		}
		if strings.TrimSpace(flag(args, "embedding-model", "")) == "" && strings.TrimSpace(flag(args, "model", "")) == "" {
			return errors.New("setup --compatible requires --embedding-model in non-interactive mode; use --openai for OpenAI")
		}
	}
	baseURL := setupEmbeddingBaseURL(args, "https://api.openai.com/v1")
	model := setupEmbeddingModel(args, "text-embedding-3-small")
	dimensions := firstNonEmpty(flag(args, "dimensions", ""), inferEmbeddingDimensions(model))
	apiKey := flag(args, "api-key", "")
	if apiKey == "" && boolFlag(args, "api-key-stdin") {
		bytes, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		apiKey = strings.TrimSpace(string(bytes))
	}
	if interactive && !boolFlag(args, "yes") {
		var err error
		baseURL, err = promptDefault(reader, "Embedding base URL", baseURL)
		if err != nil {
			return err
		}
		model, err = promptDefault(reader, "Embedding request model name", model)
		if err != nil {
			return err
		}
		dimensions, err = promptDefault(reader, "Embedding dimensions", dimensions)
		if err != nil {
			return err
		}
		if strings.TrimSpace(apiKey) == "" {
			apiKey, err = promptSecret("Embedding API key")
			if err != nil {
				return err
			}
		}
	}
	if strings.TrimSpace(dimensions) == "" {
		return fmt.Errorf("embedding dimensions are required for compatible model %q; pass --dimensions <size> so Abra can validate vector storage correctly", strings.TrimSpace(model))
	}
	baseURL = containerReachableBaseURL(strings.TrimSpace(baseURL))
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                   "compatible",
		"EMBEDDING_BASE_URL":                   baseURL,
		"EMBEDDING_API_KEY":                    strings.TrimSpace(apiKey),
		"EMBEDDING_MODEL":                      strings.TrimSpace(model),
		"EMBEDDING_DIMENSIONS":                 strings.TrimSpace(dimensions),
		"EMBEDDING_TIMEOUT":                    firstNonEmpty(flag(args, "embedding-timeout", ""), "30s"),
		"ABRA_EMBEDDING_BATCH_MAX_ITEMS":       firstNonEmpty(flag(args, "embedding-batch-max-items", ""), "16"),
		"ABRA_EMBEDDING_BATCH_MAX_TOKENS":      firstNonEmpty(flag(args, "embedding-batch-max-tokens", ""), "6000"),
		"ABRA_AI_PROVIDER_CONCURRENCY":         firstNonEmpty(flag(args, "provider-concurrency", ""), "4"),
		"RERANKER_PROVIDER":                    "",
		"RERANKER_BASE_URL":                    "",
		"RERANKER_API_KEY":                     "",
		"RERANKER_MODEL":                       "",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "false",
	}); err != nil {
		return err
	}
	fmt.Println("Embedding: compatible " + strings.TrimSpace(model))
	if isLoopbackProviderURL(baseURL) {
		fmt.Println("Endpoint: " + baseURL + " (rewritten so Abra containers can reach the host service)")
	}
	fmt.Println("After changing embedding providers, re-ingest important sources so vector recall uses the new embedding space.")
	return nil
}

func setupEmbeddingBaseURL(args cliArgs, fallback string) string {
	return firstNonEmpty(flag(args, "embedding-base-url", ""), flag(args, "base-url", ""), fallback)
}

func promptDefault(reader *bufio.Reader, label, fallback string) (string, error) {
	if fallback == "" {
		fmt.Print(label + ": ")
	} else {
		fmt.Print(label + " [" + fallback + "]: ")
	}
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback, nil
	}
	return value, nil
}

func promptSecret(label string) (string, error) {
	fmt.Print(label + ": ")
	bytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(bytes)), nil
}

func printSetupNext(args cliArgs) {
	values := setupConfiguredValues(args)
	provider := strings.TrimSpace(values["EMBEDDING_PROVIDER"])
	label := setupProviderLabel(values)
	fmt.Println("Next:")
	fmt.Println("  abra up --env-file " + envPath(args))
	fmt.Println("  abra doctor")
	fmt.Println("  From a source checkout, use `go run ./cmd/abra <command>` instead of `abra <command>` until the release binary is installed.")
	if !isLocalProviderName(provider) && provider != "" {
		fmt.Println("  verify your " + label + " embedding endpoint is reachable from Abra")
	}
	fmt.Println("Codex MCP and repo onboarding:")
	fmt.Println("  cd /path/to/project")
	fmt.Println("  abra scope")
	fmt.Println("  abra agents bootstrap --agent codex   # installs Codex MCP, ingests this repo, and verifies")
	fmt.Println("  fully quit and reopen Codex Desktop")
	fmt.Println("  abra agents ready . --scope <scope-from-abra-scope> --json")
	fmt.Println(`  abra think "What should I know before changing this project?" --scope <scope-from-abra-scope>`)
	fmt.Println("Manual alternative to bootstrap:")
	fmt.Println("  abra mcp status")
	fmt.Println("  abra agents init --agent codex")
	fmt.Println("  abra agents verify . --scope <scope-from-abra-scope>")
	fmt.Println("  abra ingest . --code --scope <scope-from-abra-scope>   # only if verify reports missing scope or empty memory")
	fmt.Println("  abra mcp install-codex")
	fmt.Println("  fully quit and reopen Codex Desktop")
	fmt.Println("  abra agents ready . --scope <scope-from-abra-scope> --json")
	fmt.Println("If Codex says Abra has no context, run `abra agents ready . --scope <scope-from-abra-scope> --json` first. Reinstall/restart MCP when server_ready=true but agent_ready=false; re-ingest only if verify reports missing scope or empty source-backed memory.")
}

func setupConfiguredValues(args cliArgs) map[string]string {
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return map[string]string{}
	}
	return values
}

func setupProviderLabel(values map[string]string) string {
	baseURL := strings.ToLower(strings.TrimSpace(values["EMBEDDING_BASE_URL"]))
	model := strings.ToLower(strings.TrimSpace(values["EMBEDDING_MODEL"]))
	if strings.Contains(baseURL, "api.openai.com") || strings.HasPrefix(model, "text-embedding-") {
		return "OpenAI"
	}
	provider := strings.TrimSpace(values["EMBEDDING_PROVIDER"])
	if provider == "" {
		return "configured"
	}
	return provider
}

func isLoopbackProviderURL(value string) bool {
	value = strings.ToLower(value)
	return strings.Contains(value, "host.docker.internal") || strings.Contains(value, "127.0.0.1") || strings.Contains(value, "localhost") || strings.Contains(value, "[::1]")
}

func isInteractive() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func yesish(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "y", "yes", "true", "on":
		return true
	default:
		return false
	}
}
