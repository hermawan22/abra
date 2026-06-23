package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
		agentReadyPrompt(wantScope),
		"abra agent install codex",
		"abra agent init " + shellQuote(root) + " --agent codex --scope " + shellQuote(wantScope),
		"abra agent verify " + shellQuote(root) + " --scope " + shellQuote(wantScope),
		"abra sync " + shellQuote(root) + " --code --scope " + shellQuote(wantScope),
		"If Codex says Abra has no context",
		"run Check first",
		"agent_ready=false",
		"sync only when Check proves missing scope or empty memory",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("scope output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "run Ingest, then Check") {
		t.Fatalf("scope output should not unconditionally suggest ingest before verify:\n%s", output)
	}
	syncIndex := strings.Index(output, "Sync only if Check proves missing scope or empty memory")
	checkIndex := strings.Index(output, "Check:  abra agent verify")
	if syncIndex < 0 || checkIndex < 0 || checkIndex > syncIndex {
		t.Fatalf("scope output should list verify before conditional sync:\n%s", output)
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
		"agent_install": "abra agent install codex",
		"agent_init":    "abra agent init",
		"agent_verify":  "abra agent verify",
		"sync":          "--scope " + shellQuote(wantScope),
	} {
		if !strings.Contains(stringValue(examples[key], ""), want) {
			t.Fatalf("%s example = %#v, want %q", key, examples[key], want)
		}
	}
	if stringValue(examples["codex"], "") != agentReadyPrompt(wantScope) {
		t.Fatalf("codex example = %#v", examples["codex"])
	}
	troubleshooting := stringValue(examples["troubleshooting"], "")
	for _, want := range []string{"run agent_verify --json first", "readiness errors", "Sync only when verify proves"} {
		if !strings.Contains(troubleshooting, want) {
			t.Fatalf("troubleshooting missing %q: %s", want, troubleshooting)
		}
	}
	if strings.Contains(troubleshooting, "run the ingest example") {
		t.Fatalf("troubleshooting should not lead with ingest: %s", troubleshooting)
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
		"Abra MCP tools are unavailable",
		"abra doctor",
		"fully restart the AI client",
		"retry before syncing",
		"abra agent verify . --scope " + wantScope + " --agent claude",
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
				"result":  map[string]any{"tools": requiredMCPToolFixtures()},
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
				if args["agent"] != "codex" {
					t.Fatalf("working_memory_compose agent = %#v, want codex", args["agent"])
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
	for _, want := range []string{
		"AGENTS.md",
		"warn  CLAUDE.md",
		"scope_discovery",
		"working_memory",
		"Ready",
		wantScope,
		"Next:",
		"Give the ready_prompt to the AI client.",
		"fully restart that client",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("verify output missing %q:\n%s", want, output)
		}
	}
}

func TestAgentsBootstrapIngestsAndVerifiesContext(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "README.md"), "# Demo\n\nAgents should use Abra before changing code.\n")
	wantScope := "repo:" + slug(filepath.Base(root))
	ingestRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sources/configs":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode source config: %v", err)
			}
			if body["scope"] != wantScope {
				t.Fatalf("source scope = %v, want %s", body["scope"], wantScope)
			}
			if body["authority"] != "source-code" {
				t.Fatalf("source authority = %v, want source-code", body["authority"])
			}
			writeTestJSON(t, w, map[string]any{
				"source_config_id": "source-bootstrap",
				"source_config":    map[string]any{"id": "source-bootstrap", "scope": wantScope},
			})
		case "/ingestion/jobs":
			ingestRequests++
			if r.Method == http.MethodPost {
				writeTestJSON(t, w, map[string]any{
					"ingestion_job": map[string]any{"id": "job-bootstrap", "status": "queued"},
				})
				return
			}
			writeTestJSON(t, w, map[string]any{
				"ingestion_jobs": []map[string]any{{
					"id":                "job-bootstrap",
					"status":            "succeeded",
					"documents_seen":    1,
					"documents_changed": 1,
					"chunks_written":    1,
					"claims_written":    1,
				}},
			})
		case "/mcp":
			var rpc map[string]any
			if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
				t.Fatalf("decode rpc: %v", err)
			}
			switch rpc["method"] {
			case "tools/list":
				writeTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      rpc["id"],
					"result":  map[string]any{"tools": requiredMCPToolFixtures()},
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
					if args["agent"] != "codex" {
						t.Fatalf("working_memory_compose agent = %#v, want codex", args["agent"])
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
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"agents", "bootstrap", root, "--base-url", server.URL, "--token", "test-token", "--no-mcp"}); err != nil {
			t.Fatalf("agents bootstrap error = %v", err)
		}
	})
	if ingestRequests == 0 {
		t.Fatal("bootstrap did not ingest any documents")
	}
	for _, want := range []string{"Bootstrapping Abra agent context", wantScope, "Ingesting repo", "working_memory", "MCP install skipped", "Ready prompt"} {
		if !strings.Contains(output, want) {
			t.Fatalf("bootstrap output missing %q:\n%s", want, output)
		}
	}
	for _, file := range []string{"AGENTS.md", "CLAUDE.md"} {
		if !fileExists(filepath.Join(root, file)) {
			t.Fatalf("bootstrap did not create %s", file)
		}
	}
}

func TestAgentsBootstrapInstallsCodexMCPBeforeFinalVerify(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "README.md"), "# Demo\n\nAbra keeps AI agents source-backed.")
	wantScope := "repo:" + slug(filepath.Base(root))

	binDir := t.TempDir()
	logPath := filepath.Join(binDir, "commands.log")
	codexPath := filepath.Join(binDir, "codex")
	mustWrite(t, codexPath, "#!/bin/sh\nprintf 'codex %s\\n' \"$*\" >> "+shellQuote(logPath)+"\nif [ \"$1 $2\" = 'mcp list' ]; then printf 'abra http://127.0.0.1:18080/mcp\\n'; fi\nexit 0\n")
	if err := os.Chmod(codexPath, 0o755); err != nil {
		t.Fatal(err)
	}
	launchctlPath := filepath.Join(binDir, "launchctl")
	mustWrite(t, launchctlPath, "#!/bin/sh\nprintf 'launchctl %s\\n' \"$*\" >> "+shellQuote(logPath)+"\nif [ \"$1\" = 'getenv' ]; then printf 'test-token\\n'; fi\nexit 0\n")
	if err := os.Chmod(launchctlPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABRA_CODEX_COMMAND", codexPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ingestRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sources/configs":
			writeTestJSON(t, w, map[string]any{
				"source_config_id": "source-bootstrap",
				"source_config":    map[string]any{"id": "source-bootstrap", "scope": wantScope},
			})
		case "/ingestion/jobs":
			ingestRequests++
			if r.Method == http.MethodPost {
				writeTestJSON(t, w, map[string]any{
					"ingestion_job": map[string]any{"id": "job-bootstrap", "status": "queued"},
				})
				return
			}
			writeTestJSON(t, w, map[string]any{
				"ingestion_jobs": []map[string]any{{
					"id":                "job-bootstrap",
					"status":            "succeeded",
					"documents_seen":    1,
					"documents_changed": 1,
					"chunks_written":    1,
					"claims_written":    1,
				}},
			})
		case "/mcp":
			var rpc map[string]any
			if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
				t.Fatalf("decode rpc: %v", err)
			}
			switch rpc["method"] {
			case "tools/list":
				writeTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      rpc["id"],
					"result":  map[string]any{"tools": requiredMCPToolFixtures()},
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
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"agents", "bootstrap", root, "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("agents bootstrap error = %v", err)
		}
	})
	if ingestRequests == 0 {
		t.Fatal("bootstrap did not ingest any documents")
	}
	installIndex := strings.Index(output, "Installing Abra MCP into Codex")
	verifyIndex := strings.Index(output, "Verifying source-backed working memory")
	if installIndex < 0 || verifyIndex < 0 || installIndex > verifyIndex {
		t.Fatalf("bootstrap should install MCP before final verify:\n%s", output)
	}
	if !strings.Contains(output, "Ready: server and Codex MCP config can use scope") {
		t.Fatalf("bootstrap should finish with server/client ready output:\n%s", output)
	}
	if !strings.Contains(output, "Codex MCP config was updated by the CLI; no manual Codex config editing is required.") {
		t.Fatalf("bootstrap should explain operator-managed Codex config install:\n%s", output)
	}
	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logBytes)
	wantCommands := []string{"codex mcp add abra"}
	if runtime.GOOS == "darwin" {
		wantCommands = append(wantCommands, "launchctl setenv", "launchctl getenv")
	}
	for _, want := range wantCommands {
		if !strings.Contains(logText, want) {
			t.Fatalf("command log missing %q:\n%s", want, logText)
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
	for _, want := range []string{
		"ok  AGENTS.md",
		"ok  CLAUDE.md",
		"skip  mcp skipped by --files-only",
		"Ready: agent instruction files are ready",
		"Next:",
		"Run `abra agent verify " + shellQuote(root) + " --scope " + shellQuote(wantScope) + " --agent 'codex'` against a live Abra MCP server",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("files-only verify output missing %q:\n%s", want, output)
		}
	}
}

func TestAgentsVerifyIncludesCodexClientAdvisory(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	wantScope := "repo:" + slug(filepath.Base(root))
	if err := run(context.Background(), []string{"agents", "init", root, "--agent", "codex"}); err != nil {
		t.Fatalf("agents init error = %v", err)
	}
	codexPath := filepath.Join(t.TempDir(), "codex")
	mustWrite(t, codexPath, "#!/bin/sh\nif [ \"$1 $2\" = 'mcp list' ]; then printf 'abra http://127.0.0.1:18080/mcp\\n'; exit 0; fi\nexit 1\n")
	if err := os.Chmod(codexPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABRA_CODEX_COMMAND", codexPath)
	t.Setenv("ABRA_API_TOKEN", "")
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
				"result":  map[string]any{"tools": requiredMCPToolFixtures()},
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
	for _, want := range []string{"Ready: Abra server can use scope", "AI client readiness has", "warn  codex_mcp_client", "ABRA_API_TOKEN is not set", "Fix the client warning(s) above"} {
		if !strings.Contains(output, want) {
			t.Fatalf("verify output missing %q:\n%s", want, output)
		}
	}
}

func TestAgentsVerifyJSONSeparatesServerAndClientReadiness(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	wantScope := "repo:" + slug(filepath.Base(root))
	if err := run(context.Background(), []string{"agents", "init", root, "--agent", "codex"}); err != nil {
		t.Fatalf("agents init error = %v", err)
	}
	codexPath := filepath.Join(t.TempDir(), "codex")
	mustWrite(t, codexPath, "#!/bin/sh\nif [ \"$1 $2\" = 'mcp list' ]; then printf 'abra http://127.0.0.1:18080/mcp\\n'; exit 0; fi\nexit 1\n")
	if err := os.Chmod(codexPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABRA_CODEX_COMMAND", codexPath)
	t.Setenv("ABRA_API_TOKEN", "")
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
				"result":  map[string]any{"tools": requiredMCPToolFixtures()},
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
		if err := run(context.Background(), []string{"agents", "verify", root, "--json", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("agents verify --json error = %v", err)
		}
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("decode verify json: %v\n%s", err, output)
	}
	if payload["ok"] != true || payload["server_ready"] != true || payload["client_ready"] != false || payload["agent_ready"] != false || intValue(payload["client_warnings"]) == 0 {
		t.Fatalf("readiness payload = %#v", payload)
	}
	nextSteps, _ := payload["next_steps"].([]any)
	if len(nextSteps) == 0 || !strings.Contains(stringValue(nextSteps[0], ""), "Fix the client warning") {
		t.Fatalf("next steps should prioritize client warning: %#v", payload["next_steps"])
	}
}

func TestAgentReadinessSummarySeparatesClientWarnings(t *testing.T) {
	checks := []map[string]any{
		{"name": "AGENTS.md", "ok": true},
		{"name": "mcp", "ok": true},
		{"name": "working_memory", "ok": true},
		{"name": "codex_mcp_client", "ok": true, "advisory": true, "client_ok": false, "level": "warn"},
	}
	serverReady, clientReady, clientWarnings := agentReadinessSummary(checks, false)
	if !serverReady || clientReady || clientWarnings != 1 {
		t.Fatalf("readiness = server:%t client:%t warnings:%d", serverReady, clientReady, clientWarnings)
	}
	if !checksOK(checks, false) || checksOK(checks, true) {
		t.Fatalf("checksOK should pass non-strict and fail strict for advisory warning")
	}
}

func TestAgentVerifyNextStepsAvoidIngestForMCPToolErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		checks []map[string]any
	}{
		{
			name: "scope discovery rpc error",
			checks: []map[string]any{
				{"name": "AGENTS.md", "ok": true},
				{"name": "mcp", "ok": true},
				{"name": "scope_discovery", "ok": false, "error": "jsonrpc error"},
			},
		},
		{
			name: "working memory rpc error",
			checks: []map[string]any{
				{"name": "AGENTS.md", "ok": true},
				{"name": "mcp", "ok": true},
				{"name": "scope_discovery", "ok": true},
				{"name": "working_memory", "ok": false, "error": "jsonrpc error"},
			},
		},
		{
			name: "mcp unavailable",
			checks: []map[string]any{
				{"name": "AGENTS.md", "ok": true},
				{"name": "mcp", "ok": false, "error": "connection refused"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := agentVerifyNextSteps("/repo", "repo:demo", "codex", false, false, tc.checks)
			joined := strings.Join(steps, "\n")
			if strings.Contains(joined, "abra ingest") || strings.Contains(joined, "abra sync") {
				t.Fatalf("tool readiness error should not suggest sync:\n%s", joined)
			}
			if !strings.Contains(joined, "abra doctor") {
				t.Fatalf("tool readiness error should suggest doctor:\n%s", joined)
			}
			if !strings.Contains(joined, "abra agent install codex") {
				t.Fatalf("tool readiness error should suggest MCP/token repair:\n%s", joined)
			}
		})
	}
}

func TestAgentVerifyNextStepsSuggestIngestOnlyForMissingMemory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		checks []map[string]any
	}{
		{
			name: "scope missing",
			checks: []map[string]any{
				{"name": "AGENTS.md", "ok": true},
				{"name": "mcp", "ok": true},
				{"name": "scope_discovery", "ok": false, "detail": "scope missing"},
			},
		},
		{
			name: "working memory empty",
			checks: []map[string]any{
				{"name": "AGENTS.md", "ok": true},
				{"name": "mcp", "ok": true},
				{"name": "scope_discovery", "ok": true},
				{"name": "working_memory", "ok": false, "detail": "facts=0 documents=0 summaries=0 graph=0"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := agentVerifyNextSteps("/repo", "repo:demo", "codex", false, false, tc.checks)
			joined := strings.Join(steps, "\n")
			if !strings.Contains(joined, "abra sync") {
				t.Fatalf("missing memory should suggest sync:\n%s", joined)
			}
			if !strings.Contains(joined, "only because verify proved") {
				t.Fatalf("sync step should be conditional and evidence-based:\n%s", joined)
			}
		})
	}
}

func TestAgentsVerifyUsesSelectedAgent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	wantScope := "repo:" + slug(filepath.Base(root))
	if err := run(context.Background(), []string{"agents", "init", root, "--agent", "claude"}); err != nil {
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
				"result":  map[string]any{"tools": requiredMCPToolFixtures()},
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
				if args["agent"] != "claude" {
					t.Fatalf("working_memory_compose agent = %#v, want claude", args["agent"])
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
		if err := run(context.Background(), []string{"agents", "verify", root, "--agent", "claude", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("agents verify error = %v", err)
		}
	})
	if !strings.Contains(output, `agent="claude"`) || strings.Contains(output, "codex_mcp_client") {
		t.Fatalf("claude verify output did not use selected agent cleanly:\n%s", output)
	}
	if !strings.Contains(output, "abra agent verify "+shellQuote(root)+" --scope "+shellQuote(wantScope)+" --agent "+shellQuote("claude")) {
		t.Fatalf("claude verify next steps should preserve selected agent:\n%s", output)
	}
}

func TestAgentsVerifyJSONIncludesReadyPromptAndNextSteps(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	wantScope := "repo:" + slug(filepath.Base(root))
	if err := run(context.Background(), []string{"agents", "init", root, "--agent", "codex"}); err != nil {
		t.Fatalf("agents init error = %v", err)
	}

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"agents", "ready", root, "--scope", wantScope, "--files-only", "--json"}); err != nil {
			t.Fatalf("agents ready --json error = %v", err)
		}
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("decode agents ready json: %v\n%s", err, output)
	}
	if payload["ok"] != true || payload["scope"] != wantScope {
		t.Fatalf("payload = %#v", payload)
	}
	readyPrompt := stringValue(payload["ready_prompt"], "")
	for _, want := range []string{wantScope, "discover_scopes", "working_memory_compose", "task=<current task>", `agent="codex"`, "Abra MCP tools are unavailable", "abra doctor", "agent_ready=false", "source-backed memory is empty", "abra agent verify . --scope " + wantScope + " --agent codex"} {
		if !strings.Contains(readyPrompt, want) {
			t.Fatalf("ready_prompt missing %q:\n%s", want, readyPrompt)
		}
	}
	nextSteps, _ := payload["next_steps"].([]any)
	if len(nextSteps) == 0 || !strings.Contains(stringValue(nextSteps[0], ""), "abra agent verify") {
		t.Fatalf("next_steps = %#v", payload["next_steps"])
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

func TestAgentsReadyIsNonMutatingVerifyAlias(t *testing.T) {
	root := filepath.Join(t.TempDir(), "empty project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{"agents", "ready", root, "--files-only", "--strict"})
		if err == nil || !strings.Contains(err.Error(), "agent instruction verification failed") {
			t.Fatalf("agents ready error = %v", err)
		}
	})
	if fileExists(filepath.Join(root, "AGENTS.md")) || fileExists(filepath.Join(root, "CLAUDE.md")) {
		t.Fatalf("agents ready should not create instruction files:\n%s", output)
	}
	for _, want := range []string{"Agent context check", "fail  AGENTS.md", "skip  mcp skipped by --files-only"} {
		if !strings.Contains(output, want) {
			t.Fatalf("agents ready output missing %q:\n%s", want, output)
		}
	}
}

func TestAgentsBootstrapNonCodexSkipsCodexInstall(t *testing.T) {
	root := filepath.Join(t.TempDir(), "demo project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "README.md"), "# Demo\n\nAgents should use Abra before changing code.\n")
	wantScope := "repo:" + slug(filepath.Base(root))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sources/configs":
			writeTestJSON(t, w, map[string]any{
				"source_config_id": "source-bootstrap",
				"source_config":    map[string]any{"id": "source-bootstrap", "scope": wantScope},
			})
		case "/ingestion/jobs":
			if r.Method == http.MethodPost {
				writeTestJSON(t, w, map[string]any{
					"ingestion_job": map[string]any{"id": "job-bootstrap", "status": "queued"},
				})
				return
			}
			writeTestJSON(t, w, map[string]any{
				"ingestion_jobs": []map[string]any{{
					"id":                "job-bootstrap",
					"status":            "succeeded",
					"documents_seen":    1,
					"documents_changed": 1,
					"chunks_written":    1,
					"claims_written":    1,
				}},
			})
		case "/mcp":
			var rpc map[string]any
			if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
				t.Fatalf("decode rpc: %v", err)
			}
			switch rpc["method"] {
			case "tools/list":
				writeTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      rpc["id"],
					"result":  map[string]any{"tools": requiredMCPToolFixtures()},
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
					if args["agent"] != "claude" {
						t.Fatalf("working_memory_compose agent = %#v, want claude", args["agent"])
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
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	codexPath := filepath.Join(t.TempDir(), "codex")
	mustWrite(t, codexPath, "#!/bin/sh\nprintf 'codex should not be called\\n' >&2\nexit 99\n")
	if err := os.Chmod(codexPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ABRA_CODEX_COMMAND", codexPath)

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"agents", "bootstrap", root, "--agent", "claude", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("agents bootstrap error = %v", err)
		}
	})
	for _, want := range []string{"Automatic MCP install is currently Codex-only", "abra mcp > .tmp/abra.mcp.json", `agent="claude"`} {
		if !strings.Contains(output, want) {
			t.Fatalf("bootstrap output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "Installing Abra MCP into Codex") {
		t.Fatalf("non-codex bootstrap attempted Codex install:\n%s", output)
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
				"result":  map[string]any{"tools": requiredMCPToolFixtures()},
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
	if !strings.Contains(output, "fail  scope_discovery") || !strings.Contains(output, "abra sync") || !strings.Contains(output, "Next:") {
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
				"result":  map[string]any{"tools": requiredMCPToolFixtures()},
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
				if args["agent"] != "codex" {
					t.Fatalf("working_memory_compose agent = %#v, want codex", args["agent"])
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
	for _, want := range []string{"ok  scope_discovery", "fail  working_memory", "facts=0 documents=0 summaries=0 graph=0", "Next:", "abra sync", "--scope " + wantScope} {
		if !strings.Contains(output, want) {
			t.Fatalf("verify output missing %q:\n%s", want, output)
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
			"result":  map[string]any{"tools": requiredMCPToolFixtures()},
		})
	}))
	defer server.Close()

	count, err := validateMCPTools(context.Background(), parseArgs([]string{"mcp", "--base-url", server.URL, "--token", "test-token"}))
	if err != nil {
		t.Fatalf("validateMCPTools error = %v", err)
	}
	if count != len(requiredMCPToolNames()) {
		t.Fatalf("tool count = %d, want %d", count, len(requiredMCPToolNames()))
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

func TestInstallCodexMutatesConfigAfterSuccessfulMCPValidation(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "commands.log")
	writeFake := func(name string) string {
		path := filepath.Join(root, name)
		mustWrite(t, path, "#!/bin/sh\nprintf '"+name+" %s\\n' \"$*\" >> "+shellQuote(logPath)+"\nexit 0\n")
		if err := os.Chmod(path, 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}
	codexPath := writeFake("codex")
	writeFake("launchctl")
	t.Setenv("ABRA_CODEX_COMMAND", codexPath)
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))

	const tokenEnv = "ABRA_TEST_CODEX_TOKEN"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/mcp" {
			t.Fatalf("request = %s %s, want POST /mcp", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("authorization"); got != "Bearer test-token" {
			t.Fatalf("authorization = %q", got)
		}
		var rpc map[string]any
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode mcp request: %v", err)
		}
		if rpc["method"] != "tools/list" {
			t.Fatalf("mcp method = %#v", rpc["method"])
		}
		writeTestJSON(t, w, map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  map[string]any{"tools": requiredMCPToolFixtures()},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"mcp", "install-codex", "--base-url", server.URL, "--token", "test-token", "--token-env", tokenEnv}); err != nil {
			t.Fatalf("install-codex error = %v", err)
		}
	})
	if os.Getenv(tokenEnv) != "test-token" {
		t.Fatalf("%s was not set", tokenEnv)
	}
	for _, want := range []string{
		"Installed Abra MCP for Codex future launches:",
		"token env: " + tokenEnv,
		fmt.Sprintf("endpoint:  validated (%d tools)", len(requiredMCPToolNames())),
		"Active Codex sessions will not see this until you fully quit and reopen Codex Desktop.",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(logBytes)
	wants := []string{
		"codex mcp list",
		"codex mcp remove abra",
		"codex mcp add abra --url " + server.URL + "/mcp --bearer-token-env-var " + tokenEnv,
	}
	last := -1
	for _, want := range wants {
		idx := strings.Index(logText, want)
		if idx < 0 {
			t.Fatalf("command log missing %q:\n%s", want, logText)
		}
		if idx < last {
			t.Fatalf("command %q ran out of order:\n%s", want, logText)
		}
		last = idx
	}
}
