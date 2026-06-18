package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	defaultEmbeddingContainer = "abra-embedding"
	defaultEmbeddingModelID   = "Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0"
	defaultServedModelName    = defaultEmbeddingModelID
	defaultEmbeddingBaseURL   = "http://host.docker.internal:8080/v1"
)

type embeddingRunnerConfig struct {
	Container string
	Image     string
	ModelID   string
	Model     string
	BaseURL   string
	Port      string
	CacheDir  string
	Dims      int
}

func models(ctx context.Context, args cliArgs) error {
	action := "status"
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch action {
	case "", "status", "check":
		return modelsStatus(ctx, args)
	case "up", "start":
		return modelsUp(ctx, args)
	case "down", "stop":
		return modelsDown(args)
	case "logs", "log":
		return modelsLogs(args)
	default:
		return fmt.Errorf("unknown models command %q\n\n%s", action, commandUsage("models"))
	}
}

func modelsUp(ctx context.Context, args cliArgs) error {
	cfg := embeddingRunner(args)
	if err := syncLocalRunnerEnv(args); err != nil {
		return err
	}
	cfg = embeddingRunner(args)
	if _, err := execLookPath("docker"); err != nil {
		return errors.New("missing required command: docker")
	}
	if boolFlag(args, "recreate") {
		_, _ = commandOutput("docker", "rm", "-f", cfg.Container)
	}
	exists := dockerContainerExists(cfg.Container)
	if exists {
		image := dockerContainerImage(cfg.Container)
		if image != "" && image != cfg.Image {
			fmt.Println("Replacing local embedding container image: " + image + " -> " + cfg.Image)
			if _, err := commandOutput("docker", "rm", "-f", cfg.Container); err != nil {
				return err
			}
			exists = false
		}
		if exists && !strings.Contains(dockerContainerCommand(cfg.Container), "--ctx-size 32768") {
			fmt.Println("Replacing local embedding container config: ctx-size 32768")
			if _, err := commandOutput("docker", "rm", "-f", cfg.Container); err != nil {
				return err
			}
			exists = false
		}
	}
	if !exists {
		if err := os.MkdirAll(cfg.CacheDir, 0o755); err != nil {
			return err
		}
		fmt.Println("Starting local embedding model:")
		fmt.Println("  model: " + cfg.ModelID)
		fmt.Println("  image: " + cfg.Image)
		fmt.Println("  port:  " + cfg.Port)
		step := []string{
			"run", "-d",
			"--name", cfg.Container,
			"--pull", "always",
			"-p", cfg.Port + ":8080",
			"-v", cfg.CacheDir + ":/root/.cache/huggingface",
			cfg.Image,
			"-hf", cfg.ModelID,
			"--embedding",
			"--pooling", "last",
			"--ctx-size", "32768",
			"--host", "0.0.0.0",
			"--port", "8080",
		}
		if err := runCommand("docker", step...); err != nil {
			return err
		}
	} else {
		fmt.Println("Starting existing local embedding container: " + cfg.Container)
		if err := runCommand("docker", "start", cfg.Container); err != nil {
			return err
		}
	}
	fmt.Println("First run may download model weights; this can take several minutes.")
	fmt.Println("Waiting for embeddings endpoint: " + cfg.BaseURL + "/embeddings")
	if err := waitEmbeddingReady(ctx, cfg, 10*time.Minute); err != nil {
		return fmt.Errorf("%w\nRun: abra models logs\nRun: abra models status", err)
	}
	fmt.Println("Local embeddings ready")
	fmt.Println("Next: abra up")
	return nil
}

func modelsStatus(ctx context.Context, args cliArgs) error {
	cfg := embeddingRunner(args)
	err := checkEmbeddingReady(ctx, cfg)
	status := map[string]any{
		"container": cfg.Container,
		"model_id":  cfg.ModelID,
		"model":     cfg.Model,
		"base_url":  cfg.BaseURL,
		"port":      cfg.Port,
		"ready":     err == nil,
	}
	if err != nil {
		status["error"] = err.Error()
		status["hint"] = "run: abra models up"
	}
	if boolFlag(args, "json") {
		return printJSON(status)
	}
	if err != nil {
		fmt.Println("Local embeddings: not ready")
		fmt.Println("endpoint: " + cfg.BaseURL + "/embeddings")
		fmt.Println("hint:     abra models up")
		fmt.Println("error:    " + err.Error())
		return nil
	}
	fmt.Println("Local embeddings: ready")
	fmt.Println("endpoint: " + cfg.BaseURL + "/embeddings")
	fmt.Println("model:    " + cfg.Model)
	return nil
}

func modelsDown(args cliArgs) error {
	cfg := embeddingRunner(args)
	if !dockerContainerExists(cfg.Container) {
		fmt.Println("Local embedding container is not present: " + cfg.Container)
		return nil
	}
	if err := runCommand("docker", "rm", "-f", cfg.Container); err != nil {
		return err
	}
	fmt.Println("Stopped local embedding container: " + cfg.Container)
	return nil
}

func modelsLogs(args cliArgs) error {
	cfg := embeddingRunner(args)
	if !dockerContainerExists(cfg.Container) {
		return errors.New("local embedding container is not present; run: abra models up")
	}
	lines := flag(args, "tail", "120")
	return runCommand("docker", "logs", "--tail", lines, cfg.Container)
}

func embeddingRunner(args cliArgs) embeddingRunnerConfig {
	values, _ := readEnvValues(envPath(args))
	if provider := strings.TrimSpace(values["EMBEDDING_PROVIDER"]); provider != "" && provider != "local" {
		values = map[string]string{}
	}
	baseURL := firstNonEmpty(flag(args, "base-url", ""), values["EMBEDDING_BASE_URL"], defaultEmbeddingBaseURL)
	model := firstNonEmpty(flag(args, "model", ""), values["EMBEDDING_MODEL"], defaultServedModelName)
	dims := intFromString(firstNonEmpty(flag(args, "dimensions", ""), values["EMBEDDING_DIMENSIONS"]), 1024)
	port := firstNonEmpty(flag(args, "port", ""), portFromBaseURL(baseURL), "8080")
	hostBaseURL := hostReachableBaseURL(baseURL)
	if flag(args, "port", "") != "" {
		hostBaseURL = replaceURLHostPort(hostBaseURL, "127.0.0.1", port)
	}
	return embeddingRunnerConfig{
		Container: firstNonEmpty(flag(args, "container", ""), defaultEmbeddingContainer),
		Image:     firstNonEmpty(flag(args, "image", ""), defaultTEIImage()),
		ModelID:   firstNonEmpty(flag(args, "model-id", ""), defaultEmbeddingModelID),
		Model:     model,
		BaseURL:   strings.TrimRight(hostBaseURL, "/"),
		Port:      port,
		CacheDir:  firstNonEmpty(flag(args, "cache-dir", ""), filepath.Join(userConfigDir(), "models", "llama.cpp")),
		Dims:      dims,
	}
}

func defaultTEIImage() string {
	return "ghcr.io/ggml-org/llama.cpp:server"
}

func dockerContainerExists(name string) bool {
	_, err := commandOutput("docker", "container", "inspect", name)
	return err == nil
}

func dockerContainerImage(name string) string {
	out, err := commandOutput("docker", "container", "inspect", "--format", "{{.Config.Image}}", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func dockerContainerCommand(name string) string {
	out, err := commandOutput("docker", "container", "inspect", "--format", "{{json .Args}}", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(out, `","`, " "))
}

func syncLocalRunnerEnv(args cliArgs) error {
	if err := ensureEnv(args); err != nil {
		return err
	}
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return err
	}
	if provider := strings.TrimSpace(values["EMBEDDING_PROVIDER"]); provider != "" && provider != "local" {
		return nil
	}
	cfg := embeddingRunner(args)
	model := firstNonEmpty(flag(args, "model", ""), values["EMBEDDING_MODEL"], defaultServedModelName)
	dims := intFromString(firstNonEmpty(flag(args, "dimensions", ""), values["EMBEDDING_DIMENSIONS"]), 1024)
	baseURL := firstNonEmpty(flag(args, "base-url", ""), values["EMBEDDING_BASE_URL"], defaultEmbeddingBaseURL)
	if flag(args, "port", "") != "" {
		baseURL = replaceURLHostPort(baseURL, "host.docker.internal", cfg.Port)
	} else {
		baseURL = containerReachableBaseURL(baseURL)
	}
	return updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                   "local",
		"EMBEDDING_BASE_URL":                   strings.TrimRight(baseURL, "/"),
		"EMBEDDING_API_KEY":                    "",
		"EMBEDDING_MODEL":                      model,
		"EMBEDDING_DIMENSIONS":                 strconv.Itoa(dims),
		"RERANKER_PROVIDER":                    "",
		"RERANKER_BASE_URL":                    "",
		"RERANKER_API_KEY":                     "",
		"RERANKER_MODEL":                       "",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "false",
	})
}

func waitEmbeddingReady(ctx context.Context, cfg embeddingRunnerConfig, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		lastErr = checkEmbeddingReady(ctx, cfg)
		if lastErr == nil {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("local embedding model did not become ready: %v", lastErr)
}

func checkEmbeddingReady(ctx context.Context, cfg embeddingRunnerConfig) error {
	body := map[string]any{
		"model": cfg.Model,
		"input": []string{"abra readiness check"},
	}
	if cfg.Dims > 0 {
		body["dimensions"] = cfg.Dims
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func localEmbeddingCheck(ctx context.Context, args cliArgs) map[string]any {
	values, err := readEnvValues(envPath(args))
	if err != nil || strings.TrimSpace(values["EMBEDDING_PROVIDER"]) != "local" {
		return map[string]any{"name": "local_embeddings", "ok": true, "skipped": true}
	}
	cfg := embeddingRunner(args)
	if err := checkEmbeddingReady(ctx, cfg); err != nil {
		return map[string]any{
			"name":  "local_embeddings",
			"ok":    false,
			"error": err.Error(),
			"hint":  "run: abra models up",
		}
	}
	return map[string]any{"name": "local_embeddings", "ok": true, "endpoint": cfg.BaseURL + "/embeddings"}
}

func friendlyProviderError(err error) error {
	if err == nil {
		return nil
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "/embeddings") || strings.Contains(text, "embedding") || strings.Contains(text, "host.docker.internal:8080") || strings.Contains(text, "connection refused") {
		return fmt.Errorf("embedding provider is not ready. Run `abra models up`, then `abra up`, then retry ingest. Original error: %w", err)
	}
	return err
}

func hostReachableBaseURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return strings.TrimRight(value, "/")
	}
	host := parsed.Hostname()
	if host == "host.docker.internal" || host == "localhost" || host == "::1" {
		return replaceURLHostPort(value, "127.0.0.1", parsed.Port())
	}
	return strings.TrimRight(value, "/")
}

func containerReachableBaseURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return strings.TrimRight(value, "/")
	}
	host := parsed.Hostname()
	if host == "127.0.0.1" || host == "localhost" || host == "::1" {
		return replaceURLHostPort(value, "host.docker.internal", parsed.Port())
	}
	return strings.TrimRight(value, "/")
}

func replaceURLHostPort(value, host, port string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" {
		return value
	}
	if port != "" {
		parsed.Host = host + ":" + port
	} else {
		parsed.Host = host
	}
	return strings.TrimRight(parsed.String(), "/")
}

func portFromBaseURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	if port := parsed.Port(); port != "" {
		return port
	}
	switch parsed.Scheme {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func intFromString(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func execLookPath(name string) (string, error) {
	return exec.LookPath(name)
}
