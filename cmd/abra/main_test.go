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
	for _, command := range []string{"config", "ingest", "setup", "models", "watch", "sources", "jobs", "observe", "observations", "scope", "agents", "mcp"} {
		t.Run(command, func(t *testing.T) {
			if err := run(context.Background(), []string{command, "--help"}); err != nil {
				t.Fatalf("run(%s --help) error = %v", command, err)
			}
		})
	}
}

func TestObservePostsObservation(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/observations" {
			t.Fatalf("request = %s %s, want POST /observations", r.Method, r.URL.Path)
		}
		if r.Header.Get("authorization") != "Bearer test-token" {
			t.Fatalf("authorization = %q", r.Header.Get("authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeTestJSON(t, w, map[string]any{"observation": map[string]any{
			"id":               "obs-1",
			"scope":            got["scope"],
			"observation_type": got["observation_type"],
			"status":           got["status"],
		}})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"observe", "Agents should rerun release checks",
			"--scope", "repo:demo",
			"--base-url", server.URL,
			"--token", "test-token",
			"--type", "episode",
			"--source-url", "file://notes.md",
			"--confidence", "0.7",
		}); err != nil {
			t.Fatalf("observe error = %v", err)
		}
	})
	if got["scope"] != "repo:demo" || got["observation_text"] != "Agents should rerun release checks" || got["observation_type"] != "episode" {
		t.Fatalf("observe body = %#v", got)
	}
	if got["source_url"] != "file://notes.md" || got["created_by"] != "abra-cli" {
		t.Fatalf("observe lineage = %#v", got)
	}
	if !strings.Contains(output, "Observation captured: obs-1") || !strings.Contains(output, "trusted: no") {
		t.Fatalf("observe output = %s", output)
	}
}

func TestObserveRequiresText(t *testing.T) {
	err := run(context.Background(), []string{"observe", "--scope", "repo:demo"})
	if err == nil || !strings.Contains(err.Error(), "observe requires text") {
		t.Fatalf("err = %v, want observe requires text", err)
	}
}

func TestListObservationsUsesScopedQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/observations" {
			t.Fatalf("request = %s %s, want GET /observations", r.Method, r.URL.Path)
		}
		query := r.URL.Query()
		for key, want := range map[string]string{
			"scope":  "repo:demo",
			"query":  "release",
			"type":   "episode",
			"status": "raw",
			"since":  "2026-06-20T00:00:00Z",
			"limit":  "3",
		} {
			if got := query.Get(key); got != want {
				t.Fatalf("query %s = %q, want %q; full query %s", key, got, want, r.URL.RawQuery)
			}
		}
		writeTestJSON(t, w, map[string]any{"observations": []map[string]any{{
			"id":               "obs-1",
			"observed_at":      "2026-06-20 00:00:00+00",
			"observation_type": "episode",
			"status":           "raw",
			"observation_text": "release check note",
		}}})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"observations", "release",
			"--scope", "repo:demo",
			"--base-url", server.URL,
			"--token", "test-token",
			"--type", "episode",
			"--status", "raw",
			"--since", "2026-06-20T00:00:00Z",
			"--limit", "3",
		}); err != nil {
			t.Fatalf("observations error = %v", err)
		}
	})
	if !strings.Contains(output, "Observations: 1") || !strings.Contains(output, "obs-1") || !strings.Contains(output, "release check note") {
		t.Fatalf("observations output = %s", output)
	}
}

func TestProposeObservationPostsLearningProposal(t *testing.T) {
	var got map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/learning/proposals" {
			t.Fatalf("request = %s %s, want POST /learning/proposals", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeTestJSON(t, w, map[string]any{"learning_proposal": map[string]any{
			"id":            "lp-1",
			"scope":         got["scope"],
			"proposal_type": got["proposal_type"],
			"target_type":   got["target_type"],
			"target_id":     got["target_id"],
			"status":        "pending",
		}})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"observations", "propose", "obs-1",
			"--scope", "repo:demo",
			"--base-url", server.URL,
			"--token", "test-token",
			"--claim", "Agents should rerun release checks before tagging.",
			"--source-url", "file://release-runbook.md",
			"--confidence", "0.7",
		}); err != nil {
			t.Fatalf("observations propose error = %v", err)
		}
	})
	if got["scope"] != "repo:demo" || got["proposal_type"] != "claim" || got["target_type"] != "observation" || got["target_id"] != "obs-1" {
		t.Fatalf("proposal body = %#v", got)
	}
	if got["source_url"] != "file://release-runbook.md" {
		t.Fatalf("source_url = %#v", got["source_url"])
	}
	payload, _ := got["payload"].(map[string]any)
	if payload["observation_id"] != "obs-1" || payload["claim"] != "Agents should rerun release checks before tagging." || payload["promotion_flow"] != "observation_to_claim" {
		t.Fatalf("payload = %#v", payload)
	}
	if !strings.Contains(output, "Observation proposed: lp-1") || !strings.Contains(output, "trusted: no") {
		t.Fatalf("output = %s", output)
	}
}

func TestSetupAndUpHelpMentionModelAutomation(t *testing.T) {
	setupHelp := commandUsage("setup")
	for _, want := range []string{"abra setup --yes", "abra setup --yes --no-models", "--skip-models"} {
		if !strings.Contains(setupHelp, want) {
			t.Fatalf("setup help missing %q:\n%s", want, setupHelp)
		}
	}
	upHelp := commandUsage("up")
	for _, want := range []string{"abra up [--no-models]", "starts the default local Qwen", "--no-models"} {
		if !strings.Contains(upHelp, want) {
			t.Fatalf("up help missing %q:\n%s", want, upHelp)
		}
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
		case "/ingest/documents":
			ingestRequests++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode ingest: %v", err)
			}
			if body["scope"] != wantScope {
				t.Fatalf("ingest scope = %v, want %s", body["scope"], wantScope)
			}
			writeTestJSON(t, w, map[string]any{"document_id": "doc"})
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
	for _, want := range []string{"Bootstrapping Abra agent context", wantScope, "Ingesting repo", "working_memory", "Codex MCP install skipped", "Ready prompt"} {
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
		"Run `abra agents verify " + shellQuote(root) + " --scope " + shellQuote(wantScope) + "` against a live Abra MCP server",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("files-only verify output missing %q:\n%s", want, output)
		}
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
	for _, want := range []string{wantScope, "discover_scopes", "working_memory_compose", "source-backed context"} {
		if !strings.Contains(readyPrompt, want) {
			t.Fatalf("ready_prompt missing %q:\n%s", want, readyPrompt)
		}
	}
	nextSteps, _ := payload["next_steps"].([]any)
	if len(nextSteps) == 0 || !strings.Contains(stringValue(nextSteps[0], ""), "abra agents verify") {
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
	if !strings.Contains(output, "fail  scope_discovery") || !strings.Contains(output, "abra ingest . --code --scope") || !strings.Contains(output, "Next:") {
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
	for _, want := range []string{"ok  scope_discovery", "fail  working_memory", "facts=0 documents=0 summaries=0 graph=0", "Next:", "abra ingest . --code --scope " + wantScope} {
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
			{
				"name":   "codex_mcp_client",
				"ok":     false,
				"detail": "ABRA_API_TOKEN is not set",
				"hint":   "run: abra mcp install-codex",
				"next": []string{
					"abra mcp install-codex",
					"for terminal Codex: set -a; source .tmp/quickstart.env; set +a; codex",
				},
			},
		}); err != nil {
			t.Fatalf("printDoctor error = %v", err)
		}
	})
	for _, want := range []string{"ok  model_config", "info provider=local model=embed", "warn  codex_mcp_client", "hint run: abra mcp install-codex", "next", "for terminal Codex: set -a"} {
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

func TestSetupStackArgsDropProviderBaseURL(t *testing.T) {
	args := parseArgs([]string{
		"setup",
		"--compatible",
		"--base-url", "https://models.example.invalid/v1",
		"--embedding-base-url", "https://models.example.invalid/v2",
		"--token", "test-token",
		"--env-file", ".tmp/custom.env",
	})
	stackArgs := setupStackArgs(args)
	if got := flag(stackArgs, "base-url", ""); got != "" {
		t.Fatalf("stack base-url = %q, want empty", got)
	}
	if got := flag(stackArgs, "embedding-base-url", ""); got != "https://models.example.invalid/v2" {
		t.Fatalf("embedding-base-url = %q", got)
	}
	if cfg(stackArgs).Token != "test-token" || cfg(stackArgs).EnvFile != ".tmp/custom.env" {
		t.Fatalf("stack args lost runtime flags: %+v", cfg(stackArgs))
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
	for _, want := range []string{"abra up --env-file", "abra agents init --agent codex", "abra agents verify"} {
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

func TestSetupProductionGuidesCompatibleProvider(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"setup", "--production", "--no-start"}); err != nil {
			t.Fatalf("setup production error = %v", err)
		}
	})
	for _, want := range []string{
		"Production env created.",
		"abra config model compatible",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=true",
		"abra up --env-file",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("production setup output missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "abra config model local") {
		t.Fatalf("production setup should not recommend local model by default:\n%s", output)
	}
}

func TestConfigModelLocalPersistsRunnerControls(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{
		"config", "model", "local",
		"--runner-image", "registry.example/llama.cpp@sha256:abc123",
		"--pull-policy", "never",
		"--readiness-timeout", "45s",
	}); err != nil {
		t.Fatalf("config model local error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["ABRA_LOCAL_EMBEDDING_IMAGE"] != "registry.example/llama.cpp@sha256:abc123" {
		t.Fatalf("runner image = %q", values["ABRA_LOCAL_EMBEDDING_IMAGE"])
	}
	if values["ABRA_LOCAL_EMBEDDING_PULL_POLICY"] != "never" {
		t.Fatalf("pull policy = %q", values["ABRA_LOCAL_EMBEDDING_PULL_POLICY"])
	}
	if values["ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT"] != "45s" {
		t.Fatalf("readiness timeout = %q", values["ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT"])
	}
}

func TestSetupOpenAINonInteractiveRequiresAPIKey(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Setenv("OPENAI_API_KEY", "")
	t.Chdir(root)

	err := run(context.Background(), []string{"setup", "--yes", "--openai", "--no-start"})
	if err == nil || !strings.Contains(err.Error(), "requires an API key") {
		t.Fatalf("setup --openai error = %v", err)
	}
}

func TestSetupOpenAINonInteractiveUsesEnvAPIKey(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Chdir(root)

	if err := run(context.Background(), []string{"setup", "--yes", "--openai", "--no-start"}); err != nil {
		t.Fatalf("setup --openai error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_PROVIDER"] != "compatible" || values["EMBEDDING_API_KEY"] != "test-openai-key" {
		t.Fatalf("openai values = %#v", values)
	}
}

func TestUpAutoStartsLocalModelsOnlyForLocalProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	localEnv := filepath.Join(home, "quickstart.env")
	mustWrite(t, localEnv, "EMBEDDING_PROVIDER=local\n")
	if !shouldStartLocalModelsForUp(parseArgs([]string{"up"})) {
		t.Fatal("up should start local models when provider is local")
	}
	if shouldStartLocalModelsForUp(parseArgs([]string{"up", "--no-models"})) {
		t.Fatal("up --no-models should not start local models")
	}
	if shouldStartLocalModelsForUp(parseArgs([]string{"up", "--skip-models"})) {
		t.Fatal("up --skip-models should not start local models")
	}

	mustWrite(t, localEnv, "EMBEDDING_PROVIDER=compatible\n")
	if shouldStartLocalModelsForUp(parseArgs([]string{"up"})) {
		t.Fatal("up should not start local models for compatible providers")
	}

	mustWrite(t, localEnv, "NODE_ENV=production\nEMBEDDING_PROVIDER=local\nALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=false\n")
	if shouldStartLocalModelsForUp(parseArgs([]string{"up"})) {
		t.Fatal("up should not auto-start local models in production without explicit local-embedding override")
	}
	mustWrite(t, localEnv, "NODE_ENV=production\nEMBEDDING_PROVIDER=local\nALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION=true\n")
	if !shouldStartLocalModelsForUp(parseArgs([]string{"up"})) {
		t.Fatal("up should auto-start local models in production when local embeddings are explicitly allowed")
	}
}

func TestModelsCommandsRespectActiveCompatibleProvider(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	mustWrite(t, filepath.Join(home, "quickstart.env"), strings.Join([]string{
		"EMBEDDING_PROVIDER=compatible",
		"EMBEDDING_BASE_URL=https://models.example.com/v1",
		"EMBEDDING_MODEL=embedding-model",
		"",
	}, "\n"))

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"models", "status"}); err != nil {
			t.Fatalf("models status error = %v", err)
		}
	})
	for _, want := range []string{
		"Local embeddings: inactive",
		"provider: compatible",
		"abra config",
		"models status --force",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("models status output missing %q:\n%s", want, output)
		}
	}

	err := run(context.Background(), []string{"models", "up"})
	if err == nil || !strings.Contains(err.Error(), "EMBEDDING_PROVIDER=compatible") {
		t.Fatalf("models up error = %v", err)
	}
}

func TestConfigModelLocalRewritesLoopbackForCompose(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)

	if err := run(context.Background(), []string{"config", "model", "local", "--base-url", "http://localhost:8080/v1"}); err != nil {
		t.Fatalf("config model local error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_BASE_URL"] != "http://host.docker.internal:8080/v1" {
		t.Fatalf("embedding base url = %q", values["EMBEDDING_BASE_URL"])
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
		"Check: abra models status",
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
	if strings.Contains(message, "abra models status") {
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
		"Check: abra models status",
		"Repair: abra up",
		"Diagnose: abra doctor",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("status output missing %q:\n%s", want, output)
		}
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
		"Check: abra models status",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("waitReady error missing %q:\n%s", want, err.Error())
		}
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
			"result": map[string]any{
				"tools": []map[string]any{
					{"name": "discover_scopes"},
					{"name": "working_memory_compose"},
				},
			},
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
		"Installed Abra MCP for Codex:",
		"token env: " + tokenEnv,
		"endpoint:  validated (2 tools)",
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
	if cfg.Publish != "127.0.0.1" {
		t.Fatalf("publish = %q", cfg.Publish)
	}
	if got := localRunnerPublish(cfg); got != "127.0.0.1:8080" {
		t.Fatalf("publish binding = %q", got)
	}
	if cfg.Dims != 1024 {
		t.Fatalf("dims = %d", cfg.Dims)
	}
	if cfg.PullPolicy != "missing" {
		t.Fatalf("pull policy = %q", cfg.PullPolicy)
	}
	if cfg.ReadinessTimeout != 10*time.Second {
		t.Fatalf("readiness timeout = %s", cfg.ReadinessTimeout)
	}
	wantImage := "ghcr.io/ggml-org/llama.cpp:server"
	if cfg.Image != wantImage {
		t.Fatalf("image = %q, want %q", cfg.Image, wantImage)
	}
}

func TestEmbeddingRunnerUsesImageAndReadinessEnv(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init error = %v", err)
	}
	args := parseArgs([]string{"models", "status"})
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                     "local",
		"ABRA_LOCAL_EMBEDDING_IMAGE":             "registry.example/llama.cpp@sha256:abc123",
		"ABRA_LOCAL_EMBEDDING_PULL_POLICY":       "never",
		"ABRA_LOCAL_EMBEDDING_READINESS_TIMEOUT": "45s",
	}); err != nil {
		t.Fatalf("update env error = %v", err)
	}
	cfg := embeddingRunner(args)
	if cfg.Image != "registry.example/llama.cpp@sha256:abc123" {
		t.Fatalf("image = %q", cfg.Image)
	}
	if cfg.PullPolicy != "never" {
		t.Fatalf("pull policy = %q", cfg.PullPolicy)
	}
	if cfg.ReadinessTimeout != 45*time.Second {
		t.Fatalf("readiness timeout = %s", cfg.ReadinessTimeout)
	}
}

func TestProductionLocalRunnerRequiresDigestPinnedImage(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"init", "--production"}); err != nil {
		t.Fatalf("init production error = %v", err)
	}
	args := parseArgs([]string{"models", "up"})
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                   "local",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "true",
		"ABRA_LOCAL_EMBEDDING_IMAGE":           "ghcr.io/ggml-org/llama.cpp:server",
	}); err != nil {
		t.Fatalf("update env error = %v", err)
	}
	if err := validateLocalRunnerImagePolicy(args, embeddingRunner(args)); err == nil || !strings.Contains(err.Error(), "digest-pinned") {
		t.Fatalf("policy error = %v", err)
	}
	if err := updateEnvValues(args, map[string]string{
		"ABRA_LOCAL_EMBEDDING_IMAGE": "registry.example/llama.cpp@sha256:abc123",
	}); err != nil {
		t.Fatalf("update digest image error = %v", err)
	}
	if err := validateLocalRunnerImagePolicy(args, embeddingRunner(args)); err != nil {
		t.Fatalf("digest image policy error = %v", err)
	}
}

func TestDownStopsLocalModelsByDefault(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init error = %v", err)
	}
	args := parseArgs([]string{"down"})
	if !shouldStopLocalModelsForDown(args) {
		t.Fatal("down should stop local models for local provider")
	}
	keep := parseArgs([]string{"down", "--keep-models"})
	if shouldStopLocalModelsForDown(keep) {
		t.Fatal("down --keep-models should not stop local models")
	}
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER": "compatible",
	}); err != nil {
		t.Fatalf("update env error = %v", err)
	}
	if shouldStopLocalModelsForDown(args) {
		t.Fatal("down should not stop models by default for compatible provider")
	}
	forced := parseArgs([]string{"down", "--models"})
	if !shouldStopLocalModelsForDown(forced) {
		t.Fatal("down --models should force model stop")
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

func TestLocalRunnerConfigHashTracksRunnerFields(t *testing.T) {
	cfg := embeddingRunnerConfig{
		Container: defaultEmbeddingContainer,
		Image:     defaultTEIImage(),
		ModelID:   defaultEmbeddingModelID,
		Model:     defaultServedModelName,
		BaseURL:   "http://127.0.0.1:8080/v1",
		Publish:   defaultEmbeddingPublish,
		Port:      "8080",
		CacheDir:  "/tmp/abra-model-cache",
		Dims:      1024,
	}
	base := localRunnerConfigHash(cfg)
	if base == "" {
		t.Fatal("empty config hash")
	}
	changedDims := cfg
	changedDims.Dims = 2048
	if localRunnerConfigHash(changedDims) == base {
		t.Fatal("hash did not change after dimensions changed")
	}
	changedModel := cfg
	changedModel.ModelID = "example/model:Q4_K_M"
	if localRunnerConfigHash(changedModel) == base {
		t.Fatal("hash did not change after model id changed")
	}
	changedPublish := cfg
	changedPublish.Publish = ""
	if localRunnerConfigHash(changedPublish) == base {
		t.Fatal("hash did not change after publish address changed")
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

func TestLocalPathIngestContinueOnErrorReportsFailures(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a-ok.md"), "# Alpha\n\nAgents should use Abra.")
	mustWrite(t, filepath.Join(root, "b-fail.md"), "# Broken\n\nThis file triggers a provider failure.")
	mustWrite(t, filepath.Join(root, "c-ok.md"), "# Charlie\n\nRelease checks should pass.")

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
		sourceURL := stringValue(body["source_url"], "")
		if strings.Contains(sourceURL, "b-fail.md") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"ai provider request failed: Post \"http://host.docker.internal:8080/v1/embeddings\": dial tcp: connect: connection refused"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"document_id": "doc"})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"ingest",
			root,
			"--include", "**/*.md",
			"--continue-on-error",
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err == nil || !strings.Contains(err.Error(), "ingest completed with 1 failure") {
			t.Fatalf("error = %v, want continue-on-error summary failure", err)
		}
	})
	if len(requests) != 3 {
		t.Fatalf("requests = %d, want 3 (%#v)", len(requests), requests)
	}
	for _, want := range []string{
		"Ingested files: 2",
		"Failed files: 1",
		"b-fail.md",
		"abra models up",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
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
