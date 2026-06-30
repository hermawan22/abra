package main

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	defaultEmbeddingPublish   = "127.0.0.1"

	defaultRerankerContainer       = "abra-reranker"
	defaultRerankerModelID         = "Qwen/Qwen3-Reranker-0.6B-GGUF:Q8_0"
	defaultRerankerServedModelName = defaultRerankerModelID
	defaultRerankerBaseURL         = "http://host.docker.internal:8081/v1"

	localRunnerHashLabel         = "io.abra.local-embedding.config-hash"
	localRunnerModelLabel        = "io.abra.local-embedding.model-id"
	localRunnerDimsLabel         = "io.abra.local-embedding.dimensions"
	localRerankerRunnerHashLabel = "io.abra.local-reranker.config-hash"
	localRerankerModelLabel      = "io.abra.local-reranker.model-id"
)

type embeddingRunnerConfig struct {
	Kind             string
	Container        string
	Image            string
	PullPolicy       string
	ModelID          string
	Model            string
	BaseURL          string
	Publish          string
	Port             string
	CacheDir         string
	Dims             int
	ReadinessTimeout time.Duration
}

type dockerContainerRuntimeState struct {
	Status    string `json:"Status"`
	Running   bool   `json:"Running"`
	ExitCode  int    `json:"ExitCode"`
	Error     string `json:"Error"`
	OOMKilled bool   `json:"OOMKilled"`
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
	if err := requireLocalModelProvider(args, "up"); err != nil {
		return err
	}
	if err := syncLocalRunnerEnv(args); err != nil {
		return err
	}
	cfgs := localRunnerConfigs(args)
	for _, cfg := range cfgs {
		if err := validateLocalRunnerImagePolicy(args, cfg); err != nil {
			return err
		}
	}
	if err := ensureDockerDaemon(); err != nil {
		return err
	}
	if boolFlag(args, "recreate") {
		for _, cfg := range cfgs {
			_, _ = commandOutput("docker", "rm", "-f", cfg.Container)
		}
	}
	for _, cfg := range cfgs {
		if err := startLocalRunner(cfg); err != nil {
			return err
		}
	}
	fmt.Println("First run may download model weights; this can take several minutes.")
	startupTimeout := localRunnerStartupTimeout(args)
	if err := waitLocalRunnersReady(ctx, cfgs, startupTimeout); err != nil {
		return fmt.Errorf("%w\nRun: abra model logs\nRun: abra model status", err)
	}
	fmt.Println("Next: abra up")
	return nil
}

func startLocalRunner(cfg embeddingRunnerConfig) error {
	exists := dockerContainerExists(cfg.Container)
	if exists {
		image := dockerContainerImage(cfg.Container)
		if image != "" && image != cfg.Image {
			fmt.Println("Replacing local " + cfg.Kind + " container image: " + image + " -> " + cfg.Image)
			if _, err := commandOutput("docker", "rm", "-f", cfg.Container); err != nil {
				return err
			}
			exists = false
		}
		if exists && localRunnerNeedsRecreate(cfg) {
			fmt.Println("Replacing local " + cfg.Kind + " container config")
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
		fmt.Println("Starting local " + cfg.Kind + " model:")
		fmt.Println("  model: " + cfg.ModelID)
		fmt.Println("  image: " + cfg.Image)
		fmt.Println("  pull:  " + cfg.PullPolicy)
		fmt.Println("  bind:  " + localRunnerPublish(cfg))
		step := localRunnerDockerRunArgs(cfg)
		if err := runCommand("docker", step...); err != nil {
			return err
		}
	} else {
		fmt.Println("Starting existing local " + cfg.Kind + " container: " + cfg.Container)
		if err := runCommand("docker", "start", cfg.Container); err != nil {
			return err
		}
	}
	return nil
}

func localRunnerDockerRunArgs(cfg embeddingRunnerConfig) []string {
	step := []string{
		"run", "-d",
		"--name", cfg.Container,
		"--pull", cfg.PullPolicy,
		"--label", localRunnerHashLabelFor(cfg) + "=" + localRunnerConfigHash(cfg),
		"--label", localRunnerModelLabelFor(cfg) + "=" + cfg.ModelID,
		"-p", localRunnerPublish(cfg) + ":8080",
		"-v", cfg.CacheDir + ":/root/.cache/huggingface",
	}
	if cfg.Kind == "embedding" {
		step = append(step,
			"--label", localRunnerDimsLabel+"="+strconv.Itoa(cfg.Dims),
			cfg.Image,
			"-hf", cfg.ModelID,
			"--embedding",
			"--pooling", "last",
		)
	} else {
		step = append(step,
			cfg.Image,
			"-hf", cfg.ModelID,
			"--reranking",
			"--pooling", "rank",
		)
	}
	return append(step,
		"--ctx-size", "32768",
		"--host", "0.0.0.0",
		"--port", "8080",
	)
}

func modelsStatus(ctx context.Context, args cliArgs) error {
	if notice := inactiveLocalModelNotice(args); notice != nil {
		if boolFlag(args, "json") {
			return printJSON(notice)
		}
		fmt.Println("Local embeddings: inactive")
		fmt.Println("provider: " + stringValue(notice["provider"], ""))
		fmt.Println("detail:   " + stringValue(notice["detail"], ""))
		fmt.Println("hint:     " + stringValue(notice["hint"], ""))
		return nil
	}
	cfgs := localRunnerConfigs(args)
	statuses := make([]map[string]any, 0, len(cfgs))
	ready := true
	var firstErr error
	for _, cfg := range cfgs {
		runnerStatus := localRunnerStatus(ctx, cfg)
		statuses = append(statuses, runnerStatus)
		if ok, _ := runnerStatus["ready"].(bool); !ok {
			ready = false
			if firstErr == nil {
				firstErr = errors.New(stringValue(runnerStatus["error"], "not ready"))
			}
		}
	}
	status := cloneMap(statuses[0])
	status["ready"] = ready
	status["runners"] = statuses
	if len(statuses) > 1 {
		status["reranker"] = statuses[1]
	}
	if firstErr != nil {
		status["error"] = firstErr.Error()
		status["hint"] = "run: abra model up"
	}
	if boolFlag(args, "json") {
		return printJSON(status)
	}
	if !ready {
		fmt.Println("Local models: not ready")
		for _, runnerStatus := range statuses {
			fmt.Println(stringValue(runnerStatus["kind"], "model") + ": " + stringValue(runnerStatus["endpoint"], ""))
			if errText := stringValue(runnerStatus["error"], ""); errText != "" {
				fmt.Println("error:    " + errText)
			}
			if state := stringValue(runnerStatus["container_state"], ""); state != "" {
				fmt.Printf("state:    %s running=%t exit_code=%d oom_killed=%t\n",
					state,
					boolValue(runnerStatus["container_running"], false),
					intValue(runnerStatus["container_exit_code"]),
					boolValue(runnerStatus["container_oom_killed"], false),
				)
			}
		}
		fmt.Println("hint:     abra model up")
		fmt.Println("timeout:  " + cfgs[0].ReadinessTimeout.String())
		return nil
	}
	fmt.Println("Local models: ready")
	for _, runnerStatus := range statuses {
		fmt.Println(stringValue(runnerStatus["kind"], "model") + ": " + stringValue(runnerStatus["endpoint"], ""))
		fmt.Println("publish:  " + stringValue(runnerStatus["publish"], ""))
		fmt.Println("model:    " + stringValue(runnerStatus["model"], ""))
	}
	return nil
}

func localRunnerStatus(ctx context.Context, cfg embeddingRunnerConfig) map[string]any {
	err := localRunnerContainerExitError(cfg)
	if err == nil {
		err = checkLocalRunnerReady(ctx, cfg)
	}
	status := map[string]any{
		"kind":              cfg.Kind,
		"container":         cfg.Container,
		"model_id":          cfg.ModelID,
		"model":             cfg.Model,
		"base_url":          cfg.BaseURL,
		"endpoint":          localRunnerReadyURL(cfg),
		"publish":           localRunnerPublish(cfg),
		"port":              cfg.Port,
		"image":             cfg.Image,
		"pull_policy":       cfg.PullPolicy,
		"readiness_timeout": cfg.ReadinessTimeout.String(),
		"ready":             err == nil,
	}
	if dockerContainerExists(cfg.Container) {
		status["expected_config_hash"] = localRunnerConfigHash(cfg)
		status["container_config_hash"] = dockerContainerLabel(cfg.Container, localRunnerHashLabelFor(cfg))
		status["config_matches"] = !localRunnerNeedsRecreate(cfg)
	}
	if state, ok := dockerContainerState(cfg.Container); ok {
		status["container_state"] = state.Status
		status["container_running"] = state.Running
		status["container_exit_code"] = state.ExitCode
		status["container_oom_killed"] = state.OOMKilled
		if strings.TrimSpace(state.Error) != "" {
			status["container_error"] = strings.TrimSpace(state.Error)
		}
	}
	if err != nil {
		status["error"] = err.Error()
		status["hint"] = "run: abra model up"
	}
	return status
}

func modelsDown(args cliArgs) error {
	if err := requireLocalModelProvider(args, "down"); err != nil {
		return err
	}
	removed := 0
	for _, cfg := range localRunnerConfigs(args) {
		if !dockerContainerExists(cfg.Container) {
			fmt.Println("Local " + cfg.Kind + " container is not present: " + cfg.Container)
			continue
		}
		if err := runCommand("docker", "rm", "-f", cfg.Container); err != nil {
			return err
		}
		fmt.Println("Stopped local " + cfg.Kind + " container: " + cfg.Container)
		removed++
	}
	if removed == 0 {
		return nil
	}
	return nil
}

func modelsLogs(args cliArgs) error {
	if err := requireLocalModelProvider(args, "logs"); err != nil {
		return err
	}
	cfgs := localRunnerConfigs(args)
	present := 0
	for _, cfg := range cfgs {
		if dockerContainerExists(cfg.Container) {
			present++
		}
	}
	if present == 0 {
		return errors.New("local model containers are not present; run: abra model up")
	}
	lines := flag(args, "tail", "120")
	for _, cfg := range cfgs {
		if !dockerContainerExists(cfg.Container) {
			continue
		}
		if len(cfgs) > 1 {
			fmt.Println("==> " + cfg.Kind + " (" + cfg.Container + ")")
		}
		if err := runCommand("docker", "logs", "--tail", lines, cfg.Container); err != nil {
			return err
		}
	}
	return nil
}

func requireLocalModelProvider(args cliArgs, action string) error {
	if boolFlag(args, "force") {
		return nil
	}
	provider := configuredEmbeddingProvider(args)
	if provider == "" || isLocalProviderName(provider) {
		return nil
	}
	return fmt.Errorf("abra model %s manages only the built-in local embedding runner, but EMBEDDING_PROVIDER=%s in %s. Abra will use the configured provider instead. Use `abra config` to inspect it, or pass --force only if you intentionally want to manage the unused local runner", action, provider, envPath(args))
}

func inactiveLocalModelNotice(args cliArgs) map[string]any {
	if boolFlag(args, "force") {
		return nil
	}
	provider := configuredEmbeddingProvider(args)
	if provider == "" || isLocalProviderName(provider) {
		return nil
	}
	return map[string]any{
		"container": defaultEmbeddingContainer,
		"ready":     false,
		"active":    false,
		"provider":  provider,
		"detail":    "Abra is configured to use EMBEDDING_PROVIDER=" + provider + ", so the built-in local runner is not part of the active path.",
		"hint":      "run `abra config` to inspect the active provider; use `abra model status --force` only to inspect the unused local runner",
	}
}

func configuredEmbeddingProvider(args cliArgs) string {
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(values["EMBEDDING_PROVIDER"]))
}

func embeddingRunner(args cliArgs) embeddingRunnerConfig {
	values, _ := readEnvValues(envPath(args))
	if provider := strings.TrimSpace(values["EMBEDDING_PROVIDER"]); provider != "" && !isLocalProviderName(provider) {
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
		Kind:             "embedding",
		Container:        firstNonEmpty(flag(args, "container", ""), defaultEmbeddingContainer),
		Image:            firstNonEmpty(flag(args, "image", ""), values["ABRA_LOCAL_EMBEDDING_IMAGE"], defaultTEIImage()),
		PullPolicy:       localRunnerPullPolicy(firstNonEmpty(flag(args, "pull-policy", ""), values["ABRA_LOCAL_EMBEDDING_PULL_POLICY"], "missing")),
		ModelID:          firstNonEmpty(flag(args, "model-id", ""), defaultEmbeddingModelID),
		Model:            model,
		BaseURL:          strings.TrimRight(hostBaseURL, "/"),
		Publish:          firstNonEmpty(flag(args, "publish-addr", ""), values["ABRA_LOCAL_EMBEDDING_PUBLISH_ADDR"], defaultEmbeddingPublish),
		Port:             port,
		CacheDir:         firstNonEmpty(flag(args, "cache-dir", ""), filepath.Join(userConfigDir(), "models", "llama.cpp")),
		Dims:             dims,
		ReadinessTimeout: localRunnerReadinessTimeout(args, values),
	}
}

func rerankerRunner(args cliArgs) embeddingRunnerConfig {
	values, _ := readEnvValues(envPath(args))
	baseURL := firstNonEmpty(flag(args, "reranker-base-url", ""), values["RERANKER_BASE_URL"], defaultRerankerBaseURL)
	model := firstNonEmpty(flag(args, "reranker-model", ""), values["RERANKER_MODEL"], defaultRerankerServedModelName)
	port := firstNonEmpty(flag(args, "reranker-port", ""), portFromBaseURL(baseURL), "8081")
	hostBaseURL := hostReachableBaseURL(baseURL)
	if flag(args, "reranker-port", "") != "" {
		hostBaseURL = replaceURLHostPort(hostBaseURL, "127.0.0.1", port)
	}
	return embeddingRunnerConfig{
		Kind:             "reranker",
		Container:        firstNonEmpty(flag(args, "reranker-container", ""), defaultRerankerContainer),
		Image:            firstNonEmpty(flag(args, "reranker-image", ""), values["ABRA_LOCAL_RERANKER_IMAGE"], values["ABRA_LOCAL_EMBEDDING_IMAGE"], defaultTEIImage()),
		PullPolicy:       localRunnerPullPolicy(firstNonEmpty(flag(args, "reranker-pull-policy", ""), values["ABRA_LOCAL_RERANKER_PULL_POLICY"], values["ABRA_LOCAL_EMBEDDING_PULL_POLICY"], "missing")),
		ModelID:          firstNonEmpty(flag(args, "reranker-model-id", ""), values["ABRA_LOCAL_RERANKER_MODEL_ID"], defaultRerankerModelID),
		Model:            model,
		BaseURL:          strings.TrimRight(hostBaseURL, "/"),
		Publish:          firstNonEmpty(flag(args, "reranker-publish-addr", ""), values["ABRA_LOCAL_RERANKER_PUBLISH_ADDR"], values["ABRA_LOCAL_EMBEDDING_PUBLISH_ADDR"], defaultEmbeddingPublish),
		Port:             port,
		CacheDir:         firstNonEmpty(flag(args, "cache-dir", ""), filepath.Join(userConfigDir(), "models", "llama.cpp")),
		ReadinessTimeout: localRerankerReadinessTimeout(args, values),
	}
}

func localRunnerConfigs(args cliArgs) []embeddingRunnerConfig {
	cfgs := []embeddingRunnerConfig{embeddingRunner(args)}
	if localRerankerActive(args) {
		cfgs = append(cfgs, rerankerRunner(args))
	}
	return cfgs
}

func localRerankerActive(args cliArgs) bool {
	if boolFlag(args, "force") {
		return true
	}
	if provider := flag(args, "reranker-provider", ""); provider != "" {
		return !isDisabledProviderName(provider)
	}
	for _, name := range []string{"reranker-base-url", "reranker-model", "reranker-model-id", "reranker-port"} {
		if flag(args, name, "") != "" {
			return true
		}
	}
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return false
	}
	if !isLocalProviderName(values["EMBEDDING_PROVIDER"]) {
		return false
	}
	provider := strings.TrimSpace(values["RERANKER_PROVIDER"])
	if provider == "" {
		return false
	}
	if isDisabledProviderName(provider) {
		return false
	}
	return isLocalProviderName(provider)
}

func isDisabledProviderName(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "none", "off", "disabled":
		return true
	default:
		return false
	}
}

func defaultRerankerBaseURLForProvider(provider string) string {
	if provider == "" || isLocalProviderName(provider) {
		return defaultRerankerBaseURL
	}
	return ""
}

func defaultRerankerModelForProvider(provider string) string {
	if provider == "" || isLocalProviderName(provider) {
		return defaultRerankerServedModelName
	}
	return ""
}

func defaultTEIImage() string {
	return "ghcr.io/ggml-org/llama.cpp:server"
}

func validateLocalRunnerImagePolicy(args cliArgs, cfg embeddingRunnerConfig) error {
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(values["NODE_ENV"]), "production") {
		return nil
	}
	if !localEmbeddingsAllowedInProduction(args, values) {
		return fmt.Errorf("production local models require explicit operator approval. Set ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=true or pass --allow-production-local-embeddings after reviewing capacity and security, and use EMBEDDING_PROVIDER=compatible for managed/self-hosted production endpoints")
	}
	if localRunnerImagePinned(cfg.Image) {
		return nil
	}
	return fmt.Errorf("production local models require a digest-pinned runner image. Set ABRA_LOCAL_EMBEDDING_IMAGE/ABRA_LOCAL_RERANKER_IMAGE to operator-verified image references with @sha256, or use EMBEDDING_PROVIDER=compatible with a managed/self-hosted endpoint")
}

func localEmbeddingsAllowedInProduction(args cliArgs, values map[string]string) bool {
	return boolFlag(args, "allow-production-local-embeddings") || yesish(values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"])
}

func localRunnerImagePinned(image string) bool {
	return strings.Contains(strings.TrimSpace(image), "@sha256:")
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

func dockerContainerLabel(name, label string) string {
	out, err := commandOutput("docker", "container", "inspect", "--format", "{{ index .Config.Labels \""+label+"\" }}", name)
	if err != nil {
		return ""
	}
	value := strings.TrimSpace(out)
	if value == "<no value>" {
		return ""
	}
	return value
}

func dockerContainerState(name string) (dockerContainerRuntimeState, bool) {
	if strings.TrimSpace(name) == "" {
		return dockerContainerRuntimeState{}, false
	}
	out, err := commandOutput("docker", "container", "inspect", "--format", "{{json .State}}", name)
	if err != nil {
		return dockerContainerRuntimeState{}, false
	}
	var state dockerContainerRuntimeState
	if err := json.Unmarshal([]byte(out), &state); err != nil {
		return dockerContainerRuntimeState{}, false
	}
	return state, true
}

func localRunnerContainerExitError(cfg embeddingRunnerConfig) error {
	state, ok := dockerContainerState(cfg.Container)
	if !ok {
		return nil
	}
	status := strings.ToLower(strings.TrimSpace(state.Status))
	if status != "exited" && status != "dead" {
		return nil
	}
	message := fmt.Sprintf("local %s container exited before readiness: %s status=%s exit_code=%d oom_killed=%t", cfg.Kind, cfg.Container, state.Status, state.ExitCode, state.OOMKilled)
	if strings.TrimSpace(state.Error) != "" {
		message += " error=" + strings.TrimSpace(state.Error)
	}
	message += "; run: abra model logs --tail 120"
	return errors.New(message)
}

func localRunnerNeedsRecreate(cfg embeddingRunnerConfig) bool {
	hash := dockerContainerLabel(cfg.Container, localRunnerHashLabelFor(cfg))
	if hash == "" {
		return true
	}
	return hash != localRunnerConfigHash(cfg)
}

func localRunnerConfigHash(cfg embeddingRunnerConfig) string {
	parts := []string{
		"kind=" + cfg.Kind,
		"image=" + cfg.Image,
		"pull_policy=" + cfg.PullPolicy,
		"model_id=" + cfg.ModelID,
		"model=" + cfg.Model,
		"dims=" + strconv.Itoa(cfg.Dims),
		"publish=" + cfg.Publish,
		"port=" + cfg.Port,
		"cache_dir=" + cfg.CacheDir,
		"pooling=" + localRunnerPooling(cfg),
		"ctx_size=32768",
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return fmt.Sprintf("%x", sum[:])
}

func localRunnerHashLabelFor(cfg embeddingRunnerConfig) string {
	if cfg.Kind == "reranker" {
		return localRerankerRunnerHashLabel
	}
	return localRunnerHashLabel
}

func localRunnerModelLabelFor(cfg embeddingRunnerConfig) string {
	if cfg.Kind == "reranker" {
		return localRerankerModelLabel
	}
	return localRunnerModelLabel
}

func localRunnerPooling(cfg embeddingRunnerConfig) string {
	if cfg.Kind == "reranker" {
		return "rank"
	}
	return "last"
}

func localRunnerPublish(cfg embeddingRunnerConfig) string {
	if strings.TrimSpace(cfg.Publish) == "" {
		return cfg.Port
	}
	return strings.TrimSpace(cfg.Publish) + ":" + cfg.Port
}

func localRunnerPullPolicy(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "always", "missing", "never":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "missing"
	}
}

func localRunnerReadinessTimeout(args cliArgs, values map[string]string) time.Duration {
	raw := firstNonEmpty(flag(args, "readiness-timeout", ""), values["ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT"])
	if raw == "" {
		return 10 * time.Second
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		if seconds, parseErr := strconv.Atoi(raw); parseErr == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		return 10 * time.Second
	}
	return timeout
}

func localRunnerStartupTimeout(args cliArgs) time.Duration {
	raw := firstNonEmpty(flag(args, "startup-timeout", ""), os.Getenv("ABRA_LOCAL_MODEL_STARTUP_TIMEOUT"))
	if raw == "" {
		return 10 * time.Minute
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		if seconds, parseErr := strconv.Atoi(raw); parseErr == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		return 10 * time.Minute
	}
	return timeout
}

func localRerankerReadinessTimeout(args cliArgs, values map[string]string) time.Duration {
	raw := firstNonEmpty(flag(args, "reranker-readiness-timeout", ""), values["ABRA_LOCAL_RERANKER_READINESS_TIMEOUT"], values["ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT"])
	if raw == "" {
		return 10 * time.Second
	}
	timeout, err := time.ParseDuration(raw)
	if err != nil || timeout <= 0 {
		if seconds, parseErr := strconv.Atoi(raw); parseErr == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
		return 10 * time.Second
	}
	return timeout
}

func syncLocalRunnerEnv(args cliArgs) error {
	if err := ensureEnv(args); err != nil {
		return err
	}
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return err
	}
	if provider := strings.TrimSpace(values["EMBEDDING_PROVIDER"]); provider != "" && !isLocalProviderName(provider) {
		return nil
	}
	cfg := embeddingRunner(args)
	model := firstNonEmpty(flag(args, "model", ""), values["EMBEDDING_MODEL"], defaultServedModelName)
	dims := intFromString(firstNonEmpty(flag(args, "dimensions", ""), values["EMBEDDING_DIMENSIONS"]), 1024)
	baseURL := firstNonEmpty(flag(args, "base-url", ""), values["EMBEDDING_BASE_URL"], defaultEmbeddingBaseURL)
	allowLocalEmbeddingsInProduction := strings.TrimSpace(values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"])
	if boolFlag(args, "allow-production-local-embeddings") {
		allowLocalEmbeddingsInProduction = "true"
	}
	if allowLocalEmbeddingsInProduction == "" {
		allowLocalEmbeddingsInProduction = "false"
	}
	if flag(args, "port", "") != "" {
		baseURL = replaceURLHostPort(baseURL, "host.docker.internal", cfg.Port)
	} else {
		baseURL = containerReachableBaseURL(baseURL)
	}
	updates := map[string]string{
		"EMBEDDING_PROVIDER":                   "local",
		"EMBEDDING_BASE_URL":                   strings.TrimRight(baseURL, "/"),
		"EMBEDDING_API_KEY":                    "",
		"EMBEDDING_MODEL":                      model,
		"EMBEDDING_DIMENSIONS":                 strconv.Itoa(dims),
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": allowLocalEmbeddingsInProduction,
	}
	rerankerProviderFlag := flag(args, "reranker-provider", "")
	rerankerProvider := firstNonEmpty(rerankerProviderFlag, values["RERANKER_PROVIDER"], "none")
	switch strings.ToLower(strings.TrimSpace(rerankerProvider)) {
	case "none", "off", "disabled":
		updates["RERANKER_PROVIDER"] = strings.TrimSpace(rerankerProvider)
		updates["RERANKER_BASE_URL"] = ""
		updates["RERANKER_API_KEY"] = ""
		updates["RERANKER_MODEL"] = ""
	default:
		updates["RERANKER_PROVIDER"] = strings.TrimSpace(rerankerProvider)
		updates["RERANKER_API_KEY"] = firstNonEmpty(flag(args, "reranker-api-key", ""), values["RERANKER_API_KEY"])
		rerankerModel := firstNonEmpty(flag(args, "reranker-model", ""), values["RERANKER_MODEL"], defaultRerankerModelForProvider(rerankerProvider))
		rerankerBaseURL := firstNonEmpty(flag(args, "reranker-base-url", ""), values["RERANKER_BASE_URL"], defaultRerankerBaseURLForProvider(rerankerProvider))
		if rerankerProviderFlag != "" {
			rerankerModel = firstNonEmpty(flag(args, "reranker-model", ""), defaultRerankerModelForProvider(rerankerProvider))
			rerankerBaseURL = firstNonEmpty(flag(args, "reranker-base-url", ""), defaultRerankerBaseURLForProvider(rerankerProvider))
		}
		updates["RERANKER_MODEL"] = rerankerModel
		if flag(args, "reranker-port", "") != "" {
			rerankerBaseURL = replaceURLHostPort(rerankerBaseURL, "host.docker.internal", flag(args, "reranker-port", ""))
		} else {
			rerankerBaseURL = containerReachableBaseURL(rerankerBaseURL)
		}
		updates["RERANKER_BASE_URL"] = strings.TrimRight(rerankerBaseURL, "/")
	}
	return updateEnvValues(args, updates)
}

func waitLocalRunnerReady(ctx context.Context, cfg embeddingRunnerConfig, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	delay := 500 * time.Millisecond
	for {
		if err := localRunnerContainerExitError(cfg); err != nil {
			return err
		}
		lastErr = checkLocalRunnerReady(ctx, cfg)
		if lastErr == nil {
			if err := localRunnerContainerExitError(cfg); err != nil {
				return err
			}
			return nil
		}
		if err := localRunnerContainerExitError(cfg); err != nil {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("local %s model did not become ready: %v", cfg.Kind, lastErr)
		}
		if delay > remaining {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("local %s model readiness canceled: %w", cfg.Kind, ctx.Err())
		case <-timer.C:
		}
		if delay < 2*time.Second {
			delay *= 2
			if delay > 2*time.Second {
				delay = 2 * time.Second
			}
		}
	}
}

func waitLocalRunnersReady(ctx context.Context, cfgs []embeddingRunnerConfig, timeout time.Duration) error {
	for _, cfg := range cfgs {
		fmt.Println("Waiting for " + cfg.Kind + " endpoint: " + localRunnerReadyURL(cfg))
	}
	if len(cfgs) == 0 {
		return nil
	}
	if len(cfgs) == 1 {
		if err := waitLocalRunnerReady(ctx, cfgs[0], timeout); err != nil {
			return err
		}
		fmt.Println("Local " + cfgs[0].Kind + " ready")
		return nil
	}
	readyCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type runnerReadyResult struct {
		Index int
		Err   error
	}
	results := make(chan runnerReadyResult, len(cfgs))
	for i, cfg := range cfgs {
		go func(index int, runner embeddingRunnerConfig) {
			err := waitLocalRunnerReady(readyCtx, runner, timeout)
			if err != nil {
				cancel()
			}
			results <- runnerReadyResult{Index: index, Err: err}
		}(i, cfg)
	}
	errs := make([]error, len(cfgs))
	for range cfgs {
		result := <-results
		errs[result.Index] = result.Err
	}
	var canceledErr error
	for i, err := range errs {
		if err != nil {
			if errors.Is(err, context.Canceled) {
				if canceledErr == nil {
					canceledErr = err
				}
				continue
			}
			return err
		}
		fmt.Println("Local " + cfgs[i].Kind + " ready")
	}
	if canceledErr != nil {
		return canceledErr
	}
	return nil
}

func checkLocalRunnerReady(ctx context.Context, cfg embeddingRunnerConfig) error {
	if cfg.Kind == "reranker" {
		return checkRerankerReady(ctx, cfg)
	}
	return checkEmbeddingReady(ctx, cfg)
}

func checkEmbeddingReady(ctx context.Context, cfg embeddingRunnerConfig) error {
	if cfg.ReadinessTimeout <= 0 {
		cfg.ReadinessTimeout = 10 * time.Second
	}
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
	resp, err := (&http.Client{Timeout: cfg.ReadinessTimeout}).Do(req)
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

func checkRerankerReady(ctx context.Context, cfg embeddingRunnerConfig) error {
	if cfg.ReadinessTimeout <= 0 {
		cfg.ReadinessTimeout = 10 * time.Second
	}
	body := map[string]any{
		"model":     cfg.Model,
		"query":     "abra readiness check",
		"documents": []string{"abra readiness document"},
		"top_n":     1,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/rerank", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	resp, err := (&http.Client{Timeout: cfg.ReadinessTimeout}).Do(req)
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

func localRunnerReadyURL(cfg embeddingRunnerConfig) string {
	if cfg.Kind == "reranker" {
		return cfg.BaseURL + "/rerank"
	}
	return cfg.BaseURL + "/embeddings"
}

func localEmbeddingCheck(ctx context.Context, args cliArgs) map[string]any {
	values, err := readEnvValues(envPath(args))
	if err != nil || !isLocalProviderName(values["EMBEDDING_PROVIDER"]) {
		return map[string]any{"name": "local_embeddings", "ok": true, "skipped": true}
	}
	cfg := embeddingRunner(args)
	if err := checkEmbeddingReady(ctx, cfg); err != nil {
		return map[string]any{
			"name":  "local_embeddings",
			"ok":    false,
			"error": err.Error(),
			"hint":  "run: abra model up",
		}
	}
	return map[string]any{"name": "local_embeddings", "ok": true, "endpoint": cfg.BaseURL + "/embeddings"}
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func friendlyProviderError(err error) error {
	if err == nil {
		return nil
	}
	values, _ := readEnvValues(defaultEnvPath())
	provider := strings.TrimSpace(values["EMBEDDING_PROVIDER"])
	localProvider := isLocalProviderName(provider)
	defaultHint := compatibleProviderRecoveryHint()
	if localProvider {
		defaultHint = "Run `abra model status`; if it is not ready, run `abra model up`, then retry ingest."
	}
	var statusErr *httpStatusError
	if errors.As(err, &statusErr) {
		if detail, ok := statusErr.Payload["provider_error"].(map[string]any); ok {
			code := stringValue(detail["code"], "provider_error")
			status := intValue(detail["status_code"])
			message := stringValue(detail["message"], "")
			retryable := boolValue(detail["retryable"], false)
			hint := stringValue(detail["hint"], "")
			if hint == "" {
				hint = defaultHint
			}
			switch code {
			case "auth_failed":
				hint = "Check the embedding API key, base URL, model, and dimensions with `abra config show`, then retry ingest."
			case "context_overflow":
				hint = "Abra retries smaller embedding batches automatically. If it still fails, lower ABRA_EMBEDDING_BATCH_MAX_ITEMS/ABRA_EMBEDDING_BATCH_MAX_TOKENS or split very large files before ingest."
			case "provider_timeout":
				if localProvider {
					hint = "Run `abra model status`; if the model is healthy, retry with a longer ABRA_CLI_TIMEOUT or lower ABRA_EMBEDDING_BATCH_MAX_ITEMS/ABRA_EMBEDDING_BATCH_MAX_TOKENS."
				} else {
					hint = "Check your compatible embedding endpoint capacity, raise EMBEDDING_TIMEOUT/ABRA_CLI_TIMEOUT, or lower ABRA_EMBEDDING_BATCH_MAX_ITEMS/ABRA_EMBEDDING_BATCH_MAX_TOKENS."
				}
			}
			return fmt.Errorf("embedding provider error (%s, status=%d, retryable=%v): %s %s Original error: %w", code, status, retryable, message, hint, err)
		}
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "context deadline exceeded") || strings.Contains(text, "client.timeout exceeded") {
		if localProvider {
			return fmt.Errorf("embedding request timed out. Run `abra model status`; if the local model is healthy, retry with a longer ABRA_CLI_TIMEOUT or lower ABRA_EMBEDDING_BATCH_MAX_ITEMS/ABRA_EMBEDDING_BATCH_MAX_TOKENS. Original error: %w", err)
		}
		return fmt.Errorf("embedding request timed out. %s Original error: %w", compatibleProviderRecoveryHint(), err)
	}
	if strings.Contains(text, "/embeddings") || strings.Contains(text, "embedding") || strings.Contains(text, "host.docker.internal:8080") || strings.Contains(text, "connection refused") {
		if localProvider {
			return fmt.Errorf("embedding provider is not ready. Run `abra model status`; if it is not ready, run `abra model up`, then retry ingest. If the stack is down, run `abra up`. Original error: %w", err)
		}
		return fmt.Errorf("embedding provider is not ready. %s Original error: %w", compatibleProviderRecoveryHint(), err)
	}
	return err
}

func compatibleProviderRecoveryHint() string {
	return "Run `abra config show` and check EMBEDDING_PROVIDER, EMBEDDING_BASE_URL, EMBEDDING_MODEL, EMBEDDING_DIMENSIONS, API key, and EMBEDDING_TIMEOUT; verify the endpoint accepts /embeddings, then run `abra doctor`."
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
