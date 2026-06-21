package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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
	"runtime"
	"strconv"
	"strings"
	"time"

	ingestpkg "github.com/hermawan22/abra/internal/ingest"
	internalversion "github.com/hermawan22/abra/internal/version"
)

const (
	checkoutEnvPath       = ".tmp/quickstart.env"
	defaultBaseURL        = "http://127.0.0.1:18080"
	defaultToken          = "dev-token"
	defaultHTTPTimeout    = 30 * time.Second
	defaultIngestTimeout  = 10 * time.Minute
	defaultWorkerInterval = 30 * time.Second
	maxCLIResponseBody    = 8 << 20
	installScript         = "https://github.com/hermawan22/abra/releases/latest/download/install.sh"
)

var (
	version = internalversion.Version
	commit  = internalversion.Commit
	date    = internalversion.Date
)

type cliArgs struct {
	Command string
	Flags   map[string]string
	Bools   map[string]bool
	Rest    []string
}

type contextConfig struct {
	EnvFile string
	BaseURL string
	Token   string
}

func main() {
	defer func() {
		if recovered := recover(); recovered != nil {
			fmt.Fprintln(os.Stderr, recovered)
			os.Exit(1)
		}
	}()
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, argv []string) error {
	args := parseArgs(argv)
	if args.Command != "" && boolFlag(args, "help") {
		fmt.Print(commandUsage(args.Command))
		return nil
	}
	switch args.Command {
	case "", "help", "-h", "--help":
		fmt.Print(usage())
		return nil
	case "version", "--version", "-v":
		return printVersion(args)
	case "install", "setup":
		return setup(ctx, args)
	case "upgrade", "update":
		return upgrade(args)
	case "uninstall":
		return uninstall(args)
	case "demo", "quickstart":
		return demo(ctx, args)
	case "init":
		return initEnv(args)
	case "config":
		return configCommand(args)
	case "models", "model":
		return models(ctx, args)
	case "scope":
		return scopeCommand(args)
	case "agents", "agent":
		return agentsCommand(ctx, args)
	case "ui", "dashboard":
		return errors.New("abra ui was removed; use `abra setup` for guided onboarding or `abra up` for non-interactive start")
	case "up", "start":
		return up(ctx, args)
	case "down", "stop":
		return down(args)
	case "status":
		return status(ctx, args)
	case "doctor":
		return doctor(ctx, args)
	case "seed":
		return seed(ctx, args)
	case "ingest":
		return ingestCommand(ctx, args)
	case "watch", "source":
		return watch(ctx, args)
	case "sources":
		return listSources(ctx, args)
	case "jobs":
		return listJobs(ctx, args)
	case "observe":
		return observe(ctx, args)
	case "observations", "episodes":
		return listObservations(ctx, args)
	case "think", "ask":
		return think(ctx, args)
	case "recall":
		return recall(ctx, args)
	case "compose":
		return composeMemory(ctx, args)
	case "mcp", "mcp-config":
		return mcp(ctx, args)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args.Command, usage())
	}
}

func printVersion(args cliArgs) error {
	executable, _ := os.Executable()
	info := map[string]any{
		"version":    version,
		"commit":     commit,
		"date":       date,
		"goos":       runtime.GOOS,
		"goarch":     runtime.GOARCH,
		"executable": executable,
	}
	if boolFlag(args, "json") {
		return printJSON(info)
	}
	fmt.Println("abra " + version)
	fmt.Println("commit: " + commit)
	fmt.Println("date: " + date)
	fmt.Println("target: " + runtime.GOOS + "/" + runtime.GOARCH)
	return nil
}

func parseArgs(argv []string) cliArgs {
	args := cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}}
	if len(argv) > 0 {
		args.Command = argv[0]
		argv = argv[1:]
	}
	for i := 0; i < len(argv); i++ {
		item := argv[i]
		if !strings.HasPrefix(item, "--") {
			args.Rest = append(args.Rest, item)
			continue
		}
		key := strings.TrimPrefix(item, "--")
		if before, after, ok := strings.Cut(key, "="); ok {
			args.Flags[before] = after
			continue
		}
		if i+1 >= len(argv) || strings.HasPrefix(argv[i+1], "--") {
			args.Bools[key] = true
			continue
		}
		args.Flags[key] = argv[i+1]
		i++
	}
	return args
}

func copyCLIArgs(args cliArgs) cliArgs {
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
	return out
}

func upgrade(args cliArgs) error {
	if _, err := exec.LookPath("curl"); err != nil {
		return errors.New("missing required command: curl")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		return errors.New("missing required command: sh")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	env := os.Environ()
	env = append(env, "ABRA_INSTALL_DIR="+filepath.Dir(exe))
	target := flag(args, "version", "")
	script := strings.TrimSpace(os.Getenv("ABRA_INSTALL_SCRIPT"))
	if script == "" {
		script = installScript
		if target != "" {
			script = releaseInstallScriptURL(target)
		}
	}
	if target != "" {
		env = append(env, "ABRA_VERSION="+target)
	}
	tmpDir, err := os.MkdirTemp("", "abra-upgrade-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	scriptPath := filepath.Join(tmpDir, "install.sh")
	download := exec.Command("curl", "-fsSL", script, "-o", scriptPath)
	download.Env = env
	if output, err := download.CombinedOutput(); err != nil {
		return installScriptDownloadError(script, err, output)
	}
	cmd := exec.Command("sh", scriptPath)
	cmd.Env = append(env, "ABRA_INSTALL_SCRIPT="+script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func releaseInstallScriptURL(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "latest" {
		return installScript
	}
	return "https://github.com/hermawan22/abra/releases/download/" + url.PathEscape(version) + "/install.sh"
}

func installScriptDownloadError(script string, err error, output []byte) error {
	detail := strings.TrimSpace(string(output))
	if detail != "" {
		detail = "\n" + detail
	}
	return fmt.Errorf(`download Abra install script failed: %w
script: %s%s

Recovery:
  1. Check the installer URL. The official script is:
     %s
  2. If you are using a fork, publish a signed release and set ABRA_INSTALL_SCRIPT to that release's install.sh URL.
  3. If you want a specific release, run: abra upgrade --version vX.Y.Z`, err, script, detail, installScript)
}

func uninstall(args cliArgs) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if !boolFlag(args, "yes") {
		fmt.Println("This removes the Abra CLI binary only. It does not remove Docker containers, volumes, env files, or memory data.")
		fmt.Println("Binary: " + exe)
		fmt.Println("Run: abra uninstall --yes")
		return nil
	}
	if err := os.Remove(exe); err != nil {
		return err
	}
	fmt.Println("Removed: " + exe)
	fmt.Println("Local stack data was left untouched. Run `abra down --reset` before uninstalling when you also want demo data removed.")
	return nil
}

func demo(ctx context.Context, args cliArgs) error {
	args.Bools["demo"] = true
	if err := up(ctx, args); err != nil {
		return err
	}
	scope := flag(args, "scope", "repo:abra-demo-"+timestamp())
	if err := ingest(ctx, args, map[string]any{
		"source_type": "markdown",
		"source_url":  "file://abra-demo-" + timestamp() + ".md",
		"title":       "Abra Demo",
		"scope":       scope,
		"content": strings.Join([]string{
			"Abra is a CLI-first governed brain layer for AI agents.",
			"Agents should use Abra before autonomous code changes.",
			"Abra returns citations, graph context, gap analysis, memory health, and an agent decision gate.",
		}, "\n"),
		"authority": "official-doc",
	}); err != nil {
		return err
	}
	result, err := postJSON(ctx, args, "/brain/think", map[string]any{
		"question":    "What should agents use before autonomous code changes?",
		"scope":       scope,
		"limit":       5,
		"max_queries": 4,
	})
	if err != nil {
		return err
	}
	printThink(result)
	printReady(args)
	return nil
}

func initEnv(args cliArgs) error {
	path := envPath(args)
	if fileExists(path) && !boolFlag(args, "force") {
		fmt.Printf("Env already exists: %s\n", path)
		fmt.Println("Use --force to overwrite.")
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	content := demoEnv()
	if boolFlag(args, "production") {
		content = productionEnvExample
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	fmt.Printf("Wrote %s\n", path)
	if boolFlag(args, "production") {
		fmt.Println("Edit placeholders before running: abra up --env-file " + path)
	}
	return nil
}

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
	if err := ensureEnv(args); err != nil {
		return err
	}
	values, err := readEnvValues(envPath(args))
	if err != nil {
		return err
	}
	view := map[string]any{
		"env_file":             envPath(args),
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
		"worker_concurrency":   firstNonEmpty(values["WORKER_CONCURRENCY"], "1"),
		"worker_max_sources":   firstNonEmpty(values["WORKER_MAX_SOURCES_PER_RUN"], "25"),
		"reranker_provider":    values["RERANKER_PROVIDER"],
		"reranker_base_url":    values["RERANKER_BASE_URL"],
		"reranker_api_key":     maskSecret(values["RERANKER_API_KEY"]),
		"reranker_model":       values["RERANKER_MODEL"],
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
	fmt.Println("worker_concurrency:   " + stringValue(view["worker_concurrency"], ""))
	fmt.Println("worker_max_sources:   " + stringValue(view["worker_max_sources"], ""))
	fmt.Println("api_key:   " + stringValue(view["embedding_api_key"], ""))
	if rerankerProvider := stringValue(view["reranker_provider"], ""); rerankerProvider != "" {
		fmt.Println("reranker:  " + rerankerProvider)
		fmt.Println("rerank_url: " + stringValue(view["reranker_base_url"], ""))
		fmt.Println("rerank_model: " + stringValue(view["reranker_model"], ""))
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
		return configModelLocalNeural(args, "local neural embeddings + reranker")
	case "qwen3", "local-smart":
		return configModelLocalNeural(args, "local neural embeddings + reranker")
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
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                   "local",
		"EMBEDDING_BASE_URL":                   containerReachableBaseURL(flag(args, "base-url", defaultEmbeddingBaseURL)),
		"EMBEDDING_API_KEY":                    apiKey,
		"EMBEDDING_MODEL":                      flag(args, "model", defaultServedModelName),
		"EMBEDDING_DIMENSIONS":                 flag(args, "dimensions", "1024"),
		"EMBEDDING_TIMEOUT":                    flag(args, "embedding-timeout", "10m"),
		"ABRA_AI_PROVIDER_CONCURRENCY":         flag(args, "provider-concurrency", "1"),
		"RERANKER_PROVIDER":                    flag(args, "reranker-provider", ""),
		"RERANKER_BASE_URL":                    flag(args, "reranker-base-url", ""),
		"RERANKER_API_KEY":                     apiKey,
		"RERANKER_MODEL":                       flag(args, "reranker-model", ""),
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
	fmt.Println("Run `abra up` to start the default local Qwen embedding runner and stack, or use `abra models up` only to manage the runner directly.")
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
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                     "compatible",
		"EMBEDDING_BASE_URL":                     baseURL,
		"EMBEDDING_API_KEY":                      apiKey,
		"EMBEDDING_MODEL":                        model,
		"EMBEDDING_DIMENSIONS":                   dimensions,
		"EMBEDDING_TIMEOUT":                      flag(args, "embedding-timeout", "30s"),
		"ABRA_AI_PROVIDER_CONCURRENCY":           flag(args, "provider-concurrency", "4"),
		"RERANKER_PROVIDER":                      "",
		"RERANKER_BASE_URL":                      "",
		"RERANKER_API_KEY":                       "",
		"RERANKER_MODEL":                         "",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION":   "false",
		"ABRA_LOCAL_EMBEDDING_IMAGE":             "",
		"ABRA_LOCAL_EMBEDDING_PULL_POLICY":       "missing",
		"ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT": "10s",
	}); err != nil {
		return err
	}
	fmt.Println("Model config updated: " + label)
	printRestartHint(args)
	return nil
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
		"ABRA_AI_PROVIDER_CONCURRENCY",
		"RERANKER_PROVIDER",
		"RERANKER_BASE_URL",
		"RERANKER_API_KEY",
		"RERANKER_MODEL",
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
	fmt.Println("After changing embedding providers, re-ingest important sources so vector recall uses the new embedding space.")
}

func up(ctx context.Context, args cliArgs) error {
	projectDir, err := ensureProjectDir(ctx, args)
	if err != nil {
		return err
	}
	if err := ensureEnv(args); err != nil {
		return err
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("missing required command: docker")
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
			"hint":   "keep WORKER_CONCURRENCY=1 for the default local Qwen runner, or raise ABRA_AI_PROVIDER_CONCURRENCY only after provider capacity is measured",
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
			"hint":   "run: abra config model local, or abra config model compatible --base-url <url> --model <model>",
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
			"hint":   "local model readiness is checked by local_embeddings; use abra models status when ingest or setup stalls",
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
			"detail": "ABRA_AI_PROVIDER_CONCURRENCY=" + raw + " can overload a single local Qwen model runner",
			"hint":   "set ABRA_AI_PROVIDER_CONCURRENCY=1 in " + path + ", then run: abra down && abra up",
		}
	}
	return map[string]any{
		"name":   "ai_provider_concurrency",
		"ok":     true,
		"detail": "ABRA_AI_PROVIDER_CONCURRENCY=" + raw,
	}
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
			"hint":   "keep ABRA_COMPOSE_RECALL_CONCURRENCY=1 for the default local Qwen runner, or raise ABRA_AI_PROVIDER_CONCURRENCY only after provider capacity is measured",
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
	return map[string]any{"name": "mcp", "ok": true, "detail": fmt.Sprintf("tools=%d required=discover_scopes,working_memory_compose", toolCount)}
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
		check["hint"] = "run: abra setup, then abra mcp install-codex"
		return check
	}
	if actualToken == "" {
		check["ok"] = false
		check["detail"] = tokenEnv + " is not set in this shell; Codex also needs it in the Codex process environment"
		check["hint"] = "run: abra mcp install-codex, fully quit and reopen Codex Desktop, or export " + tokenEnv + " before launching terminal Codex"
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
		"then run: abra agents verify . --scope " + shellQuote(scopeOrDefault(args, ".")),
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
		check["hint"] = "run: abra setup, then abra mcp install-codex"
		return check
	}
	actualToken, err := commandOutput("launchctl", "getenv", tokenEnv)
	if err != nil {
		check["ok"] = false
		check["detail"] = "could not read macOS launch environment: " + err.Error()
		check["hint"] = "run: abra mcp install-codex, or export " + tokenEnv + " before launching terminal Codex"
		return check
	}
	actualToken = strings.TrimSpace(actualToken)
	switch {
	case actualToken == "":
		check["ok"] = false
		check["detail"] = tokenEnv + " is not set in the macOS launch environment used by Codex Desktop"
		check["hint"] = "run: abra mcp install-codex, then fully quit and reopen Codex Desktop"
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

func seed(ctx context.Context, args cliArgs) error {
	scope := flag(args, "scope", os.Getenv("ABRA_SCOPE"))
	if strings.TrimSpace(scope) == "" {
		scope = "repo:abra-demo"
	}
	content := strings.TrimSpace(strings.Join(args.Rest, " "))
	if content == "" {
		content = strings.Join([]string{
			"Abra is a CLI-first governed brain layer for AI agents.",
			"Agents should use Abra before autonomous code changes.",
			"Abra returns citations, graph context, gap analysis, memory health, and an agent decision gate.",
		}, "\n")
	}
	if err := ingest(ctx, args, map[string]any{
		"source_type": flag(args, "source-type", "markdown"),
		"source_url":  flag(args, "source-url", "cli://seed-"+timestamp()),
		"title":       flag(args, "title", "Abra CLI Seed"),
		"scope":       scope,
		"content":     content,
		"authority":   flag(args, "authority", "official-doc"),
	}); err != nil {
		return err
	}
	fmt.Println("Seeded memory in " + scope)
	fmt.Printf("Try: abra think %q --scope %s\n", "What should agents use before code changes?", scope)
	return nil
}

func ingestCommand(ctx context.Context, args cliArgs) error {
	if flag(args, "path", "") == "" && flag(args, "file", "") == "" && flag(args, "text", "") == "" && len(args.Rest) > 0 {
		if info, err := os.Stat(args.Rest[0]); err == nil {
			if info.IsDir() {
				args.Flags["path"] = args.Rest[0]
				args.Rest = args.Rest[1:]
			} else {
				args.Flags["file"] = args.Rest[0]
				args.Rest = args.Rest[1:]
			}
		}
	}
	if flag(args, "path", "") != "" {
		if boolFlag(args, "tracked") || boolFlag(args, "worker") {
			if !boolFlag(args, "no-wait") {
				args.Bools["wait"] = true
			}
			return sourceIngest(ctx, args)
		}
		if boolFlag(args, "direct") && boolFlag(args, "wait") {
			return errors.New("--direct cannot be combined with --wait; direct local ingest runs synchronously")
		}
		return localPathIngest(ctx, args)
	}
	if flag(args, "git", "") != "" || flag(args, "repo", "") != "" {
		return sourceIngest(ctx, args)
	}
	scope := scopeOrDefault(args, ".")
	content := flag(args, "text", "")
	sourceURL := flag(args, "source-url", "")
	title := flag(args, "title", "CLI Note")
	if file := flag(args, "file", ""); file != "" {
		bytes, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		content = string(bytes)
		if sourceURL == "" {
			abs, err := filepath.Abs(file)
			if err != nil {
				return err
			}
			sourceURL = localFileURL(filepath.Dir(abs), filepath.Base(abs))
		}
		if title == "CLI Note" {
			title = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		}
	}
	if content == "" {
		content = strings.TrimSpace(strings.Join(args.Rest, " "))
	}
	if content == "" {
		return errors.New("ingest requires a path, --text, --file, or positional content")
	}
	if sourceURL == "" {
		sourceURL = "cli://" + slug(title) + "-" + timestamp()
	}
	body := map[string]any{
		"source_type": flag(args, "source-type", "markdown"),
		"source_url":  sourceURL,
		"title":       title,
		"scope":       scope,
		"content":     content,
		"authority":   flag(args, "authority", "official-doc"),
	}
	result, err := postJSONWithTimeout(ctx, args, "/ingest/documents", body, cliTimeout(args, defaultIngestTimeout))
	if err != nil {
		return friendlyProviderError(err)
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Println("Ingested: " + stringValue(result["document_id"], stringValue(body["source_url"], "")))
	fmt.Println("scope: " + scope)
	return nil
}

func localPathIngest(ctx context.Context, args cliArgs) error {
	root := flag(args, "path", ".")
	abs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	scope := scopeOrDefault(args, abs)
	sourceID := flag(args, "source-id", "")
	if sourceID == "" {
		sourceID = flag(args, "name", "")
	}
	if sourceID == "" {
		sourceID = slug(abs)
	}
	if sourceID == "" {
		sourceID = "local-" + timestamp()
	}
	source := ingestpkg.SourceSpec{
		ID:          sourceID,
		Type:        ingestpkg.SourceTypeLocalRepo,
		Root:        abs,
		Scope:       scope,
		Include:     csv(flag(args, "include", "")),
		Exclude:     csv(flag(args, "exclude", "")),
		IncludeCode: boolFlag(args, "code"),
		CodeInclude: csv(flag(args, "code-include", "")),
		CodeExclude: csv(flag(args, "code-exclude", "")),
		MaxFileBytes: int64(intFlag(
			args,
			"max-file-bytes",
			int(ingestpkg.DefaultMaxFileBytes),
		)),
		IncludeGenerated: boolFlag(args, "include-generated"),
		Metadata: map[string]string{
			"created_by":      flag(args, "created-by", "abra-cli"),
			"ingest_channel":  "cli-local",
			"authority":       flag(args, "authority", "manual-unverified"),
			"authority_score": strconv.FormatFloat(floatFlag(args, "authority-score", 0.35), 'f', -1, 64),
		},
	}
	if len(source.Include) == 0 {
		source.Include = []string{"**/*.md"}
	}
	ingestor, err := ingestpkg.NewLocalRepoMarkdownIngestor(source)
	if err != nil {
		return err
	}
	ingestResult, err := ingestor.IngestWithStats(ctx)
	if err != nil {
		return err
	}
	documents := ingestResult.Documents
	if len(documents) == 0 {
		if len(ingestResult.Skipped) > 0 {
			return fmt.Errorf("no ingestable files found; skipped %d file(s) before read (raise --max-file-bytes or pass --include-generated when appropriate)", len(ingestResult.Skipped))
		}
		return errors.New("no matching files found; adjust --include, add --code, or check --path")
	}
	results := make([]map[string]any, 0, len(documents))
	failures := make([]map[string]any, 0)
	skippedEmpty := 0
	continueOnError := boolFlag(args, "continue-on-error") || boolFlag(args, "continue")
	progress := !boolFlag(args, "json") && !boolFlag(args, "quiet")
	if progress {
		fmt.Printf("Ingesting files: %d\n", len(documents))
		fmt.Println("scope: " + scope)
		fmt.Println("model work can take a while on the first local embedding call; current file is printed before each request.")
	}
	for index, doc := range documents {
		if strings.TrimSpace(doc.Content) == "" {
			skippedEmpty++
			if progress {
				fmt.Printf("[%d/%d] skip empty %s\n", index+1, len(documents), doc.Path)
			}
			continue
		}
		metadata := stringMapToAny(doc.Metadata)
		metadata["ingest_path"] = doc.Path
		metadata["ingest_checksum"] = doc.Checksum
		metadata["ingest_fingerprint"] = doc.Fingerprint
		sourceURL := localFileURL(abs, doc.Path)
		if progress {
			fmt.Printf("[%d/%d] ingest %s\n", index+1, len(documents), doc.Path)
		}
		result, err := postJSONWithTimeout(ctx, args, "/ingest/documents", map[string]any{
			"source_type": string(doc.SourceType),
			"source_url":  sourceURL,
			"source_id":   doc.SourceID,
			"title":       doc.Title,
			"scope":       doc.Scope,
			"content":     doc.Content,
			"metadata":    metadata,
		}, cliTimeout(args, defaultIngestTimeout))
		if err != nil {
			friendly := friendlyProviderError(err)
			if !continueOnError {
				return fmt.Errorf("ingest %s: %w", doc.Path, friendly)
			}
			failures = append(failures, map[string]any{
				"path":       doc.Path,
				"source_url": sourceURL,
				"error":      friendly.Error(),
			})
			if progress {
				fmt.Printf("[%d/%d] failed %s\n", index+1, len(documents), doc.Path)
			}
			continue
		}
		results = append(results, map[string]any{
			"path":        doc.Path,
			"source_url":  sourceURL,
			"document_id": stringValue(result["document_id"], ""),
			"chunks":      result["chunks"],
			"claims":      result["claims"],
			"relations":   result["relations"],
		})
		if progress {
			fmt.Printf("[%d/%d] ok %s\n", index+1, len(documents), doc.Path)
		}
	}
	if len(results) == 0 && len(failures) == 0 {
		return fmt.Errorf("no non-empty matching files found; skipped %d empty file(s)", skippedEmpty)
	}
	if boolFlag(args, "json") {
		if err := printJSON(map[string]any{"scope": scope, "documents": results, "failures": failures, "skipped_empty": skippedEmpty, "skipped_files": skippedFilesForJSON(ingestResult.Skipped)}); err != nil {
			return err
		}
		if len(failures) > 0 {
			return fmt.Errorf("ingest completed with %d failure(s)", len(failures))
		}
		return nil
	}
	fmt.Printf("Ingested files: %d\n", len(results))
	if skippedEmpty > 0 {
		fmt.Printf("Skipped empty files: %d\n", skippedEmpty)
	}
	if len(ingestResult.Skipped) > 0 {
		fmt.Printf("Skipped files before read: %d\n", len(ingestResult.Skipped))
		for i, skipped := range ingestResult.Skipped {
			if i >= 5 {
				fmt.Printf("- ... %d more skipped file(s)\n", len(ingestResult.Skipped)-i)
				break
			}
			fmt.Printf("- %s: %s (%d bytes)\n", skipped.Path, skipped.Reason, skipped.Bytes)
		}
	}
	if len(failures) > 0 {
		fmt.Printf("Failed files: %d\n", len(failures))
		for i, failure := range failures {
			if i >= 5 {
				fmt.Printf("- ... %d more failure(s)\n", len(failures)-i)
				break
			}
			fmt.Printf("- %s: %s\n", stringValue(failure["path"], ""), stringValue(failure["error"], "unknown error"))
		}
	}
	fmt.Println("scope: " + scope)
	fmt.Println("source: " + source.ID)
	if len(failures) > 0 {
		return fmt.Errorf("ingest completed with %d failure(s)", len(failures))
	}
	return nil
}

func skippedFilesForJSON(skipped []ingestpkg.SkippedFile) []map[string]any {
	out := make([]map[string]any, 0, len(skipped))
	for _, file := range skipped {
		out = append(out, map[string]any{
			"path":   file.Path,
			"reason": file.Reason,
			"bytes":  file.Bytes,
		})
	}
	return out
}

func watch(ctx context.Context, args cliArgs) error {
	if len(args.Rest) == 0 {
		return errors.New(commandUsage("watch"))
	}
	mode := strings.ToLower(strings.TrimSpace(args.Rest[0]))
	args.Rest = args.Rest[1:]
	switch mode {
	case "local", "path", "repo":
		if flag(args, "path", "") == "" {
			args.Flags["path"] = "."
		}
	case "git", "github", "remote":
		if flag(args, "git", "") == "" && flag(args, "repo", "") == "" {
			if len(args.Rest) == 0 {
				return errors.New("watch git requires --git <url> or a positional repo URL")
			}
			args.Flags["git"] = args.Rest[0]
			args.Rest = args.Rest[1:]
		}
	case "mcp":
		if flag(args, "mcp-url", "") == "" && flag(args, "url", "") == "" {
			if len(args.Rest) == 0 {
				return errors.New("source mcp requires --mcp-url <url> or a positional MCP HTTP URL")
			}
			args.Flags["mcp-url"] = args.Rest[0]
			args.Rest = args.Rest[1:]
		}
	default:
		return fmt.Errorf("unknown watch mode %q\n\n%s", mode, commandUsage("watch"))
	}
	return sourceIngest(ctx, args)
}

func sourceIngest(ctx context.Context, args cliArgs) error {
	sourceType := "local_repo"
	sourceURL := ""
	scopeHint := "."
	config := map[string]any{}
	isMCPSource := flag(args, "mcp-url", "") != "" || flag(args, "url", "") != "" || strings.EqualFold(flag(args, "type", ""), "mcp")
	if repo := firstNonEmpty(flag(args, "git", ""), flag(args, "repo", "")); repo != "" {
		sourceType = "git_repo"
		sourceURL = repo
		scopeHint = repo
		config["repository_url"] = repo
		if ref := firstNonEmpty(flag(args, "ref", ""), flag(args, "branch", "")); ref != "" {
			config["git_ref"] = ref
		}
		config["git_depth"] = intFlag(args, "depth", 1)
	} else if isMCPSource {
		sourceType = "mcp"
		sourceURL = firstNonEmpty(flag(args, "mcp-url", ""), flag(args, "url", ""))
		scopeHint = sourceURL
		tool := flag(args, "tool", "")
		if tool == "" {
			return errors.New("source mcp requires --tool <mcp-tool-name>")
		}
		config["server_url"] = sourceURL
		config["tool"] = tool
		if raw := flag(args, "arguments-json", flag(args, "args-json", "")); raw != "" {
			arguments, err := parseJSONObjectFlag(raw, "arguments-json")
			if err != nil {
				return err
			}
			config["arguments"] = arguments
		} else {
			config["arguments"] = map[string]any{}
		}
		if envName := flag(args, "bearer-token-env", ""); envName != "" {
			config["bearer_token_env"] = envName
		}
		if docSourceType := flag(args, "document-source-type", ""); docSourceType != "" {
			config["document_source_type"] = docSourceType
		}
	} else {
		path := flag(args, "path", ".")
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		sourceURL = "file://" + filepath.ToSlash(abs)
		config["root"] = abs
		scopeHint = abs
	}
	scope := scopeOrDefault(args, scopeHint)
	if sourceType != "mcp" {
		if include := csv(flag(args, "include", "")); len(include) > 0 {
			config["include"] = include
		} else {
			config["include"] = []string{"**/*.md"}
		}
		if exclude := csv(flag(args, "exclude", "")); len(exclude) > 0 {
			config["exclude"] = exclude
		}
		if boolFlag(args, "code") {
			config["include_code"] = true
			if codeInclude := csv(flag(args, "code-include", "")); len(codeInclude) > 0 {
				config["code_include"] = codeInclude
			}
			if codeExclude := csv(flag(args, "code-exclude", "")); len(codeExclude) > 0 {
				config["code_exclude"] = codeExclude
			}
		}
		if maxFileBytes := intFlag(args, "max-file-bytes", 0); maxFileBytes > 0 {
			config["max_file_bytes"] = maxFileBytes
		}
		if boolFlag(args, "include-generated") {
			config["include_generated"] = true
		}
	}
	name := flag(args, "name", "")
	if name == "" {
		name = slug(strings.TrimPrefix(strings.TrimPrefix(sourceURL, "file://"), "https://"))
		if name == "" {
			name = "source-" + timestamp()
		}
	}
	body := map[string]any{
		"name":            name,
		"source_type":     sourceType,
		"scope":           scope,
		"base_url":        sourceURL,
		"connector_kind":  flag(args, "connector", defaultConnectorKind(sourceType)),
		"status":          flag(args, "status", "active"),
		"authority":       flag(args, "authority", "manual-unverified"),
		"authority_score": floatFlag(args, "authority-score", 0.35),
		"config":          config,
		"metadata": map[string]any{
			"created_by": "abra-cli",
		},
		"created_by": flag(args, "created-by", "abra-cli"),
	}
	if freshnessSeconds := intFlag(args, "freshness-seconds", 0); freshnessSeconds > 0 {
		body["freshness_policy"] = map[string]any{"max_age_seconds": freshnessSeconds}
	}
	if schedule := flag(args, "schedule", ""); schedule != "" {
		body["schedule_cron"] = schedule
	}
	if approvalID := flag(args, "approval-id", ""); approvalID != "" {
		body["approval_id"] = approvalID
	}
	source, err := postJSON(ctx, args, "/sources/configs", body)
	if err != nil {
		return err
	}
	sourceID := stringValue(source["source_config_id"], "")
	if sourceID == "" {
		return errors.New("source config response did not include source_config_id")
	}
	job, err := postJSON(ctx, args, "/ingestion/jobs", map[string]any{
		"source_config_id": sourceID,
		"trigger_type":     flag(args, "trigger", "manual"),
		"created_by":       flag(args, "created-by", "abra-cli"),
		"max_attempts":     intFlag(args, "max-attempts", 3),
		"metadata":         map[string]any{"channel": "cli"},
	})
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(map[string]any{"source": source, "job": job})
	}
	fmt.Println("Source configured: " + sourceID)
	fmt.Println("scope: " + scope)
	if ingestionJob, _ := job["ingestion_job"].(map[string]any); ingestionJob != nil {
		fmt.Println("Job queued: " + stringValue(ingestionJob["id"], ""))
	}
	fmt.Println("Check jobs: abra jobs --scope " + scope)
	if boolFlag(args, "wait") {
		jobID := ""
		if ingestionJob, _ := job["ingestion_job"].(map[string]any); ingestionJob != nil {
			jobID = stringValue(ingestionJob["id"], "")
		}
		_, err := waitForSourceJob(ctx, args, scope, sourceID, jobID)
		return err
	}
	if sourceType == "local_repo" {
		fmt.Println("Tip: local tracked sources require the worker to see the same path. Use `abra ingest . --code` for direct local ingestion.")
	}
	return nil
}

func listSources(ctx context.Context, args cliArgs) error {
	if len(args.Rest) > 0 {
		action := strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
		switch action {
		case "sync", "enqueue", "run":
			return syncSource(ctx, args)
		default:
			return fmt.Errorf("unknown sources action %q\n\n%s", action, commandUsage("sources"))
		}
	}
	path := "/sources/configs?limit=" + strconv.Itoa(intFlag(args, "limit", 50))
	if scope := flag(args, "scope", os.Getenv("ABRA_SCOPE")); scope != "" {
		path += "&scope=" + urlQueryEscape(scope)
	}
	result, _, err := getJSON(ctx, args, path)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	items, _ := result["source_configs"].([]any)
	fmt.Printf("Sources: %d\n", len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		fmt.Printf("- %s  %s  %s  %s\n", stringValue(item["id"], ""), stringValue(item["status"], ""), stringValue(item["source_type"], ""), stringValue(item["name"], ""))
	}
	return nil
}

func syncSource(ctx context.Context, args cliArgs) error {
	sourceID := firstNonEmpty(flag(args, "source-config-id", ""), flag(args, "source-id", ""), flag(args, "id", ""))
	if sourceID == "" && len(args.Rest) > 0 {
		sourceID = strings.TrimSpace(args.Rest[0])
		args.Rest = args.Rest[1:]
	}
	if sourceID == "" {
		return errors.New("sources sync requires a source config id, for example: abra sources sync source-123")
	}
	result, err := postJSON(ctx, args, "/ingestion/jobs", map[string]any{
		"source_config_id": sourceID,
		"trigger_type":     flag(args, "trigger", "manual"),
		"created_by":       flag(args, "created-by", "abra-cli"),
		"max_attempts":     intFlag(args, "max-attempts", 3),
		"metadata":         map[string]any{"channel": "cli", "command": "sources sync"},
	})
	if err != nil {
		return err
	}
	job, _ := result["ingestion_job"].(map[string]any)
	jobID := stringValue(job["id"], "")
	scope := firstNonEmpty(stringValue(job["scope"], ""), flag(args, "scope", ""), os.Getenv("ABRA_SCOPE"))
	if boolFlag(args, "wait") {
		if scope == "" {
			return errors.New("--wait requires --scope when the enqueue response does not include scope")
		}
		if boolFlag(args, "json") {
			waitedJob, err := waitForSourceJob(ctx, args, scope, sourceID, jobID)
			if err != nil {
				return err
			}
			return printJSON(map[string]any{"enqueue": result, "job": waitedJob})
		}
		fmt.Println("Source queued: " + sourceID)
		if jobID != "" {
			fmt.Println("Job queued: " + jobID)
		}
		fmt.Println("Check jobs: abra jobs --scope " + scope + " --source-config-id " + sourceID)
		_, err := waitForSourceJob(ctx, args, scope, sourceID, jobID)
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Println("Source queued: " + sourceID)
	if jobID != "" {
		fmt.Println("Job queued: " + jobID)
	}
	if scope != "" {
		fmt.Println("Check jobs: abra jobs --scope " + scope + " --source-config-id " + sourceID)
	}
	return nil
}

func listJobs(ctx context.Context, args cliArgs) error {
	scope := scopeOrDefault(args, ".")
	path := "/ingestion/jobs?scope=" + urlQueryEscape(scope) + "&limit=" + strconv.Itoa(intFlag(args, "limit", 20))
	if sourceID := flag(args, "source-config-id", ""); sourceID != "" {
		path += "&source_config_id=" + urlQueryEscape(sourceID)
	}
	result, _, err := getJSON(ctx, args, path)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	items, _ := result["ingestion_jobs"].([]any)
	fmt.Printf("Jobs: %d\n", len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		fmt.Printf("- %s  %s  seen=%v changed=%v source=%s\n",
			stringValue(item["id"], ""),
			stringValue(item["status"], ""),
			item["documents_seen"],
			item["documents_changed"],
			stringValue(item["source_config_id"], ""),
		)
	}
	return nil
}

func ingest(ctx context.Context, args cliArgs, body map[string]any) error {
	_, err := postJSONWithTimeout(ctx, args, "/ingest/documents", body, cliTimeout(args, defaultIngestTimeout))
	return err
}

func observe(ctx context.Context, args cliArgs) error {
	text := strings.TrimSpace(strings.Join(args.Rest, " "))
	if text == "" {
		text = strings.TrimSpace(flag(args, "text", ""))
	}
	if text == "" {
		return errors.New("observe requires text, for example: abra observe \"Agents should rerun release checks before tagging\"")
	}
	scope := scopeOrDefault(args, ".")
	metadata := map[string]any{"channel": "cli"}
	if title := strings.TrimSpace(flag(args, "title", "")); title != "" {
		metadata["title"] = title
	}
	body := map[string]any{
		"scope":            scope,
		"observation_text": text,
		"observation_type": flag(args, "type", flag(args, "observation-type", "episode")),
		"status":           flag(args, "status", "raw"),
		"authority":        flag(args, "authority", "manual-unverified"),
		"authority_score":  floatFlag(args, "authority-score", 0.35),
		"confidence":       floatFlag(args, "confidence", 0.35),
		"source_url":       flag(args, "source-url", ""),
		"source_type":      flag(args, "source-type", ""),
		"source_id":        flag(args, "source-id", ""),
		"observed_at":      flag(args, "observed-at", ""),
		"created_by":       flag(args, "created-by", "abra-cli"),
		"approval_id":      flag(args, "approval-id", ""),
		"metadata":         metadata,
	}
	result, err := postJSON(ctx, args, "/observations", body)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") && !boolFlag(args, "propose") {
		return printJSON(result)
	}
	observation, _ := result["observation"].(map[string]any)
	if boolFlag(args, "propose") {
		observationID := stringValue(observation["id"], "")
		proposed, err := proposeObservation(ctx, args, observationID, text)
		if err != nil {
			return err
		}
		if boolFlag(args, "json") {
			return printJSON(proposed)
		}
		proposal, _ := proposed["learning_proposal"].(map[string]any)
		fmt.Println("Observation proposed: " + stringValue(proposal["id"], "unknown"))
		fmt.Println("scope: " + stringValue(observation["scope"], scope))
		fmt.Println("type: " + stringValue(proposal["proposal_type"], "claim"))
		fmt.Println("status: " + stringValue(proposal["status"], "pending"))
		fmt.Println("trusted: no, accepted proposal still requires explicit apply")
		return nil
	}
	fmt.Println("Observation captured: " + stringValue(observation["id"], "unknown"))
	fmt.Println("scope: " + stringValue(observation["scope"], scope))
	fmt.Println("type: " + stringValue(observation["observation_type"], stringValue(body["observation_type"], "episode")))
	fmt.Println("status: " + stringValue(observation["status"], stringValue(body["status"], "raw")))
	fmt.Println("trusted: no, promote through review before treating as a claim")
	return nil
}

func listObservations(ctx context.Context, args cliArgs) error {
	if len(args.Rest) > 0 && args.Rest[0] == "propose" {
		observationID := ""
		if len(args.Rest) > 1 {
			observationID = args.Rest[1]
		}
		if observationID == "" {
			observationID = flag(args, "observation-id", "")
		}
		if observationID == "" {
			return errors.New("observations propose requires an observation id")
		}
		result, err := proposeObservation(ctx, args, observationID, flag(args, "claim", ""))
		if err != nil {
			return err
		}
		if boolFlag(args, "json") {
			return printJSON(result)
		}
		proposal, _ := result["learning_proposal"].(map[string]any)
		observation, _ := result["observation"].(map[string]any)
		fmt.Println("Observation proposed: " + stringValue(proposal["id"], "unknown"))
		fmt.Println("observation: " + stringValue(observation["id"], observationID))
		fmt.Println("scope: " + stringValue(observation["scope"], scopeOrDefault(args, ".")))
		fmt.Println("type: " + stringValue(proposal["proposal_type"], "claim"))
		fmt.Println("status: " + stringValue(proposal["status"], "pending"))
		fmt.Println("trusted: no, accepted proposal still requires explicit apply")
		return nil
	}
	scope := scopeOrDefault(args, ".")
	params := url.Values{}
	params.Set("scope", scope)
	if query := strings.TrimSpace(strings.Join(args.Rest, " ")); query != "" {
		params.Set("query", query)
	}
	if query := strings.TrimSpace(flag(args, "query", "")); query != "" {
		params.Set("query", query)
	}
	for _, pair := range []struct {
		flag  string
		param string
	}{
		{"type", "type"},
		{"observation-type", "observation_type"},
		{"status", "status"},
		{"since", "since"},
		{"until", "until"},
	} {
		if value := strings.TrimSpace(flag(args, pair.flag, "")); value != "" {
			params.Set(pair.param, value)
		}
	}
	params.Set("limit", strconv.Itoa(intFlag(args, "limit", 20)))
	result, _, err := getJSON(ctx, args, "/observations?"+params.Encode())
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	observations, _ := result["observations"].([]any)
	fmt.Printf("Observations: %d\n", len(observations))
	for _, raw := range observations {
		observation, _ := raw.(map[string]any)
		text := stringValue(observation["observation_text"], "")
		if len(text) > 96 {
			text = text[:93] + "..."
		}
		fmt.Printf("- %s  %s  %s/%s  %s\n",
			stringValue(observation["id"], ""),
			stringValue(observation["observed_at"], ""),
			stringValue(observation["observation_type"], "episode"),
			stringValue(observation["status"], "raw"),
			text,
		)
	}
	return nil
}

func proposeObservation(ctx context.Context, args cliArgs, observationID, candidateClaim string) (map[string]any, error) {
	observationID = strings.TrimSpace(observationID)
	if observationID == "" {
		return nil, errors.New("observation id is required")
	}
	scope := scopeOrDefault(args, ".")
	payload := map[string]any{"channel": "cli", "observation_id": observationID, "promotion_flow": "observation_to_claim"}
	if candidateClaim = strings.TrimSpace(candidateClaim); candidateClaim != "" {
		payload["claim"] = candidateClaim
	}
	body := map[string]any{
		"scope":         scope,
		"proposal_type": flag(args, "proposal-type", flag(args, "type", "claim")),
		"title":         flag(args, "title", ""),
		"rationale":     flag(args, "rationale", ""),
		"target_type":   "observation",
		"target_id":     observationID,
		"source_url":    flag(args, "source-url", ""),
		"confidence":    floatFlag(args, "confidence", 0),
		"created_by":    flag(args, "created-by", "abra-cli"),
		"approval_id":   flag(args, "approval-id", ""),
		"payload":       payload,
	}
	return postJSON(ctx, args, "/learning/proposals", body)
}

func think(ctx context.Context, args cliArgs) error {
	question := strings.TrimSpace(strings.Join(args.Rest, " "))
	if question == "" {
		question = flag(args, "question", "")
	}
	if question == "" {
		return errors.New("think requires a question, for example: abra think \"what should I know?\"")
	}
	scope := scopeOrDefault(args, ".")
	result, err := postJSON(ctx, args, "/brain/think", map[string]any{
		"question":           question,
		"scope":              scope,
		"agent":              flag(args, "agent", ""),
		"limit":              intFlag(args, "limit", 5),
		"max_queries":        intFlag(args, "max-queries", 4),
		"token_budget":       intFlag(args, "token-budget", 0),
		"include_unverified": boolFlag(args, "include-unverified"),
	})
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	printThink(result)
	return nil
}

func recall(ctx context.Context, args cliArgs) error {
	query := strings.TrimSpace(strings.Join(args.Rest, " "))
	if query == "" {
		query = flag(args, "query", "")
	}
	if query == "" {
		return errors.New("recall requires a query, for example: abra recall \"agent memory\"")
	}
	scope := scopeOrDefault(args, ".")
	result, err := postJSON(ctx, args, "/recall", map[string]any{
		"query":              query,
		"scope":              scope,
		"limit":              intFlag(args, "limit", 5),
		"include_unverified": boolFlag(args, "include-unverified"),
	})
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	claims, _ := result["claims"].([]any)
	fmt.Printf("Recall: %d claims\n", len(claims))
	for i, raw := range claims {
		if i >= 8 {
			break
		}
		claim, _ := raw.(map[string]any)
		fmt.Printf("- %s (%s)\n", stringValue(claim["claim_text"], ""), stringValue(claim["status"], "unknown"))
	}
	return nil
}

func composeMemory(ctx context.Context, args cliArgs) error {
	task := strings.TrimSpace(strings.Join(args.Rest, " "))
	if task == "" {
		task = flag(args, "task", "")
	}
	if task == "" {
		return errors.New("compose requires a task, for example: abra compose \"ship a change\"")
	}
	scope := scopeOrDefault(args, ".")
	result, err := postJSON(ctx, args, "/memory/compose", map[string]any{
		"task":               task,
		"scope":              scope,
		"hook":               flag(args, "hook", "before_task"),
		"agent":              flag(args, "agent", ""),
		"limit":              intFlag(args, "limit", 5),
		"max_queries":        intFlag(args, "max-queries", 4),
		"token_budget":       intFlag(args, "token-budget", 1200),
		"include_unverified": boolFlag(args, "include-unverified"),
		"persist_learning":   boolFlag(args, "persist-learning"),
	})
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	verification, _ := result["verification"].(map[string]any)
	decision, _ := result["agent_decision"].(map[string]any)
	stats, _ := result["stats"].(map[string]any)
	health, _ := result["memory_health"].(map[string]any)
	scope = stringValue(result["scope"], scope)
	fmt.Printf("Compose: %s / %s\n", stringValue(verification["verdict"], "unknown"), stringValue(decision["decision"], "unknown"))
	fmt.Println("scope: " + scope)
	if len(stats) > 0 {
		fmt.Printf("context: facts=%d documents=%d summaries=%d graph=%d blocks=%d\n",
			intValue(stats["facts"]),
			intValue(stats["supporting_documents"]),
			intValue(stats["summaries"]),
			intValue(stats["graph_relations"]),
			intValue(stats["context_blocks"]),
		)
	}
	if len(health) > 0 {
		fmt.Printf("health: %s score=%d signals=%d\n", stringValue(health["status"], "unknown"), intValue(health["score"]), lenSlice(health["signals"]))
		if signals, _ := health["signals"].([]any); len(signals) > 0 {
			fmt.Println("health signals:")
			printComposeSignals(signals, 5)
		}
	}
	if quality, _ := verification["retrieval_quality"].(map[string]any); len(quality) > 0 {
		fmt.Printf("retrieval: results=%d sources=%d low_confidence=%t low_source_diversity=%t\n",
			intValue(quality["result_count"]),
			intValue(quality["unique_sources"]),
			boolValue(quality["low_confidence"], false),
			boolValue(quality["low_source_diversity"], false),
		)
	}
	if citations, _ := result["citations"].([]any); len(citations) > 0 {
		fmt.Println("citations:")
		for i, raw := range citations {
			if i >= 5 {
				fmt.Printf("- +%d more\n", len(citations)-i)
				break
			}
			item, _ := raw.(map[string]any)
			fmt.Printf("- %s: %s\n", stringValue(item["ref"], "?"), stringValue(item["source_url"], "unknown"))
		}
	}
	if evidence, _ := result["evidence"].([]any); len(evidence) > 0 {
		fmt.Println("evidence:")
		for i, raw := range evidence {
			if i >= 5 {
				fmt.Printf("- +%d more\n", len(evidence)-i)
				break
			}
			item, _ := raw.(map[string]any)
			ref := stringValue(item["ref"], "")
			if ref != "" {
				ref = "[" + ref + "] "
			}
			fmt.Printf("- %s%s (%d)\n", ref, stringValue(item["source_url"], "unknown"), intValue(item["count"]))
		}
	}
	if actions, ok := decision["required_actions"].([]any); ok && len(actions) > 0 {
		fmt.Println("required actions:")
		for _, action := range actions {
			fmt.Println("- " + stringValue(action, ""))
		}
	}
	if actions, ok := decision["allowed_next_actions"].([]any); ok && len(actions) > 0 {
		fmt.Println("allowed next actions:")
		printComposeStringList(actions, 6)
	}
	if validation, ok := result["validation_plan"].([]any); ok && len(validation) > 0 {
		fmt.Println("validation plan:")
		printComposeValidationPlan(validation, 6)
	}
	if steps, ok := result["suggested_steps"].([]any); ok && len(steps) > 0 {
		fmt.Println("suggested steps:")
		printComposeStringList(steps, 5)
	}
	if boolFlag(args, "prompt") {
		window, _ := result["context_window"].(map[string]any)
		prompt := stringValue(window["prompt"], "")
		if prompt == "" {
			fmt.Println("prompt-ready context: unavailable")
		} else {
			fmt.Println("prompt-ready context:")
			fmt.Println(prompt)
		}
	}
	if len(stats) > 0 && intValue(stats["facts"])+intValue(stats["supporting_documents"])+intValue(stats["summaries"])+intValue(stats["graph_relations"]) == 0 {
		fmt.Println("No source-backed context found for this scope.")
		fmt.Println("Confirm the project scope: abra scope")
		fmt.Println("Then ingest the project with that exact scope: abra ingest . --code --scope " + scope)
	}
	return nil
}

func printComposeSignals(signals []any, limit int) {
	for i, raw := range signals {
		if i >= limit {
			fmt.Printf("- +%d more\n", len(signals)-i)
			return
		}
		signal, _ := raw.(map[string]any)
		code := stringValue(signal["code"], "unknown")
		severity := stringValue(signal["severity"], "unknown")
		action := stringValue(signal["action"], "")
		count := intValue(signal["count"])
		line := "- " + code + " (" + severity
		if count > 0 {
			line += fmt.Sprintf(", count=%d", count)
		}
		line += ")"
		if action != "" {
			line += " -> " + action
		}
		fmt.Println(line)
	}
}

func printComposeStringList(values []any, limit int) {
	for i, raw := range values {
		if i >= limit {
			fmt.Printf("- +%d more\n", len(values)-i)
			return
		}
		text := strings.TrimSpace(stringValue(raw, ""))
		if text == "" {
			continue
		}
		fmt.Println("- " + text)
	}
}

func printComposeValidationPlan(values []any, limit int) {
	for i, raw := range values {
		if i >= limit {
			fmt.Printf("- +%d more\n", len(values)-i)
			return
		}
		item, _ := raw.(map[string]any)
		label := firstNonEmpty(stringValue(item["command"], ""), stringValue(item["type"], ""), stringValue(item["name"], "validation"))
		if boolValue(item["required"], false) {
			label = "required " + label
		} else {
			label = "optional " + label
		}
		reason := strings.TrimSpace(stringValue(item["reason"], ""))
		if reason != "" {
			label += " - " + reason
		}
		fmt.Println("- " + label)
	}
}

func mcp(ctx context.Context, args cliArgs) error {
	action := ""
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch action {
	case "install-codex", "codex":
		return installCodexMCP(ctx, args)
	case "":
	default:
		return fmt.Errorf("unknown mcp command %q\n\n%s", action, commandUsage("mcp"))
	}
	tokenEnv := flag(args, "token-env", "ABRA_API_TOKEN")
	server := map[string]any{
		"type":                 "http",
		"url":                  strings.TrimRight(cfg(args).BaseURL, "/") + "/mcp",
		"bearer_token_env_var": tokenEnv,
	}
	if boolFlag(args, "literal-token") {
		server["headers"] = map[string]string{
			"Authorization": "Bearer " + cfg(args).Token,
		}
		delete(server, "bearer_token_env_var")
	}
	body := map[string]any{
		"mcpServers": map[string]any{
			"abra": server,
		},
	}
	return printJSON(body)
}

func scopeCommand(args cliArgs) error {
	path := "."
	if len(args.Rest) > 0 {
		path = args.Rest[0]
	}
	scope := scopeOrDefault(args, path)
	if boolFlag(args, "json") {
		return printJSON(map[string]any{
			"scope": scope,
			"path":  path,
			"examples": map[string]string{
				"bootstrap":       "abra agents bootstrap " + shellQuote(path) + " --agent codex --scope " + shellQuote(scope),
				"mcp_install":     "abra mcp install-codex",
				"agents_init":     "abra agents init " + shellQuote(path) + " --agent codex --scope " + shellQuote(scope),
				"agents_verify":   "abra agents verify " + shellQuote(path) + " --scope " + shellQuote(scope),
				"ingest":          "abra ingest " + shellQuote(path) + " --code --scope " + shellQuote(scope),
				"think":           "abra think \"what should I know before changing this project?\" --scope " + scope,
				"codex":           agentReadyPrompt(scope),
				"compose":         "abra compose \"ship this change\" --scope " + scope + " --agent codex",
				"troubleshooting": "If an AI client says Abra has no context, run the ingest example with the exact scope above, then run agents_verify and retry the agent task.",
			},
		})
	}
	fmt.Println("Scope: " + scope)
	fmt.Println("Use this exact scope with Abra MCP and AI agents.")
	fmt.Println("Bootstrap: abra agents bootstrap " + shellQuote(path) + " --agent codex --scope " + shellQuote(scope))
	fmt.Println("MCP:    abra mcp install-codex")
	fmt.Println("Agent:  abra agents init " + shellQuote(path) + " --agent codex --scope " + shellQuote(scope))
	fmt.Println("Ingest: abra ingest " + shellQuote(path) + " --code --scope " + shellQuote(scope))
	fmt.Println("Check:  abra agents verify " + shellQuote(path) + " --scope " + shellQuote(scope))
	fmt.Println("Think:  abra think \"what should I know before changing this project?\" --scope " + scope)
	fmt.Println("Codex:  " + agentReadyPrompt(scope))
	fmt.Println("Fix:    If Codex says Abra has no context, run Ingest, then Check, then retry with the Codex prompt above.")
	return nil
}

func agentsCommand(ctx context.Context, args cliArgs) error {
	action := "init"
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	if action != "init" && action != "verify" && action != "check" && action != "bootstrap" && action != "ready" {
		return fmt.Errorf("unknown agents command %q\n\n%s", action, commandUsage("agents"))
	}
	path := flag(args, "path", ".")
	if len(args.Rest) > 0 {
		path = args.Rest[0]
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	scope := scopeOrDefault(args, abs)
	if action == "verify" || action == "check" || action == "ready" {
		return verifyAgentContext(ctx, args, abs, scope)
	}
	if action == "bootstrap" {
		return bootstrapAgentContext(ctx, args, abs, scope)
	}
	agent := flag(args, "agent", "codex")
	force := boolFlag(args, "force")
	dryRun := boolFlag(args, "dry-run")
	results, err := writeAgentInstructionFiles(abs, scope, agent, force, dryRun)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(map[string]any{
			"scope": scope,
			"agent": agent,
			"path":  abs,
			"files": results,
		})
	}
	fmt.Println("Agent instructions for scope: " + scope)
	for _, result := range results {
		fmt.Println(stringValue(result["action"], "") + ": " + stringValue(result["path"], ""))
	}
	if isCodexAgent(agent) {
		fmt.Println("MCP:    abra mcp install-codex")
	} else {
		fmt.Println("MCP:    abra mcp > .tmp/abra.mcp.json")
	}
	fmt.Println("Ingest: abra ingest " + shellQuote(path) + " --code --scope " + shellQuote(scope))
	fmt.Println("Check:  abra agents verify " + shellQuote(path) + " --scope " + shellQuote(scope) + " --agent " + shellQuote(agent))
	fmt.Println("Then:   tell your AI agent to read AGENTS.md or CLAUDE.md before changing code.")
	return nil
}

func bootstrapAgentContext(ctx context.Context, args cliArgs, path, scope string) error {
	if boolFlag(args, "json") {
		return errors.New("agents bootstrap does not support --json yet")
	}
	agent := flag(args, "agent", "codex")
	force := boolFlag(args, "force")
	fmt.Println("Bootstrapping Abra agent context")
	fmt.Println("scope: " + scope)
	results, err := writeAgentInstructionFiles(path, scope, agent, force, false)
	if err != nil {
		return err
	}
	for _, result := range results {
		fmt.Println(stringValue(result["action"], "") + ": " + stringValue(result["path"], ""))
	}

	ingestArgs := copyCLIArgs(args)
	ingestArgs.Flags["path"] = path
	ingestArgs.Flags["scope"] = scope
	ingestArgs.Bools["code"] = true
	delete(ingestArgs.Bools, "json")
	ingestArgs.Rest = nil
	fmt.Println("Ingesting repo with exact scope...")
	if err := localPathIngest(ctx, ingestArgs); err != nil {
		return err
	}

	if boolFlag(args, "no-mcp") || boolFlag(args, "skip-mcp") {
		fmt.Println("MCP install skipped by flag.")
	} else if isCodexAgent(agent) {
		fmt.Println("Installing Abra MCP into Codex...")
		mcpArgs := copyCLIArgs(args)
		delete(mcpArgs.Bools, "json")
		if err := installCodexMCP(ctx, mcpArgs); err != nil {
			return err
		}
	} else {
		fmt.Println("Automatic MCP install is currently Codex-only.")
		fmt.Println("MCP config: abra mcp > .tmp/abra.mcp.json")
		fmt.Println("Then configure " + agent + " to use the generated MCP config or " + strings.TrimRight(cfg(args).BaseURL, "/") + "/mcp.")
	}

	fmt.Println("Verifying source-backed working memory...")
	verifyArgs := copyCLIArgs(args)
	delete(verifyArgs.Bools, "json")
	if err := verifyAgentContext(ctx, verifyArgs, path, scope); err != nil {
		return err
	}
	fmt.Println("Ready prompt:")
	fmt.Println(agentReadyPrompt(scope, agent))
	return nil
}

func verifyAgentContext(ctx context.Context, args cliArgs, path, scope string) error {
	filesOnly := boolFlag(args, "files-only")
	strict := boolFlag(args, "strict")
	agent := normalizedAgentFlag(args)
	checks := []map[string]any{
		agentFileCheck(filepath.Join(path, "AGENTS.md"), scope, []string{"working_memory_compose", "discover_scopes", `expected_scope: "` + scope + `"`, "current task", `agent: "` + agent + `"`}),
		optionalAgentFileCheck(filepath.Join(path, "CLAUDE.md"), "@AGENTS.md"),
	}
	if filesOnly {
		checks = append(checks, map[string]any{
			"name":   "mcp",
			"ok":     true,
			"level":  "skip",
			"detail": "skipped by --files-only",
		})
	} else if toolCount, err := validateMCPTools(ctx, args); err != nil {
		checks = append(checks, map[string]any{
			"name":  "mcp",
			"ok":    false,
			"hint":  "start Abra with `abra up`, check `abra doctor`, then retry",
			"error": err.Error(),
		})
	} else {
		checks = append(checks, map[string]any{
			"name":   "mcp",
			"ok":     true,
			"detail": fmt.Sprintf("tools=%d required=discover_scopes,working_memory_compose", toolCount),
		})
		scopeCheck := discoverScopeCheck(ctx, args, scope)
		checks = append(checks, scopeCheck)
		if boolValue(scopeCheck["ok"], false) {
			checks = append(checks, workingMemoryContextCheck(ctx, args, scope, agent))
		}
		if isCodexAgent(agent) {
			checks = append(checks, agentClientAdvisoryChecks(args)...)
		}
	}
	ok := checksOK(checks, strict)
	serverReady, clientReady, clientWarnings := agentReadinessSummary(checks, filesOnly)
	agentReady := serverReady && clientReady
	readyPrompt := agentReadyPrompt(scope, agent)
	nextSteps := agentVerifyNextSteps(path, scope, agent, ok, filesOnly)
	if ok && !filesOnly && !clientReady {
		nextSteps = append([]string{"Fix the client warning(s) above before relying on the active AI client, then fully restart that client."}, nextSteps...)
	}
	if boolFlag(args, "json") {
		if err := printJSON(map[string]any{
			"ok":              ok,
			"server_ready":    serverReady,
			"client_ready":    clientReady,
			"agent_ready":     agentReady,
			"client_warnings": clientWarnings,
			"scope":           scope,
			"path":            path,
			"files_only":      filesOnly,
			"strict":          strict,
			"checks":          checks,
			"ready_prompt":    readyPrompt,
			"next_steps":      nextSteps,
		}); err != nil {
			return err
		}
		if !ok {
			if strict && serverReady && !clientReady {
				return errors.New("agent client readiness failed under --strict; fix AI client advisory checks and rerun")
			}
			return errors.New("agent context verification failed")
		}
		return nil
	}
	fmt.Println("Agent context check for scope: " + scope)
	for _, check := range checks {
		status := "ok"
		if stringValue(check["level"], "") == "warn" {
			status = "warn"
		}
		if stringValue(check["level"], "") == "skip" {
			status = "skip"
		}
		if !boolValue(check["ok"], false) {
			status = "fail"
		}
		line := status + "  " + stringValue(check["name"], "")
		if detail := stringValue(check["detail"], ""); detail != "" {
			line += " " + detail
		}
		fmt.Println(line)
		if hint := stringValue(check["hint"], ""); hint != "" {
			fmt.Println("hint " + hint)
		}
		if errText := stringValue(check["error"], ""); errText != "" {
			fmt.Println("error " + errText)
		}
	}
	if !ok {
		printAgentNextSteps(nextSteps)
		if strict && serverReady && !clientReady {
			return errors.New("agent client readiness failed under --strict; fix AI client advisory checks and rerun")
		}
		if filesOnly {
			return errors.New("agent instruction verification failed; run `abra agents init --force` after confirming local custom instructions are backed up")
		}
		return errors.New("agent context verification failed; run `abra agents init`, `abra ingest . --code --scope " + scope + "`, and `abra doctor`")
	}
	if filesOnly {
		fmt.Println("Ready: agent instruction files are ready for scope " + scope + ".")
		fmt.Println("Prompt: " + readyPrompt)
		printAgentNextSteps(nextSteps)
		return nil
	}
	if clientReady {
		fmt.Println("Ready: server and Codex MCP config can use scope " + scope + " with working_memory_compose. Restart Codex if this changed after the active app launched.")
	} else {
		fmt.Printf("Ready: Abra server can use scope %s with working_memory_compose, but AI client readiness has %d warning(s).\n", scope, clientWarnings)
	}
	fmt.Println("Prompt: " + readyPrompt)
	printAgentNextSteps(nextSteps)
	return nil
}

func agentReadinessSummary(checks []map[string]any, filesOnly bool) (serverReady bool, clientReady bool, clientWarnings int) {
	serverReady = !filesOnly
	clientReady = !filesOnly
	for _, check := range checks {
		if boolValue(check["advisory"], false) {
			if stringValue(check["level"], "") == "warn" || boolValue(check["client_ok"], true) == false {
				clientWarnings++
				clientReady = false
			}
			continue
		}
		if !boolValue(check["ok"], false) {
			serverReady = false
		}
	}
	return serverReady, clientReady, clientWarnings
}

func agentReadyPrompt(scope string, agents ...string) string {
	agent := "codex"
	if len(agents) > 0 && strings.TrimSpace(agents[0]) != "" {
		agent = strings.ToLower(strings.TrimSpace(agents[0]))
	}
	return `Use Abra MCP first. Exact scope: ` + scope + `. Call discover_scopes with expected_scope="` + scope + `", then call working_memory_compose with task=<current task>, scope="` + scope + `", and agent="` + agent + `" before answering or changing code. If Abra MCP tools are unavailable, run abra doctor, fix the MCP/token warning, fully restart the AI client, and retry before re-ingesting. If discover_scopes does not show ` + scope + ` or working_memory_compose returns no source-backed context, run abra scope, ingest the project with that exact scope, rerun abra agents verify . --scope ` + scope + ` --agent ` + agent + `, then retry with this exact scope.`
}

func normalizedAgentFlag(args cliArgs) string {
	agent := strings.ToLower(strings.TrimSpace(flag(args, "agent", "codex")))
	if agent == "" {
		return "codex"
	}
	return agent
}

func isCodexAgent(agent string) bool {
	return strings.EqualFold(strings.TrimSpace(agent), "codex")
}

func agentVerifyNextSteps(path, scope, agent string, ok, filesOnly bool) []string {
	if ok && filesOnly {
		return []string{
			"Run `abra agents verify " + shellQuote(path) + " --scope " + shellQuote(scope) + " --agent " + shellQuote(agent) + "` against a live Abra MCP server before giving the prompt to an AI client.",
			"Give the ready_prompt to the AI client.",
		}
	}
	if ok {
		return []string{
			"Give the ready_prompt to the AI client.",
			"If the AI client still says Abra has no context, fully restart that client and rerun `abra agents verify " + shellQuote(path) + " --scope " + shellQuote(scope) + " --agent " + shellQuote(agent) + "`.",
		}
	}
	return []string{
		"Run `abra agents init " + shellQuote(path) + " --agent " + shellQuote(agent) + " --scope " + shellQuote(scope) + "` if instruction files are missing or stale.",
		"Run `abra ingest " + shellQuote(path) + " --code --scope " + shellQuote(scope) + "` if scope discovery or working memory is empty.",
		"Run `abra doctor` to check API, MCP, token, and local model readiness.",
		"Rerun `abra agents verify " + shellQuote(path) + " --scope " + shellQuote(scope) + " --agent " + shellQuote(agent) + "`.",
	}
}

func agentClientAdvisoryChecks(args cliArgs) []map[string]any {
	checks := []map[string]any{
		codexMCPClientCheck(args),
		codexLaunchEnvCheck(args),
	}
	for _, check := range checks {
		check["advisory"] = true
		if boolValue(check["ok"], false) {
			continue
		}
		check["client_ok"] = false
		check["ok"] = true
		check["level"] = "warn"
	}
	return checks
}

func printAgentNextSteps(steps []string) {
	if len(steps) == 0 {
		return
	}
	fmt.Println("Next:")
	for _, step := range steps {
		fmt.Println("- " + step)
	}
}

func optionalAgentFileCheck(path, required string) map[string]any {
	check := agentFileCheck(path, required, nil)
	if boolValue(check["ok"], false) {
		return check
	}
	check["ok"] = true
	check["level"] = "warn"
	if _, hasDetail := check["detail"]; !hasDetail {
		check["detail"] = "optional compatibility file missing"
	}
	check["hint"] = "run `abra agents init` if this repository should support tools that require " + filepath.Base(path)
	delete(check, "error")
	return check
}

func agentFileCheck(path, required string, extra []string) map[string]any {
	content, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{
			"name":  filepath.Base(path),
			"ok":    false,
			"hint":  "run `abra agents init` in the project root",
			"error": err.Error(),
		}
	}
	text := string(content)
	missing := []string{}
	for _, want := range append([]string{required}, extra...) {
		if strings.TrimSpace(want) != "" && !strings.Contains(text, want) {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		return map[string]any{
			"name":   filepath.Base(path),
			"ok":     false,
			"detail": "missing " + strings.Join(missing, ", "),
			"hint":   "run `abra agents init --force` after confirming local custom instructions are backed up",
		}
	}
	return map[string]any{"name": filepath.Base(path), "ok": true}
}

func discoverScopeCheck(ctx context.Context, args cliArgs, scope string) map[string]any {
	result, err := callMCPTool(ctx, args, "discover_scopes", map[string]any{
		"expected_scope": scope,
		"limit":          10,
	})
	if err != nil {
		return map[string]any{
			"name":  "scope_discovery",
			"ok":    false,
			"hint":  "run `abra ingest . --code --scope " + scope + "` and retry",
			"error": err.Error(),
		}
	}
	if mcpScopeResultHasScope(result, scope) {
		return map[string]any{
			"name":   "scope_discovery",
			"ok":     true,
			"detail": "discover_scopes returned " + scope,
		}
	}
	hint := "run `abra ingest . --code --scope " + scope + "` and retry with the exact scope"
	if boolValue(result["candidate_truncated"], false) {
		hint += "; discovery candidates were truncated"
	}
	return map[string]any{
		"name":   "scope_discovery",
		"ok":     false,
		"detail": "discover_scopes did not return " + scope,
		"hint":   hint,
	}
}

func workingMemoryContextCheck(ctx context.Context, args cliArgs, scope, agent string) map[string]any {
	result, err := callMCPTool(ctx, args, "working_memory_compose", map[string]any{
		"task":         "verify agent context for " + scope,
		"scope":        scope,
		"agent":        agent,
		"limit":        3,
		"max_queries":  3,
		"token_budget": 600,
		"diagnostic":   true,
	})
	if err != nil {
		return map[string]any{
			"name":  "working_memory",
			"ok":    false,
			"hint":  "run `abra ingest . --code --scope " + scope + "`, then retry `abra agents verify . --scope " + scope + "`",
			"error": err.Error(),
		}
	}
	facts, documents, summaries, graph := memoryContextCounts(result)
	if facts+documents+summaries+graph > 0 {
		return map[string]any{
			"name":   "working_memory",
			"ok":     true,
			"detail": fmt.Sprintf("facts=%d documents=%d summaries=%d graph=%d", facts, documents, summaries, graph),
		}
	}
	return map[string]any{
		"name":   "working_memory",
		"ok":     false,
		"detail": fmt.Sprintf("facts=%d documents=%d summaries=%d graph=%d", facts, documents, summaries, graph),
		"hint":   "run `abra ingest . --code --scope " + scope + "`, then retry `abra agents verify . --scope " + scope + "`",
	}
}

func memoryContextCounts(result map[string]any) (facts, documents, summaries, graph int) {
	if stats, ok := result["stats"].(map[string]any); ok {
		facts = intValue(stats["facts"])
		documents = intValue(stats["supporting_documents"])
		summaries = intValue(stats["summaries"])
		graph = intValue(stats["graph_relations"])
	}
	if facts == 0 {
		facts = lenSlice(result["facts"])
	}
	if documents == 0 {
		documents = lenSlice(result["supporting_documents"])
	}
	if summaries == 0 {
		summaries = lenSlice(result["summaries"])
	}
	if graph == 0 {
		graph = lenSlice(result["graph_context"])
	}
	return facts, documents, summaries, graph
}

func checksOK(checks []map[string]any, strict bool) bool {
	for _, check := range checks {
		if !boolValue(check["ok"], false) {
			return false
		}
		if strict && stringValue(check["level"], "") == "warn" {
			return false
		}
	}
	return true
}

type agentInstructionFile struct {
	Path    string
	Content string
}

func writeAgentInstructionFiles(abs, scope, agent string, force, dryRun bool) ([]map[string]any, error) {
	files := []agentInstructionFile{
		{
			Path:    filepath.Join(abs, "AGENTS.md"),
			Content: agentInstructions(scope, agent),
		},
		{
			Path:    filepath.Join(abs, "CLAUDE.md"),
			Content: "@AGENTS.md\n",
		},
	}
	results := make([]map[string]any, 0, len(files))
	for _, file := range files {
		exists := fileExists(file.Path)
		action := "created"
		switch {
		case dryRun && exists && !force:
			action = "would_skip"
		case dryRun:
			action = "would_write"
		case exists && !force:
			action = "skipped"
		default:
			if err := os.WriteFile(file.Path, []byte(file.Content), 0o644); err != nil {
				return nil, err
			}
			if exists {
				action = "updated"
			}
		}
		results = append(results, map[string]any{
			"path":   file.Path,
			"action": action,
		})
	}
	return results, nil
}

func agentInstructions(scope, agent string) string {
	return `# Agent Instructions

Before answering architecture questions or changing code in this repository, use Abra MCP when it is available.

1. Use exact scope ` + "`" + scope + "`" + `.
2. If discovering scopes first, call ` + "`discover_scopes`" + ` with ` + "`expected_scope: \"" + scope + "\"`" + ` so this repo is not hidden by unrelated scopes.
3. Call ` + "`working_memory_compose`" + ` with the current task, scope ` + "`" + scope + "`" + `, and ` + "`agent: \"" + agent + "\"`" + ` before implementation work.
4. Follow the returned ` + "`agent_decision`" + `, verification, memory health, conflicts, impact map, and validation plan.
5. If Abra MCP tools are unavailable, run ` + "`abra doctor`" + `, fix the MCP/token warning, fully restart the AI client, and retry before re-ingesting.
6. If the packet has no source-backed context or the exact scope is missing from discovery, run ` + "`abra scope`" + `, then ` + "`abra ingest . --code --scope " + scope + "`" + `, then ` + "`abra agents verify . --scope " + scope + " --agent " + agent + "`" + `, and retry the MCP call.
7. Do not include secrets, API keys, local tokens, or private business context in committed files.
`
}

func installCodexMCP(ctx context.Context, args cliArgs) error {
	codex, err := codexCommandPath()
	if err != nil {
		return err
	}
	tokenEnv := flag(args, "token-env", "ABRA_API_TOKEN")
	token := cfg(args).Token
	if token == "" {
		return errors.New("missing Abra token")
	}
	if err := runQuiet(codex, "mcp", "list"); err != nil {
		return fmt.Errorf("Codex CLI could not read its MCP configuration: %w\nFix the Codex config, then retry `abra mcp install-codex`", err)
	}
	toolCount, err := validateMCPTools(ctx, args)
	if err != nil {
		return fmt.Errorf("Abra MCP endpoint validation failed before changing Codex config: %w\n\nRecovery:\n  1. Start or repair Abra: abra up\n  2. Check API, MCP, token env, and model readiness: abra doctor\n  3. If local embeddings are not ready: abra models status && abra models up\n  4. Retry after the endpoint is ready: %s", err, codexInstallCommand(tokenEnv))
	}
	launchctlWarning := ""
	if runtime.GOOS == "darwin" {
		if err := runQuiet("launchctl", "setenv", tokenEnv, token); err != nil {
			launchctlWarning = err.Error()
		}
	}
	os.Setenv(tokenEnv, token)
	_ = runQuiet(codex, "mcp", "remove", "abra")
	if err := runQuiet(codex, "mcp", "add", "abra", "--url", strings.TrimRight(cfg(args).BaseURL, "/")+"/mcp", "--bearer-token-env-var", tokenEnv); err != nil {
		return fmt.Errorf("codex mcp add failed: %w", err)
	}
	fmt.Println("Installed Abra MCP for Codex future launches:")
	fmt.Println("  url:       " + strings.TrimRight(cfg(args).BaseURL, "/") + "/mcp")
	fmt.Println("  token env: " + tokenEnv)
	fmt.Printf("  endpoint:  validated (%d tools)\n", toolCount)
	if launchctlWarning != "" {
		fmt.Println("Warning: could not set macOS launch environment: " + launchctlWarning)
		fmt.Println("Set " + tokenEnv + " in the shell that starts Codex, then retry.")
	}
	if runtime.GOOS != "darwin" {
		fmt.Println("Set " + tokenEnv + " in the shell that starts Codex, then retry.")
	}
	fmt.Println("Verify runtime: abra doctor")
	fmt.Println("For each repo: cd /path/to/project && abra agents bootstrap --agent codex")
	fmt.Println("Active Codex sessions will not see this until you fully quit and reopen Codex Desktop.")
	fmt.Println("If Codex says Abra has no context after restart: cd into the repo and run `abra agents verify . --scope <scope-from-abra-scope>`; re-ingest only if verify says scope or source-backed memory is missing.")
	return nil
}

func codexInstallCommand(tokenEnv string) string {
	if strings.TrimSpace(tokenEnv) == "" || tokenEnv == "ABRA_API_TOKEN" {
		return "abra mcp install-codex"
	}
	return "abra mcp install-codex --token-env " + tokenEnv
}

func validateMCPTools(ctx context.Context, args cliArgs) (int, error) {
	result, err := postJSON(ctx, args, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]any{},
	})
	if err != nil {
		return 0, err
	}
	names := mcpToolNames(result)
	for _, required := range []string{"discover_scopes", "working_memory_compose"} {
		if !names[required] {
			return len(names), fmt.Errorf("missing required MCP tool %q", required)
		}
	}
	return len(names), nil
}

func callMCPTool(ctx context.Context, args cliArgs, name string, arguments map[string]any) (map[string]any, error) {
	result, err := postJSON(ctx, args, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": arguments,
		},
	})
	if err != nil {
		return nil, err
	}
	if rawError, ok := result["error"].(map[string]any); ok {
		return nil, errors.New(stringValue(rawError["message"], "mcp tool call failed"))
	}
	rawResult, _ := result["result"].(map[string]any)
	rawContent, _ := rawResult["content"].([]any)
	for _, item := range rawContent {
		content, _ := item.(map[string]any)
		if stringValue(content["type"], "") != "text" {
			continue
		}
		text := stringValue(content["text"], "")
		if text == "" {
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(text), &decoded); err != nil {
			return nil, fmt.Errorf("decode MCP %s response: %w", name, err)
		}
		return decoded, nil
	}
	return nil, fmt.Errorf("MCP %s response did not include text JSON content", name)
}

func mcpScopeResultHasScope(result map[string]any, scope string) bool {
	if stringValue(result["recommended_scope"], "") == scope {
		return true
	}
	for _, key := range []string{"matches", "scopes"} {
		rawItems, _ := result[key].([]any)
		for _, rawItem := range rawItems {
			item, _ := rawItem.(map[string]any)
			if stringValue(item["scope"], "") == scope {
				return true
			}
		}
	}
	return false
}

func mcpToolNames(result map[string]any) map[string]bool {
	rawResult, _ := result["result"].(map[string]any)
	rawTools, _ := rawResult["tools"].([]any)
	names := map[string]bool{}
	for _, rawTool := range rawTools {
		tool, _ := rawTool.(map[string]any)
		name := strings.TrimSpace(stringValue(tool["name"], ""))
		if name != "" {
			names[name] = true
		}
	}
	return names
}

func codexCommandPath() (string, error) {
	if override := strings.TrimSpace(os.Getenv("ABRA_CODEX_COMMAND")); override != "" {
		return override, nil
	}
	macPath := "/Applications/Codex.app/Contents/Resources/codex"
	if runtime.GOOS == "darwin" && fileExists(macPath) {
		return macPath, nil
	}
	if path, err := exec.LookPath("codex"); err == nil {
		return path, nil
	}
	return "", errors.New("missing Codex CLI; install Codex or add `codex` to PATH")
}

func runQuiet(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func postJSON(ctx context.Context, args cliArgs, path string, body map[string]any) (map[string]any, error) {
	return postJSONWithTimeout(ctx, args, path, body, cliTimeout(args, defaultHTTPTimeout))
}

func postJSONWithTimeout(ctx context.Context, args cliArgs, path string, body map[string]any, timeout time.Duration) (map[string]any, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg(args).BaseURL, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+cfg(args).Token)
	return doJSON(req, timeout)
}

func getJSON(ctx context.Context, args cliArgs, path string) (map[string]any, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg(args).BaseURL, "/")+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("authorization", "Bearer "+cfg(args).Token)
	body, err := doJSON(req, cliTimeout(args, defaultHTTPTimeout))
	if err != nil {
		if statusErr := (&httpStatusError{}); errors.As(err, &statusErr) {
			return body, statusErr.Code, err
		}
		return body, 0, err
	}
	return body, 200, nil
}

type httpStatusError struct {
	Code    int
	Body    string
	Payload map[string]any
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("http %d: %s", e.Code, e.Body)
}

func doJSON(req *http.Request, timeout time.Duration) (map[string]any, error) {
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, maxCLIResponseBody+1))
	if readErr != nil {
		return nil, readErr
	}
	if len(raw) > maxCLIResponseBody {
		raw = raw[:maxCLIResponseBody]
		return nil, fmt.Errorf("response body exceeded %d bytes", maxCLIResponseBody)
	}
	var out map[string]any
	if len(strings.TrimSpace(string(raw))) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = map[string]any{}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, &httpStatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(raw)), Payload: out}
	}
	return out, nil
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
		lines = append(lines, "Check: abra models status")
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

func printThink(result map[string]any) {
	fmt.Println()
	fmt.Println("Abra think")
	fmt.Println(stringValue(result["answer"], "No answer."))
	fmt.Println()
	fmt.Println("scope: " + stringValue(result["scope"], ""))
	if verification, _ := result["verification"].(map[string]any); verification != nil {
		fmt.Println("verification: " + stringValue(verification["verdict"], "unknown"))
	}
	if decision, _ := result["agent_decision"].(map[string]any); decision != nil {
		fmt.Println("decision: " + stringValue(decision["decision"], "unknown"))
	}
	if citations, _ := result["citations"].([]any); len(citations) > 0 {
		fmt.Println("citations:")
		for _, raw := range citations {
			item, _ := raw.(map[string]any)
			fmt.Printf("- %s: %s\n", stringValue(item["ref"], "?"), stringValue(item["source_url"], "unknown"))
		}
	}
}

func printReady(args cliArgs) {
	fmt.Println()
	fmt.Println("Abra is ready")
	fmt.Println("MCP:       " + strings.TrimRight(cfg(args).BaseURL, "/") + "/mcp")
	fmt.Println("Token env: ABRA_API_TOKEN (configured; value not printed)")
	fmt.Println("Next:      cd /path/to/project && abra agents bootstrap --agent codex")
	fmt.Println("Restart:   fully quit and reopen Codex Desktop after bootstrap")
	fmt.Println(`Then:      abra think "What should I know before changing this project?" --scope <scope>`)
	fmt.Println("Manual:    abra mcp install-codex && abra agents init --agent codex && abra ingest . --code --scope <scope> && abra agents verify . --scope <scope>")
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

func ensureEnv(args cliArgs) error {
	if fileExists(envPath(args)) {
		return nil
	}
	return initEnv(args)
}

func envPath(args cliArgs) string {
	path := flag(args, "env-file", flag(args, "env", defaultEnvPath()))
	return filepath.Clean(path)
}

func defaultEnvPath() string {
	if isAbraSourceCheckout(".") {
		return checkoutEnvPath
	}
	return filepath.Join(userConfigDir(), "quickstart.env")
}

func isAbraSourceCheckout(dir string) bool {
	return hasLocalCompose(dir) &&
		fileExists(filepath.Join(dir, "go.mod")) &&
		fileExists(filepath.Join(dir, "cmd", "abra", "main.go")) &&
		fileExists(filepath.Join(dir, "migrations", "001_init.sql"))
}

func hasLocalCompose(dir string) bool {
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if fileExists(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

func userConfigDir() string {
	if value := strings.TrimSpace(os.Getenv("ABRA_HOME")); value != "" {
		return value
	}
	if configDir, err := os.UserConfigDir(); err == nil && configDir != "" {
		return filepath.Join(configDir, "abra")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".abra")
	}
	return ".abra"
}

func projectDir(args cliArgs) (string, error) {
	if explicit := flag(args, "project-dir", ""); explicit != "" {
		return filepath.Abs(explicit)
	}
	if isAbraSourceCheckout(".") {
		return filepath.Abs(".")
	}
	return managedRuntimeDir(), nil
}

func ensureProjectDir(ctx context.Context, args cliArgs) (string, error) {
	dir, err := projectDir(args)
	if err != nil {
		return "", err
	}
	if hasLocalCompose(dir) {
		if err := prepareRuntimeProjectDir(dir); err != nil {
			return "", err
		}
		return dir, nil
	}
	if err := downloadRuntimeSource(ctx, dir); err != nil {
		return "", err
	}
	if !hasLocalCompose(dir) {
		return "", fmt.Errorf("runtime bundle did not include docker-compose.yml: %s", dir)
	}
	if err := prepareRuntimeProjectDir(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func managedRuntimeDir() string {
	return filepath.Join(userConfigDir(), "runtime", runtimeVersion(), "source")
}

func isManagedRuntimeDir(dir string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absRuntime, err := filepath.Abs(managedRuntimeDir())
	if err != nil {
		return false
	}
	return filepath.Clean(absDir) == filepath.Clean(absRuntime)
}

func prepareRuntimeProjectDir(dir string) error {
	if !isManagedRuntimeDir(dir) {
		return nil
	}
	err := os.Remove(filepath.Join(dir, "docker-compose.dev.yml"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func runtimeVersion() string {
	if strings.TrimSpace(version) != "" && version != "dev" {
		return strings.TrimSpace(version)
	}
	return "main"
}

func runtimeSourceURL() string {
	if value := strings.TrimSpace(os.Getenv("ABRA_SOURCE_URL")); value != "" {
		return value
	}
	if runtimeVersion() == "main" {
		return "https://github.com/hermawan22/abra/archive/refs/heads/main.tar.gz"
	}
	return "https://github.com/hermawan22/abra/archive/refs/tags/" + runtimeVersion() + ".tar.gz"
}

func downloadRuntimeSource(ctx context.Context, targetDir string) error {
	url := runtimeSourceURL()
	fmt.Println("Downloading Abra runtime: " + url)
	tmpDir := targetDir + ".tmp-" + timestamp()
	if err := os.RemoveAll(tmpDir); err != nil {
		return err
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_ = os.RemoveAll(tmpDir)
		return fmt.Errorf("download runtime source: http %d", resp.StatusCode)
	}
	if err := extractTarGz(resp.Body, tmpDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return err
	}
	if err := os.RemoveAll(targetDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return err
	}
	if err := os.MkdirAll(filepath.Dir(targetDir), 0o755); err != nil {
		_ = os.RemoveAll(tmpDir)
		return err
	}
	if err := os.Rename(tmpDir, targetDir); err != nil {
		_ = os.RemoveAll(tmpDir)
		return err
	}
	return nil
}

func extractTarGz(reader io.Reader, targetDir string) error {
	gz, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(filepath.ToSlash(header.Name), "/")
		parts := strings.SplitN(name, "/", 2)
		if len(parts) != 2 || parts[1] == "" {
			continue
		}
		rel := filepath.Clean(filepath.FromSlash(parts[1]))
		if rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
			return fmt.Errorf("unsafe archive path: %s", header.Name)
		}
		dst := filepath.Join(targetDir, rel)
		if !strings.HasPrefix(dst, filepath.Clean(targetDir)+string(os.PathSeparator)) && filepath.Clean(dst) != filepath.Clean(targetDir) {
			return fmt.Errorf("unsafe archive destination: %s", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, tr)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
}

func cfg(args cliArgs) contextConfig {
	envFile := envPath(args)
	fileValues := map[string]string{}
	if fileExists(envFile) {
		if values, err := readEnvValues(envFile); err == nil {
			fileValues = values
		}
	}
	fileBaseURL := firstNonEmpty(fileValues["ABRA_BASE_URL"], fileValues["ABRA_URL"])
	if fileBaseURL == "" {
		fileBaseURL = baseURLFromPort(fileValues["ABRA_PORT"])
	}
	envBaseURL := firstNonEmpty(os.Getenv("ABRA_BASE_URL"), os.Getenv("ABRA_URL"))
	if envBaseURL == "" {
		envBaseURL = baseURLFromPort(os.Getenv("ABRA_PORT"))
	}
	return contextConfig{
		EnvFile: envFile,
		BaseURL: flag(args, "base-url", firstNonEmpty(envBaseURL, fileBaseURL, defaultBaseURL)),
		Token:   flag(args, "token", firstNonEmpty(os.Getenv("ABRA_API_TOKEN"), firstCSV(os.Getenv("ABRA_API_KEYS"), ""), fileValues["ABRA_API_TOKEN"], firstCSV(fileValues["ABRA_API_KEYS"], ""), defaultToken)),
	}
}

func baseURLFromPort(port string) string {
	port = strings.TrimSpace(port)
	if port == "" {
		return ""
	}
	return "http://127.0.0.1:" + port
}

func flag(args cliArgs, name, fallback string) string {
	if value := strings.TrimSpace(args.Flags[name]); value != "" {
		return value
	}
	return fallback
}

func defaultConnectorKind(sourceType string) string {
	if strings.TrimSpace(sourceType) == "mcp" {
		return "mcp"
	}
	return "generic"
}

func parseJSONObjectFlag(raw, flagName string) (map[string]any, error) {
	var value map[string]any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, fmt.Errorf("--%s must be a JSON object: %w", flagName, err)
	}
	if value == nil {
		value = map[string]any{}
	}
	return value, nil
}

func scopeOrDefault(args cliArgs, pathHint string) string {
	if value := flag(args, "scope", ""); value != "" {
		return value
	}
	if value := strings.TrimSpace(os.Getenv("ABRA_SCOPE")); value != "" {
		return value
	}
	return defaultScope(pathHint)
}

func defaultScope(pathHint string) string {
	root := "."
	if strings.TrimSpace(pathHint) != "" {
		root = pathHint
	}
	if scope := scopeFromRepositoryURL(root); scope != "" {
		return scope
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if info, err := os.Stat(abs); err == nil && !info.IsDir() {
		abs = filepath.Dir(abs)
	}
	if gitRoot := findGitRoot(abs); gitRoot != "" {
		abs = gitRoot
	}
	name := slug(filepath.Base(abs))
	if name == "" {
		name = "local"
	}
	return "repo:" + name
}

func scopeFromRepositoryURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "git@") {
		if idx := strings.Index(raw, ":"); idx >= 0 && idx+1 < len(raw) {
			return scopeFromRepoPath(raw[idx+1:])
		}
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return scopeFromRepoPath(parsed.Path)
}

func scopeFromRepoPath(path string) string {
	parts := strings.Split(strings.Trim(strings.TrimSuffix(path, ".git"), "/"), "/")
	if len(parts) >= 2 {
		name := slug(parts[len(parts)-2] + "-" + parts[len(parts)-1])
		if name != "" {
			return "repo:" + name
		}
	}
	if len(parts) == 1 {
		if name := slug(parts[0]); name != "" {
			return "repo:" + name
		}
	}
	return ""
}

func findGitRoot(start string) string {
	dir := start
	for {
		if info, err := os.Stat(filepath.Join(dir, ".git")); err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

func boolFlag(args cliArgs, name string) bool {
	if args.Bools[name] {
		return true
	}
	value := strings.ToLower(strings.TrimSpace(args.Flags[name]))
	return value == "1" || value == "true" || value == "yes"
}

func intFlag(args cliArgs, name string, fallback int) int {
	value := flag(args, name, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func floatFlag(args cliArgs, name string, fallback float64) float64 {
	value := flag(args, name, "")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed < 0 {
		return fallback
	}
	return parsed
}

func cliTimeout(args cliArgs, fallback time.Duration) time.Duration {
	value := firstNonEmpty(flag(args, "timeout", ""), os.Getenv("ABRA_CLI_TIMEOUT"))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err == nil && parsed > 0 {
		return parsed
	}
	seconds, err := strconv.Atoi(value)
	if err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return fallback
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func firstCSV(value, fallback string) string {
	for _, part := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			return trimmed
		}
	}
	return fallback
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func csv(value string) []string {
	parts := []string{}
	for _, item := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

func urlQueryEscape(value string) string {
	return url.QueryEscape(value)
}

func stringMapToAny(input map[string]string) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func localFileURL(root, relPath string) string {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Join(root, filepath.FromSlash(relPath)))}
	return u.String()
}

func waitForSourceJob(ctx context.Context, args cliArgs, scope, sourceID, jobID string) (map[string]any, error) {
	timeout := waitTimeout(args)
	deadline := time.Now().Add(timeout)
	quiet := boolFlag(args, "json")
	for {
		limit := "1"
		if jobID != "" {
			limit = "20"
		}
		path := "/ingestion/jobs?scope=" + urlQueryEscape(scope) + "&source_config_id=" + urlQueryEscape(sourceID) + "&limit=" + limit
		result, _, err := getJSON(ctx, args, path)
		if err == nil {
			jobs, _ := result["ingestion_jobs"].([]any)
			if job := matchingIngestionJob(jobs, jobID); job != nil {
				status := stringValue(job["status"], "")
				switch status {
				case "succeeded":
					if !quiet {
						fmt.Printf("Job succeeded: %s seen=%v changed=%v chunks=%v claims=%v\n", stringValue(job["id"], jobID), job["documents_seen"], job["documents_changed"], job["chunks_written"], job["claims_written"])
					}
					return job, nil
				case "failed", "canceled":
					return job, fmt.Errorf("job %s %s: %s", stringValue(job["id"], jobID), status, stringValue(job["last_error"], ""))
				}
				if !quiet {
					fmt.Println("Job " + stringValue(job["id"], jobID) + " " + status + "...")
				}
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	return nil, errors.New("job did not finish within " + timeout.String() + "; run `abra jobs --scope " + scope + "`")
}

func matchingIngestionJob(jobs []any, jobID string) map[string]any {
	for _, raw := range jobs {
		job, _ := raw.(map[string]any)
		if job == nil {
			continue
		}
		if jobID == "" || stringValue(job["id"], "") == jobID {
			return job
		}
	}
	return nil
}

func waitTimeout(args cliArgs) time.Duration {
	value := firstNonEmpty(flag(args, "wait-timeout", ""), flag(args, "timeout", ""), os.Getenv("ABRA_CLI_WAIT_TIMEOUT"))
	if value == "" {
		return time.Minute
	}
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return time.Minute
	}
	return timeout
}

func stringValue(value any, fallback string) string {
	switch typed := value.(type) {
	case string:
		if typed != "" {
			return typed
		}
	case fmt.Stringer:
		return typed.String()
	}
	return fallback
}

func boolValue(value any, fallback bool) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes":
			return true
		case "0", "false", "no":
			return false
		}
	}
	return fallback
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	}
	return 0
}

func lenSlice(value any) int {
	if items, ok := value.([]any); ok {
		return len(items)
	}
	return 0
}

func printJSON(value any) error {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func timestamp() string {
	return time.Now().UTC().Format("20060102150405")
}

func slug(value string) string {
	value = strings.ToLower(value)
	var out strings.Builder
	lastDash := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			out.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			out.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(out.String(), "-")
}

func usage() string {
	return `Abra CLI

Usage:
  abra --version
  abra version
  abra setup
  abra up
  abra upgrade [--version vX.Y.Z]
  abra uninstall --yes
  abra demo
  abra quickstart
  abra init [--production]
  abra config show
  abra scope
  abra models up
  abra models status
  abra models logs
  abra config model local
  abra config model openai --api-key-stdin
  abra config model compatible --base-url <url> --model <model> [--api-key-stdin]
  abra agents bootstrap
  abra agents init
  abra agents verify
  abra down [--reset] [--keep-models]
  abra status
  abra doctor
  abra seed [--scope repo:demo]
  abra ingest . [--code] [--continue-on-error] [--quiet]
  abra ingest ./notes.md
  abra ingest --scope repo:demo --text "Agents should use Abra" [--title Intro]
  abra ingest --git https://github.com/owner/repo.git [--ref main] [--scope repo:demo]
  abra watch local --scope repo:demo --path . [--freshness-seconds 3600] [--schedule "@every 1h"] [--wait]
  abra watch git --scope repo:demo --git https://github.com/owner/repo.git [--ref main] [--freshness-seconds 3600] [--wait]
  abra source mcp --scope team:platform --mcp-url https://mcp.example/mcp --tool export_documents [--schedule "@every 10m"] [--wait]
  abra sources [--scope repo:demo]
  abra jobs [--scope repo:demo]
  abra observe "Agents should rerun release checks before tagging" [--scope repo:demo] [--propose]
  abra observations [--scope repo:demo] [--query release]
  abra observations propose <observation-id> [--claim "..."] [--source-url file://runbook.md]
  abra think "What should agents use?"
  abra recall "agent memory"
  abra compose "ship a change"
  abra mcp
  abra mcp install-codex

Common flags:
  --base-url http://127.0.0.1:18080
  --env-file <path>
  --token demo-only-dev-token
  --json

First run:
  abra setup
  abra agents bootstrap --agent codex
  abra think "What should I know before changing this project?" --scope <scope-from-abra-scope>

Abra is CLI + MCP only. No browser UI is shipped.
`
}

func commandUsage(command string) string {
	switch command {
	case "ingest":
		return `Usage:
  abra ingest . [--code] [--continue-on-error] [--quiet]
  abra ingest ./notes.md
  abra ingest --text "source-backed content" [--title Intro]
  abra ingest --git https://github.com/owner/repo.git [--ref main] [--wait]

Manual document flags:
  --scope          memory scope, default repo:<current-git-root-or-folder>
  --text           document text
  --file           read document text from file
  --title          document title
  --source-url     stable source URL
  --source-type    default markdown

Source ingestion flags:
  --path           local repository or directory to ingest from the CLI
  --git, --repo    remote Git repository URL to clone through the worker
  --ref, --branch  Git ref for --git
  --include        comma-separated document globs, default **/*.md
  --exclude        comma-separated exclude globs
  --code           also ingest code intelligence from supported code files
  --code-include   comma-separated code globs for --code
  --code-exclude   comma-separated code exclude globs for --code
  --max-file-bytes skip matched files larger than this before reading, default 1048576
  --include-generated
                  include generated/minified/lock files that are skipped by default
  --wait           wait for the queued worker job when using --git or watch
  --tracked        register a local path source and queue a worker job; path must be worker-visible
  --no-wait        return immediately after queueing a tracked local path ingestion job
  --wait-timeout   max wait for queued worker jobs, default 1m
  --freshness-seconds
                  mark the source due when the last successful refresh is older than this many seconds
  --schedule       worker refresh cadence: @hourly, @daily, or @every <N><s|m|h|d>
  --direct         force direct local ingestion through /ingest/documents
  --continue-on-error
                  keep direct local ingestion running after per-file failures; exits nonzero if any fail
  --quiet         suppress direct local per-file progress in human output
  --timeout        HTTP timeout for direct local/file/text ingest, default 10m
`
	case "config":
		return `Usage:
  abra config show [--json]
  abra config path
  abra config model local [--base-url http://host.docker.internal:8080/v1] [--runner-image image@sha256:...] [--pull-policy missing] [--readiness-timeout 10s]
  abra config model qwen3
  abra config model openai --api-key-stdin
  abra config model compatible --base-url <url> --model <model> --dimensions <size> [--api-key-stdin]

Config edits the Abra runtime env file used by abra up. It intentionally only
exposes core runtime settings needed for local operation and embedding/reranker connection.
After changing model config, restart with: abra down && abra up
After changing embedding providers, re-ingest important sources for reliable vector recall.
`
	case "models", "model":
		return `Usage:
  abra models up [--recreate] [--port 8080] [--pull-policy missing] [--model-id Qwen/Qwen3-Embedding-0.6B-GGUF] [--model Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0]
  abra models status [--json]
  abra models logs
  abra models down

Starts and manages the built-in local embedding runner for the default local
Qwen3 setup. Abra keeps the binary lightweight: model weights stay in Docker's
model cache, while the CLI owns startup, health checks, and lifecycle.

Operational flags:
  --model-id       Hugging Face GGUF repository, default Qwen/Qwen3-Embedding-0.6B-GGUF
  --model          served model name, default Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0
  --dimensions     embedding dimensions, default 1024
  --image          llama.cpp server image; use a digest-pinned image in production
  --pull-policy    Docker image pull policy: missing, always, or never
  --readiness-timeout timeout for one readiness request, default 10s
  --cache-dir      host model cache directory
  --container      Docker container name
  --base-url       local OpenAI-compatible base URL
  --port           host port for the embedding server, default 8080
  --publish-addr   host address to publish on, default 127.0.0.1
`
	case "ui", "dashboard":
		return `Usage:
  abra setup

The previous interactive UI command was removed. Use abra setup for guided
onboarding, or abra up for non-interactive stack startup.
`
	case "watch", "source":
		return `Usage:
  abra watch local --scope repo:demo --path . [--include "**/*.md"] [--code] [--freshness-seconds 3600] [--schedule "@every 1h"] [--wait]
  abra watch git --scope repo:demo --git https://github.com/owner/repo.git [--ref main] [--freshness-seconds 3600] [--wait]
  abra source mcp --scope team:platform --mcp-url https://mcp.example/mcp --tool export_documents [--arguments-json '{"space":"ENG"}'] [--document-source-type confluence] [--schedule "@every 10m"] [--wait]

This creates or updates a source config, then enqueues an ingestion job.
The OSS worker supports markdown, local_repo, git_repo, and MCP HTTP sources
whose configured tool returns normalized Abra documents. External systems such
as Jira, Confluence, Slack, and Drive can either expose an MCP document-export
tool or push normalized documents through the HTTP/MCP ingestion API.
Use --freshness-seconds for max source age and --schedule for @hourly, @daily,
or @every <N><s|m|h|d> worker refresh cadence. Manual sync still bypasses the
due check.
Use --wait-timeout or ABRA_CLI_WAIT_TIMEOUT for slow local model or large repo runs.
`
	case "sources":
		return `Usage:
  abra sources [--scope repo:demo] [--limit 50] [--json]
  abra sources sync <source-config-id> [--scope repo:demo] [--wait] [--wait-timeout 10m] [--json]

Lists configured ingestion sources or queues a manual sync for an existing source.
`
	case "jobs":
		return `Usage:
  abra jobs --scope repo:demo [--source-config-id source...] [--limit 20] [--json]

Lists worker ingestion jobs for a scope.
`
	case "observe":
		return `Usage:
  abra observe "Agents should rerun release checks before tagging" [--scope repo:demo]
  abra observe --text "..." --type episode --source-url file://notes.md --confidence 0.7 [--json]
  abra observe "..." --propose --scope repo:demo --source-url file://runbook.md

Captures a raw observation. Observations are scoped, searchable, audited, and
not trusted claims until a review/promote flow explicitly turns them into one.
Use --propose to immediately create a pending learning proposal from the
captured observation without writing trusted memory.
`
	case "observations", "episodes":
		return `Usage:
  abra observations --scope repo:demo [--query release] [--type episode] [--status raw] [--limit 20] [--json]
  abra observations propose <observation-id> --scope repo:demo [--claim "..."] [--source-url file://runbook.md] [--json]
  abra episodes --scope repo:demo

Lists raw episodic observations for a scope.
The propose subcommand creates a pending learning proposal targeting the
observation. Accepted proposals still return an apply plan; they do not auto-write claims.
`
	case "think":
		return `Usage:
  abra think "question" --scope repo:demo [--agent codex] [--json]

Asks the governed brain layer. Returns a cited answer, verification, gaps,
memory health, and an agent decision gate.
`
	case "recall":
		return `Usage:
  abra recall "query" --scope repo:demo [--include-unverified] [--json]

Runs hybrid lexical/vector retrieval over source-backed memory.
`
	case "compose":
		return `Usage:
  abra compose "task" --scope repo:demo [--agent codex] [--hook before_task] [--prompt] [--persist-learning] [--json]

Builds a task-specific working-memory packet for AI coding agents. Human output
includes the decision gate, retrieval quality, health signals, validation plan,
allowed next actions, and suggested steps. Use --prompt to also print the
prompt-ready context window for another AI client. By default compose is
read-only; --persist-learning writes actionable learning suggestions as pending
review proposals and requires write access.
`
	case "scope":
		return `Usage:
  abra scope [path] [--json]

Prints the stable memory scope for a project path and shows the exact commands
and agent prompt to use. Use this when an AI client says Abra has no context:
the usual cause is a scope mismatch between ingest and working_memory_compose.
Compare this output with discover_scopes in the MCP client; if the exact scope
is missing, ingest the project with the printed command and retry.
`
	case "agents", "agent":
		return `Usage:
  abra agents bootstrap [path] [--agent codex] [--scope repo:project] [--force] [--no-mcp]
  abra agents init [path] [--agent codex] [--scope repo:project] [--force] [--dry-run] [--json]
  abra agents verify [path] --scope repo:project [--agent codex] [--files-only] [--strict] [--json]
  abra agents ready [path] --scope repo:project [--agent codex] [--files-only] [--strict] [--json]

Writes repo-local AI agent instruction files that point every client at the
same Abra scope. It creates AGENTS.md for agent-neutral instructions and
CLAUDE.md importing AGENTS.md so Claude Code reads the same guidance without
duplicating content. Existing files are skipped unless --force is set.

` + "`abra agents bootstrap`" + ` is the one-command Codex-ready path: it writes
agent instructions, ingests the repo with the exact scope and --code, installs
the Abra MCP endpoint into Codex, and verifies source-backed working memory
unless --no-mcp is set.

` + "`abra agents verify`" + ` checks AGENTS.md, CLAUDE.md, the MCP endpoint, required
agent tools, discover_scopes for the exact project scope, and a lightweight
working_memory_compose packet with source-backed context. Use it when an AI
client says Abra has no context. Use --files-only for CI checks that should not
contact a live Abra MCP server. Use --strict when warning-level compatibility
checks should fail the command. ` + "`abra agents ready`" + ` is a non-mutating alias for
verify.
`
	case "mcp", "mcp-config":
		return `Usage:
  abra mcp [--token-env ABRA_API_TOKEN] [--literal-token]
  abra mcp install-codex [--token-env ABRA_API_TOKEN]

` + "`abra mcp`" + ` prints generic remote HTTP MCP client JSON. By default it
uses bearer_token_env_var instead of writing a literal token; use --literal-token
only for legacy clients that cannot read bearer-token env vars.
` + "`abra mcp install-codex`" + ` installs Abra into Codex as a streamable HTTP MCP
server using the Codex CLI, stores the bearer-token env var name, validates the
Abra MCP endpoint, and sets the token for the current macOS launch environment
when available. Fully quit and reopen Codex Desktop after installing or changing
the token env. Run abra doctor when Codex cannot see Abra; it checks API/MCP
readiness, model config/readiness, and the current shell token env.
`
	case "doctor":
		return `Usage:
  abra doctor [--json] [--strict] [--token-env ABRA_API_TOKEN]

Checks local commands, runtime env permissions, embedding model config, local
embedding readiness, API readiness, MCP tools, and Codex token-env hints. Use it
after abra setup, after changing model config, and before rerunning
abra mcp install-codex when Codex cannot connect. Use --strict for CI or release
preflight checks that should exit non-zero when any check is not ok.
`
	case "setup":
		return `Usage:
  abra setup
  abra setup --yes
  abra setup --yes --no-models
  abra setup --local
  abra setup --openai --api-key-stdin
  abra setup --compatible --embedding-base-url <url> --embedding-model <model> --dimensions <size> [--api-key-stdin]
  abra setup --provider compatible --embedding-base-url <url> --embedding-model <model> --dimensions <size>
  abra setup --yes --no-start

Guided first-run onboarding. It checks prerequisites, creates the runtime env,
chooses the embedding provider, and can start the local stack. The default
local provider uses the built-in Qwen/Qwen3-Embedding-0.6B runner, which
abra up starts automatically and abra models up/status manages directly.

If setup writes config but later commands cannot embed, run abra doctor first.
For the default local provider, abra up starts the model runner automatically;
use abra models status and abra models up when you want to inspect or repair it
directly.

Common setup flags:
  --embedding-base-url  embedding provider base URL
  --base-url            legacy alias for --embedding-base-url during setup
  --embedding-model     embedding model name
  --model               provider selector or legacy embedding model alias
  --dimensions          embedding dimensions; required for unknown compatible models, inferred for known OpenAI/Qwen/BGE/Nomic/Gemini models
  --embedding-timeout   provider timeout, default 10m for local and 30s for compatible
  --provider-concurrency provider call concurrency, default 1 for local and 4 for compatible
  --api-key             embedding provider API key
  --api-key-stdin       read embedding provider API key from stdin
  --no-models           do not start the local embedding runner
  --skip-models         alias for --no-models
  --no-start            write config but do not start the Abra stack
  --skip-up             alias for --no-start
`
	case "install", "up", "quickstart", "demo":
		return `Usage:
  abra setup
  abra up [--no-models]
  abra demo
  abra install

abra setup is the guided first-run path. abra up starts the default local Qwen
embedding runner when the env uses EMBEDDING_PROVIDER=local, then starts the
local Docker Compose stack non-interactively: Postgres, migrations, API, and
worker. Use --no-models when you intentionally manage the embedding endpoint
yourself. abra install is kept as a compatibility alias for abra setup; the curl
installer is what installs the CLI binary.
`
	case "upgrade", "update":
		return `Usage:
  abra upgrade
  abra upgrade --version vX.Y.Z

Re-runs the public install script into the current binary directory. Set
ABRA_INSTALL_SCRIPT to override the installer URL or ABRA_INSTALL_DIR when
running the install script directly.
`
	case "uninstall":
		return `Usage:
  abra uninstall --yes

Removes the Abra CLI binary only. It does not remove Docker containers,
volumes, env files, runtime bundles, or memory data.
`
	default:
		return usage()
	}
}

func demoEnv() string {
	composeFile := "docker-compose.yml"
	abraImage := defaultRuntimeImageRef()
	if isAbraSourceCheckout(".") {
		composeFile = "docker-compose.yml:docker-compose.dev.yml"
		abraImage = "abra:local"
	}
	return strings.NewReplacer(
		"{{COMPOSE_FILE}}", composeFile,
		"{{ABRA_IMAGE}}", abraImage,
	).Replace(demoEnvTemplate)
}

func defaultRuntimeImageRef() string {
	version := runtimeVersion()
	if version == "main" {
		return "ghcr.io/hermawan22/abra:main"
	}
	return "ghcr.io/hermawan22/abra:" + version
}

const demoEnvTemplate = `COMPOSE_FILE={{COMPOSE_FILE}}
ABRA_API_KEYS=demo-only-dev-token
ABRA_API_TOKEN=demo-only-dev-token
NODE_ENV=development
ABRA_APPROVAL_MODE=advisory
ABRA_PORT=18080
ABRA_IMAGE={{ABRA_IMAGE}}
POSTGRES_IMAGE=pgvector/pgvector:pg16
POSTGRES_USER=abra
POSTGRES_PASSWORD=dev-only-postgres-password
POSTGRES_DB=abra
POSTGRES_PORT=5433
ABRA_DATABASE_URL=postgres://abra:dev-only-postgres-password@postgres:5432/abra
EMBEDDING_PROVIDER=local
EMBEDDING_BASE_URL=http://host.docker.internal:8080/v1
EMBEDDING_MODEL=Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0
EMBEDDING_DIMENSIONS=1024
EMBEDDING_TIMEOUT=10m
ABRA_AI_PROVIDER_CONCURRENCY=1
RERANKER_PROVIDER=
RERANKER_BASE_URL=
RERANKER_MODEL=
ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false
ABRA_LOCAL_EMBEDDING_IMAGE=
ABRA_LOCAL_EMBEDDING_PULL_POLICY=missing
ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT=10s
REDACT_PII=true
RATE_LIMIT_MAX=1000
RATE_LIMIT_WINDOW=1 minute
ABRA_API_READ_TIMEOUT=10m
ABRA_MAX_REQUEST_BODY_BYTES=26214400
WORKER_INTERVAL=30s
WORKER_MAX_SOURCES_PER_RUN=25
WORKER_CONCURRENCY=1
ABRA_DEPLOYMENT_ENVIRONMENT=development
`

const productionEnvExample = `NODE_ENV=production
ABRA_API_KEYS=replace-with-generated-token
ABRA_WEBHOOK_SECRETS=replace-with-webhook-signing-secret
ABRA_APPROVAL_MODE=enforce
# Replace these placeholder digests with the release image digest and an operator-verified pgvector digest.
ABRA_IMAGE=ghcr.io/hermawan22/abra@sha256:0000000000000000000000000000000000000000000000000000000000000000
POSTGRES_IMAGE=pgvector/pgvector@sha256:0000000000000000000000000000000000000000000000000000000000000000
POSTGRES_USER=abra
POSTGRES_PASSWORD=replace-with-generated-database-password
POSTGRES_DB=abra
ABRA_DATABASE_URL=postgres://abra:replace-with-generated-database-password@postgres:5432/abra
EMBEDDING_PROVIDER=compatible
EMBEDDING_BASE_URL=https://embedding-provider.example/v1
EMBEDDING_API_KEY=replace-with-embedding-key
EMBEDDING_MODEL=embedding-model
EMBEDDING_DIMENSIONS=1024
EMBEDDING_TIMEOUT=30s
ABRA_AI_PROVIDER_CONCURRENCY=4
RERANKER_PROVIDER=
RERANKER_BASE_URL=
RERANKER_API_KEY=
RERANKER_MODEL=
ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false
ABRA_LOCAL_EMBEDDING_IMAGE=
ABRA_LOCAL_EMBEDDING_PULL_POLICY=missing
ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT=10s
REDACT_PII=true
RATE_LIMIT_MAX=120
RATE_LIMIT_WINDOW=1 minute
ABRA_API_READ_TIMEOUT=2m
ABRA_MAX_REQUEST_BODY_BYTES=26214400
WORKER_INTERVAL=30s
WORKER_MAX_SOURCES_PER_RUN=25
WORKER_CONCURRENCY=1
ABRA_PORT=18080
`
