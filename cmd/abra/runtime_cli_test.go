package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	if got := shellQuote("dev'token"); got != "'dev'\"'\"'token'" {
		t.Fatalf("shellQuote = %q", got)
	}
}

func TestWaitTimeoutUsesFlagOrEnv(t *testing.T) {
	t.Setenv("ABRA_CLI_WAIT_TIMEOUT", "7s")
	if got := waitTimeout(parseArgs([]string{"watch"})); got != 7*time.Second {
		t.Fatalf("env wait timeout = %s", got)
	}
	if got := waitTimeout(parseArgs([]string{"watch", "--wait-timeout", "2m"})); got != 2*time.Minute {
		t.Fatalf("flag wait timeout = %s", got)
	}
	if got := waitTimeout(parseArgs([]string{"watch", "--timeout", "90s"})); got != 90*time.Second {
		t.Fatalf("timeout alias = %s", got)
	}
	if got := waitTimeout(parseArgs([]string{"watch", "--wait-timeout", "bad"})); got != time.Minute {
		t.Fatalf("invalid wait timeout = %s", got)
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

func TestModelConfigCheckExplainsLocalModel(t *testing.T) {
	for _, provider := range []string{"local", "qwen3"} {
		t.Run(provider, func(t *testing.T) {
			root := t.TempDir()
			home := t.TempDir()
			t.Setenv("ABRA_HOME", home)
			t.Chdir(root)
			mustWrite(t, filepath.Join(home, "quickstart.env"), strings.Join([]string{
				"EMBEDDING_PROVIDER=" + provider,
				"EMBEDDING_BASE_URL=http://host.docker.internal:8080/v1",
				"EMBEDDING_MODEL=Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0",
				"EMBEDDING_DIMENSIONS=1024",
				"",
			}, "\n"))

			check := modelConfigCheck(parseArgs([]string{"doctor"}))
			if check["ok"] != true {
				t.Fatalf("check = %#v", check)
			}
			detail := stringValue(check["detail"], "")
			for _, want := range []string{"provider=" + provider, "base_url=http://host.docker.internal:8080/v1", "dimensions=1024"} {
				if !strings.Contains(detail, want) {
					t.Fatalf("detail missing %q: %s", want, detail)
				}
			}
			if !strings.Contains(stringValue(check["hint"], ""), "abra model status") {
				t.Fatalf("hint = %q", check["hint"])
			}
		})
	}
}

func TestModelConfigCheckRejectsProductionLocalWithoutApproval(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	mustWrite(t, filepath.Join(home, "quickstart.env"), strings.Join([]string{
		"NODE_ENV=production",
		"EMBEDDING_PROVIDER=local",
		"EMBEDDING_BASE_URL=http://host.docker.internal:8080/v1",
		"EMBEDDING_MODEL=Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0",
		"EMBEDDING_DIMENSIONS=1024",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false",
		"",
	}, "\n"))

	check := modelConfigCheck(parseArgs([]string{"doctor"}))
	if check["ok"] != false {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(stringValue(check["detail"], ""), "ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=true") {
		t.Fatalf("detail = %q", check["detail"])
	}
	if !strings.Contains(stringValue(check["hint"], ""), "abra config model compatible") {
		t.Fatalf("hint = %q", check["hint"])
	}
}

func TestDockerDaemonCheckReportsReachability(t *testing.T) {
	bin := t.TempDir()
	dockerPath := filepath.Join(bin, "docker")
	mustWrite(t, dockerPath, "#!/bin/sh\nif [ \"$1\" = 'info' ]; then printf '25.0.0\\n'; exit 0; fi\nexit 2\n")
	if err := os.Chmod(dockerPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	check := dockerDaemonCheck()
	if check["ok"] != true || !strings.Contains(stringValue(check["detail"], ""), "25.0.0") {
		t.Fatalf("check = %#v", check)
	}
	if err := ensureDockerDaemon(); err != nil {
		t.Fatalf("ensureDockerDaemon error = %v", err)
	}
}

func TestDockerDaemonCheckExplainsStoppedDaemon(t *testing.T) {
	bin := t.TempDir()
	dockerPath := filepath.Join(bin, "docker")
	mustWrite(t, dockerPath, "#!/bin/sh\nprintf 'Cannot connect to the Docker daemon\\n' >&2\nexit 1\n")
	if err := os.Chmod(dockerPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	check := dockerDaemonCheck()
	if check["ok"] != false {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(stringValue(check["detail"], ""), "Cannot connect") {
		t.Fatalf("detail = %q", check["detail"])
	}
	if !strings.Contains(stringValue(check["hint"], ""), "OrbStack") {
		t.Fatalf("hint = %q", check["hint"])
	}
	err := ensureDockerDaemon()
	if err == nil || !strings.Contains(err.Error(), "start Docker Desktop or OrbStack") {
		t.Fatalf("ensureDockerDaemon error = %v", err)
	}
}

func TestEmbeddingBatchCheckExplainsLocalLimits(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	mustWrite(t, filepath.Join(home, "quickstart.env"), strings.Join([]string{
		"EMBEDDING_PROVIDER=local",
		"ABRA_EMBEDDING_BATCH_MAX_ITEMS=16",
		"ABRA_EMBEDDING_BATCH_MAX_TOKENS=6000",
		"",
	}, "\n"))

	check := embeddingBatchCheck(parseArgs([]string{"doctor"}))
	if check["ok"] != true {
		t.Fatalf("check = %#v", check)
	}
	if !strings.Contains(stringValue(check["detail"], ""), "large batches can exceed") {
		t.Fatalf("detail = %q", check["detail"])
	}
	if !strings.Contains(stringValue(check["hint"], ""), "ABRA_EMBEDDING_BATCH_MAX_ITEMS=6") {
		t.Fatalf("hint = %q", check["hint"])
	}
}

func TestEmbeddingBatchCheckRejectsInvalidValues(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	mustWrite(t, filepath.Join(home, "quickstart.env"), strings.Join([]string{
		"EMBEDDING_PROVIDER=compatible",
		"ABRA_EMBEDDING_BATCH_MAX_ITEMS=0",
		"ABRA_EMBEDDING_BATCH_MAX_TOKENS=6000",
		"",
	}, "\n"))

	check := embeddingBatchCheck(parseArgs([]string{"doctor"}))
	if check["ok"] != false || !strings.Contains(stringValue(check["detail"], ""), "between 1 and 128") {
		t.Fatalf("check = %#v", check)
	}
}

func TestConfigShowIncludesEmbeddingBatchLimits(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	mustWrite(t, filepath.Join(home, "quickstart.env"), strings.Join([]string{
		"EMBEDDING_PROVIDER=local",
		"EMBEDDING_BASE_URL=http://host.docker.internal:8080/v1",
		"EMBEDDING_MODEL=Qwen/Qwen3-Embedding-0.6B-GGUF:Q8_0",
		"EMBEDDING_DIMENSIONS=1024",
		"",
	}, "\n"))

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"config", "show", "--json"}); err != nil {
			t.Fatalf("config show error = %v", err)
		}
	})
	var view map[string]any
	if err := json.Unmarshal([]byte(output), &view); err != nil {
		t.Fatalf("decode config show: %v\n%s", err, output)
	}
	if view["batch_max_items"] != "6" {
		t.Fatalf("batch_max_items = %#v", view["batch_max_items"])
	}
	if view["batch_max_tokens"] != "3000" {
		t.Fatalf("batch_max_tokens = %#v", view["batch_max_tokens"])
	}
}

func TestWorkerIntervalCheckWarnsAggressiveLocalDefault(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	mustWrite(t, filepath.Join(home, "quickstart.env"), "WORKER_INTERVAL=1s\n")

	check := workerIntervalCheck(parseArgs([]string{"doctor"}))
	if check["ok"] != false {
		t.Fatalf("check = %#v", check)
	}
	detail := stringValue(check["detail"], "")
	if !strings.Contains(detail, "WORKER_INTERVAL=1s") || !strings.Contains(detail, "compete with recall") {
		t.Fatalf("detail = %q", detail)
	}
	if hint := stringValue(check["hint"], ""); !strings.Contains(hint, "WORKER_INTERVAL=30s") {
		t.Fatalf("hint = %q", hint)
	}
}

func TestAIProviderConcurrencyCheckWarnsLocalOversubscription(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	mustWrite(t, filepath.Join(home, "quickstart.env"), strings.Join([]string{
		"EMBEDDING_PROVIDER=local",
		"ABRA_AI_PROVIDER_CONCURRENCY=4",
		"",
	}, "\n"))

	check := aiProviderConcurrencyCheck(parseArgs([]string{"doctor"}))
	if check["ok"] != false {
		t.Fatalf("check = %#v", check)
	}
	detail := stringValue(check["detail"], "")
	if !strings.Contains(detail, "ABRA_AI_PROVIDER_CONCURRENCY=4") || !strings.Contains(detail, "single local model runner") {
		t.Fatalf("detail = %q", detail)
	}
	if hint := stringValue(check["hint"], ""); !strings.Contains(hint, "ABRA_AI_PROVIDER_CONCURRENCY=1") {
		t.Fatalf("hint = %q", hint)
	}
}

func TestAIProviderConcurrencyCheckReportsDefaultsAndInvalidValues(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	envFile := filepath.Join(home, "quickstart.env")
	mustWrite(t, envFile, "EMBEDDING_PROVIDER=compatible\n")

	check := aiProviderConcurrencyCheck(parseArgs([]string{"doctor"}))
	if check["ok"] != true {
		t.Fatalf("check = %#v", check)
	}
	if detail := stringValue(check["detail"], ""); !strings.Contains(detail, "runtime default is 4") {
		t.Fatalf("detail = %q", detail)
	}

	mustWrite(t, envFile, "EMBEDDING_PROVIDER=compatible\nABRA_AI_PROVIDER_CONCURRENCY=33\n")
	check = aiProviderConcurrencyCheck(parseArgs([]string{"doctor"}))
	if check["ok"] != false {
		t.Fatalf("check = %#v", check)
	}
	if detail := stringValue(check["detail"], ""); !strings.Contains(detail, "between 1 and 32") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestPrintDoctorIncludesDetailsAndHints(t *testing.T) {
	output := captureStdout(t, func() {
		if err := printDoctor(parseArgs([]string{"doctor"}), []map[string]any{
			{"name": "model_config", "ok": true, "detail": "provider=local model=embed"},
			{
				"name":   "codex_mcp_client",
				"ok":     false,
				"detail": "ABRA_API_TOKEN is not set",
				"hint":   "run: abra agent install codex",
				"next": []string{
					"abra agent install codex",
					"for terminal Codex: set -a; source .tmp/quickstart.env; set +a; codex",
				},
			},
		}); err != nil {
			t.Fatalf("printDoctor error = %v", err)
		}
	})
	for _, want := range []string{"ok  model_config", "info provider=local model=embed", "warn  codex_mcp_client", "hint run: abra agent install codex", "next", "for terminal Codex: set -a"} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, output)
		}
	}
}

func TestCodexMCPRecoveryStepsDoNotPrintToken(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	secret := "super-secret-token"
	mustWrite(t, filepath.Join(home, "quickstart.env"), "ABRA_API_TOKEN="+secret+"\n")

	steps := codexMCPRecoverySteps(parseArgs([]string{"doctor"}), "ABRA_API_TOKEN")
	encoded, err := json.Marshal(steps)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("doctor recovery leaked token: %s", encoded)
	}
	if !strings.Contains(string(encoded), "source") || !strings.Contains(string(encoded), "quickstart.env") {
		t.Fatalf("doctor recovery missing terminal Codex source guidance: %s", encoded)
	}
}

func TestPrintDoctorStrictFailsAfterOutput(t *testing.T) {
	var err error
	output := captureStdout(t, func() {
		err = printDoctor(parseArgs([]string{"doctor", "--strict"}), []map[string]any{
			{"name": "model_config", "ok": true, "detail": "provider=local model=embed"},
			{"name": "readyz", "ok": false, "hint": "run: abra up"},
		})
	})
	if err == nil || !strings.Contains(err.Error(), "doctor checks failed") {
		t.Fatalf("strict doctor error = %v", err)
	}
	if !strings.Contains(output, "warn  readyz") || !strings.Contains(output, "hint run: abra up") {
		t.Fatalf("strict doctor should still print details before failing:\n%s", output)
	}
}

func TestPrintDoctorJSONStrictFailsWithMachineReadableOutput(t *testing.T) {
	var err error
	output := captureStdout(t, func() {
		err = printDoctor(parseArgs([]string{"doctor", "--json", "--strict"}), []map[string]any{
			{"name": "readyz", "ok": false, "hint": "run: abra up"},
		})
	})
	if err == nil || !strings.Contains(err.Error(), "doctor checks failed") {
		t.Fatalf("strict json doctor error = %v", err)
	}
	var payload map[string]any
	if decodeErr := json.Unmarshal([]byte(output), &payload); decodeErr != nil {
		t.Fatalf("decode strict json output: %v\n%s", decodeErr, output)
	}
	if payload["ok"] != false {
		t.Fatalf("strict json ok = %#v, want false", payload["ok"])
	}
}

func TestCodexInstallCommandIncludesCustomTokenEnv(t *testing.T) {
	if got := codexInstallCommand("ABRA_API_TOKEN"); got != "abra agent install codex" {
		t.Fatalf("default command = %q", got)
	}
	if got := codexInstallCommand("ABRA_OTHER_TOKEN"); got != "abra agent install codex --token-env ABRA_OTHER_TOKEN" {
		t.Fatalf("custom command = %q", got)
	}
}

func TestCodexMCPListHasAbra(t *testing.T) {
	for _, output := range []string{
		"abra\n",
		"abra http://127.0.0.1:18080/mcp\n",
		`{"name":"abra","url":"http://127.0.0.1:18080/mcp"}`,
	} {
		if !codexMCPListHasAbra(output) {
			t.Fatalf("expected abra in output %q", output)
		}
	}
	if codexMCPListHasAbra("other http://127.0.0.1:18080/mcp\n") {
		t.Fatal("unexpected abra match")
	}
}

func TestCodexMCPClientCheckReportsCodexConfigFailure(t *testing.T) {
	root := t.TempDir()
	codexPath := filepath.Join(root, "codex")
	mustWrite(t, codexPath, "#!/bin/sh\nprintf 'config parse error\\n' >&2\nexit 2\n")
	if err := os.Chmod(codexPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABRA_CODEX_COMMAND", codexPath)
	t.Setenv("ABRA_API_TOKEN", defaultToken)

	check := codexMCPClientCheck(parseArgs([]string{"doctor"}))
	if check["ok"] != false {
		t.Fatalf("check = %#v", check)
	}
	if detail := stringValue(check["detail"], ""); !strings.Contains(detail, "could not be read") || !strings.Contains(detail, "config parse error") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestCodexMCPClientCheckRequiresAbraEntry(t *testing.T) {
	root := t.TempDir()
	codexPath := filepath.Join(root, "codex")
	mustWrite(t, codexPath, "#!/bin/sh\nif [ \"$1 $2\" = 'mcp list' ]; then printf 'other http://127.0.0.1:18080/mcp\\n'; exit 0; fi\nexit 1\n")
	if err := os.Chmod(codexPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABRA_CODEX_COMMAND", codexPath)
	t.Setenv("ABRA_API_TOKEN", defaultToken)

	check := codexMCPClientCheck(parseArgs([]string{"doctor"}))
	if check["ok"] != false {
		t.Fatalf("check = %#v", check)
	}
	if detail := stringValue(check["detail"], ""); !strings.Contains(detail, "entry `abra` is not installed") {
		t.Fatalf("detail = %q", detail)
	}
}

func TestCodexMCPClientCheckPassesWhenRegisteredAndTokenMatches(t *testing.T) {
	root := t.TempDir()
	codexPath := filepath.Join(root, "codex")
	mustWrite(t, codexPath, "#!/bin/sh\nif [ \"$1 $2\" = 'mcp list' ]; then printf 'abra http://127.0.0.1:18080/mcp\\n'; exit 0; fi\nexit 1\n")
	if err := os.Chmod(codexPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABRA_CODEX_COMMAND", codexPath)
	t.Setenv("ABRA_API_TOKEN", defaultToken)

	check := codexMCPClientCheck(parseArgs([]string{"doctor"}))
	if check["ok"] != true {
		t.Fatalf("check = %#v", check)
	}
}

func TestMCPConfigUsesTokenEnvByDefault(t *testing.T) {
	t.Setenv("ABRA_API_TOKEN", "fixture-token-value")
	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"mcp", "--base-url", "http://127.0.0.1:18080"}); err != nil {
			t.Fatalf("mcp error = %v", err)
		}
	})
	if strings.Contains(output, "fixture-token-value") || strings.Contains(output, "Authorization") {
		t.Fatalf("default mcp config leaked literal token:\n%s", output)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("decode mcp config: %v\n%s", err, output)
	}
	servers := payload["mcpServers"].(map[string]any)
	abra := servers["abra"].(map[string]any)
	if abra["bearer_token_env_var"] != "ABRA_API_TOKEN" {
		t.Fatalf("mcp token env = %#v", abra["bearer_token_env_var"])
	}
}

func TestMCPConfigLiteralTokenIsOptIn(t *testing.T) {
	t.Setenv("ABRA_API_TOKEN", "fixture-token-value")
	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"mcp", "--base-url", "http://127.0.0.1:18080", "--literal-token"}); err != nil {
			t.Fatalf("mcp error = %v", err)
		}
	})
	if !strings.Contains(output, "Authorization") || !strings.Contains(output, "fixture-token-value") {
		t.Fatalf("literal mcp config missing opt-in token:\n%s", output)
	}
}

func TestMCPStatusChecksServerToolsAndClientRecovery(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	mustWrite(t, filepath.Join(home, "quickstart.env"), "ABRA_API_TOKEN=test-token\n")

	binDir := t.TempDir()
	codexPath := filepath.Join(binDir, "codex")
	mustWrite(t, codexPath, "#!/bin/sh\nif [ \"$1 $2\" = 'mcp list' ]; then printf 'other http://127.0.0.1:9999/mcp\\n'; exit 0; fi\nexit 1\n")
	if err := os.Chmod(codexPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABRA_CODEX_COMMAND", codexPath)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			writeTestJSON(t, w, map[string]any{"ok": true, "embedding_provider": "local"})
		case "/mcp":
			writeTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"result":  map[string]any{"tools": requiredMCPToolFixtures()},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"mcp", "status", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("mcp status error = %v", err)
		}
	})
	for _, want := range []string{"ok  readyz", "ok  mcp", "warn  codex_mcp_client", "abra agent install codex"} {
		if !strings.Contains(output, want) {
			t.Fatalf("mcp status output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "test-token") {
		t.Fatalf("mcp status leaked token:\n%s", output)
	}
}

func TestReadyFailureMessageIncludesLocalModelRecovery(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	mustWrite(t, filepath.Join(home, "quickstart.env"), "EMBEDDING_PROVIDER=local\n")

	message := readyFailureMessage(parseArgs([]string{"up"}), map[string]any{
		"embedding_error":            "connection refused",
		"embedding_status":           "timeout",
		"embedding_check_timeout":    "10s",
		"embedding_provider_timeout": "10m",
	}, http.StatusServiceUnavailable, nil, "Abra did not become ready")
	for _, want := range []string{
		"Abra did not become ready",
		"status: 503",
		"detail: connection refused",
		"embedding_status: timeout",
		"embedding_check_timeout: 10s",
		"embedding_provider_timeout: 10m",
		"Check: abra model status",
		"Repair: abra up",
		"Diagnose: abra doctor",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("ready failure message missing %q:\n%s", want, message)
		}
	}
}

func TestReadyFailureMessageIncludesNetworkError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	mustWrite(t, filepath.Join(home, "quickstart.env"), "EMBEDDING_PROVIDER=compatible\n")

	message := readyFailureMessage(parseArgs([]string{"status"}), nil, 0, errors.New("dial tcp 127.0.0.1:18080: connect: connection refused"), "")
	for _, want := range []string{
		"detail: dial tcp 127.0.0.1:18080: connect: connection refused",
		"Repair: abra up",
		"Diagnose: abra doctor",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("ready failure message missing %q:\n%s", want, message)
		}
	}
	if strings.Contains(message, "abra model status") {
		t.Fatalf("compatible provider failure should not include local model hint:\n%s", message)
	}
}

func TestStatusPrintsReadyFailureDetail(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	mustWrite(t, filepath.Join(home, "quickstart.env"), "EMBEDDING_PROVIDER=local\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/readyz" || r.URL.Query().Get("deep") != "1" {
			t.Fatalf("unexpected readiness path %s", r.URL.String())
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		writeTestJSON(t, w, map[string]any{
			"embedding_error":            "embedding endpoint refused connection",
			"embedding_status":           "timeout",
			"embedding_check_timeout":    "10s",
			"embedding_provider_timeout": "10m",
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"status", "--base-url", server.URL}); err != nil {
			t.Fatalf("status error = %v", err)
		}
	})
	for _, want := range []string{
		"Abra: not ready (503)",
		"detail: embedding endpoint refused connection",
		"embedding_status: timeout",
		"embedding_check_timeout: 10s",
		"embedding_provider_timeout: 10m",
		"Check: abra model status",
		"Repair: abra up",
		"Diagnose: abra doctor",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("status output missing %q:\n%s", want, output)
		}
	}
}

func TestStatusJSONReturnsFailurePayloadAndError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	mustWrite(t, filepath.Join(home, "quickstart.env"), "EMBEDDING_PROVIDER=local\n")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeTestJSON(t, w, map[string]any{"embedding_error": "model cold"})
	}))
	defer server.Close()

	var runErr error
	output := captureStdout(t, func() {
		runErr = run(context.Background(), []string{"status", "--json", "--base-url", server.URL})
	})
	if runErr == nil {
		t.Fatal("status --json should fail when Abra is not ready")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("decode status json: %v\n%s", err, output)
	}
	if payload["ready"] != false || payload["status"] != float64(http.StatusServiceUnavailable) || payload["embedding_error"] != "model cold" {
		t.Fatalf("status payload = %#v", payload)
	}
}

func TestQueryCommandsReturnFriendlyMissingInputErrors(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "think", args: []string{"think"}, want: "think requires a question"},
		{name: "recall", args: []string{"recall"}, want: "recall requires a query"},
		{name: "compose", args: []string{"compose"}, want: "compose requires a task"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Fatalf("command panicked: %v", recovered)
				}
			}()
			err := run(context.Background(), tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestReadyzPathUsesDeepCheckForLocalAliases(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	envFile := filepath.Join(home, "quickstart.env")
	for _, provider := range []string{"local", "qwen3", "local-smart"} {
		mustWrite(t, envFile, "EMBEDDING_PROVIDER="+provider+"\n")
		if got := readyzPath(parseArgs([]string{"status"})); got != "/readyz?deep=1" {
			t.Fatalf("readyz path for provider %s = %q", provider, got)
		}
	}
	mustWrite(t, envFile, "EMBEDDING_PROVIDER=compatible\n")
	if got := readyzPath(parseArgs([]string{"status"})); got != "/readyz" {
		t.Fatalf("readyz path for compatible = %q", got)
	}
}

func TestReadyzPathUsesShallowCheckWhenLocalModelsAreSkipped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	mustWrite(t, filepath.Join(home, "quickstart.env"), "EMBEDDING_PROVIDER=local\n")

	for _, args := range [][]string{
		{"up", "--no-models"},
		{"up", "--skip-models"},
	} {
		if got := readyzPath(parseArgs(args)); got != "/readyz" {
			t.Fatalf("readyz path for %v = %q, want shallow /readyz", args, got)
		}
	}
}

func TestPrintReadyWarnsWhenModelsAreSkipped(t *testing.T) {
	output := captureStdout(t, func() {
		printReady(parseArgs([]string{"up", "--no-models", "--base-url", "http://127.0.0.1:18080"}))
	})
	for _, want := range []string{
		"Abra is ready",
		"Models:    skipped; run `abra model up` before ingest/think when using local embeddings",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("printReady output missing %q:\n%s", want, output)
		}
	}
}

func TestWaitReadyReturnsLastReadinessDetailOnCancel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	mustWrite(t, filepath.Join(home, "quickstart.env"), "EMBEDDING_PROVIDER=local\n")
	ctx, cancel := context.WithCancel(context.Background())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"embedding_error":            "model still loading",
			"embedding_status":           "timeout",
			"embedding_check_timeout":    "10s",
			"embedding_provider_timeout": "10m",
		}); err != nil {
			t.Fatalf("write json: %v", err)
		}
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()
	}))
	defer server.Close()

	err := waitReady(ctx, parseArgs([]string{"up", "--base-url", server.URL}))
	if err == nil {
		t.Fatal("expected waitReady error")
	}
	for _, want := range []string{
		"context canceled",
		"Abra did not become ready",
		"status: 503",
		"detail: model still loading",
		"embedding_status: timeout",
		"embedding_check_timeout: 10s",
		"embedding_provider_timeout: 10m",
		"Check: abra model status",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("waitReady error missing %q:\n%s", want, err.Error())
		}
	}
}
