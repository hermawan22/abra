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
)

const (
	checkoutEnvPath      = ".tmp/quickstart.env"
	defaultBaseURL       = "http://127.0.0.1:18080"
	defaultToken         = "dev-token"
	defaultHTTPTimeout   = 30 * time.Second
	defaultIngestTimeout = 10 * time.Minute
	installScript        = "https://raw.githubusercontent.com/hermawan22/abra/main/scripts/install.sh"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
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
	case "version":
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
	case "think", "ask":
		return think(ctx, args)
	case "recall":
		return recall(ctx, args)
	case "compose":
		return composeMemory(ctx, args)
	case "mcp", "mcp-config":
		return mcp(args)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args.Command, usage())
	}
}

func printVersion(args cliArgs) error {
	info := map[string]any{
		"version": version,
		"commit":  commit,
		"date":    date,
		"goos":    runtime.GOOS,
		"goarch":  runtime.GOARCH,
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

func upgrade(args cliArgs) error {
	if _, err := exec.LookPath("curl"); err != nil {
		return errors.New("missing required command: curl")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		return errors.New("missing required command: sh")
	}
	script := envOr("ABRA_INSTALL_SCRIPT", installScript)
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	env := os.Environ()
	env = append(env, "ABRA_INSTALL_DIR="+filepath.Dir(exe))
	if target := flag(args, "version", ""); target != "" {
		env = append(env, "ABRA_VERSION="+target)
	}
	cmd := exec.Command("sh", "-c", "curl -fsSL \"$ABRA_INSTALL_SCRIPT\" | sh")
	cmd.Env = append(env, "ABRA_INSTALL_SCRIPT="+script)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
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
	content := demoEnv
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
		"reranker_provider":    values["RERANKER_PROVIDER"],
		"reranker_base_url":    values["RERANKER_BASE_URL"],
		"reranker_api_key":     maskSecret(values["RERANKER_API_KEY"]),
		"reranker_model":       values["RERANKER_MODEL"],
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
	fmt.Println("api_key:   " + stringValue(view["embedding_api_key"], ""))
	if rerankerProvider := stringValue(view["reranker_provider"], ""); rerankerProvider != "" {
		fmt.Println("reranker:  " + rerankerProvider)
		fmt.Println("rerank_url: " + stringValue(view["reranker_base_url"], ""))
		fmt.Println("rerank_model: " + stringValue(view["reranker_model"], ""))
		fmt.Println("rerank_key:   " + stringValue(view["reranker_api_key"], ""))
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
		if flag(args, "dimensions", "") == "" {
			args.Flags["dimensions"] = "1536"
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
		"EMBEDDING_BASE_URL":                   flag(args, "base-url", defaultEmbeddingBaseURL),
		"EMBEDDING_API_KEY":                    apiKey,
		"EMBEDDING_MODEL":                      flag(args, "model", defaultServedModelName),
		"EMBEDDING_DIMENSIONS":                 flag(args, "dimensions", "1024"),
		"EMBEDDING_TIMEOUT":                    flag(args, "embedding-timeout", "10m"),
		"RERANKER_PROVIDER":                    flag(args, "reranker-provider", ""),
		"RERANKER_BASE_URL":                    flag(args, "reranker-base-url", ""),
		"RERANKER_API_KEY":                     apiKey,
		"RERANKER_MODEL":                       flag(args, "reranker-model", ""),
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "false",
	}); err != nil {
		return err
	}
	fmt.Println("Model config updated: " + label)
	fmt.Println("Run `abra models up` for the default local Qwen embedding runner, or point this config at any compatible custom provider.")
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
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                   "compatible",
		"EMBEDDING_BASE_URL":                   baseURL,
		"EMBEDDING_API_KEY":                    apiKey,
		"EMBEDDING_MODEL":                      model,
		"EMBEDDING_DIMENSIONS":                 flag(args, "dimensions", "1536"),
		"EMBEDDING_TIMEOUT":                    flag(args, "embedding-timeout", "30s"),
		"RERANKER_PROVIDER":                    "",
		"RERANKER_BASE_URL":                    "",
		"RERANKER_API_KEY":                     "",
		"RERANKER_MODEL":                       "",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "false",
	}); err != nil {
		return err
	}
	fmt.Println("Model config updated: " + label)
	printRestartHint(args)
	return nil
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
		"RERANKER_PROVIDER",
		"RERANKER_BASE_URL",
		"RERANKER_API_KEY",
		"RERANKER_MODEL",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION",
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
	if !hasLocalCompose(".") {
		fmt.Println("Using runtime: " + projectDir)
	}
	steps := [][]string{
		{"compose", "--project-name", "abra", "--env-file", env, "build", "api", "worker", "migrate"},
		{"compose", "--project-name", "abra", "--env-file", env, "up", "-d", "postgres"},
		{"compose", "--project-name", "abra", "--env-file", env, "run", "--rm", "migrate"},
		{"compose", "--project-name", "abra", "--env-file", env, "up", "-d", "api", "worker"},
	}
	for _, step := range steps {
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
	step := []string{"compose", "--project-name", "abra", "--env-file", env, "down"}
	if boolFlag(args, "reset") {
		step = append(step, "--volumes")
	}
	return runCommandIn(projectDir, "docker", step...)
}

func status(ctx context.Context, args cliArgs) error {
	result, code, err := getJSON(ctx, args, "/readyz")
	if err != nil || code < 200 || code >= 300 {
		fmt.Printf("Abra: not ready (%d)\n", code)
		fmt.Println("Run: abra up")
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
	}
	checks = append(checks, localEmbeddingCheck(ctx, args))
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

func mcpCheck(ctx context.Context, args cliArgs) map[string]any {
	result, err := postJSON(ctx, args, "/mcp", map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/list",
		"params":  map[string]any{},
	})
	if err != nil {
		return map[string]any{"name": "mcp", "ok": false, "error": err.Error()}
	}
	rawResult, _ := result["result"].(map[string]any)
	tools, _ := rawResult["tools"].([]any)
	return map[string]any{"name": "mcp", "ok": len(tools) > 0, "tools": len(tools)}
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
		return printJSON(map[string]any{"ok": ok, "checks": checks})
	}
	for _, check := range checks {
		status := "ok"
		if value, _ := check["ok"].(bool); !value {
			status = "warn"
		}
		fmt.Println(status + "  " + stringValue(check["name"], "check"))
		if hint := stringValue(check["hint"], ""); hint != "" {
			fmt.Println("hint " + hint)
		}
		if errText := stringValue(check["error"], ""); errText != "" {
			fmt.Println("err  " + errText)
		}
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
		return localPathIngest(ctx, args)
	}
	if flag(args, "git", "") != "" || flag(args, "repo", "") != "" {
		return sourceIngest(ctx, args)
	}
	scope := required(args, "scope")
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
	documents, err := ingestor.Ingest(ctx)
	if err != nil {
		return err
	}
	if len(documents) == 0 {
		return errors.New("no matching files found; adjust --include, add --code, or check --path")
	}
	results := make([]map[string]any, 0, len(documents))
	skippedEmpty := 0
	for _, doc := range documents {
		if strings.TrimSpace(doc.Content) == "" {
			skippedEmpty++
			continue
		}
		metadata := stringMapToAny(doc.Metadata)
		metadata["ingest_path"] = doc.Path
		metadata["ingest_checksum"] = doc.Checksum
		metadata["ingest_fingerprint"] = doc.Fingerprint
		sourceURL := localFileURL(abs, doc.Path)
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
			return fmt.Errorf("ingest %s: %w", doc.Path, friendlyProviderError(err))
		}
		results = append(results, map[string]any{
			"path":        doc.Path,
			"source_url":  sourceURL,
			"document_id": stringValue(result["document_id"], ""),
			"chunks":      result["chunks"],
			"claims":      result["claims"],
			"relations":   result["relations"],
		})
	}
	if len(results) == 0 {
		return fmt.Errorf("no non-empty matching files found; skipped %d empty file(s)", skippedEmpty)
	}
	if boolFlag(args, "json") {
		return printJSON(map[string]any{"scope": scope, "documents": results, "skipped_empty": skippedEmpty})
	}
	fmt.Printf("Ingested files: %d\n", len(results))
	if skippedEmpty > 0 {
		fmt.Printf("Skipped empty files: %d\n", skippedEmpty)
	}
	fmt.Println("scope: " + scope)
	fmt.Println("source: " + source.ID)
	return nil
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
	default:
		return fmt.Errorf("unknown watch mode %q\n\n%s", mode, commandUsage("watch"))
	}
	return sourceIngest(ctx, args)
}

func sourceIngest(ctx context.Context, args cliArgs) error {
	scope := scopeOrDefault(args, ".")
	sourceType := "local_repo"
	sourceURL := ""
	config := map[string]any{}
	if repo := firstNonEmpty(flag(args, "git", ""), flag(args, "repo", "")); repo != "" {
		sourceType = "git_repo"
		sourceURL = repo
		config["repository_url"] = repo
		if ref := firstNonEmpty(flag(args, "ref", ""), flag(args, "branch", "")); ref != "" {
			config["git_ref"] = ref
		}
		config["git_depth"] = intFlag(args, "depth", 1)
	} else {
		path := flag(args, "path", ".")
		abs, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		sourceURL = "file://" + filepath.ToSlash(abs)
		config["root"] = abs
	}
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
		"connector_kind":  flag(args, "connector", "generic"),
		"status":          flag(args, "status", "active"),
		"authority":       flag(args, "authority", "manual-unverified"),
		"authority_score": floatFlag(args, "authority-score", 0.35),
		"config":          config,
		"metadata": map[string]any{
			"created_by": "abra-cli",
		},
		"created_by": flag(args, "created-by", "abra-cli"),
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
		return waitForSourceJob(ctx, args, scope, sourceID)
	}
	return nil
}

func listSources(ctx context.Context, args cliArgs) error {
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

func think(ctx context.Context, args cliArgs) error {
	question := strings.TrimSpace(strings.Join(args.Rest, " "))
	if question == "" {
		question = required(args, "question")
	}
	scope := required(args, "scope")
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
		query = required(args, "query")
	}
	result, err := postJSON(ctx, args, "/recall", map[string]any{
		"query":              query,
		"scope":              required(args, "scope"),
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
		task = required(args, "task")
	}
	result, err := postJSON(ctx, args, "/memory/compose", map[string]any{
		"task":               task,
		"scope":              required(args, "scope"),
		"hook":               flag(args, "hook", "before_task"),
		"agent":              flag(args, "agent", ""),
		"limit":              intFlag(args, "limit", 5),
		"max_queries":        intFlag(args, "max-queries", 4),
		"token_budget":       intFlag(args, "token-budget", 1200),
		"include_unverified": boolFlag(args, "include-unverified"),
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
	scope := stringValue(result["scope"], required(args, "scope"))
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
	}
	if actions, ok := decision["required_actions"].([]any); ok && len(actions) > 0 {
		fmt.Println("required actions:")
		for _, action := range actions {
			fmt.Println("- " + stringValue(action, ""))
		}
	}
	if len(stats) > 0 && intValue(stats["facts"])+intValue(stats["supporting_documents"])+intValue(stats["summaries"]) == 0 {
		fmt.Println("No source-backed context found for this scope.")
		fmt.Println("Run: abra scope")
		fmt.Println("Run: abra ingest . --code --scope " + scope)
	}
	return nil
}

func mcp(args cliArgs) error {
	action := ""
	if len(args.Rest) > 0 {
		action = strings.ToLower(strings.TrimSpace(args.Rest[0]))
		args.Rest = args.Rest[1:]
	}
	switch action {
	case "install-codex", "codex":
		return installCodexMCP(args)
	case "":
	default:
		return fmt.Errorf("unknown mcp command %q\n\n%s", action, commandUsage("mcp"))
	}
	body := map[string]any{
		"mcpServers": map[string]any{
			"abra": map[string]any{
				"type": "http",
				"url":  strings.TrimRight(cfg(args).BaseURL, "/") + "/mcp",
				"headers": map[string]string{
					"Authorization": "Bearer " + cfg(args).Token,
				},
			},
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
				"ingest":  "abra ingest " + shellQuote(path) + " --code --scope " + shellQuote(scope),
				"think":   "abra think \"what should I know before changing this project?\" --scope " + scope,
				"codex":   "Use Abra MCP first. Scope: " + scope + ". Call working_memory_compose before answering or changing code.",
				"compose": "abra compose \"ship this change\" --scope " + scope + " --agent codex",
			},
		})
	}
	fmt.Println("Scope: " + scope)
	fmt.Println("Use this exact scope with Abra MCP and AI agents.")
	fmt.Println("Ingest: abra ingest " + shellQuote(path) + " --code --scope " + shellQuote(scope))
	fmt.Println("Think:  abra think \"what should I know before changing this project?\" --scope " + scope)
	fmt.Println("Codex:  Use Abra MCP first. Scope: " + scope + ". Call working_memory_compose before answering or changing code.")
	return nil
}

func installCodexMCP(args cliArgs) error {
	codex, err := codexCommandPath()
	if err != nil {
		return err
	}
	tokenEnv := flag(args, "token-env", "ABRA_API_TOKEN")
	token := cfg(args).Token
	if token == "" {
		return errors.New("missing Abra token")
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
	fmt.Println("Installed Abra MCP for Codex:")
	fmt.Println("  url:       " + strings.TrimRight(cfg(args).BaseURL, "/") + "/mcp")
	fmt.Println("  token env: " + tokenEnv)
	if launchctlWarning != "" {
		fmt.Println("Warning: could not set macOS launch environment: " + launchctlWarning)
		fmt.Println("Set this before starting Codex: export " + tokenEnv + "=" + shellQuote(token))
	}
	if runtime.GOOS != "darwin" {
		fmt.Println("Set this before starting Codex: export " + tokenEnv + "=" + shellQuote(token))
	}
	fmt.Println("Fully quit and reopen Codex Desktop after installing or changing the token env.")
	fmt.Println("Opening a new thread is enough only when the env var was already available to the Codex process.")
	fmt.Println("Scope hint: run `abra scope` in each project and pass that scope to working_memory_compose.")
	return nil
}

func codexCommandPath() (string, error) {
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
	Code int
	Body string
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
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if len(strings.TrimSpace(string(raw))) > 0 {
		_ = json.Unmarshal(raw, &out)
	}
	if out == nil {
		out = map[string]any{}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return out, &httpStatusError{Code: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
	}
	return out, nil
}

func waitReady(ctx context.Context, args cliArgs) error {
	for i := 0; i < 60; i++ {
		_, code, err := getJSON(ctx, args, "/readyz")
		if err == nil && code >= 200 && code < 300 {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("Abra did not become ready")
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
	fmt.Println("Token:     " + cfg(args).Token)
	fmt.Println("Codex:     abra mcp install-codex")
	fmt.Println("Scope:     cd /path/to/project && abra scope")
	fmt.Println("Next:      cd /path/to/project && abra ingest . --code --scope <scope>")
	fmt.Println(`Then:      abra think "What should I know before changing this project?" --scope <scope>`)
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
	if hasLocalCompose(".") {
		return checkoutEnvPath
	}
	return filepath.Join(userConfigDir(), "quickstart.env")
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
	if hasLocalCompose(".") {
		return filepath.Abs(".")
	}
	return filepath.Join(userConfigDir(), "runtime", runtimeVersion(), "source"), nil
}

func ensureProjectDir(ctx context.Context, args cliArgs) (string, error) {
	dir, err := projectDir(args)
	if err != nil {
		return "", err
	}
	if hasLocalCompose(dir) {
		return dir, nil
	}
	if err := downloadRuntimeSource(ctx, dir); err != nil {
		return "", err
	}
	if !hasLocalCompose(dir) {
		return "", fmt.Errorf("runtime bundle did not include docker-compose.yml: %s", dir)
	}
	return dir, nil
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

func required(args cliArgs, name string) string {
	value := flag(args, name, "")
	if value == "" && name == "scope" {
		value = scopeOrDefault(args, ".")
	}
	if value == "" {
		panic(fmt.Sprintf("missing --%s", name))
	}
	return value
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

func waitForSourceJob(ctx context.Context, args cliArgs, scope, sourceID string) error {
	for i := 0; i < 60; i++ {
		path := "/ingestion/jobs?scope=" + urlQueryEscape(scope) + "&source_config_id=" + urlQueryEscape(sourceID) + "&limit=1"
		result, _, err := getJSON(ctx, args, path)
		if err == nil {
			jobs, _ := result["ingestion_jobs"].([]any)
			if len(jobs) > 0 {
				job, _ := jobs[0].(map[string]any)
				status := stringValue(job["status"], "")
				switch status {
				case "succeeded":
					fmt.Printf("Job succeeded: seen=%v changed=%v chunks=%v claims=%v\n", job["documents_seen"], job["documents_changed"], job["chunks_written"], job["claims_written"])
					return nil
				case "failed", "canceled":
					return fmt.Errorf("job %s: %s", status, stringValue(job["last_error"], ""))
				}
				fmt.Println("Job " + status + "...")
			}
		}
		time.Sleep(time.Second)
	}
	return errors.New("job did not finish within 60s; run `abra jobs --scope " + scope + "`")
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
  abra config model local
  abra config model openai --api-key-stdin
  abra config model compatible --base-url <url> --model <model> [--api-key-stdin]
  abra down [--reset]
  abra status
  abra doctor
  abra seed [--scope repo:demo]
  abra ingest . [--code]
  abra ingest ./notes.md
  abra ingest --scope repo:demo --text "Agents should use Abra" [--title Intro]
  abra ingest --git https://github.com/owner/repo.git [--ref main] [--scope repo:demo]
  abra watch local --scope repo:demo --path . [--wait]
  abra watch git --scope repo:demo --git https://github.com/owner/repo.git [--wait]
  abra sources [--scope repo:demo]
  abra jobs [--scope repo:demo]
  abra think "What should agents use?"
  abra recall "agent memory"
  abra compose "ship a change"
  abra mcp
  abra mcp install-codex

Common flags:
  --base-url http://127.0.0.1:18080
  --env-file <path>
  --token dev-token
  --json

Abra is CLI + MCP only. No browser UI is shipped.
`
}

func commandUsage(command string) string {
	switch command {
	case "ingest":
		return `Usage:
  abra ingest . [--code]
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
  --path           local repository or directory to ingest immediately from the CLI
  --git, --repo    remote Git repository URL to clone through the worker
  --ref, --branch  Git ref for --git
  --include        comma-separated document globs, default **/*.md
  --exclude        comma-separated exclude globs
  --code           also ingest code intelligence from supported code files
  --wait           wait up to 60s for the queued worker job when using --git
  --timeout        HTTP timeout for direct local ingest, default 10m
`
	case "config":
		return `Usage:
  abra config show [--json]
  abra config path
  abra config model local [--base-url http://host.docker.internal:8080/v1] [--reranker-base-url http://host.docker.internal:8081]
  abra config model openai --api-key-stdin
  abra config model compatible --base-url <url> --model <model> [--api-key-stdin] [--dimensions 1536]

Config edits the Abra runtime env file used by abra up. It intentionally only
exposes core runtime settings needed for local operation and embedding/reranker connection.
After changing model config, restart with: abra down && abra up
After changing embedding providers, re-ingest important sources for reliable vector recall.
`
	case "models", "model":
		return `Usage:
  abra models up [--recreate] [--port 8080] [--model-id Qwen/Qwen3-Embedding-0.6B-GGUF] [--model Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0]
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
  --image          llama.cpp server image
  --cache-dir      host model cache directory
  --container      Docker container name
  --base-url       local OpenAI-compatible base URL
  --port           host port for the embedding server, default 8080
`
	case "ui", "dashboard":
		return `Usage:
  abra setup

The previous interactive UI command was removed. Use abra setup for guided
onboarding, or abra up for non-interactive stack startup.
`
	case "watch", "source":
		return `Usage:
  abra watch local --scope repo:demo --path . [--include "**/*.md"] [--code] [--wait]
  abra watch git --scope repo:demo --git https://github.com/owner/repo.git [--ref main] [--wait]

This creates or updates a source config, then enqueues an ingestion job.
The OSS worker supports markdown, local_repo, and git_repo. External systems
such as Jira, Confluence, Slack, and Drive should push normalized documents
through the HTTP/MCP ingestion API or a deployment-specific connector overlay.
`
	case "sources":
		return `Usage:
  abra sources [--scope repo:demo] [--limit 50] [--json]

Lists configured ingestion sources.
`
	case "jobs":
		return `Usage:
  abra jobs --scope repo:demo [--source-config-id source...] [--limit 20] [--json]

Lists worker ingestion jobs for a scope.
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
  abra compose "task" --scope repo:demo [--agent codex] [--hook before_task] [--json]

Builds a task-specific working-memory packet for AI coding agents.
`
	case "scope":
		return `Usage:
  abra scope [path] [--json]

Prints the stable memory scope for a project path and shows the exact commands
and agent prompt to use. Use this when an AI client says Abra has no context:
the usual cause is a scope mismatch between ingest and working_memory_compose.
`
	case "mcp", "mcp-config":
		return `Usage:
  abra mcp
  abra mcp install-codex [--token-env ABRA_API_TOKEN]

` + "`abra mcp`" + ` prints generic remote HTTP MCP client JSON.
` + "`abra mcp install-codex`" + ` installs Abra into Codex as a streamable HTTP MCP
server using the Codex CLI, stores the bearer-token env var name, and sets the
token for the current macOS launch environment when available. Fully quit and
reopen Codex Desktop after installing or changing the token env.
`
	case "setup":
		return `Usage:
  abra setup
  abra setup --local
  abra setup --openai --api-key-stdin
  abra setup --compatible --base-url <url> --embedding-model <model> [--api-key-stdin]
  abra setup --provider compatible --base-url <url> --embedding-model <model>
  abra setup --yes --no-start

Guided first-run onboarding. It checks prerequisites, creates the runtime env,
chooses the embedding provider, and can start the local stack. The default
local provider uses abra models up to serve Qwen/Qwen3-Embedding-0.6B through a
local OpenAI-compatible endpoint.

Common setup flags:
  --base-url            embedding provider base URL
  --embedding-model     embedding model name
  --model               provider selector or legacy embedding model alias
  --dimensions          embedding dimensions
  --embedding-timeout   provider timeout, default 10m for local and 30s for compatible
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
  abra up
  abra demo
  abra install

abra setup is the guided first-run path. abra up starts the local Docker Compose
stack non-interactively: Postgres, migrations, API, and worker. abra install is
kept as a compatibility alias for abra setup; the curl installer is what installs
the CLI binary.
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

const demoEnv = `ABRA_API_KEYS=dev-token
ABRA_API_TOKEN=dev-token
NODE_ENV=development
ABRA_APPROVAL_MODE=advisory
ABRA_PORT=18080
POSTGRES_PORT=5433
EMBEDDING_PROVIDER=local
EMBEDDING_BASE_URL=http://host.docker.internal:8080/v1
EMBEDDING_MODEL=Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0
EMBEDDING_DIMENSIONS=1024
EMBEDDING_TIMEOUT=10m
RERANKER_PROVIDER=
RERANKER_BASE_URL=
RERANKER_MODEL=
ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false
REDACT_PII=true
RATE_LIMIT_MAX=1000
RATE_LIMIT_WINDOW=1 minute
WORKER_INTERVAL=1s
ABRA_DEPLOYMENT_ENVIRONMENT=development
`

const productionEnvExample = `NODE_ENV=production
ABRA_API_KEYS=replace-with-generated-token
ABRA_APPROVAL_MODE=enforce
EMBEDDING_PROVIDER=compatible
EMBEDDING_BASE_URL=https://embedding-provider.example/v1
EMBEDDING_API_KEY=replace-with-embedding-key
EMBEDDING_MODEL=embedding-model
EMBEDDING_DIMENSIONS=1024
EMBEDDING_TIMEOUT=30s
RERANKER_PROVIDER=
RERANKER_BASE_URL=
RERANKER_API_KEY=
RERANKER_MODEL=
ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false
REDACT_PII=true
RATE_LIMIT_MAX=120
RATE_LIMIT_WINDOW=1 minute
ABRA_PORT=18080
`
