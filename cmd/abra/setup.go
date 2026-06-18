package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

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
		fmt.Println("  abra config model local --env-file " + envPath(args))
		fmt.Println("Then start with:")
		fmt.Println("  abra up --env-file " + envPath(args))
		return nil
	}

	interactive := isInteractive()
	reader := bufio.NewReader(os.Stdin)
	fmt.Println("Abra setup")
	fmt.Println("This will check prerequisites, create the runtime env, choose embeddings, and optionally start the local stack.")
	fmt.Println()
	printSetupPrerequisites()
	fmt.Println("Env file: " + envPath(args))

	if err := ensureEnv(args); err != nil {
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
			startModels, err = promptDefault(reader, "Start local Qwen embedding model now?", "yes")
			if err != nil {
				return err
			}
		}
		if yesish(startModels) {
			if err := modelsUp(ctx, args); err != nil {
				return err
			}
		} else {
			fmt.Println("Skipped local model runner.")
			fmt.Println("Run before ingest: abra models up")
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
	if err := up(ctx, args); err != nil {
		return err
	}
	fmt.Println("Local stack is ready.")
	return nil
}

func setupUsesLocalEmbeddings(args cliArgs) bool {
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return false
	}
	return strings.TrimSpace(values["EMBEDDING_PROVIDER"]) == "local"
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
			fmt.Println("Embedding model:")
			fmt.Println("  1. local - default Qwen3 local neural embeddings via an OpenAI-compatible local server")
			fmt.Println("  2. compatible - custom OpenAI-compatible embedding endpoint")
			fmt.Println("  3. openai - OpenAI text-embedding-3-small convenience alias")
			choice, err := promptDefault(reader, "Choose embedding model [1/2/3]", "1")
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
		return fmt.Errorf("unknown setup model %q; use local, compatible, or openai", mode)
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
	baseURL := firstNonEmpty(flag(args, "base-url", ""), "http://host.docker.internal:8080/v1")
	model := setupEmbeddingModel(args, "text-embeddings-inference")
	dimensions := firstNonEmpty(flag(args, "dimensions", ""), "1024")
	rerankerBaseURL := firstNonEmpty(flag(args, "reranker-base-url", ""), "http://host.docker.internal:8081")
	rerankerModel := firstNonEmpty(flag(args, "reranker-model", ""), "text-embeddings-inference")
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
		model, err = promptDefault(reader, "Embedding request model", model)
		if err != nil {
			return err
		}
		dimensions, err = promptDefault(reader, "Embedding dimensions", dimensions)
		if err != nil {
			return err
		}
		rerankerBaseURL, err = promptDefault(reader, "Qwen3 reranker base URL", rerankerBaseURL)
		if err != nil {
			return err
		}
		rerankerModel, err = promptDefault(reader, "Reranker request model", rerankerModel)
		if err != nil {
			return err
		}
	}
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                   "local",
		"EMBEDDING_BASE_URL":                   strings.TrimSpace(baseURL),
		"EMBEDDING_API_KEY":                    strings.TrimSpace(apiKey),
		"EMBEDDING_MODEL":                      strings.TrimSpace(model),
		"EMBEDDING_DIMENSIONS":                 strings.TrimSpace(dimensions),
		"RERANKER_PROVIDER":                    "local",
		"RERANKER_BASE_URL":                    strings.TrimSpace(rerankerBaseURL),
		"RERANKER_API_KEY":                     strings.TrimSpace(apiKey),
		"RERANKER_MODEL":                       strings.TrimSpace(rerankerModel),
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "false",
	}); err != nil {
		return err
	}
	fmt.Println("Embedding: local neural default (Qwen3-compatible)")
	fmt.Println("Local model runner: abra models up")
	fmt.Println("Host endpoint: embedding http://127.0.0.1:8080/v1")
	fmt.Println("Compose endpoints are written as host.docker.internal so Abra containers can reach those host services.")
	fmt.Println("After changing embedding providers, re-ingest important sources so vector recall uses the new embedding space.")
	return nil
}

func setupOpenAIEmbeddings(args cliArgs, reader *bufio.Reader, interactive bool) error {
	args.Flags["base-url"] = firstNonEmpty(flag(args, "base-url", ""), "https://api.openai.com/v1")
	args.Flags["embedding-model"] = setupEmbeddingModel(args, "text-embedding-3-small")
	args.Flags["dimensions"] = firstNonEmpty(flag(args, "dimensions", ""), "1536")
	return setupCompatibleEmbeddings(args, reader, interactive)
}

func setupCompatibleEmbeddings(args cliArgs, reader *bufio.Reader, interactive bool) error {
	baseURL := firstNonEmpty(flag(args, "base-url", ""), "https://api.openai.com/v1")
	model := setupEmbeddingModel(args, "text-embedding-3-small")
	dimensions := firstNonEmpty(flag(args, "dimensions", ""), "1536")
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
		model, err = promptDefault(reader, "Embedding model", model)
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
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                   "compatible",
		"EMBEDDING_BASE_URL":                   strings.TrimSpace(baseURL),
		"EMBEDDING_API_KEY":                    strings.TrimSpace(apiKey),
		"EMBEDDING_MODEL":                      strings.TrimSpace(model),
		"EMBEDDING_DIMENSIONS":                 strings.TrimSpace(dimensions),
		"RERANKER_PROVIDER":                    "",
		"RERANKER_BASE_URL":                    "",
		"RERANKER_API_KEY":                     "",
		"RERANKER_MODEL":                       "",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "false",
	}); err != nil {
		return err
	}
	fmt.Println("Embedding: compatible " + strings.TrimSpace(model))
	fmt.Println("After changing embedding providers, re-ingest important sources so vector recall uses the new embedding space.")
	return nil
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
	fmt.Println("Next:")
	fmt.Println("  abra models up")
	fmt.Println("  abra up --env-file " + envPath(args))
	fmt.Println("  abra ingest . --code")
	fmt.Println(`  abra think "What should I know before changing this project?"`)
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
