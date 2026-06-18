package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultEnvPath = ".tmp/quickstart.env"
	defaultBaseURL = "http://127.0.0.1:18080"
	defaultToken   = "dev-token"
	installScript  = "https://raw.githubusercontent.com/hermawan22/abra/main/scripts/install.sh"
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
	switch args.Command {
	case "", "help", "-h", "--help":
		fmt.Print(usage())
		return nil
	case "version":
		return printVersion(args)
	case "install", "setup":
		return install(ctx, args)
	case "upgrade", "update":
		return upgrade(args)
	case "uninstall":
		return uninstall(args)
	case "demo", "quickstart":
		return demo(ctx, args)
	case "init":
		return initEnv(args)
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

func install(ctx context.Context, args cliArgs) error {
	if boolFlag(args, "production") {
		args.Bools["production"] = true
		if err := initEnv(args); err != nil {
			return err
		}
		fmt.Println("Production env created. Edit credentials, then run: abra up --env-file " + envPath(args))
		return nil
	}
	if err := up(ctx, args); err != nil {
		return err
	}
	fmt.Println("Install complete.")
	return nil
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

func up(ctx context.Context, args cliArgs) error {
	if err := ensureEnv(args); err != nil {
		return err
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return errors.New("missing required command: docker")
	}
	env := envPath(args)
	fmt.Println("Using env: " + env)
	steps := [][]string{
		{"compose", "--env-file", env, "build", "api", "worker", "migrate"},
		{"compose", "--env-file", env, "up", "-d", "postgres"},
		{"compose", "--env-file", env, "run", "--rm", "migrate"},
		{"compose", "--env-file", env, "up", "-d", "api", "worker"},
	}
	for _, step := range steps {
		if err := runCommand("docker", step...); err != nil {
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
	if err := ensureEnv(args); err != nil {
		return err
	}
	step := []string{"compose", "--env-file", envPath(args), "down"}
	if boolFlag(args, "reset") {
		step = append(step, "--volumes")
	}
	return runCommand("docker", step...)
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
	scope := required(args, "scope")
	content := flag(args, "text", "")
	if file := flag(args, "file", ""); file != "" {
		bytes, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		content = string(bytes)
	}
	if content == "" {
		content = strings.TrimSpace(strings.Join(args.Rest, " "))
	}
	if content == "" {
		return errors.New("ingest requires --text, --file, or positional content")
	}
	body := map[string]any{
		"source_type": flag(args, "source-type", "markdown"),
		"source_url":  flag(args, "source-url", "cli://"+slug(flag(args, "title", "note"))+"-"+timestamp()),
		"title":       flag(args, "title", "CLI Note"),
		"scope":       scope,
		"content":     content,
		"authority":   flag(args, "authority", "official-doc"),
	}
	result, err := postJSON(ctx, args, "/ingest/documents", body)
	if err != nil {
		return err
	}
	if boolFlag(args, "json") {
		return printJSON(result)
	}
	fmt.Println("Ingested: " + stringValue(result["document_id"], stringValue(body["source_url"], "")))
	fmt.Println("scope: " + scope)
	return nil
}

func ingest(ctx context.Context, args cliArgs, body map[string]any) error {
	_, err := postJSON(ctx, args, "/ingest/documents", body)
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
	fmt.Printf("Compose: %s / %s\n", stringValue(verification["verdict"], "unknown"), stringValue(decision["decision"], "unknown"))
	return nil
}

func mcp(args cliArgs) error {
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

func postJSON(ctx context.Context, args cliArgs, path string, body map[string]any) (map[string]any, error) {
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
	return doJSON(req)
}

func getJSON(ctx context.Context, args cliArgs, path string) (map[string]any, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg(args).BaseURL, "/")+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("authorization", "Bearer "+cfg(args).Token)
	body, err := doJSON(req)
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

func doJSON(req *http.Request) (map[string]any, error) {
	client := &http.Client{Timeout: 30 * time.Second}
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
	fmt.Println(`Next:      abra ingest --scope repo:demo --title Intro --source-url file://intro.md --text "Agents should use Abra before autonomous code changes."`)
	fmt.Println(`Then:      abra think "What should agents use before autonomous code changes?" --scope repo:demo`)
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func ensureEnv(args cliArgs) error {
	if fileExists(envPath(args)) {
		return nil
	}
	return initEnv(args)
}

func envPath(args cliArgs) string {
	path := flag(args, "env-file", flag(args, "env", defaultEnvPath))
	return filepath.Clean(path)
}

func cfg(args cliArgs) contextConfig {
	return contextConfig{
		EnvFile: envPath(args),
		BaseURL: flag(args, "base-url", envOr("ABRA_BASE_URL", envOr("ABRA_URL", defaultBaseURL))),
		Token:   flag(args, "token", envOr("ABRA_API_TOKEN", firstCSV(os.Getenv("ABRA_API_KEYS"), defaultToken))),
	}
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
		value = os.Getenv("ABRA_SCOPE")
	}
	if value == "" {
		panic(fmt.Sprintf("missing --%s", name))
	}
	return value
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
  abra install
  abra upgrade [--version v0.1.0]
  abra uninstall --yes
  abra demo
  abra quickstart
  abra init [--production]
  abra up [--env-file .tmp/quickstart.env]
  abra down [--reset]
  abra status
  abra doctor
  abra seed [--scope repo:demo]
  abra ingest --scope repo:demo --text "Agents should use Abra" [--title Intro]
  abra think "What should agents use?" --scope repo:demo
  abra recall "agent memory" --scope repo:demo
  abra compose "ship a change" --scope repo:demo
  abra mcp

Common flags:
  --base-url http://127.0.0.1:18080
  --env-file .tmp/quickstart.env
  --token dev-token
  --json

Abra is CLI + MCP only. No browser UI is shipped.
`
}

const demoEnv = `ABRA_API_KEYS=dev-token
ABRA_API_TOKEN=dev-token
ABRA_APPROVAL_MODE=advisory
ABRA_PORT=18080
POSTGRES_PORT=5433
EMBEDDING_PROVIDER=local
EMBEDDING_MODEL=embedding-model-1536
EMBEDDING_DIMENSIONS=1536
ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=true
REDACT_PII=true
RATE_LIMIT_MAX=1000
RATE_LIMIT_WINDOW=1 minute
WORKER_INTERVAL=1s
ABRA_DEPLOYMENT_ENVIRONMENT=development
`

const productionEnvExample = `ABRA_API_KEYS=replace-with-generated-token
ABRA_APPROVAL_MODE=enforce
EMBEDDING_PROVIDER=compatible
EMBEDDING_BASE_URL=https://embedding-provider.example/v1
EMBEDDING_API_KEY=replace-with-embedding-key
EMBEDDING_MODEL=embedding-model-1536
EMBEDDING_DIMENSIONS=1536
ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false
REDACT_PII=true
RATE_LIMIT_MAX=120
RATE_LIMIT_WINDOW=1 minute
ABRA_PORT=18080
`
