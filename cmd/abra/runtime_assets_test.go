package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestDefaultEnvPathIgnoresNonAbraComposeProjects(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), "services:\n  app:\n    image: example/app\n")

	got := envPath(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	want := filepath.Join(home, "quickstart.env")
	if got != want {
		t.Fatalf("envPath = %q, want global env path %q for non-Abra compose project", got, want)
	}
	dir, err := projectDir(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if err != nil {
		t.Fatalf("projectDir error = %v", err)
	}
	wantDir := filepath.Join(home, "runtime", runtimeVersion(), "source")
	if dir != wantDir {
		t.Fatalf("projectDir = %q, want runtime source %q for non-Abra compose project", dir, wantDir)
	}
}

func TestDefaultEnvPathUsesCheckoutOnlyForAbraSourceCheckout(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), "services:\n  api:\n    build: .\n")
	mustWrite(t, filepath.Join(root, "go.mod"), "module github.com/hermawan22/abra\n")
	mustWrite(t, filepath.Join(root, "cmd", "abra", "main.go"), "package main\n")
	mustWrite(t, filepath.Join(root, "migrations", "001_init.sql"), "-- init\n")

	got := envPath(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if got != checkoutEnvPath {
		t.Fatalf("envPath = %q, want checkout env path %q", got, checkoutEnvPath)
	}
	dir, err := projectDir(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if err != nil {
		t.Fatalf("projectDir error = %v", err)
	}
	absRoot, _ := filepath.Abs(root)
	if dir != absRoot {
		t.Fatalf("projectDir = %q, want checkout dir %q", dir, absRoot)
	}
}

func TestEnsureEnvBackfillsLegacyCheckoutQuickstartDefaults(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), "services:\n  api:\n    build: .\n")
	mustWrite(t, filepath.Join(root, "docker-compose.dev.yml"), "services:\n  api:\n    build: .\n")
	mustWrite(t, filepath.Join(root, "go.mod"), "module github.com/hermawan22/abra\n")
	mustWrite(t, filepath.Join(root, "cmd", "abra", "main.go"), "package main\n")
	mustWrite(t, filepath.Join(root, "migrations", "001_init.sql"), "-- init\n")
	mustWrite(t, filepath.Join(root, checkoutEnvPath), strings.Join([]string{
		"ABRA_API_KEYS=dev-token",
		"ABRA_API_TOKEN=dev-token",
		"NODE_ENV=development",
		"ABRA_PORT=18080",
		"EMBEDDING_PROVIDER=local",
		"EMBEDDING_BASE_URL=http://host.docker.internal:8080/v1",
		"EMBEDDING_MODEL=Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0",
		"EMBEDDING_DIMENSIONS=1024",
		"",
	}, "\n"))

	if err := ensureEnv(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}}); err != nil {
		t.Fatalf("ensureEnv error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, checkoutEnvPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"POSTGRES_PASSWORD=abra\n",
		"ABRA_DATABASE_URL=postgres://abra:abra@postgres:5432/abra\n",
		"RERANKER_PROVIDER=\n",
		"ABRA_MAX_REQUEST_BODY_BYTES=26214400\n",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("backfilled env missing %q:\n%s", want, content)
		}
	}
}

func TestDemoEnvUsesPublishedRuntimeImageOutsideCheckout(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	content := demoEnv()
	if !strings.Contains(content, "COMPOSE_FILE=docker-compose.yml\n") {
		t.Fatalf("runtime demo env should use base compose only:\n%s", content)
	}
	if !strings.Contains(content, "ABRA_IMAGE=ghcr.io/hermawan22/abra:"+runtimeVersion()+"\n") {
		t.Fatalf("runtime demo env should use published image:\n%s", content)
	}
	if !strings.Contains(content, "ABRA_EMBEDDING_BATCH_MAX_ITEMS=6\n") || !strings.Contains(content, "ABRA_EMBEDDING_BATCH_MAX_TOKENS=3000\n") {
		t.Fatalf("runtime demo env should include local embedding batch limits:\n%s", content)
	}
	if strings.Contains(content, "ABRA_IMAGE=abra:local") {
		t.Fatalf("runtime demo env must not use local image:\n%s", content)
	}
}

func TestDemoEnvUsesRuntimeImageDigestWhenAvailable(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	oldVersion := version
	version = "v9.9.9"
	t.Cleanup(func() { version = oldVersion })
	if err := os.MkdirAll(managedRuntimeDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	digest := "ghcr.io/hermawan22/abra@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	mustWrite(t, filepath.Join(managedRuntimeDir(), "IMAGE_DIGEST"), digest+"\n")

	content := demoEnv()
	if !strings.Contains(content, "ABRA_IMAGE="+digest+"\n") {
		t.Fatalf("runtime demo env should use digest image:\n%s", content)
	}
}

func TestDemoEnvUsesLocalImageInsideSourceCheckout(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), "services:\n  api:\n    build: .\n")
	mustWrite(t, filepath.Join(root, "go.mod"), "module github.com/hermawan22/abra\n")
	mustWrite(t, filepath.Join(root, "cmd", "abra", "main.go"), "package main\n")
	mustWrite(t, filepath.Join(root, "migrations", "001_init.sql"), "-- init\n")

	content := demoEnv()
	if !strings.Contains(content, "COMPOSE_FILE=docker-compose.yml:docker-compose.dev.yml\n") {
		t.Fatalf("checkout demo env should use dev override:\n%s", content)
	}
	if !strings.Contains(content, "ABRA_IMAGE=abra:local\n") {
		t.Fatalf("checkout demo env should use local image:\n%s", content)
	}
}

func TestExtractTarGzRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	archive := tarGzFixture(t, map[string]string{
		"abra-test/../escape.txt": "nope\n",
	})
	err := extractTarGz(bytes.NewReader(archive), root)
	if err == nil || !strings.Contains(err.Error(), "unsafe archive path") {
		t.Fatalf("extractTarGz error = %v, want unsafe archive path", err)
	}
	if fileExists(filepath.Join(root, "..", "escape.txt")) {
		t.Fatal("archive traversal wrote outside target")
	}
}

func TestEnsureProjectDirDownloadsRuntimeOutsideCheckout(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	archive := runtimeArchive(t)
	sum := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer server.Close()
	t.Setenv("ABRA_SOURCE_URL", server.URL+"/abra.tar.gz")
	t.Setenv("ABRA_SOURCE_SHA256", fmt.Sprintf("%x", sum))

	dir, err := ensureProjectDir(context.Background(), cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if err != nil {
		t.Fatalf("ensureProjectDir error = %v", err)
	}
	if !fileExists(filepath.Join(dir, "docker-compose.yml")) {
		t.Fatalf("runtime docker-compose.yml was not extracted into %s", dir)
	}
	if fileExists(filepath.Join(dir, "docker-compose.dev.yml")) {
		t.Fatalf("runtime docker-compose.dev.yml should be pruned from managed runtime %s", dir)
	}
	for _, path := range []string{"go.mod", filepath.Join("cmd", "abra", "main.go"), filepath.Join("migrations", "001_init.sql")} {
		if !fileExists(filepath.Join(dir, path)) {
			t.Fatalf("runtime fixture source fingerprint file %s was not extracted into %s", path, dir)
		}
	}
	if composeUsesDevOverride(dir) {
		t.Fatalf("downloaded runtime archive must not use dev override")
	}
	steps := composeUpSteps(dir, filepath.Join(home, "quickstart.env"))
	if len(steps) == 0 || !containsString(steps[0], "pull") || containsString(steps[0], "build") {
		t.Fatalf("runtime up steps should pull published images, got %#v", steps)
	}
}

func tarGzFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		content := []byte(body)
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestEnsureProjectDirRejectsUnverifiedRuntimeSourceURL(t *testing.T) {
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

	_, err := ensureProjectDir(context.Background(), cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if err == nil || !strings.Contains(err.Error(), "ABRA_SOURCE_URL requires ABRA_SOURCE_SHA256") {
		t.Fatalf("ensureProjectDir error = %v, want ABRA_SOURCE_SHA256 requirement", err)
	}
}

func TestEnsureProjectDirAllowsExplicitUnverifiedRuntimeSourceURLOptOut(t *testing.T) {
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
	t.Setenv("ABRA_ALLOW_UNVERIFIED_SOURCE_URL", "1")

	dir, err := ensureProjectDir(context.Background(), cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if err != nil {
		t.Fatalf("ensureProjectDir error = %v", err)
	}
	if !fileExists(filepath.Join(dir, "docker-compose.yml")) {
		t.Fatalf("runtime docker-compose.yml was not extracted into %s", dir)
	}
}

func TestEnsureProjectDirDownloadsVerifiedRuntimeBundleOutsideCheckout(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Setenv("ABRA_VERIFY_RUNTIME_ATTESTATION", "0")
	t.Chdir(root)
	oldVersion := version
	version = "v9.9.9"
	t.Cleanup(func() { version = oldVersion })

	archive := runtimeArchive(t)
	asset := runtimeBundleAssetName()
	sum := sha256.Sum256(archive)
	sums := fmt.Sprintf("%x  %s\n", sum, asset)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + asset:
			_, _ = w.Write(archive)
		case "/SHA256SUMS":
			_, _ = w.Write([]byte(sums))
		default:
			t.Fatalf("unexpected runtime download path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	t.Setenv("ABRA_RELEASE_BASE_URL", server.URL)

	dir, err := ensureProjectDir(context.Background(), cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if err != nil {
		t.Fatalf("ensureProjectDir error = %v", err)
	}
	if !fileExists(filepath.Join(dir, "docker-compose.yml")) {
		t.Fatalf("runtime docker-compose.yml was not extracted into %s", dir)
	}
}

func TestInitEnvUsesRuntimeBundleDigestOutsideCheckout(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	oldVersion := version
	version = "v9.9.9"
	t.Cleanup(func() { version = oldVersion })
	if err := os.MkdirAll(managedRuntimeDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	digest := "ghcr.io/hermawan22/abra@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	mustWrite(t, filepath.Join(managedRuntimeDir(), "IMAGE_DIGEST"), digest+"\n")

	if err := initEnv(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}}); err != nil {
		t.Fatalf("initEnv error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "ABRA_IMAGE="+digest+"\n") {
		t.Fatalf("generated env should use runtime digest image:\n%s", content)
	}
	if strings.Contains(string(content), "ABRA_IMAGE=ghcr.io/hermawan22/abra:v9.9.9\n") {
		t.Fatalf("generated env pinned mutable tag despite runtime digest bundle:\n%s", content)
	}
}

func TestEnsureRuntimeImageDigestRewritesGeneratedMutableTagOutsideCheckout(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	oldVersion := version
	version = "v9.9.9"
	t.Cleanup(func() { version = oldVersion })

	if err := initEnv(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}}); err != nil {
		t.Fatalf("initEnv error = %v", err)
	}
	envFile := filepath.Join(home, "quickstart.env")
	before, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(before), "ABRA_IMAGE=ghcr.io/hermawan22/abra:v9.9.9\n") {
		t.Fatalf("fixture env should start with mutable release tag:\n%s", before)
	}

	if err := os.MkdirAll(managedRuntimeDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	digest := "ghcr.io/hermawan22/abra@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"
	mustWrite(t, filepath.Join(managedRuntimeDir(), "IMAGE_DIGEST"), digest+"\n")
	if err := ensureRuntimeImageDigest(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}}); err != nil {
		t.Fatalf("ensureRuntimeImageDigest error = %v", err)
	}
	after, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "ABRA_IMAGE="+digest+"\n") {
		t.Fatalf("env should be rewritten to digest image:\n%s", after)
	}
	if strings.Contains(string(after), "ABRA_IMAGE=ghcr.io/hermawan22/abra:v9.9.9\n") {
		t.Fatalf("env still pins mutable release tag:\n%s", after)
	}
}
