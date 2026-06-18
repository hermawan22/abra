package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandHelpDoesNotRequireFlags(t *testing.T) {
	for _, command := range []string{"config", "ingest", "watch", "sources", "jobs"} {
		t.Run(command, func(t *testing.T) {
			if err := run(context.Background(), []string{command, "--help"}); err != nil {
				t.Fatalf("run(%s --help) error = %v", command, err)
			}
		})
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

func TestConfigModelLocalClearsRemoteFields(t *testing.T) {
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
	if values["EMBEDDING_BASE_URL"] != "" || values["EMBEDDING_API_KEY"] != "" {
		t.Fatalf("remote fields not cleared: base=%q key=%q", values["EMBEDDING_BASE_URL"], values["EMBEDDING_API_KEY"])
	}
	if values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"] != "true" {
		t.Fatalf("local production guard = %q", values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"])
	}
}

func TestConfigMasksSecrets(t *testing.T) {
	if got := maskSecret("secret-model-key"); got != "secr...-key" {
		t.Fatalf("maskSecret = %q", got)
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
