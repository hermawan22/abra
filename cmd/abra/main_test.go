package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
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
	for _, command := range []string{"config", "ingest", "setup", "models", "watch", "sources", "jobs", "scope", "agents", "mcp"} {
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
	for _, want := range []string{
		wantScope,
		"working_memory_compose",
		"abra mcp install-codex",
		"abra agents init " + shellQuote(root) + " --agent codex --scope " + shellQuote(wantScope),
		"abra agents verify " + shellQuote(root) + " --scope " + shellQuote(wantScope),
		"abra ingest " + shellQuote(root) + " --code --scope " + shellQuote(wantScope),
		"If Codex says Abra has no context",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("scope output missing %q:\n%s", want, output)
		}
	}
	ingestIndex := strings.Index(output, "Ingest: abra ingest")
	checkIndex := strings.Index(output, "Check:  abra agents verify")
	if ingestIndex < 0 || checkIndex < 0 || ingestIndex > checkIndex {
		t.Fatalf("scope output should list ingest before verify:\n%s", output)
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
	for key, want := range map[string]string{
		"mcp_install":   "abra mcp install-codex",
		"agents_init":   "abra agents init",
		"agents_verify": "abra agents verify",
		"ingest":        "--scope " + shellQuote(wantScope),
	} {
		if !strings.Contains(stringValue(examples[key], ""), want) {
			t.Fatalf("%s example = %#v, want %q", key, examples[key], want)
		}
	}
	if !strings.Contains(stringValue(examples["codex"], ""), "working_memory_compose") {
		t.Fatalf("codex example = %#v", examples["codex"])
	}
}

func TestAgentsInitCreatesCrossAgentInstructionFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"agents", "init", root, "--agent", "claude"}); err != nil {
			t.Fatalf("agents init error = %v", err)
		}
	})
	wantScope := "repo:" + slug(filepath.Base(root))
	agents, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	claude, err := os.ReadFile(filepath.Join(root, "CLAUDE.md"))
	if err != nil {
		t.Fatalf("read CLAUDE.md: %v", err)
	}
	for _, want := range []string{
		"Use exact scope `" + wantScope + "`",
		"working_memory_compose",
		`agent: "claude"`,
		"Do not include secrets",
	} {
		if !strings.Contains(string(agents), want) {
			t.Fatalf("AGENTS.md missing %q:\n%s", want, string(agents))
		}
	}
	if string(claude) != "@AGENTS.md\n" {
		t.Fatalf("CLAUDE.md = %q", string(claude))
	}
	if !strings.Contains(output, wantScope) || !strings.Contains(output, "CLAUDE.md") {
		t.Fatalf("output = %s", output)
	}
}

func TestAgentsInitSkipsExistingFilesUnlessForced(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "AGENTS.md"), "custom\n")

	if err := run(context.Background(), []string{"agents", "init", root}); err != nil {
		t.Fatalf("agents init error = %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	if string(content) != "custom\n" {
		t.Fatalf("AGENTS.md overwritten without --force:\n%s", string(content))
	}

	if err := run(context.Background(), []string{"agents", "init", root, "--force"}); err != nil {
		t.Fatalf("agents init --force error = %v", err)
	}
	content, err = os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read forced AGENTS.md: %v", err)
	}
	if !strings.Contains(string(content), "working_memory_compose") {
		t.Fatalf("AGENTS.md not updated with --force:\n%s", string(content))
	}
}

func TestAgentsVerifyChecksMCPAndScopeDiscovery(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	wantScope := "repo:" + slug(filepath.Base(root))
	if err := run(context.Background(), []string{"agents", "init", root, "--agent", "codex"}); err != nil {
		t.Fatalf("agents init error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatalf("remove CLAUDE.md: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc map[string]any
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode rpc: %v", err)
		}
		switch rpc["method"] {
		case "tools/list":
			writeTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      rpc["id"],
				"result": map[string]any{"tools": []map[string]any{
					{"name": "discover_scopes"},
					{"name": "working_memory_compose"},
				}},
			})
		case "tools/call":
			params, _ := rpc["params"].(map[string]any)
			var payload []byte
			switch params["name"] {
			case "discover_scopes":
				payload, _ = json.Marshal(map[string]any{
					"recommended_scope": wantScope,
					"matches":           []map[string]any{{"scope": wantScope}},
				})
			case "working_memory_compose":
				args, _ := params["arguments"].(map[string]any)
				if args["diagnostic"] != true {
					t.Fatalf("working_memory_compose diagnostic = %#v, want true", args["diagnostic"])
				}
				payload, _ = json.Marshal(map[string]any{
					"stats": map[string]any{
						"facts":                1,
						"supporting_documents": 1,
						"summaries":            1,
						"graph_relations":      1,
					},
				})
			default:
				t.Fatalf("unexpected tool %v", params["name"])
			}
			writeTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      rpc["id"],
				"result":  map[string]any{"content": []map[string]any{{"type": "text", "text": string(payload)}}},
			})
		default:
			t.Fatalf("unexpected method %v", rpc["method"])
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"agents", "verify", root, "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("agents verify error = %v", err)
		}
	})
	for _, want := range []string{"AGENTS.md", "warn  CLAUDE.md", "scope_discovery", "working_memory", "Ready", wantScope} {
		if !strings.Contains(output, want) {
			t.Fatalf("verify output missing %q:\n%s", want, output)
		}
	}
}

func TestAgentsVerifyFilesOnlyStrictSkipsMCP(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	wantScope := "repo:" + slug(filepath.Base(root))
	if err := run(context.Background(), []string{"agents", "init", root, "--agent", "codex"}); err != nil {
		t.Fatalf("agents init error = %v", err)
	}

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"agents", "verify", root, "--scope", wantScope, "--files-only", "--strict"}); err != nil {
			t.Fatalf("agents verify --files-only --strict error = %v", err)
		}
	})
	for _, want := range []string{"ok  AGENTS.md", "ok  CLAUDE.md", "skip  mcp skipped by --files-only", "Ready: agent instruction files are ready"} {
		if !strings.Contains(output, want) {
			t.Fatalf("files-only verify output missing %q:\n%s", want, output)
		}
	}
}

func TestAgentsVerifyFilesOnlyStrictFailsOnWarning(t *testing.T) {
	root := t.TempDir()
	wantScope := "repo:" + slug(filepath.Base(root))
	if err := run(context.Background(), []string{"agents", "init", root, "--agent", "codex"}); err != nil {
		t.Fatalf("agents init error = %v", err)
	}
	if err := os.Remove(filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatalf("remove CLAUDE.md: %v", err)
	}

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{"agents", "verify", root, "--scope", wantScope, "--files-only", "--strict"})
		if err == nil || !strings.Contains(err.Error(), "agent instruction verification failed") {
			t.Fatalf("agents verify --files-only --strict error = %v", err)
		}
	})
	if !strings.Contains(output, "warn  CLAUDE.md") || !strings.Contains(output, "skip  mcp skipped by --files-only") {
		t.Fatalf("strict files-only verify output did not explain warning failure:\n%s", output)
	}
}

func TestAgentsVerifyFailsWhenScopeIsMissing(t *testing.T) {
	root := t.TempDir()
	if err := run(context.Background(), []string{"agents", "init", root}); err != nil {
		t.Fatalf("agents init error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc map[string]any
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode rpc: %v", err)
		}
		switch rpc["method"] {
		case "tools/list":
			writeTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      rpc["id"],
				"result": map[string]any{"tools": []map[string]any{
					{"name": "discover_scopes"},
					{"name": "working_memory_compose"},
				}},
			})
		case "tools/call":
			payload, _ := json.Marshal(map[string]any{
				"recommended_scope": "",
				"matches":           []map[string]any{},
				"scopes":            []map[string]any{{"scope": "repo:other"}},
			})
			writeTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      rpc["id"],
				"result":  map[string]any{"content": []map[string]any{{"type": "text", "text": string(payload)}}},
			})
		default:
			t.Fatalf("unexpected method %v", rpc["method"])
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{"agents", "verify", root, "--base-url", server.URL, "--token", "test-token"})
		if err == nil || !strings.Contains(err.Error(), "agent context verification failed") {
			t.Fatalf("agents verify error = %v", err)
		}
	})
	if !strings.Contains(output, "fail  scope_discovery") || !strings.Contains(output, "abra ingest . --code --scope") {
		t.Fatalf("verify output did not explain missing scope:\n%s", output)
	}
}

func TestAgentsVerifyFailsWhenWorkingMemoryIsEmpty(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	wantScope := "repo:" + slug(filepath.Base(root))
	if err := run(context.Background(), []string{"agents", "init", root, "--agent", "codex"}); err != nil {
		t.Fatalf("agents init error = %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var rpc map[string]any
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode rpc: %v", err)
		}
		switch rpc["method"] {
		case "tools/list":
			writeTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      rpc["id"],
				"result": map[string]any{"tools": []map[string]any{
					{"name": "discover_scopes"},
					{"name": "working_memory_compose"},
				}},
			})
		case "tools/call":
			params, _ := rpc["params"].(map[string]any)
			var payload []byte
			switch params["name"] {
			case "discover_scopes":
				payload, _ = json.Marshal(map[string]any{
					"recommended_scope": wantScope,
					"matches":           []map[string]any{{"scope": wantScope}},
				})
			case "working_memory_compose":
				args, _ := params["arguments"].(map[string]any)
				if args["diagnostic"] != true {
					t.Fatalf("working_memory_compose diagnostic = %#v, want true", args["diagnostic"])
				}
				payload, _ = json.Marshal(map[string]any{
					"stats": map[string]any{
						"facts":                0,
						"supporting_documents": 0,
						"summaries":            0,
						"graph_relations":      0,
						"context_blocks":       1,
					},
				})
			default:
				t.Fatalf("unexpected tool %v", params["name"])
			}
			writeTestJSON(t, w, map[string]any{
				"jsonrpc": "2.0",
				"id":      rpc["id"],
				"result":  map[string]any{"content": []map[string]any{{"type": "text", "text": string(payload)}}},
			})
		default:
			t.Fatalf("unexpected method %v", rpc["method"])
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{"agents", "verify", root, "--base-url", server.URL, "--token", "test-token"})
		if err == nil || !strings.Contains(err.Error(), "agent context verification failed") {
			t.Fatalf("agents verify error = %v", err)
		}
	})
	for _, want := range []string{"ok  scope_discovery", "fail  working_memory", "facts=0 documents=0 summaries=0 graph=0", "abra ingest . --code --scope " + wantScope} {
		if !strings.Contains(output, want) {
			t.Fatalf("verify output missing %q:\n%s", want, output)
		}
	}
}

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

	check := modelConfigCheck(parseArgs([]string{"doctor"}))
	if check["ok"] != true {
		t.Fatalf("check = %#v", check)
	}
	detail := stringValue(check["detail"], "")
	for _, want := range []string{"provider=local", "base_url=http://host.docker.internal:8080/v1", "dimensions=1024"} {
		if !strings.Contains(detail, want) {
			t.Fatalf("detail missing %q: %s", want, detail)
		}
	}
	if !strings.Contains(stringValue(check["hint"], ""), "abra models status") {
		t.Fatalf("hint = %q", check["hint"])
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
	if !strings.Contains(detail, "ABRA_AI_PROVIDER_CONCURRENCY=4") || !strings.Contains(detail, "single local Qwen") {
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
			{"name": "codex_mcp_client", "ok": false, "detail": "ABRA_API_TOKEN is not set", "hint": "run: abra mcp install-codex"},
		}); err != nil {
			t.Fatalf("printDoctor error = %v", err)
		}
	})
	for _, want := range []string{"ok  model_config", "info provider=local model=embed", "warn  codex_mcp_client", "hint run: abra mcp install-codex"} {
		if !strings.Contains(output, want) {
			t.Fatalf("doctor output missing %q:\n%s", want, output)
		}
	}
}

func TestCodexInstallCommandIncludesCustomTokenEnv(t *testing.T) {
	if got := codexInstallCommand("ABRA_API_TOKEN"); got != "abra mcp install-codex" {
		t.Fatalf("default command = %q", got)
	}
	if got := codexInstallCommand("ABRA_OTHER_TOKEN"); got != "abra mcp install-codex --token-env ABRA_OTHER_TOKEN" {
		t.Fatalf("custom command = %q", got)
	}
}

func TestSetupNormalizesAggressiveWorkerInterval(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	mustWrite(t, filepath.Join(home, "quickstart.env"), strings.Join([]string{
		"ABRA_API_KEYS=dev-token",
		"WORKER_INTERVAL=1s",
		"",
	}, "\n"))

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"setup", "--yes", "--no-start"}); err != nil {
			t.Fatalf("setup error = %v", err)
		}
	})
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["WORKER_INTERVAL"] != "30s" {
		t.Fatalf("worker interval = %q\noutput:\n%s", values["WORKER_INTERVAL"], output)
	}
}

func TestInstallScriptDownloadErrorExplainsRecovery(t *testing.T) {
	err := installScriptDownloadError(
		"https://raw.githubusercontent.com/abra-brain/abra/main/scripts/install.sh",
		errors.New("exit status 22"),
		[]byte("curl: (22) The requested URL returned error: 404"),
	)
	message := err.Error()
	for _, want := range []string{
		"download Abra install script failed",
		"404",
		installScript,
		"ABRA_INSTALL_SCRIPT",
		"abra upgrade --version vX.Y.Z",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("error missing %q:\n%s", want, message)
		}
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

func TestDefaultScopeDerivesRemoteRepositoryIdentity(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{raw: "https://github.com/owner/repo.git", want: "repo:owner-repo"},
		{raw: "git@github.com:owner/repo.git", want: "repo:owner-repo"},
	} {
		if got := defaultScope(tc.raw); got != tc.want {
			t.Fatalf("defaultScope(%q) = %q, want %q", tc.raw, got, tc.want)
		}
	}
}

func TestValidateMCPToolsRequiresAgentIntegrationTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{"tools": []map[string]any{
				{"name": "discover_scopes"},
				{"name": "working_memory_compose"},
			}},
		})
	}))
	defer server.Close()

	count, err := validateMCPTools(context.Background(), parseArgs([]string{"mcp", "--base-url", server.URL, "--token", "test-token"}))
	if err != nil {
		t.Fatalf("validateMCPTools error = %v", err)
	}
	if count != 2 {
		t.Fatalf("tool count = %d, want 2", count)
	}
}

func TestValidateMCPToolsFailsWhenRequiredToolMissing(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]any{"tools": []map[string]any{{"name": "recall"}}},
		})
	}))
	defer server.Close()

	_, err := validateMCPTools(context.Background(), parseArgs([]string{"mcp", "--base-url", server.URL, "--token", "test-token"}))
	if err == nil || !strings.Contains(err.Error(), "discover_scopes") {
		t.Fatalf("validateMCPTools error = %v", err)
	}
}

func TestSetupYesNoStartDefaultsLocalQwen(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"setup", "--yes", "--no-start"}); err != nil {
			t.Fatalf("setup error = %v", err)
		}
	})
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
	if values["ABRA_AI_PROVIDER_CONCURRENCY"] != "1" {
		t.Fatalf("provider concurrency = %q", values["ABRA_AI_PROVIDER_CONCURRENCY"])
	}
	if values["WORKER_INTERVAL"] != "30s" {
		t.Fatalf("worker interval = %q", values["WORKER_INTERVAL"])
	}
	if values["RERANKER_PROVIDER"] != "" {
		t.Fatalf("reranker provider = %q", values["RERANKER_PROVIDER"])
	}
	if values["RERANKER_BASE_URL"] != "" {
		t.Fatalf("reranker base url = %q", values["RERANKER_BASE_URL"])
	}
	if !strings.Contains(output, "abra models up") {
		t.Fatalf("local setup next steps should include models up:\n%s", output)
	}
	for _, want := range []string{"abra agents init --agent codex", "abra agents verify"} {
		if !strings.Contains(output, want) {
			t.Fatalf("setup next steps missing %q:\n%s", want, output)
		}
	}
	wantScope := "repo:" + slug(filepath.Base(root))
	if !strings.Contains(output, "abra ingest . --code --scope "+shellQuote(wantScope)) ||
		!strings.Contains(output, `abra think "What should I know before changing this project?" --scope `+shellQuote(wantScope)) {
		t.Fatalf("setup next steps should include exact scope %s:\n%s", wantScope, output)
	}
}

func TestPrintReadyShowsAgentVerificationFlow(t *testing.T) {
	output := captureStdout(t, func() {
		printReady(parseArgs([]string{"up"}))
	})
	for _, want := range []string{
		"abra mcp install-codex",
		"abra agents init --agent codex",
		"abra agents verify",
		"abra ingest . --code --scope <scope>",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("ready output missing %q:\n%s", want, output)
		}
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

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"setup", "--openai", "--api-key-stdin", "--no-start"}); err != nil {
			t.Fatalf("setup openai error = %v", err)
		}
	})
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
	if strings.Contains(output, "abra models up") {
		t.Fatalf("openai setup next steps should not suggest local models:\n%s", output)
	}
	if !strings.Contains(output, "verify your OpenAI embedding endpoint is reachable from Abra") {
		t.Fatalf("openai setup next steps should mention OpenAI endpoint readiness:\n%s", output)
	}
}

func TestInstallCodexPrevalidatesMCPBeforeMutatingConfig(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "codex.log")
	codexPath := filepath.Join(root, "codex")
	mustWrite(t, codexPath, "#!/bin/sh\nprintf '%s\\n' \"$*\" >> "+shellQuote(logPath)+"\nexit 0\n")
	if err := os.Chmod(codexPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABRA_CODEX_COMMAND", codexPath)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]any{
				"tools": []map[string]any{{"name": "discover_scopes"}},
			},
		})
	}))
	defer server.Close()

	err := run(context.Background(), []string{"mcp", "install-codex", "--base-url", server.URL, "--token", "test-token"})
	if err == nil {
		t.Fatal("expected MCP prevalidation error")
	}
	if !strings.Contains(err.Error(), "before changing Codex config") {
		t.Fatalf("error should explain prevalidation: %v", err)
	}
	logBytes, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	logText := string(logBytes)
	if !strings.Contains(logText, "mcp list") {
		t.Fatalf("codex mcp list was not called:\n%s", logText)
	}
	if strings.Contains(logText, "mcp remove") || strings.Contains(logText, "mcp add") {
		t.Fatalf("codex config was mutated before MCP validation:\n%s", logText)
	}
}

func TestSetupCompatibleNoStartDoesNotSuggestLocalModels(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"setup", "--compatible", "--base-url", "http://localhost:9999/v1", "--embedding-model", "custom-embedding", "--api-key", "compatible-key", "--no-start"}); err != nil {
			t.Fatalf("setup compatible error = %v", err)
		}
	})
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_BASE_URL"] != "http://host.docker.internal:9999/v1" {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
	}
	if values["EMBEDDING_MODEL"] != "custom-embedding" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if strings.Contains(output, "abra models up") {
		t.Fatalf("compatible setup next steps should not suggest local models:\n%s", output)
	}
	if !strings.Contains(output, "verify your compatible embedding endpoint is reachable") {
		t.Fatalf("compatible setup next steps should mention endpoint readiness:\n%s", output)
	}
	if !strings.Contains(output, "rewritten so Abra containers can reach the host service") {
		t.Fatalf("compatible loopback setup should explain host rewrite:\n%s", output)
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
	if values["ABRA_AI_PROVIDER_CONCURRENCY"] != "4" {
		t.Fatalf("provider concurrency = %q", values["ABRA_AI_PROVIDER_CONCURRENCY"])
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
	if values["ABRA_AI_PROVIDER_CONCURRENCY"] != "4" {
		t.Fatalf("provider concurrency = %q", values["ABRA_AI_PROVIDER_CONCURRENCY"])
	}
	if values["EMBEDDING_BASE_URL"] != "http://host.docker.internal:9999/v1" {
		t.Fatalf("base url = %q", values["EMBEDDING_BASE_URL"])
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
	if values["ABRA_AI_PROVIDER_CONCURRENCY"] != "1" {
		t.Fatalf("provider concurrency = %q", values["ABRA_AI_PROVIDER_CONCURRENCY"])
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

func TestDoJSONRejectsOversizedResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte("x"), maxCLIResponseBody+1))
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := doJSON(req, time.Second); err == nil || !strings.Contains(err.Error(), "response body exceeded") {
		t.Fatalf("error = %v, want response body exceeded", err)
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
		"--direct",
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

func TestLocalPathShortcutQueuesTrackedJobWithTrackedFlag(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README.md"), "# Local Brain\n\nAgents should use Abra.")

	var sourceRequest map[string]any
	var jobRequest map[string]any
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/sources/configs":
			if err := json.NewDecoder(r.Body).Decode(&sourceRequest); err != nil {
				t.Fatalf("decode source body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"source_config_id": "source-local"})
		case "/ingestion/jobs":
			if r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ingestion_jobs": []map[string]any{{
						"id":               "job-local",
						"status":           "succeeded",
						"source_config_id": "source-local",
					}},
				})
				return
			}
			if err := json.NewDecoder(r.Body).Decode(&jobRequest); err != nil {
				t.Fatalf("decode job body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ingestion_job": map[string]any{"id": "job-local", "status": "queued"},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"ingest",
		root,
		"--code",
		"--tracked",
		"--base-url", server.URL,
		"--token", "test-token",
	})
	if err != nil {
		t.Fatalf("tracked shortcut ingest error = %v", err)
	}
	wantScope := "repo:" + slug(filepath.Base(root))
	if sourceRequest["scope"] != wantScope {
		t.Fatalf("source scope = %v, want %s", sourceRequest["scope"], wantScope)
	}
	if sourceRequest["source_type"] != "local_repo" {
		t.Fatalf("source_type = %v", sourceRequest["source_type"])
	}
	config, _ := sourceRequest["config"].(map[string]any)
	if config["root"] != root || config["include_code"] != true {
		t.Fatalf("config = %#v", config)
	}
	if jobRequest["source_config_id"] != "source-local" || jobRequest["trigger_type"] != "manual" {
		t.Fatalf("job request = %#v", jobRequest)
	}
	for _, unexpected := range paths {
		if unexpected == "/ingest/documents" {
			t.Fatalf("tracked shortcut should not direct-ingest documents: paths=%v", paths)
		}
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

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("content-type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write json: %v", err)
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
