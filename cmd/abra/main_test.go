package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCommandHelpDoesNotRequireFlags(t *testing.T) {
	for _, command := range []string{"config", "ingest", "setup", "models", "watch", "sources", "jobs", "scope", "mcp"} {
		t.Run(command, func(t *testing.T) {
			if err := run(context.Background(), []string{command, "--help"}); err != nil {
				t.Fatalf("run(%s --help) error = %v", command, err)
			}
		})
	}
}

func TestScopeCommandPrintsAgentGuidance(t *testing.T) {
	root := filepath.Join(t.TempDir(), "project with spaces")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "README.md"), "# Demo\n")

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"scope", root}); err != nil {
			t.Fatalf("scope error = %v", err)
		}
	})
	wantScope := "repo:" + slug(filepath.Base(root))
	for _, want := range []string{wantScope, "working_memory_compose", "abra ingest " + shellQuote(root) + " --code --scope " + shellQuote(wantScope)} {
		if !strings.Contains(output, want) {
			t.Fatalf("scope output missing %q:\n%s", want, output)
		}
	}
}

func TestScopeCommandJSON(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README.md"), "# Demo\n")

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"scope", root, "--json"}); err != nil {
			t.Fatalf("scope json error = %v", err)
		}
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("decode scope json: %v\n%s", err, output)
	}
	wantScope := "repo:" + slug(filepath.Base(root))
	if payload["scope"] != wantScope {
		t.Fatalf("scope = %v, want %s", payload["scope"], wantScope)
	}
	examples, _ := payload["examples"].(map[string]any)
	if !strings.Contains(stringValue(examples["codex"], ""), "working_memory_compose") {
		t.Fatalf("codex example = %#v", examples["codex"])
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	if got := shellQuote("dev'token"); got != "'dev'\"'\"'token'" {
		t.Fatalf("shellQuote = %q", got)
	}
}

func TestCfgReadsRuntimeEnvFile(t *testing.T) {
	t.Setenv("ABRA_BASE_URL", "")
	t.Setenv("ABRA_URL", "")
	t.Setenv("ABRA_PORT", "")
	t.Setenv("ABRA_API_TOKEN", "")
	t.Setenv("ABRA_API_KEYS", "")
	envFile := filepath.Join(t.TempDir(), "quickstart.env")
	mustWrite(t, envFile, "ABRA_PORT=19999\nABRA_API_KEYS=file-token,other\n")

	got := cfg(parseArgs([]string{"status", "--env-file", envFile}))
	if got.BaseURL != "http://127.0.0.1:19999" {
		t.Fatalf("BaseURL = %q", got.BaseURL)
	}
	if got.Token != "file-token" {
		t.Fatalf("Token = %q", got.Token)
	}

	override := cfg(parseArgs([]string{"status", "--env-file", envFile, "--base-url", "http://127.0.0.1:18888", "--token", "flag-token"}))
	if override.BaseURL != "http://127.0.0.1:18888" || override.Token != "flag-token" {
		t.Fatalf("flag override = %+v", override)
	}
}

func TestSetupYesNoStartDefaultsLocalQwen(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"setup", "--yes", "--no-start"}); err != nil {
		t.Fatalf("setup error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_PROVIDER"] != "local" {
		t.Fatalf("provider = %q", values["EMBEDDING_PROVIDER"])
	}
	if values["EMBEDDING_BASE_URL"] != "http://host.docker.internal:8080/v1" {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
	}
	if values["EMBEDDING_MODEL"] != defaultServedModelName {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["EMBEDDING_DIMENSIONS"] != "1024" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
	}
	if values["EMBEDDING_TIMEOUT"] != "10m" {
		t.Fatalf("timeout = %q", values["EMBEDDING_TIMEOUT"])
	}
	if values["RERANKER_PROVIDER"] != "" {
		t.Fatalf("reranker provider = %q", values["RERANKER_PROVIDER"])
	}
	if values["RERANKER_BASE_URL"] != "" {
		t.Fatalf("reranker base url = %q", values["RERANKER_BASE_URL"])
	}
}

func TestSetupOpenAIStdinNoStart(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	stdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = stdin
		_ = reader.Close()
	})
	_, _ = writer.WriteString("openai-test-key\n")
	_ = writer.Close()

	if err := run(context.Background(), []string{"setup", "--openai", "--api-key-stdin", "--no-start"}); err != nil {
		t.Fatalf("setup openai error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_BASE_URL"] != "https://api.openai.com/v1" {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
	}
	if values["EMBEDDING_MODEL"] != "text-embedding-3-small" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["EMBEDDING_API_KEY"] != "openai-test-key" {
		t.Fatalf("api key = %q", values["EMBEDDING_API_KEY"])
	}
}

func TestSetupRejectsConflictingProviders(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{"setup", "--local", "--openai", "--no-start"})
	if err == nil {
		t.Fatal("expected conflicting provider error")
	}
	if !strings.Contains(err.Error(), "choose one embedding provider only") {
		t.Fatalf("error = %v", err)
	}
}

func TestSetupOpenAIModelAliasIsEmbeddingModel(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	stdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = stdin
		_ = reader.Close()
	})
	_, _ = writer.WriteString("openai-test-key\n")
	_ = writer.Close()

	if err := run(context.Background(), []string{"setup", "--openai", "--model", "custom-embedding", "--api-key-stdin", "--no-start"}); err != nil {
		t.Fatalf("setup openai error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_MODEL"] != "custom-embedding" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
}

func TestUICommandRemoved(t *testing.T) {
	for _, command := range []string{"ui", "dashboard"} {
		t.Run(command, func(t *testing.T) {
			err := run(context.Background(), []string{command})
			if err == nil {
				t.Fatal("expected removed command error")
			}
			if !strings.Contains(err.Error(), "was removed") {
				t.Fatalf("error = %v", err)
			}
		})
	}
	help := commandUsage("ui")
	if strings.Contains(help, "cockpit") {
		t.Fatalf("ui help still describes cockpit: %s", help)
	}
	if !strings.Contains(help, "removed") {
		t.Fatalf("ui help should explain removal: %s", help)
	}
}

func TestConfigModelCompatibleUpdatesEnv(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{
		"config",
		"model",
		"compatible",
		"--base-url", "https://models.example/v1",
		"--api-key", "secret-model-key",
		"--model", "embed-1536",
	})
	if err != nil {
		t.Fatalf("config model compatible error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_PROVIDER"] != "compatible" {
		t.Fatalf("provider = %q", values["EMBEDDING_PROVIDER"])
	}
	if values["EMBEDDING_BASE_URL"] != "https://models.example/v1" {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
	}
	if values["EMBEDDING_API_KEY"] != "secret-model-key" {
		t.Fatalf("api key = %q", values["EMBEDDING_API_KEY"])
	}
	if values["EMBEDDING_MODEL"] != "embed-1536" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"] != "false" {
		t.Fatalf("local production guard = %q", values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"])
	}
}

func TestConfigModelCompatibleAllowsNoAPIKey(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{
		"config",
		"model",
		"compatible",
		"--base-url", "http://localhost:9999/v1",
		"--model", "custom-embed",
		"--dimensions", "768",
	})
	if err != nil {
		t.Fatalf("config model compatible error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_API_KEY"] != "" {
		t.Fatalf("api key = %q", values["EMBEDDING_API_KEY"])
	}
	if values["EMBEDDING_DIMENSIONS"] != "768" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
	}
	if values["EMBEDDING_TIMEOUT"] != "30s" {
		t.Fatalf("timeout = %q", values["EMBEDDING_TIMEOUT"])
	}
}

func TestConfigModelOpenAIDefaults(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	stdin := os.Stdin
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdin = reader
	t.Cleanup(func() {
		os.Stdin = stdin
		_ = reader.Close()
	})
	_, _ = writer.WriteString("openai-test-key\n")
	_ = writer.Close()

	err = run(context.Background(), []string{
		"config",
		"model",
		"openai",
		"--api-key-stdin",
	})
	if err != nil {
		t.Fatalf("config model openai error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_BASE_URL"] != "https://api.openai.com/v1" {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
	}
	if values["EMBEDDING_MODEL"] != "text-embedding-3-small" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["EMBEDDING_DIMENSIONS"] != "1536" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
	}
	if values["EMBEDDING_API_KEY"] != "openai-test-key" {
		t.Fatalf("api key = %q", values["EMBEDDING_API_KEY"])
	}
}

func TestConfigModelLocalRestoresQwenDefaults(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{
		"config", "model", "compatible",
		"--base-url", "https://models.example/v1",
		"--api-key", "secret-model-key",
		"--model", "embed-1536",
	}); err != nil {
		t.Fatalf("config model compatible error = %v", err)
	}
	if err := run(context.Background(), []string{"config", "model", "local"}); err != nil {
		t.Fatalf("config model local error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_PROVIDER"] != "local" {
		t.Fatalf("provider = %q", values["EMBEDDING_PROVIDER"])
	}
	if values["EMBEDDING_BASE_URL"] != "http://host.docker.internal:8080/v1" {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
	}
	if values["EMBEDDING_API_KEY"] != "" {
		t.Fatalf("api key = %q", values["EMBEDDING_API_KEY"])
	}
	if values["EMBEDDING_TIMEOUT"] != "10m" {
		t.Fatalf("timeout = %q", values["EMBEDDING_TIMEOUT"])
	}
	if values["RERANKER_PROVIDER"] != "" || values["RERANKER_BASE_URL"] != "" {
		t.Fatalf("reranker fields = provider %q base %q", values["RERANKER_PROVIDER"], values["RERANKER_BASE_URL"])
	}
	if values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"] != "false" {
		t.Fatalf("local production guard = %q", values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"])
	}
}

func TestConfigMasksSecrets(t *testing.T) {
	if got := maskSecret("secret-model-key"); got != "secr...-key" {
		t.Fatalf("maskSecret = %q", got)
	}
}

func TestCLITimeoutParsesDurationAndSeconds(t *testing.T) {
	if got := cliTimeout(cliArgs{Flags: map[string]string{"timeout": "10m"}, Bools: map[string]bool{}}, time.Second); got != 10*time.Minute {
		t.Fatalf("duration timeout = %s", got)
	}
	if got := cliTimeout(cliArgs{Flags: map[string]string{"timeout": "45"}, Bools: map[string]bool{}}, time.Second); got != 45*time.Second {
		t.Fatalf("seconds timeout = %s", got)
	}
}

func TestEmbeddingRunnerUsesLocalQwenDefaults(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"config", "model", "local"}); err != nil {
		t.Fatalf("config model local error = %v", err)
	}
	cfg := embeddingRunner(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if cfg.ModelID != defaultEmbeddingModelID {
		t.Fatalf("model id = %q", cfg.ModelID)
	}
	if cfg.Model != defaultServedModelName {
		t.Fatalf("served model = %q", cfg.Model)
	}
	if cfg.BaseURL != "http://127.0.0.1:8080/v1" {
		t.Fatalf("base url = %q", cfg.BaseURL)
	}
	if cfg.Port != "8080" {
		t.Fatalf("port = %q", cfg.Port)
	}
	if cfg.Dims != 1024 {
		t.Fatalf("dims = %d", cfg.Dims)
	}
	wantImage := "ghcr.io/ggml-org/llama.cpp:server"
	if cfg.Image != wantImage {
		t.Fatalf("image = %q, want %q", cfg.Image, wantImage)
	}
}

func TestEmbeddingRunnerIgnoresCompatibleProviderConfig(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{
		"config", "model", "compatible",
		"--base-url", "https://models.example/v1",
		"--model", "embed-3072",
		"--dimensions", "3072",
	}); err != nil {
		t.Fatalf("config model compatible error = %v", err)
	}
	cfg := embeddingRunner(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if cfg.BaseURL != "http://127.0.0.1:8080/v1" {
		t.Fatalf("base url = %q", cfg.BaseURL)
	}
	if cfg.Port != "8080" {
		t.Fatalf("port = %q", cfg.Port)
	}
	if cfg.Model != defaultServedModelName {
		t.Fatalf("model = %q", cfg.Model)
	}
	if cfg.Dims != 1024 {
		t.Fatalf("dims = %d", cfg.Dims)
	}
}

func TestSyncLocalRunnerEnvUsesSelectedPort(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init error = %v", err)
	}
	args := parseArgs([]string{"models", "up", "--port", "9090"})
	args.Rest = []string{}
	if err := syncLocalRunnerEnv(args); err != nil {
		t.Fatalf("sync local env error = %v", err)
	}
	values, err := readEnvValues(envPath(args))
	if err != nil {
		t.Fatal(err)
	}
	if values["EMBEDDING_BASE_URL"] != "http://host.docker.internal:9090/v1" {
		t.Fatalf("EMBEDDING_BASE_URL = %q", values["EMBEDDING_BASE_URL"])
	}
}

func TestFriendlyProviderErrorAddsModelsHint(t *testing.T) {
	err := friendlyProviderError(&httpStatusError{
		Code: 400,
		Body: `{"error":"ai provider request failed: Post \"http://host.docker.internal:8080/v1/embeddings\": dial tcp: connect: connection refused"}`,
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "abra models up") {
		t.Fatalf("error = %v", err)
	}
}

func TestLocalPathIngestPostsMatchedFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README.md"), "# Readme\n\nServices must use Abra before release.")
	mustWrite(t, filepath.Join(root, "src", "app.ts"), "export function route() { return '/readyz' }\n")
	mustWrite(t, filepath.Join(root, "node_modules", "ignored.md"), "# Ignored\n")

	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/documents" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		requests = append(requests, body)
		_ = json.NewEncoder(w).Encode(map[string]any{"document_id": "doc"})
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"ingest",
		"--scope", "repo:test",
		"--path", root,
		"--include", "**/*.md",
		"--code",
		"--base-url", server.URL,
		"--token", "test-token",
	})
	if err != nil {
		t.Fatalf("local path ingest error = %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2 (%#v)", len(requests), requests)
	}
	if requests[0]["title"] != "Readme" {
		t.Fatalf("markdown title = %v", requests[0]["title"])
	}
	if !strings.HasPrefix(stringValue(requests[0]["source_url"], ""), "file://") {
		t.Fatalf("source_url = %v", requests[0]["source_url"])
	}
	metadata, _ := requests[1]["metadata"].(map[string]any)
	if metadata["content_kind"] != "code" || metadata["ingest_path"] != "src/app.ts" {
		t.Fatalf("code metadata = %#v", metadata)
	}
}

func TestLocalPathShortcutUsesDefaultScope(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README.md"), "# Local Brain\n\nAgents should use Abra.")

	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/documents" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"document_id": "doc"})
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"ingest",
		root,
		"--base-url", server.URL,
		"--token", "test-token",
	})
	if err != nil {
		t.Fatalf("shortcut ingest error = %v", err)
	}
	wantScope := "repo:" + slug(filepath.Base(root))
	if request["scope"] != wantScope {
		t.Fatalf("scope = %v, want %s", request["scope"], wantScope)
	}
}

func TestLocalPathIngestSkipsEmptyFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README.md"), "# Local Brain\n\nAgents should use Abra.")
	mustWrite(t, filepath.Join(root, "src", "empty.ts"), "")

	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/documents" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		requests = append(requests, body)
		_ = json.NewEncoder(w).Encode(map[string]any{"document_id": "doc"})
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"ingest",
		root,
		"--code",
		"--base-url", server.URL,
		"--token", "test-token",
	})
	if err != nil {
		t.Fatalf("shortcut ingest error = %v", err)
	}
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1 (%#v)", len(requests), requests)
	}
	if requests[0]["title"] != "Local Brain" {
		t.Fatalf("title = %v", requests[0]["title"])
	}
}

func TestDefaultEnvPathOutsideCheckoutUsesAbraHome(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	got := envPath(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	want := filepath.Join(home, "quickstart.env")
	if got != want {
		t.Fatalf("envPath = %q, want %q", got, want)
	}
}

func TestEnsureProjectDirDownloadsRuntimeOutsideCheckout(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	archive := runtimeArchive(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	t.Setenv("ABRA_SOURCE_URL", server.URL+"/abra.tar.gz")

	dir, err := ensureProjectDir(context.Background(), cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if err != nil {
		t.Fatalf("ensureProjectDir error = %v", err)
	}
	if !fileExists(filepath.Join(dir, "docker-compose.yml")) {
		t.Fatalf("runtime docker-compose.yml was not extracted into %s", dir)
	}
}

func runtimeArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("services: {}\n")
	if err := tw.WriteHeader(&tar.Header{
		Name: "abra-test/docker-compose.yml",
		Mode: 0o644,
		Size: int64(len(content)),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	fn()
	_ = writer.Close()
	os.Stdout = original
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		t.Fatal(err)
	}
	_ = reader.Close()
	return buf.String()
}
