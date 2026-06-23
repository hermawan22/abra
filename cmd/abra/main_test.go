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
)

func requiredMCPToolFixtures() []map[string]any {
	tools := make([]map[string]any, 0, len(requiredMCPToolNames()))
	for _, name := range requiredMCPToolNames() {
		tools = append(tools, map[string]any{"name": name})
	}
	return tools
}

func TestCommandHelpDoesNotRequireFlags(t *testing.T) {
	for _, command := range []string{"connect", "sync", "ask", "context", "agent", "model", "brain", "govern", "plugin", "config", "ingest", "setup", "models", "watch", "connectors", "sources", "jobs", "observe", "observations", "scope", "agents", "memory", "mcp"} {
		t.Run(command, func(t *testing.T) {
			if err := run(context.Background(), []string{command, "--help"}); err != nil {
				t.Fatalf("run(%s --help) error = %v", command, err)
			}
		})
	}
}

func TestVersionJSONIncludesExecutablePath(t *testing.T) {
	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"version", "--json"}); err != nil {
			t.Fatalf("version error = %v", err)
		}
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("decode version: %v\n%s", err, output)
	}
	if stringValue(payload["version"], "") == "" || stringValue(payload["executable"], "") == "" {
		t.Fatalf("version payload missing version or executable: %#v", payload)
	}
}

func TestTopLevelVersionFlags(t *testing.T) {
	for _, flag := range []string{"--version", "-v"} {
		t.Run(flag, func(t *testing.T) {
			output := captureStdout(t, func() {
				if err := run(context.Background(), []string{flag}); err != nil {
					t.Fatalf("run(%s) error = %v", flag, err)
				}
			})
			if !strings.Contains(output, "abra ") || !strings.Contains(output, "target: ") {
				t.Fatalf("version output for %s = %s", flag, output)
			}
		})
	}
}

func TestConfigAndMCPHelpShowAgentFirstOnboardingPath(t *testing.T) {
	configHelp := commandUsage("config")
	for _, want := range []string{
		"connect Abra to the embedding model",
		"common local/compatible paths do not",
		"require manual env file editing",
		"Check readiness with: abra doctor",
	} {
		if !strings.Contains(configHelp, want) {
			t.Fatalf("config help missing %q:\n%s", want, configHelp)
		}
	}

	mcpHelp := commandUsage("mcp")
	for _, want := range []string{
		"abra mcp status",
		"No manual Codex config editing is required",
		"Common Codex path:",
		"abra setup",
		"abra doctor",
		"abra agent bootstrap --agent codex",
		"abra agent ready . --scope <scope-from-abra-scope> --json",
		"full model/API/MCP preflight",
	} {
		if !strings.Contains(mcpHelp, want) {
			t.Fatalf("mcp help missing %q:\n%s", want, mcpHelp)
		}
	}
}

func TestTopLevelHelpShowsAgentOnboardingStatusFlow(t *testing.T) {
	help := usage()
	for _, want := range []string{
		"abra doctor",
		"cd /path/to/project",
		"abra scope",
		"abra agent bootstrap --agent <agent>",
		"fully restart the agent runtime",
		"abra agent ready . --scope <scope-from-abra-scope> --json",
		"agents use MCP working_memory_compose / brain_think with the verified scope",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("top-level help missing %q:\n%s", want, help)
		}
	}
}

func TestContextBriefHumanOutput(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody = decodeMCPToolCall(t, r, "working_memory_compose")
		writeMCPToolResult(t, w, 1, map[string]any{
			"task":  "ship a change",
			"scope": "repo:demo",
			"verification": map[string]any{
				"verdict": "strong",
			},
			"agent_decision": map[string]any{
				"decision": "proceed",
			},
			"memory_health": map[string]any{
				"status": "healthy",
				"score":  100,
			},
			"stats": map[string]any{
				"facts":                2,
				"supporting_documents": 1,
				"summaries":            1,
				"graph_relations":      3,
			},
			"validation_plan": []any{
				map[string]any{"command": "go test ./...", "required": true},
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"context", "ship a change", "--scope", "repo:demo", "--brief", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("context error = %v", err)
		}
	})
	if requestBody["task"] != "ship a change" || requestBody["scope"] != "repo:demo" || requestBody["hook"] != "before_task" {
		t.Fatalf("unexpected context request body: %#v", requestBody)
	}
	for _, want := range []string{
		"Context: strong / proceed",
		"Trust: scope=repo:demo health=healthy score=100 conflicts=0 risks=0",
		"Context: facts=2 documents=1 summaries=1 graph=3",
		"Validation: 1 step(s)",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("context brief output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "Evidence") || strings.Contains(output, "Allowed next") {
		t.Fatalf("context brief output should stay compact:\n%s", output)
	}
}

func TestLoadBrainEvalCanonicalSuite(t *testing.T) {
	raw, err := loadBrainEvalSuite(parseArgs([]string{"eval", "brain", "--suite", "canonical"}))
	if err != nil {
		t.Fatalf("load canonical suite: %v", err)
	}
	var suite brainEvalSuite
	if err := json.Unmarshal(raw, &suite); err != nil {
		t.Fatalf("decode canonical suite: %v", err)
	}
	if len(suite.Cases) == 0 {
		t.Fatalf("canonical suite has no cases")
	}
	fileRaw, err := os.ReadFile("../../examples/evals/brain/canonical.json")
	if err != nil {
		t.Fatalf("read checked-in canonical suite: %v", err)
	}
	var fileSuite brainEvalSuite
	if err := json.Unmarshal(fileRaw, &fileSuite); err != nil {
		t.Fatalf("decode checked-in canonical suite: %v", err)
	}
	embeddedNormalized, err := json.Marshal(suite)
	if err != nil {
		t.Fatalf("marshal embedded canonical suite: %v", err)
	}
	fileNormalized, err := json.Marshal(fileSuite)
	if err != nil {
		t.Fatalf("marshal checked-in canonical suite: %v", err)
	}
	if string(embeddedNormalized) != string(fileNormalized) {
		t.Fatalf("embedded canonical suite diverged from checked-in examples/evals/brain/canonical.json")
	}
	if _, err := loadBrainEvalSuite(parseArgs([]string{"eval", "brain", "--suite", "unknown"})); err == nil || !strings.Contains(err.Error(), "unknown brain eval suite") {
		t.Fatalf("unknown suite error = %v", err)
	}
}

func TestInstallScriptDownloadErrorExplainsRecovery(t *testing.T) {
	err := installScriptDownloadError(
		"https://example.invalid/abra/install.sh",
		errors.New("exit status 22"),
		[]byte("curl: (22) The requested URL returned error: 404"),
	)
	message := err.Error()
	for _, want := range []string{
		"download Abra install script failed",
		"404",
		installScript,
		"release's install.sh URL",
		"abra upgrade --version vX.Y.Z",
	} {
		if !strings.Contains(message, want) {
			t.Fatalf("error missing %q:\n%s", want, message)
		}
	}
	if strings.Contains(message, "raw install.sh") || strings.Contains(message, "raw.githubusercontent.com") {
		t.Fatalf("error should not recommend raw branch installer URLs:\n%s", message)
	}
}

func TestInstallScriptDefaultsToPublishedRelease(t *testing.T) {
	if strings.Contains(installScript, "raw.githubusercontent.com") || strings.Contains(installScript, "/main/") {
		t.Fatalf("default install script should use published release asset, got %s", installScript)
	}
	if got := releaseInstallScriptURL(""); got != installScript {
		t.Fatalf("empty version URL = %q, want %q", got, installScript)
	}
	if got := releaseInstallScriptURL("latest"); got != installScript {
		t.Fatalf("latest URL = %q, want %q", got, installScript)
	}
	if got := releaseInstallScriptURL("v0.3.7"); got != "https://github.com/hermawan22/abra/releases/download/v0.3.7/install.sh" {
		t.Fatalf("pinned URL = %q", got)
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

func TestPrintReadyShowsAgentVerificationFlow(t *testing.T) {
	output := captureStdout(t, func() {
		printReady(parseArgs([]string{"up"}))
	})
	for _, want := range []string{
		"abra agent install <agent>",
		"abra agent bootstrap --agent <agent>",
		"abra agent init --agent <agent>",
		"abra agent verify",
		"abra sync . --code --scope <scope>",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("ready output missing %q:\n%s", want, output)
		}
	}
}

func TestConfigMasksSecrets(t *testing.T) {
	if got := maskSecret("secret-model-key"); got != "secr...-key" {
		t.Fatalf("maskSecret = %q", got)
	}
}

func TestProductionEnvExampleIncludesCompatibleBatchLimits(t *testing.T) {
	if !strings.Contains(productionEnvExample, "ABRA_EMBEDDING_BATCH_MAX_ITEMS=16\n") ||
		!strings.Contains(productionEnvExample, "ABRA_EMBEDDING_BATCH_MAX_TOKENS=6000\n") {
		t.Fatalf("production env example should include compatible embedding batch limits:\n%s", productionEnvExample)
	}
}

func runtimeArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	files := map[string]string{
		"docker-compose.yml":      "services: {}\n",
		"docker-compose.dev.yml":  "services:\n  api:\n    build: .\n",
		"go.mod":                  "module github.com/hermawan22/abra\n",
		"cmd/abra/main.go":        "package main\n",
		"migrations/001_init.sql": "-- init\n",
		"IMAGE_DIGEST":            "ghcr.io/hermawan22/abra@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef\n",
	}
	for name, body := range files {
		content := []byte(body)
		if err := tw.WriteHeader(&tar.Header{
			Name: "abra-test/" + name,
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
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

func writeMCPToolResult(t *testing.T, w http.ResponseWriter, id any, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatalf("encode mcp tool payload: %v", err)
	}
	writeTestJSON(t, w, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"content":           []map[string]any{{"type": "text", "text": string(raw)}},
			"structuredContent": value,
		},
	})
}

func decodeMCPToolCall(t *testing.T, r *http.Request, wantTool string) map[string]any {
	t.Helper()
	if r.Method != http.MethodPost || r.URL.Path != "/mcp" {
		t.Fatalf("request = %s %s, want POST /mcp", r.Method, r.URL.Path)
	}
	var rpc map[string]any
	if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
		t.Fatalf("decode mcp request: %v", err)
	}
	params, _ := rpc["params"].(map[string]any)
	if rpc["method"] != "tools/call" || params["name"] != wantTool {
		t.Fatalf("unexpected mcp request: %#v", rpc)
	}
	args, _ := params["arguments"].(map[string]any)
	return args
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	original := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		os.Stdout = original
		if !closed {
			_ = writer.Close()
		}
		_ = reader.Close()
	}()
	os.Stdout = writer
	fn()
	closed = true
	_ = writer.Close()
	os.Stdout = original
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, reader); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}
