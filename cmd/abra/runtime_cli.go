package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func up(ctx context.Context, args cliArgs) error {
	projectDir, err := ensureProjectDir(ctx, args)
	if err != nil {
		return err
	}
	if err := ensureEnv(args); err != nil {
		return err
	}
	if err := ensureRuntimeImageDigest(args); err != nil {
		return err
	}
	if err := ensureDockerDaemon(); err != nil {
		return err
	}
	env, err := filepath.Abs(envPath(args))
	if err != nil {
		return err
	}
	fmt.Println("Using env: " + env)
	if !isAbraSourceCheckout(".") {
		fmt.Println("Using runtime: " + projectDir)
	}
	if shouldStartLocalModelsForUp(args) {
		fmt.Println("Starting local embedding runner for provider=" + activeEmbeddingProvider(args) + ".")
		if err := modelsUp(ctx, args); err != nil {
			return err
		}
	}
	for _, step := range composeUpSteps(projectDir, env) {
		if err := runCommandIn(projectDir, "docker", step...); err != nil {
			return err
		}
	}
	if err := waitReady(ctx, args); err != nil {
		return err
	}
	if !boolFlag(args, "demo") {
		printReady(args)
	}
	return nil
}

func shouldStartLocalModelsForUp(args cliArgs) bool {
	if boolFlag(args, "no-models") || boolFlag(args, "skip-models") {
		return false
	}
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return false
	}
	if !isLocalProviderName(values["EMBEDDING_PROVIDER"]) {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(values["NODE_ENV"]), "production") && !yesish(values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"]) {
		return false
	}
	return true
}

func activeEmbeddingProvider(args cliArgs) string {
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return "local"
	}
	return valueOr(strings.TrimSpace(values["EMBEDDING_PROVIDER"]), "local")
}

func down(args cliArgs) error {
	projectDir, err := projectDir(args)
	if err != nil {
		return err
	}
	if err := ensureEnv(args); err != nil {
		return err
	}
	env, err := filepath.Abs(envPath(args))
	if err != nil {
		return err
	}
	step := composeCommandArgs(projectDir, env, composeUsesDevOverride(projectDir), "down")
	if boolFlag(args, "reset") {
		step = append(step, "--volumes")
	}
	if err := runCommandIn(projectDir, "docker", step...); err != nil {
		return err
	}
	if shouldStopLocalModelsForDown(args) {
		modelArgs := copyCLIArgs(args)
		if boolFlag(args, "models") || boolFlag(args, "all") {
			modelArgs.Bools["force"] = true
		}
		return modelsDown(modelArgs)
	}
	return nil
}

func composeUpSteps(projectDir, env string) [][]string {
	useDevOverride := composeUsesDevOverride(projectDir)
	steps := [][]string{}
	if useDevOverride {
		steps = append(steps, composeCommandArgs(projectDir, env, useDevOverride, "build", "api", "worker", "migrate"))
	} else {
		steps = append(steps, composeCommandArgs(projectDir, env, useDevOverride, "pull", "postgres", "api", "worker", "migrate"))
	}
	steps = append(steps,
		composeCommandArgs(projectDir, env, useDevOverride, "up", "-d", "postgres"),
		composeCommandArgs(projectDir, env, useDevOverride, "run", "--rm", "migrate"),
		composeCommandArgs(projectDir, env, useDevOverride, "up", "-d", "api", "worker"),
	)
	return steps
}

func composeUsesDevOverride(projectDir string) bool {
	return !isManagedRuntimeDir(projectDir) &&
		isAbraSourceCheckout(projectDir) &&
		fileExists(filepath.Join(projectDir, "docker-compose.dev.yml"))
}

func composeCommandArgs(projectDir, env string, useDevOverride bool, command ...string) []string {
	args := []string{"compose", "--project-name", "abra", "--env-file", env}
	if useDevOverride {
		args = append(args, "-f", "docker-compose.yml", "-f", "docker-compose.dev.yml")
	}
	return append(args, command...)
}

func shouldStopLocalModelsForDown(args cliArgs) bool {
	if boolFlag(args, "keep-models") {
		return false
	}
	if boolFlag(args, "models") || boolFlag(args, "all") {
		return true
	}
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return false
	}
	return isLocalProviderName(values["EMBEDDING_PROVIDER"])
}

func status(ctx context.Context, args cliArgs) error {
	result, code, err := getJSON(ctx, args, readyzPath(args))
	if err != nil || code < 200 || code >= 300 {
		if boolFlag(args, "json") {
			payload := map[string]any{}
			for key, value := range result {
				payload[key] = value
			}
			payload["ready"] = false
			payload["status"] = code
			if err != nil {
				payload["error"] = err.Error()
			}
			if printErr := printJSON(payload); printErr != nil {
				return printErr
			}
			return fmt.Errorf("abra not ready (%d)", code)
		}
		fmt.Printf("Abra: not ready (%d)\n", code)
		fmt.Print(readyFailureMessage(args, result, code, err, ""))
		return nil
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Println("Abra: ready")
	fmt.Println("base_url: " + cfg(args).BaseURL)
	fmt.Println("embedding: " + stringValue(result["embedding_provider"], "unknown"))
	fmt.Printf("auth_required: %v\n", result["auth_required"])
	return nil
}

func doctor(ctx context.Context, args cliArgs) error {
	checks := []map[string]any{}
	checks = append(checks, commandCheck("docker"))
	checks = append(checks, dockerDaemonCheck())
	checks = append(checks, commandCheck("curl"))
	checks = append(checks, commandCheck("sh"))
	checks = append(checks, map[string]any{"name": "go_native_cli", "ok": true, "version": version})
	if envPath(args) != "" {
		checks = append(checks, envFileCheck(envPath(args)))
		checks = append(checks, workerIntervalCheck(args))
		checks = append(checks, workerConcurrencyCheck(args))
	}
	checks = append(checks, modelConfigCheck(args))
	checks = append(checks, aiProviderConcurrencyCheck(args))
	checks = append(checks, embeddingBatchCheck(args))
	if envPath(args) != "" {
		checks = append(checks, composeConcurrencyCheck(args))
	}
	checks = append(checks, localEmbeddingCheck(ctx, args))
	checks = append(checks, codexMCPClientCheck(args))
	checks = append(checks, codexLaunchEnvCheck(args))
	result, code, err := getJSON(ctx, args, "/readyz")
	if err != nil || code < 200 || code >= 300 {
		checks = append(checks, map[string]any{"name": "readyz", "ok": false, "status": code, "hint": "run: abra up"})
		return printDoctor(args, checks)
	}
	checks = append(checks, map[string]any{
		"name":                 "readyz",
		"ok":                   true,
		"embedding_provider":   stringValue(result["embedding_provider"], "unknown"),
		"approval_enforcement": result["approval_enforcement"],
		"auth_required":        result["auth_required"],
	})
	checks = append(checks, mcpCheck(ctx, args))
	checks = append(checks, browserUICheck(ctx, args))
	return printDoctor(args, checks)
}

func commandCheck(name string) map[string]any {
	if path, err := exec.LookPath(name); err == nil {
		return map[string]any{"name": "command_" + name, "ok": true, "path": path}
	}
	return map[string]any{"name": "command_" + name, "ok": false, "hint": "install " + name}
}

func ensureDockerDaemon() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("missing required command: docker")
	}
	output, err := commandOutput("docker", "info", "--format", "{{.ServerVersion}}")
	if err == nil {
		return nil
	}
	detail := strings.TrimSpace(output)
	if detail != "" {
		detail = ": " + detail
	}
	return fmt.Errorf("docker daemon is not reachable%s\nstart Docker Desktop or OrbStack, then rerun `abra up`\ndiagnose with `abra doctor`", detail)
}

func dockerDaemonCheck() map[string]any {
	if _, err := exec.LookPath("docker"); err != nil {
		return map[string]any{
			"name":   "docker_daemon",
			"ok":     false,
			"detail": "docker command is not installed",
			"hint":   "install Docker Desktop or OrbStack",
		}
	}
	output, err := commandOutput("docker", "info", "--format", "{{.ServerVersion}}")
	if err != nil {
		detail := strings.TrimSpace(output)
		if detail == "" {
			detail = err.Error()
		}
		return map[string]any{
			"name":   "docker_daemon",
			"ok":     false,
			"detail": detail,
			"hint":   "start Docker Desktop or OrbStack, then run: abra up",
		}
	}
	return map[string]any{
		"name":   "docker_daemon",
		"ok":     true,
		"detail": "server_version=" + strings.TrimSpace(output),
	}
}

func envFileCheck(path string) map[string]any {
	info, err := os.Stat(path)
	if err != nil {
		return map[string]any{"name": "env_file", "ok": false, "path": path, "hint": "run: abra init"}
	}
	mode := info.Mode().Perm()
	ok := mode&0o077 == 0
	check := map[string]any{"name": "env_file", "ok": ok, "path": path, "mode": mode.String()}
	if !ok {
		check["hint"] = "run: chmod 600 " + path
	}
	return check
}

func workerIntervalCheck(args cliArgs) map[string]any {
	path := envPath(args)
	values, err := readEnvValues(path)
	if err != nil {
		return map[string]any{
			"name":   "worker_interval",
			"ok":     false,
			"detail": "runtime env is not readable: " + path,
			"hint":   "run: abra setup",
		}
	}
	raw := strings.TrimSpace(values["WORKER_INTERVAL"])
	if raw == "" {
		return map[string]any{
			"name":   "worker_interval",
			"ok":     true,
			"detail": "WORKER_INTERVAL is unset; Compose will use its production default",
		}
	}
	interval, err := time.ParseDuration(raw)
	if err != nil {
		return map[string]any{
			"name":   "worker_interval",
			"ok":     false,
			"detail": "WORKER_INTERVAL=" + raw + " is not a valid Go duration",
			"hint":   "set WORKER_INTERVAL=30s in " + path + ", then run: abra down && abra up",
		}
	}
	if interval < 10*time.Second {
		return map[string]any{
			"name":   "worker_interval",
			"ok":     false,
			"detail": "WORKER_INTERVAL=" + raw + " is very aggressive and can compete with recall and working-memory latency on local stacks",
			"hint":   "set WORKER_INTERVAL=30s in " + path + ", then run: abra down && abra up",
		}
	}
	return map[string]any{
		"name":   "worker_interval",
		"ok":     true,
		"detail": "WORKER_INTERVAL=" + raw,
	}
}

func workerConcurrencyCheck(args cliArgs) map[string]any {
	path := envPath(args)
	values, err := readEnvValues(path)
	if err != nil {
		return map[string]any{
			"name":   "worker_concurrency",
			"ok":     false,
			"detail": "runtime env is not readable: " + path,
			"hint":   "run: abra setup",
		}
	}
	raw := strings.TrimSpace(values["WORKER_CONCURRENCY"])
	if raw == "" {
		return map[string]any{
			"name":   "worker_concurrency",
			"ok":     true,
			"detail": "WORKER_CONCURRENCY is unset; runtime default is 1",
		}
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 32 {
		return map[string]any{
			"name":   "worker_concurrency",
			"ok":     false,
			"detail": "WORKER_CONCURRENCY=" + raw + " must be an integer between 1 and 32",
			"hint":   "set WORKER_CONCURRENCY=1 in " + path + ", then run: abra down && abra up",
		}
	}
	provider := strings.TrimSpace(values["EMBEDDING_PROVIDER"])
	providerConcurrencyRaw := strings.TrimSpace(values["ABRA_AI_PROVIDER_CONCURRENCY"])
	if providerConcurrencyRaw == "" {
		providerConcurrencyRaw = defaultProviderConcurrency(provider)
	}
	providerConcurrency, providerErr := strconv.Atoi(providerConcurrencyRaw)
	if providerErr == nil && value > providerConcurrency && isLocalProviderName(provider) {
		return map[string]any{
			"name":   "worker_concurrency",
			"ok":     true,
			"detail": "WORKER_CONCURRENCY=" + raw + " with local provider concurrency=" + providerConcurrencyRaw + "; jobs may queue behind the single local model runner",
			"hint":   "keep WORKER_CONCURRENCY=1 for the default local runner, or raise ABRA_AI_PROVIDER_CONCURRENCY only after provider capacity is measured",
		}
	}
	return map[string]any{
		"name":   "worker_concurrency",
		"ok":     true,
		"detail": "WORKER_CONCURRENCY=" + raw,
	}
}

func modelConfigCheck(args cliArgs) map[string]any {
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return map[string]any{
			"name":   "model_config",
			"ok":     false,
			"detail": "runtime env is not readable: " + envPath(args),
			"hint":   "run: abra setup",
		}
	}
	provider := strings.TrimSpace(values["EMBEDDING_PROVIDER"])
	baseURL := strings.TrimSpace(values["EMBEDDING_BASE_URL"])
	model := strings.TrimSpace(values["EMBEDDING_MODEL"])
	dimensions := strings.TrimSpace(values["EMBEDDING_DIMENSIONS"])
	if provider == "" {
		return map[string]any{
			"name":   "model_config",
			"ok":     false,
			"detail": "embedding provider is empty",
			"hint":   "run: abra setup, or configure one with abra config model local",
		}
	}
	if baseURL == "" || model == "" {
		return map[string]any{
			"name":   "model_config",
			"ok":     false,
			"detail": "embedding provider=" + provider + " base_url=" + valueOr(baseURL, "<empty>") + " model=" + valueOr(model, "<empty>"),
			"hint":   "run: abra config model local, or abra config model compatible --base-url <url> --model <model> --dimensions <n>",
		}
	}
	detail := "provider=" + provider + " model=" + model + " base_url=" + baseURL
	if dimensions != "" {
		detail += " dimensions=" + dimensions
	}
	if isLocalProviderName(provider) {
		return map[string]any{
			"name":   "model_config",
			"ok":     true,
			"detail": detail,
			"hint":   "local model readiness is checked by local_embeddings; use abra model status when sync or setup stalls",
		}
	}
	return map[string]any{"name": "model_config", "ok": true, "detail": detail}
}

func aiProviderConcurrencyCheck(args cliArgs) map[string]any {
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return map[string]any{
			"name":   "ai_provider_concurrency",
			"ok":     false,
			"detail": "runtime env is not readable: " + envPath(args),
			"hint":   "run: abra setup",
		}
	}
	path := envPath(args)
	provider := strings.TrimSpace(values["EMBEDDING_PROVIDER"])
	raw := strings.TrimSpace(values["ABRA_AI_PROVIDER_CONCURRENCY"])
	defaultValue := defaultProviderConcurrency(provider)
	if raw == "" {
		return map[string]any{
			"name":   "ai_provider_concurrency",
			"ok":     true,
			"detail": "ABRA_AI_PROVIDER_CONCURRENCY is unset; runtime default is " + defaultValue + " for provider=" + valueOr(provider, "<empty>"),
		}
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 || value > 32 {
		return map[string]any{
			"name":   "ai_provider_concurrency",
			"ok":     false,
			"detail": "ABRA_AI_PROVIDER_CONCURRENCY=" + raw + " must be an integer between 1 and 32",
			"hint":   "set ABRA_AI_PROVIDER_CONCURRENCY=" + defaultValue + " in " + path + ", then run: abra down && abra up",
		}
	}
	if isLocalProviderName(provider) && value > 1 {
		return map[string]any{
			"name":   "ai_provider_concurrency",
			"ok":     false,
			"detail": "ABRA_AI_PROVIDER_CONCURRENCY=" + raw + " can overload a single local model runner",
			"hint":   "set ABRA_AI_PROVIDER_CONCURRENCY=1 in " + path + ", then run: abra down && abra up",
		}
	}
	return map[string]any{
		"name":   "ai_provider_concurrency",
		"ok":     true,
		"detail": "ABRA_AI_PROVIDER_CONCURRENCY=" + raw,
	}
}

func embeddingBatchCheck(args cliArgs) map[string]any {
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return map[string]any{
			"name":   "embedding_batch",
			"ok":     false,
			"detail": "runtime env is not readable: " + envPath(args),
			"hint":   "run: abra setup",
		}
	}
	path := envPath(args)
	provider := strings.TrimSpace(values["EMBEDDING_PROVIDER"])
	defaultItems, defaultTokens := defaultEmbeddingBatchLimits(provider)
	itemsRaw := firstNonEmpty(strings.TrimSpace(values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"]), defaultItems)
	tokensRaw := firstNonEmpty(strings.TrimSpace(values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"]), defaultTokens)
	items, itemsErr := strconv.Atoi(itemsRaw)
	tokens, tokensErr := strconv.Atoi(tokensRaw)
	if itemsErr != nil || items < 1 || items > 128 {
		return map[string]any{
			"name":   "embedding_batch",
			"ok":     false,
			"detail": "ABRA_EMBEDDING_BATCH_MAX_ITEMS=" + itemsRaw + " must be an integer between 1 and 128",
			"hint":   "set ABRA_EMBEDDING_BATCH_MAX_ITEMS=" + defaultItems + " in " + path + ", then run: abra down && abra up",
		}
	}
	if tokensErr != nil || tokens < 1 || tokens > 200000 {
		return map[string]any{
			"name":   "embedding_batch",
			"ok":     false,
			"detail": "ABRA_EMBEDDING_BATCH_MAX_TOKENS=" + tokensRaw + " must be an integer between 1 and 200000",
			"hint":   "set ABRA_EMBEDDING_BATCH_MAX_TOKENS=" + defaultTokens + " in " + path + ", then run: abra down && abra up",
		}
	}
	if isLocalProviderName(provider) && (items > 6 || tokens > 3000) {
		return map[string]any{
			"name":   "embedding_batch",
			"ok":     true,
			"detail": "items=" + itemsRaw + " tokens=" + tokensRaw + " for local provider; large batches can exceed the local runner context window",
			"hint":   "use ABRA_EMBEDDING_BATCH_MAX_ITEMS=6 and ABRA_EMBEDDING_BATCH_MAX_TOKENS=3000 for the default local runner unless capacity is measured",
		}
	}
	return map[string]any{
		"name":   "embedding_batch",
		"ok":     true,
		"detail": "items=" + itemsRaw + " tokens=" + tokensRaw,
	}
}

func defaultEmbeddingBatchLimits(provider string) (string, string) {
	if isLocalProviderName(provider) {
		return "6", "3000"
	}
	return "16", "6000"
}

func defaultBatchMaxItems(provider string) string {
	items, _ := defaultEmbeddingBatchLimits(provider)
	return items
}

func defaultBatchMaxTokens(provider string) string {
	_, tokens := defaultEmbeddingBatchLimits(provider)
	return tokens
}

func composeConcurrencyCheck(args cliArgs) map[string]any {
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return map[string]any{
			"name":   "compose_concurrency",
			"ok":     false,
			"detail": "runtime env is not readable: " + envPath(args),
			"hint":   "run: abra setup",
		}
	}
	provider := strings.TrimSpace(values["EMBEDDING_PROVIDER"])
	recallRaw := firstNonEmpty(strings.TrimSpace(values["ABRA_COMPOSE_RECALL_CONCURRENCY"]), "1")
	graphRaw := firstNonEmpty(strings.TrimSpace(values["ABRA_COMPOSE_GRAPH_CONCURRENCY"]), "4")
	providerRaw := firstNonEmpty(strings.TrimSpace(values["ABRA_AI_PROVIDER_CONCURRENCY"]), defaultProviderConcurrency(provider))
	recall, recallErr := strconv.Atoi(recallRaw)
	graph, graphErr := strconv.Atoi(graphRaw)
	providerConcurrency, providerErr := strconv.Atoi(providerRaw)
	if recallErr != nil || recall < 1 || recall > 32 {
		return map[string]any{
			"name":   "compose_concurrency",
			"ok":     false,
			"detail": "ABRA_COMPOSE_RECALL_CONCURRENCY=" + recallRaw + " must be an integer between 1 and 32",
			"hint":   "set ABRA_COMPOSE_RECALL_CONCURRENCY=1 in " + envPath(args) + ", then run: abra down && abra up",
		}
	}
	if graphErr != nil || graph < 1 || graph > 32 {
		return map[string]any{
			"name":   "compose_concurrency",
			"ok":     false,
			"detail": "ABRA_COMPOSE_GRAPH_CONCURRENCY=" + graphRaw + " must be an integer between 1 and 32",
			"hint":   "set ABRA_COMPOSE_GRAPH_CONCURRENCY=4 in " + envPath(args) + ", then run: abra down && abra up",
		}
	}
	if providerErr == nil && isLocalProviderName(provider) && recall > providerConcurrency {
		return map[string]any{
			"name":   "compose_concurrency",
			"ok":     true,
			"detail": "recall=" + recallRaw + " graph=" + graphRaw + " with local provider concurrency=" + providerRaw + "; recall calls may queue behind the local model runner",
			"hint":   "keep ABRA_COMPOSE_RECALL_CONCURRENCY=1 for the default local runner, or raise ABRA_AI_PROVIDER_CONCURRENCY only after provider capacity is measured",
		}
	}
	return map[string]any{
		"name":   "compose_concurrency",
		"ok":     true,
		"detail": "recall=" + recallRaw + " graph=" + graphRaw,
	}
}

func defaultProviderConcurrency(provider string) string {
	if isLocalProviderName(provider) {
		return "1"
	}
	return "4"
}

func isLocalProviderName(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "local", "qwen3", "local-smart":
		return true
	default:
		return false
	}
}

func mcpCheck(ctx context.Context, args cliArgs) map[string]any {
	toolCount, err := validateMCPTools(ctx, args)
	if err != nil {
		return map[string]any{"name": "mcp", "ok": false, "error": err.Error(), "hint": "run: abra up, then abra doctor"}
	}
	return map[string]any{"name": "mcp", "ok": true, "detail": fmt.Sprintf("tools=%d required=%s", toolCount, strings.Join(requiredMCPToolNames(), ","))}
}

func codexMCPClientCheck(args cliArgs) map[string]any {
	codex, err := codexCommandPath()
	if err != nil {
		return map[string]any{
			"name":    "codex_mcp_client",
			"ok":      true,
			"skipped": true,
			"detail":  "Codex CLI not found; skip this check unless you use Codex",
		}
	}
	tokenEnv := flag(args, "token-env", "ABRA_API_TOKEN")
	expectedToken := cfg(args).Token
	actualToken := strings.TrimSpace(os.Getenv(tokenEnv))
	check := map[string]any{
		"name":      "codex_mcp_client",
		"path":      codex,
		"token_env": tokenEnv,
	}
	mcpList, err := commandOutput(codex, "mcp", "list")
	if err != nil {
		check["ok"] = false
		detail := "Codex MCP configuration could not be read: " + err.Error()
		if strings.TrimSpace(mcpList) != "" {
			detail += ": " + strings.TrimSpace(mcpList)
		}
		check["detail"] = detail
		check["hint"] = "fix Codex MCP config, then rerun: " + codexInstallCommand(tokenEnv)
		return check
	}
	if !codexMCPListHasAbra(mcpList) {
		check["ok"] = false
		check["detail"] = "Codex MCP entry `abra` is not installed"
		check["hint"] = "run: " + codexInstallCommand(tokenEnv)
		check["next"] = codexMCPRecoverySteps(args, tokenEnv)
		return check
	}
	if strings.TrimSpace(expectedToken) == "" {
		check["ok"] = false
		check["detail"] = "Abra token is empty"
		check["hint"] = "run: abra setup, then abra agent install codex"
		return check
	}
	if actualToken == "" {
		check["ok"] = false
		check["detail"] = tokenEnv + " is not set in this shell; Codex also needs it in the Codex process environment"
		check["hint"] = "run: abra agent install codex, fully quit and reopen Codex Desktop, or export " + tokenEnv + " before launching terminal Codex"
		check["next"] = codexMCPRecoverySteps(args, tokenEnv)
		return check
	}
	if actualToken != expectedToken {
		check["ok"] = false
		check["detail"] = tokenEnv + " is set but does not match the active Abra env token"
		check["hint"] = "rerun: " + codexInstallCommand(tokenEnv) + ", then fully quit and reopen Codex Desktop"
		check["next"] = codexMCPRecoverySteps(args, tokenEnv)
		return check
	}
	check["ok"] = true
	check["detail"] = tokenEnv + " is set in this shell; restart Codex Desktop if this changed after Codex launched"
	return check
}

func codexMCPListHasAbra(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "abra" || strings.HasPrefix(line, "abra ") || strings.HasPrefix(line, "abra\t") || strings.Contains(line, `"abra"`) {
			return true
		}
	}
	return false
}

func codexMCPRecoverySteps(args cliArgs, tokenEnv string) []string {
	return []string{
		codexInstallCommand(tokenEnv),
		"fully quit and reopen Codex Desktop",
		"for terminal Codex: set -a; source " + shellQuote(envPath(args)) + "; set +a; codex",
		"then run: abra agent verify . --scope " + shellQuote(scopeOrDefault(args, ".")),
	}
}

func codexLaunchEnvCheck(args cliArgs) map[string]any {
	tokenEnv := flag(args, "token-env", "ABRA_API_TOKEN")
	check := map[string]any{
		"name":      "codex_desktop_launch_env",
		"token_env": tokenEnv,
	}
	if runtime.GOOS != "darwin" {
		check["ok"] = true
		check["skipped"] = true
		check["detail"] = "macOS launch environment check only applies to Codex Desktop on macOS"
		return check
	}
	expectedToken := cfg(args).Token
	if strings.TrimSpace(expectedToken) == "" {
		check["ok"] = false
		check["detail"] = "Abra token is empty"
		check["hint"] = "run: abra setup, then abra agent install codex"
		return check
	}
	actualToken, err := commandOutput("launchctl", "getenv", tokenEnv)
	if err != nil {
		check["ok"] = false
		check["detail"] = "could not read macOS launch environment: " + err.Error()
		check["hint"] = "run: abra agent install codex, or export " + tokenEnv + " before launching terminal Codex"
		return check
	}
	actualToken = strings.TrimSpace(actualToken)
	switch {
	case actualToken == "":
		check["ok"] = false
		check["detail"] = tokenEnv + " is not set in the macOS launch environment used by Codex Desktop"
		check["hint"] = "run: abra agent install codex, then fully quit and reopen Codex Desktop"
	case actualToken != expectedToken:
		check["ok"] = false
		check["detail"] = tokenEnv + " in macOS launch environment does not match the active Abra env token"
		check["hint"] = "rerun: " + codexInstallCommand(tokenEnv) + ", then fully quit and reopen Codex Desktop"
	default:
		check["ok"] = true
		check["detail"] = tokenEnv + " is set for Codex Desktop launches; fully reopen Codex if it was already running"
	}
	return check
}

func browserUICheck(ctx context.Context, args cliArgs) map[string]any {
	body, code, _ := getJSON(ctx, args, "/app/")
	return map[string]any{
		"name":   "browser_ui_removed",
		"ok":     code == http.StatusNotFound && stringValue(body["error"], "") == "browser_ui_not_shipped",
		"status": code,
	}
}

func printDoctor(args cliArgs, checks []map[string]any) error {
	ok := true
	for _, check := range checks {
		if value, _ := check["ok"].(bool); !value {
			ok = false
			break
		}
	}
	if boolFlag(args, "json") {
		if err := printJSON(map[string]any{"ok": ok, "checks": checks}); err != nil {
			return err
		}
		if boolFlag(args, "strict") && !ok {
			return errors.New("doctor checks failed")
		}
		return nil
	}
	for _, check := range checks {
		status := "ok"
		if value, _ := check["ok"].(bool); !value {
			status = "warn"
		}
		fmt.Println(status + "  " + stringValue(check["name"], "check"))
		if detail := stringValue(check["detail"], ""); detail != "" {
			fmt.Println("info " + detail)
		}
		if hint := stringValue(check["hint"], ""); hint != "" {
			fmt.Println("hint " + hint)
		}
		if next, ok := check["next"].([]string); ok && len(next) > 0 {
			fmt.Println("next")
			for _, step := range next {
				fmt.Println("  - " + step)
			}
		}
		if errText := stringValue(check["error"], ""); errText != "" {
			fmt.Println("err  " + errText)
		}
	}
	if boolFlag(args, "strict") && !ok {
		return errors.New("doctor checks failed")
	}
	return nil
}

func waitReady(ctx context.Context, args cliArgs) error {
	var lastResult map[string]any
	var lastCode int
	var lastErr error
	for i := 0; i < 60; i++ {
		result, code, err := getJSON(ctx, args, readyzPath(args))
		if err == nil && code >= 200 && code < 300 {
			return nil
		}
		lastResult = result
		lastCode = code
		lastErr = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("%s\n%s", ctx.Err(), readyFailureMessage(args, lastResult, lastCode, lastErr, "Abra did not become ready"))
		case <-time.After(time.Second):
		}
	}
	return errors.New(readyFailureMessage(args, lastResult, lastCode, lastErr, "Abra did not become ready"))
}

func readyFailureMessage(args cliArgs, result map[string]any, code int, err error, prefix string) string {
	lines := []string{}
	if strings.TrimSpace(prefix) != "" {
		lines = append(lines, prefix)
	}
	if code > 0 {
		lines = append(lines, fmt.Sprintf("status: %d", code))
	}
	if detail := readyFailureDetail(result, err); detail != "" {
		lines = append(lines, "detail: "+detail)
	}
	for _, field := range []struct {
		key   string
		label string
	}{
		{key: "embedding_status", label: "embedding_status"},
		{key: "embedding_check_timeout", label: "embedding_check_timeout"},
		{key: "embedding_provider_timeout", label: "embedding_provider_timeout"},
	} {
		if value := strings.TrimSpace(stringValue(result[field.key], "")); value != "" {
			lines = append(lines, field.label+": "+value)
		}
	}
	if setupUsesLocalEmbeddings(args) {
		lines = append(lines, "Check: abra model status")
		lines = append(lines, "Repair: abra up")
	} else {
		lines = append(lines, "Repair: abra up")
	}
	lines = append(lines, "Diagnose: abra doctor")
	return strings.Join(lines, "\n") + "\n"
}

func readyFailureDetail(result map[string]any, err error) string {
	for _, key := range []string{"embedding_error", "error"} {
		if value := strings.TrimSpace(stringValue(result[key], "")); value != "" {
			return value
		}
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func readyzPath(args cliArgs) string {
	values, err := readEnvValues(envPath(args))
	if err == nil && isLocalProviderName(values["EMBEDDING_PROVIDER"]) {
		return "/readyz?deep=1"
	}
	return "/readyz"
}

func printReady(args cliArgs) {
	fmt.Println()
	fmt.Println("Abra is ready")
	fmt.Println("MCP:       " + strings.TrimRight(cfg(args).BaseURL, "/") + "/mcp")
	fmt.Println("Token env: ABRA_API_TOKEN (configured; value not printed)")
	fmt.Println("Next:      cd /path/to/project && abra agent bootstrap --agent <agent>")
	fmt.Println("Restart:   fully restart the agent runtime after bootstrap")
	fmt.Println("Then:      abra agent ready . --scope <scope> --agent <agent> --json")
	fmt.Println("Agent:     use MCP working_memory_compose / brain_think with the verified scope")
	fmt.Println("Manual:    abra agent install <agent> && abra agent init --agent <agent> && abra agent verify . --scope <scope>")
	fmt.Println("Sync:      abra sync . --code --scope <scope>   # only if verify reports missing scope or empty memory")
}

func runCommand(name string, args ...string) error {
	return runCommandIn("", name, args...)
}

func runCommandIn(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func commandOutput(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}
