package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

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

func TestObserveConversationCapturesPreferenceTurnsAndProposes(t *testing.T) {
	root := t.TempDir()
	transcript := filepath.Join(root, "conversation.md")
	mustWrite(t, transcript, strings.Join([]string{
		"User: saya lebih suka jawaban yang singkat dan langsung.",
		"Assistant: siap.",
		"User: ini cuma konteks biasa tanpa preferensi.",
	}, "\n"))

	observationRequests := []map[string]any{}
	proposalRequests := []map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/observations":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode observation body: %v", err)
			}
			observationRequests = append(observationRequests, body)
			writeTestJSON(t, w, map[string]any{"observation": map[string]any{
				"id":               "obs-1",
				"scope":            body["scope"],
				"observation_type": body["observation_type"],
				"status":           body["status"],
				"source_url":       body["source_url"],
			}})
		case "/learning/proposals":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode proposal body: %v", err)
			}
			proposalRequests = append(proposalRequests, body)
			writeTestJSON(t, w, map[string]any{"learning_proposal": map[string]any{
				"id":            "lp-1",
				"scope":         body["scope"],
				"proposal_type": body["proposal_type"],
				"target_type":   body["target_type"],
				"target_id":     body["target_id"],
				"status":        "pending",
			}})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"observe", "conversation",
			"--file", transcript,
			"--scope", "repo:demo",
			"--propose",
			"--base-url", server.URL,
			"--token", "test-token",
		}); err != nil {
			t.Fatalf("observe conversation error = %v", err)
		}
	})
	if len(observationRequests) != 1 || len(proposalRequests) != 1 {
		t.Fatalf("observations=%#v proposals=%#v", observationRequests, proposalRequests)
	}
	observation := observationRequests[0]
	if observation["observation_type"] != "preference" || observation["source_type"] != "conversation" {
		t.Fatalf("observation body = %#v", observation)
	}
	if !strings.Contains(stringValue(observation["observation_text"], ""), "lebih suka") {
		t.Fatalf("observation text = %#v", observation["observation_text"])
	}
	metadata, _ := observation["metadata"].(map[string]any)
	if metadata["adapter"] != "conversation" || metadata["role"] != "user" {
		t.Fatalf("metadata = %#v", metadata)
	}
	proposal := proposalRequests[0]
	if proposal["proposal_type"] != "claim" || proposal["target_type"] != "observation" || proposal["target_id"] != "obs-1" {
		t.Fatalf("proposal body = %#v", proposal)
	}
	if !strings.Contains(output, "Conversation observations captured: 1") || !strings.Contains(output, "trusted: no") {
		t.Fatalf("output = %s", output)
	}
}

func TestIsPreferenceTurnSkipsNegatedPreferenceMentions(t *testing.T) {
	cases := []string{
		"ini cuma konteks biasa tanpa preferensi.",
		"not a preference, just background context.",
		"no preference here, only a note.",
	}
	for _, content := range cases {
		if isPreferenceTurn(conversationTurn{Role: "user", Content: content}) {
			t.Fatalf("negated preference mention should be skipped: %q", content)
		}
	}
	if !isPreferenceTurn(conversationTurn{Role: "user", Content: "saya lebih suka jawaban singkat"}) {
		t.Fatal("positive preference was not detected")
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
	for _, want := range []string{"abra setup --yes", "abra setup --yes --no-models", "--skip-models", "not a chat model", "CLI commands only", "no manual env file editing"} {
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

func TestConfigAndMCPHelpShowCLIOnlyOnboardingPath(t *testing.T) {
	configHelp := commandUsage("config")
	for _, want := range []string{
		"connect Abra to the embedding model",
		"common local/OpenAI-compatible paths do not",
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

func TestTopLevelHelpShowsCodexOnboardingStatusFlow(t *testing.T) {
	help := usage()
	for _, want := range []string{
		"abra doctor",
		"cd /path/to/project",
		"abra scope",
		"abra agent bootstrap --agent codex",
		"fully quit and reopen Codex Desktop",
		"abra agent ready . --scope <scope-from-abra-scope> --json",
		"abra ask \"What should I know before changing this project?\"",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("top-level help missing %q:\n%s", want, help)
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
		t.Fatalf("bootstrap should explain CLI-only Codex config install:\n%s", output)
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

func TestComposeReportsNoSourceBackedContextWhenOnlyGateBlocksExist(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/memory/compose" {
			t.Fatalf("request = %s %s, want POST /memory/compose", r.Method, r.URL.Path)
		}
		writeTestJSON(t, w, map[string]any{
			"scope": "repo:demo",
			"verification": map[string]any{
				"verdict": "weak",
			},
			"agent_decision": map[string]any{
				"decision": "needs_context",
			},
			"stats": map[string]any{
				"facts":                0,
				"supporting_documents": 0,
				"summaries":            0,
				"graph_relations":      0,
				"context_blocks":       1,
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"compose", "ship a change", "--scope", "repo:demo", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("compose error = %v", err)
		}
	})
	for _, want := range []string{"context: facts=0 documents=0 summaries=0 graph=0 blocks=1", "No source-backed context found for this scope.", "abra sync . --code --scope repo:demo"} {
		if !strings.Contains(output, want) {
			t.Fatalf("compose output missing %q:\n%s", want, output)
		}
	}
}

func TestComposeHumanOutputIncludesAgentHandoff(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/memory/compose" {
			t.Fatalf("request = %s %s, want POST /memory/compose", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("decode compose request: %v", err)
		}
		writeTestJSON(t, w, map[string]any{
			"scope": "repo:demo",
			"verification": map[string]any{
				"verdict": "partial",
				"retrieval_quality": map[string]any{
					"result_count":         4,
					"unique_sources":       1,
					"low_confidence":       false,
					"low_source_diversity": true,
				},
			},
			"agent_decision": map[string]any{
				"decision":             "caution",
				"required_actions":     []any{"corroborate_with_additional_source"},
				"allowed_next_actions": []any{"inspect_validation_plan"},
			},
			"memory_health": map[string]any{
				"status": "needs_review",
				"score":  82,
				"signals": []any{
					map[string]any{"code": "source_refresh_due", "severity": "warning", "count": 2, "action": "refresh_stale_sources"},
				},
			},
			"validation_plan": []any{
				map[string]any{"command": "go test ./...", "required": true, "reason": "Go code changed"},
			},
			"suggested_steps": []any{"Refresh stale source before broad edits."},
			"context_window": map[string]any{
				"prompt": "Task: ship a change\nUse citations.",
			},
			"stats": map[string]any{
				"facts":                1,
				"supporting_documents": 1,
				"summaries":            1,
				"graph_relations":      1,
				"context_blocks":       4,
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"compose", "ship a change", "--scope", "repo:demo", "--prompt", "--persist-learning", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("compose error = %v", err)
		}
	})
	if requestBody["persist_learning"] != true {
		t.Fatalf("persist_learning = %#v, want true", requestBody["persist_learning"])
	}
	for _, want := range []string{
		"Compose: partial / caution",
		"health signals:",
		"source_refresh_due (warning, count=2) -> refresh_stale_sources",
		"retrieval: results=4 sources=1 low_confidence=false low_source_diversity=true",
		"required actions:",
		"corroborate_with_additional_source",
		"allowed next actions:",
		"inspect_validation_plan",
		"validation plan:",
		"required go test ./... - Go code changed",
		"suggested steps:",
		"Refresh stale source before broad edits.",
		"prompt-ready context:",
		"Task: ship a change",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("compose output missing %q:\n%s", want, output)
		}
	}
}

func TestMemoryStatusAndDoctor(t *testing.T) {
	signals := make([]any, 0, 9)
	for i := 1; i <= 9; i++ {
		signals = append(signals, map[string]any{
			"code":     fmt.Sprintf("signal_%02d", i),
			"severity": "warning",
			"count":    i,
			"action":   fmt.Sprintf("fix_signal_%02d", i),
		})
	}
	requests := 0
	payload := map[string]any{
		"status": "needs_review",
		"score":  72,
		"documents": map[string]any{
			"total":      3,
			"active":     2,
			"stale":      1,
			"deprecated": 1,
			"deleted":    1,
		},
		"claims": map[string]any{
			"total":                       5,
			"verified":                    4,
			"inferred":                    1,
			"unverified":                  1,
			"challenged":                  1,
			"deprecated":                  1,
			"expired":                     1,
			"stale":                       1,
			"with_evidence":               4,
			"trusted_from_code_documents": 1,
		},
		"graph": map[string]any{
			"entities":             9,
			"active_entities":      8,
			"relations":            10,
			"active_relations":     7,
			"challenged_relations": 1,
			"stale_relations":      1,
		},
		"sources": map[string]any{
			"total":   2,
			"active":  1,
			"due":     1,
			"overdue": 1,
			"error":   0,
		},
		"ingestion": map[string]any{
			"queued_jobs":        2,
			"running_jobs":       1,
			"retry_jobs":         1,
			"failed_jobs":        1,
			"stale_running_jobs": 1,
		},
		"conflicts": map[string]any{
			"total":     4,
			"open":      2,
			"reviewing": 1,
			"blocking":  1,
			"high":      1,
		},
		"learning": map[string]any{
			"total":                    6,
			"pending":                  3,
			"accepted":                 1,
			"applied":                  1,
			"rejected":                 1,
			"duplicate_pending_groups": 1,
		},
		"signals": signals,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet || r.URL.Path != "/memory/health" {
			t.Fatalf("request = %s %s, want GET /memory/health", r.Method, r.URL.Path)
		}
		if r.URL.Query().Get("scope") != "repo:demo" {
			t.Fatalf("scope query = %q, want repo:demo", r.URL.Query().Get("scope"))
		}
		writeTestJSON(t, w, payload)
	}))
	defer server.Close()

	statusOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"memory", "status", "--scope", "repo:demo", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("memory status error = %v", err)
		}
	})
	for _, want := range []string{
		"Memory: needs_review score=72",
		"scope: repo:demo",
		"documents: total=3 active=2 stale=1 deprecated=1 deleted=1",
		"claims: total=5 verified=4 inferred=1 unverified=1 challenged=1 deprecated=1 expired=1 stale=1 with_evidence=4 trusted_from_code_documents=1",
		"graph: entities=9 active_entities=8 relations=10 active_relations=7 challenged_relations=1 stale_relations=1",
		"sources: total=2 active=1 due=1 overdue=1 error=0",
		"ingestion: queued_jobs=2 running_jobs=1 retry_jobs=1 failed_jobs=1 stale_running_jobs=1",
		"conflicts: total=4 open=2 reviewing=1 blocking=1 high=1",
		"learning: total=6 pending=3 accepted=1 applied=1 rejected=1 duplicate_pending_groups=1",
		"signal_08 (warning, count=8) -> fix_signal_08",
		"- +1 more",
		"abra brain doctor --scope repo:demo",
	} {
		if !strings.Contains(statusOutput, want) {
			t.Fatalf("memory status output missing %q:\n%s", want, statusOutput)
		}
	}
	if strings.Contains(statusOutput, "signal_09") {
		t.Fatalf("memory status should truncate signals:\n%s", statusOutput)
	}

	doctorOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"memory", "doctor", "--scope", "repo:demo", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("memory doctor error = %v", err)
		}
	})
	if !strings.Contains(doctorOutput, "signal_09 (warning, count=9) -> fix_signal_09") {
		t.Fatalf("memory doctor should include all signals:\n%s", doctorOutput)
	}
	if strings.Contains(doctorOutput, "- +1 more") {
		t.Fatalf("memory doctor should not truncate signals:\n%s", doctorOutput)
	}

	jsonOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"memory", "health", "--scope", "repo:demo", "--json", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("memory health --json error = %v", err)
		}
	})
	var decoded map[string]any
	if err := json.Unmarshal([]byte(jsonOutput), &decoded); err != nil {
		t.Fatalf("decode memory health json: %v\n%s", err, jsonOutput)
	}
	if decoded["status"] != "needs_review" || intValue(decoded["score"]) != 72 {
		t.Fatalf("memory health json = %#v", decoded)
	}
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}
}

func TestMemoryStatusAndDoctorSourceDiagnostics(t *testing.T) {
	payload := map[string]any{
		"status": "critical",
		"score":  41,
		"sources": map[string]any{
			"total":             3,
			"healthy":           1,
			"unhealthy":         2,
			"refresh_due":       1,
			"custom_new_metric": 7,
			"unhealthy_sources": []any{
				map[string]any{
					"source_config_id":  "source-wiki",
					"source_type":       "mcp",
					"status":            "error",
					"last_error":        "401 unauthorized",
					"last_error_at":     "2026-06-21T02:03:04Z",
					"remediation_hints": []any{"Rotate CONFLUENCE_MCP_TOKEN.", "Retry after credentials are valid."},
				},
			},
			"remediation_hints": []any{"Run `abra sources logs <source-config-id>` for failed connector details."},
		},
		"source_diagnostics": []any{
			map[string]any{
				"id":      "source-code",
				"status":  "overdue",
				"message": "last successful refresh is older than freshness policy",
				"action":  "run abra sources sync source-code --scope repo:demo",
			},
		},
		"signals": []any{
			map[string]any{"code": "source_configs_error", "severity": "critical", "count": 1, "action": "fix source configuration or credentials and retry ingestion"},
		},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/memory/health" {
			t.Fatalf("request = %s %s, want GET /memory/health", r.Method, r.URL.Path)
		}
		writeTestJSON(t, w, payload)
	}))
	defer server.Close()

	statusOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"memory", "status", "--scope", "repo:demo", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("memory status error = %v", err)
		}
	})
	for _, want := range []string{
		"sources: total=3 healthy=1 unhealthy=2 refresh_due=1 custom_new_metric=7",
		"Run `abra brain doctor --scope repo:demo` for source diagnostics.",
	} {
		if !strings.Contains(statusOutput, want) {
			t.Fatalf("memory status output missing %q:\n%s", want, statusOutput)
		}
	}
	for _, notWant := range []string{" due=", " overdue=", " error=", "source-wiki", "401 unauthorized"} {
		if strings.Contains(statusOutput, notWant) {
			t.Fatalf("memory status output should not include %q:\n%s", notWant, statusOutput)
		}
	}

	doctorOutput := captureStdout(t, func() {
		if err := run(context.Background(), []string{"memory", "doctor", "--scope", "repo:demo", "--base-url", server.URL, "--token", "test-token"}); err != nil {
			t.Fatalf("memory doctor error = %v", err)
		}
	})
	for _, want := range []string{
		"source diagnostics:",
		"source-code (overdue): last successful refresh is older than freshness policy",
		"action: run abra sources sync source-code --scope repo:demo",
		"inspect: abra connect status 'source-code'",
		"logs: abra connect logs 'source-code' --scope 'repo:demo'",
		"source-wiki (mcp, error): 401 unauthorized",
		"last_error_at: 2026-06-21T02:03:04Z",
		"remediation_hints:",
		"Rotate CONFLUENCE_MCP_TOKEN.",
		"source remediation:",
		"Run `abra sources logs <source-config-id>` for failed connector details.",
	} {
		if !strings.Contains(doctorOutput, want) {
			t.Fatalf("memory doctor output missing %q:\n%s", want, doctorOutput)
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
	if err == nil || !strings.Contains(err.Error(), "Start Docker Desktop or OrbStack") {
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

func TestComposeConcurrencyCheckWarnsWhenLocalRecallExceedsProviderCapacity(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	envFile := filepath.Join(home, "quickstart.env")
	mustWrite(t, envFile, strings.Join([]string{
		"EMBEDDING_PROVIDER=local",
		"ABRA_AI_PROVIDER_CONCURRENCY=1",
		"ABRA_COMPOSE_RECALL_CONCURRENCY=3",
		"ABRA_COMPOSE_GRAPH_CONCURRENCY=4",
		"",
	}, "\n"))

	check := composeConcurrencyCheck(parseArgs([]string{"doctor"}))
	if check["ok"] != true {
		t.Fatalf("check = %#v", check)
	}
	if detail := stringValue(check["detail"], ""); !strings.Contains(detail, "recall=3") || !strings.Contains(detail, "local provider concurrency=1") {
		t.Fatalf("detail = %q", detail)
	}
	if hint := stringValue(check["hint"], ""); !strings.Contains(hint, "ABRA_COMPOSE_RECALL_CONCURRENCY=1") {
		t.Fatalf("hint = %q", hint)
	}
}

func TestComposeConcurrencyCheckRejectsInvalidValues(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)
	envFile := filepath.Join(home, "quickstart.env")
	mustWrite(t, envFile, "EMBEDDING_PROVIDER=compatible\nABRA_COMPOSE_RECALL_CONCURRENCY=33\n")

	check := composeConcurrencyCheck(parseArgs([]string{"doctor"}))
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

func TestUpgradeVerifiesInstallScriptAttestationBeforeExecuting(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceScript := filepath.Join(root, "install.sh")
	mustWrite(t, sourceScript, "echo should-not-run\n")
	marker := filepath.Join(root, "executed")
	mustWrite(t, filepath.Join(bin, "curl"), `#!/bin/sh
out=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    out="$1"
  fi
  shift
done
cp "$TEST_INSTALL_SCRIPT" "$out"
`)
	mustWrite(t, filepath.Join(bin, "sh"), `#!/bin/sh
printf executed > "$TEST_INSTALL_MARKER"
`)
	mustWrite(t, filepath.Join(bin, "gh"), `#!/bin/sh
exit 42
`)
	for _, name := range []string{"curl", "sh", "gh"} {
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_INSTALL_SCRIPT", sourceScript)
	t.Setenv("TEST_INSTALL_MARKER", marker)
	t.Setenv("ABRA_INSTALL_SCRIPT", "https://example.invalid/install.sh")
	t.Setenv("ABRA_VERIFY_INSTALL_ATTESTATION", "1")

	err := upgrade(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if err == nil || !strings.Contains(err.Error(), "attestation verification failed for install.sh") {
		t.Fatalf("upgrade error = %v, want install.sh attestation failure", err)
	}
	if fileExists(marker) {
		t.Fatalf("install script executed before successful attestation")
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
				"result": map[string]any{"tools": []map[string]any{
					{"name": "discover_scopes"},
					{"name": "working_memory_compose"},
				}},
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

func TestPrintThinkIncludesRecoveryWhenContextIsWeak(t *testing.T) {
	output := captureStdout(t, func() {
		printThink(map[string]any{
			"answer": "No source-backed answer.",
			"scope":  "repo:demo",
			"verification": map[string]any{
				"verdict": "weak",
			},
			"agent_decision": map[string]any{
				"decision": "needs_review",
			},
		})
	})
	for _, want := range []string{
		"next:",
		"abra agent verify . --scope 'repo:demo' --json",
		"abra doctor",
		"abra sync . --code --scope 'repo:demo'",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("think recovery output missing %q:\n%s", want, output)
		}
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
	if values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"] != "6" {
		t.Fatalf("batch max items = %q", values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"] != "3000" {
		t.Fatalf("batch max tokens = %q", values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"])
	}
	if values["ABRA_AI_PROVIDER_CONCURRENCY"] != "1" {
		t.Fatalf("provider concurrency = %q", values["ABRA_AI_PROVIDER_CONCURRENCY"])
	}
	if values["WORKER_INTERVAL"] != "30s" {
		t.Fatalf("worker interval = %q", values["WORKER_INTERVAL"])
	}
	if values["RERANKER_PROVIDER"] != "none" {
		t.Fatalf("reranker provider = %q", values["RERANKER_PROVIDER"])
	}
	if values["RERANKER_BASE_URL"] != "" {
		t.Fatalf("reranker base url = %q", values["RERANKER_BASE_URL"])
	}
	if values["RERANKER_MODEL"] != "" {
		t.Fatalf("reranker model = %q", values["RERANKER_MODEL"])
	}
	if !strings.Contains(output, "Reranker: disabled by default") {
		t.Fatalf("setup output should report disabled default reranker:\n%s", output)
	}
	for _, want := range []string{
		"abra up --env-file",
		"abra doctor",
		"go run ./cmd/abra <command>",
		"Codex MCP and repo onboarding:",
		"cd /path/to/project",
		"abra agent bootstrap --agent codex   # installs Codex MCP, syncs this repo, and verifies",
		"fully quit and reopen Codex Desktop",
		"abra agent ready . --scope <scope-from-abra-scope> --json",
		"abra agent status",
		"abra agent init --agent codex",
		"abra agent verify",
		"If Codex says Abra has no context",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("setup next steps missing %q:\n%s", want, output)
		}
	}
	if !strings.Contains(output, "abra sync . --code --scope <scope-from-abra-scope>") ||
		!strings.Contains(output, `abra ask "What should I know before changing this project?" --scope <scope-from-abra-scope>`) {
		t.Fatalf("setup next steps should defer scope until after cd and abra scope:\n%s", output)
	}
	verifyIndex := strings.Index(output, "abra agent verify . --scope <scope-from-abra-scope>")
	syncIndex := strings.Index(output, "abra sync . --code --scope <scope-from-abra-scope>")
	if verifyIndex < 0 || syncIndex < 0 || verifyIndex > syncIndex {
		t.Fatalf("setup manual path should verify before conditional sync:\n%s", output)
	}
	for _, want := range []string{"run `abra agent ready . --scope <scope-from-abra-scope> --json` first", "server_ready=true but agent_ready=false", "sync only if verify reports missing scope or empty source-backed memory"} {
		if !strings.Contains(output, want) {
			t.Fatalf("setup recovery guidance missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "rerun `abra scope`, ingest") {
		t.Fatalf("setup recovery should not lead with re-ingest:\n%s", output)
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
	if values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"] != "6" {
		t.Fatalf("batch max items = %q", values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"] != "3000" {
		t.Fatalf("batch max tokens = %q", values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"])
	}
}

func TestConfigModelLocalInfersCompatibleReranker(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"config", "model", "local",
			"--reranker-base-url", "http://localhost:9998/v1",
			"--reranker-model", "custom-reranker",
			"--reranker-api-key", "reranker-key",
			"--reranker-timeout", "45s",
		}); err != nil {
			t.Fatalf("config model local error = %v", err)
		}
	})
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["RERANKER_PROVIDER"] != "compatible" {
		t.Fatalf("reranker provider = %q", values["RERANKER_PROVIDER"])
	}
	if values["RERANKER_BASE_URL"] != "http://host.docker.internal:9998/v1" {
		t.Fatalf("reranker base url = %q", values["RERANKER_BASE_URL"])
	}
	if values["RERANKER_MODEL"] != "custom-reranker" {
		t.Fatalf("reranker model = %q", values["RERANKER_MODEL"])
	}
	if values["RERANKER_API_KEY"] != "reranker-key" {
		t.Fatalf("reranker api key = %q", values["RERANKER_API_KEY"])
	}
	if values["RERANKER_TIMEOUT"] != "45s" {
		t.Fatalf("reranker timeout = %q", values["RERANKER_TIMEOUT"])
	}
	if !strings.Contains(output, "Reranker config updated: compatible custom-reranker") {
		t.Fatalf("config output missing reranker summary:\n%s", output)
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
	mustWrite(t, localEnv, "EMBEDDING_PROVIDER=qwen3\n")
	if !shouldStartLocalModelsForUp(parseArgs([]string{"up"})) {
		t.Fatal("up should start local models when provider is qwen3 alias")
	}
	mustWrite(t, localEnv, "EMBEDDING_PROVIDER=local-smart\n")
	if !shouldStartLocalModelsForUp(parseArgs([]string{"up"})) {
		t.Fatal("up should start local models when provider is local-smart alias")
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

func TestModelsCommandsTreatLocalAliasesAsActive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	mustWrite(t, filepath.Join(home, "quickstart.env"), strings.Join([]string{
		"EMBEDDING_PROVIDER=qwen3",
		"EMBEDDING_BASE_URL=http://host.docker.internal:9999/v1",
		"EMBEDDING_MODEL=alias-model",
		"EMBEDDING_DIMENSIONS=1024",
		"",
	}, "\n"))

	if err := requireLocalModelProvider(parseArgs([]string{"models", "up"}), "up"); err != nil {
		t.Fatalf("qwen3 alias should be active local provider: %v", err)
	}
	if notice := inactiveLocalModelNotice(parseArgs([]string{"models", "status"})); notice != nil {
		t.Fatalf("qwen3 alias should not be inactive: %#v", notice)
	}
	cfg := embeddingRunner(cliArgs{Flags: map[string]string{}, Bools: map[string]bool{}})
	if cfg.Model != "alias-model" {
		t.Fatalf("runner model = %q", cfg.Model)
	}
	if cfg.BaseURL != "http://127.0.0.1:9999/v1" {
		t.Fatalf("runner base url = %q", cfg.BaseURL)
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

func TestPrintReadyShowsAgentVerificationFlow(t *testing.T) {
	output := captureStdout(t, func() {
		printReady(parseArgs([]string{"up"}))
	})
	for _, want := range []string{
		"abra agent install codex",
		"abra agent bootstrap --agent codex",
		"abra agent init --agent codex",
		"abra agent verify",
		"abra sync . --code --scope <scope>",
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
		"Installed Abra MCP for Codex future launches:",
		"token env: " + tokenEnv,
		"endpoint:  validated (2 tools)",
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

func TestSetupCompatibleNoStartDoesNotSuggestLocalModels(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{"setup", "--compatible", "--base-url", "http://localhost:9999/v1", "--embedding-model", "custom-embedding", "--dimensions", "768", "--api-key", "compatible-key", "--no-start"}); err != nil {
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
	if values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"] != "16" {
		t.Fatalf("batch max items = %q", values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"] != "6000" {
		t.Fatalf("batch max tokens = %q", values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"])
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

func TestSetupCompatibleConfiguresCustomReranker(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"setup",
			"--compatible",
			"--base-url", "https://models.example/v1",
			"--embedding-model", "custom-embedding",
			"--dimensions", "768",
			"--api-key", "provider-key",
			"--reranker-base-url", "http://localhost:9998/v1",
			"--reranker-model", "custom-reranker",
			"--no-start",
		}); err != nil {
			t.Fatalf("setup compatible error = %v", err)
		}
	})
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["RERANKER_PROVIDER"] != "compatible" {
		t.Fatalf("reranker provider = %q", values["RERANKER_PROVIDER"])
	}
	if values["RERANKER_BASE_URL"] != "http://host.docker.internal:9998/v1" {
		t.Fatalf("reranker base url = %q", values["RERANKER_BASE_URL"])
	}
	if values["RERANKER_MODEL"] != "custom-reranker" {
		t.Fatalf("reranker model = %q", values["RERANKER_MODEL"])
	}
	if values["RERANKER_API_KEY"] != "provider-key" {
		t.Fatalf("reranker api key = %q", values["RERANKER_API_KEY"])
	}
	if values["RERANKER_TIMEOUT"] != "30s" {
		t.Fatalf("reranker timeout = %q", values["RERANKER_TIMEOUT"])
	}
	if !strings.Contains(output, "Reranker: compatible custom-reranker") {
		t.Fatalf("setup output missing reranker summary:\n%s", output)
	}
}

func TestSetupCompatibleNonInteractiveRequiresExplicitEndpointAndModel(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{"setup", "--compatible", "--yes", "--no-start"})
	if err == nil {
		t.Fatal("expected explicit endpoint error")
	}
	if !strings.Contains(err.Error(), "--embedding-base-url") || !strings.Contains(err.Error(), "--openai") {
		t.Fatalf("error = %v", err)
	}

	err = run(context.Background(), []string{"setup", "--compatible", "--yes", "--embedding-base-url", "http://localhost:9999/v1", "--no-start"})
	if err == nil {
		t.Fatal("expected explicit model error")
	}
	if !strings.Contains(err.Error(), "--embedding-model") || !strings.Contains(err.Error(), "--openai") {
		t.Fatalf("error = %v", err)
	}
}

func TestSetupRejectsCustomHTTPProviderSelector(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{"setup", "--provider", "custom-http", "--yes", "--no-start"})
	if err == nil {
		t.Fatal("expected unsupported setup provider error")
	}
	if !strings.Contains(err.Error(), `unknown setup embedding provider "custom-http"`) || !strings.Contains(err.Error(), "use local, compatible, or openai") {
		t.Fatalf("error = %v", err)
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

	if err := run(context.Background(), []string{"setup", "--openai", "--model", "custom-embedding", "--dimensions", "2048", "--api-key-stdin", "--no-start"}); err != nil {
		t.Fatalf("setup openai error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_MODEL"] != "custom-embedding" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["EMBEDDING_DIMENSIONS"] != "2048" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
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
		"--dimensions", "1536",
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
	if values["EMBEDDING_DIMENSIONS"] != "1536" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
	}
	if values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"] != "false" {
		t.Fatalf("local production guard = %q", values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"])
	}
	if values["ABRA_AI_PROVIDER_CONCURRENCY"] != "4" {
		t.Fatalf("provider concurrency = %q", values["ABRA_AI_PROVIDER_CONCURRENCY"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"] != "16" {
		t.Fatalf("batch max items = %q", values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"] != "6000" {
		t.Fatalf("batch max tokens = %q", values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"])
	}
}

func TestConfigModelCompatibleInfersKnownDimensions(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{
		"config",
		"model",
		"compatible",
		"--base-url", "http://localhost:9999/v1",
		"--model", defaultServedModelName,
	})
	if err != nil {
		t.Fatalf("config model compatible error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_DIMENSIONS"] != "1024" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
	}
}

func TestConfigModelCompatibleRequiresDimensionsForUnknownModel(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{
		"config",
		"model",
		"compatible",
		"--base-url", "http://localhost:9999/v1",
		"--model", "custom-embedding",
	})
	if err == nil {
		t.Fatal("expected dimensions error")
	}
	if !strings.Contains(err.Error(), "embedding dimensions are required") || !strings.Contains(err.Error(), "--dimensions") {
		t.Fatalf("error = %v", err)
	}
}

func TestConfigModelRejectsCustomHTTPSelector(t *testing.T) {
	err := run(context.Background(), []string{
		"config",
		"model",
		"custom-http",
		"--base-url", "https://provider.example/embed",
		"--model", "custom-model",
		"--dimensions", "1024",
	})
	if err == nil {
		t.Fatal("expected unsupported model config error")
	}
	if !strings.Contains(err.Error(), `unknown model config "custom-http"`) {
		t.Fatalf("error = %v", err)
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
	if values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"] != "16" {
		t.Fatalf("batch max items = %q", values["ABRA_EMBEDDING_BATCH_MAX_ITEMS"])
	}
	if values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"] != "6000" {
		t.Fatalf("batch max tokens = %q", values["ABRA_EMBEDDING_BATCH_MAX_TOKENS"])
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

func TestConfigModelCompatibleConfiguresCustomReranker(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"config",
			"model",
			"compatible",
			"--base-url", "https://models.example/v1",
			"--api-key", "embedding-key",
			"--model", "custom-embed",
			"--dimensions", "768",
			"--reranker-base-url", "http://localhost:9998/v1",
			"--reranker-model", "custom-reranker",
			"--reranker-api-key", "reranker-key",
			"--reranker-timeout", "45s",
		})
		if err != nil {
			t.Fatalf("config model compatible error = %v", err)
		}
	})
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_PROVIDER"] != "compatible" {
		t.Fatalf("provider = %q", values["EMBEDDING_PROVIDER"])
	}
	if values["RERANKER_PROVIDER"] != "compatible" {
		t.Fatalf("reranker provider = %q", values["RERANKER_PROVIDER"])
	}
	if values["RERANKER_BASE_URL"] != "http://host.docker.internal:9998/v1" {
		t.Fatalf("reranker base url = %q", values["RERANKER_BASE_URL"])
	}
	if values["RERANKER_API_KEY"] != "reranker-key" {
		t.Fatalf("reranker api key = %q", values["RERANKER_API_KEY"])
	}
	if values["RERANKER_MODEL"] != "custom-reranker" {
		t.Fatalf("reranker model = %q", values["RERANKER_MODEL"])
	}
	if values["RERANKER_TIMEOUT"] != "45s" {
		t.Fatalf("reranker timeout = %q", values["RERANKER_TIMEOUT"])
	}
	if !strings.Contains(output, "Reranker config updated: compatible custom-reranker") {
		t.Fatalf("config output missing reranker summary:\n%s", output)
	}
}

func TestConfigModelCompatibleRejectsIncompleteReranker(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{
		"config",
		"model",
		"compatible",
		"--base-url", "https://models.example/v1",
		"--model", "custom-embed",
		"--dimensions", "768",
		"--reranker-provider", "compatible",
	})
	if err == nil {
		t.Fatal("expected incomplete reranker error")
	}
	if !strings.Contains(err.Error(), "--reranker-base-url") || !strings.Contains(err.Error(), "--reranker-model") {
		t.Fatalf("error = %v", err)
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

func TestConfigModelOpenAIInfersLargeDimensions(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	err := run(context.Background(), []string{
		"config",
		"model",
		"openai",
		"--api-key", "openai-test-key",
		"--model", "text-embedding-3-large",
	})
	if err != nil {
		t.Fatalf("config model openai error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_MODEL"] != "text-embedding-3-large" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["EMBEDDING_DIMENSIONS"] != "3072" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
	}
}

func TestSetupOpenAIInfersLargeDimensions(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{
		"setup",
		"--openai",
		"--embedding-model", "text-embedding-3-large",
		"--api-key", "openai-test-key",
		"--no-start",
	}); err != nil {
		t.Fatalf("setup openai error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_MODEL"] != "text-embedding-3-large" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["EMBEDDING_DIMENSIONS"] != "3072" {
		t.Fatalf("dimensions = %q", values["EMBEDDING_DIMENSIONS"])
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
		"--dimensions", "1536",
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
	if values["RERANKER_PROVIDER"] != "none" || values["RERANKER_BASE_URL"] != "" {
		t.Fatalf("reranker fields = provider %q base %q", values["RERANKER_PROVIDER"], values["RERANKER_BASE_URL"])
	}
	if values["RERANKER_MODEL"] != "" {
		t.Fatalf("reranker model = %q", values["RERANKER_MODEL"])
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

func TestLocalRunnerStartupTimeoutUsesFlagAndEnv(t *testing.T) {
	t.Setenv("ABRA_LOCAL_MODEL_STARTUP_TIMEOUT", "3m")
	if got := localRunnerStartupTimeout(parseArgs([]string{"models", "up"})); got != 3*time.Minute {
		t.Fatalf("startup timeout from env = %s", got)
	}
	if got := localRunnerStartupTimeout(parseArgs([]string{"models", "up", "--startup-timeout", "45"})); got != 45*time.Second {
		t.Fatalf("startup timeout from seconds flag = %s", got)
	}
	if got := localRunnerStartupTimeout(parseArgs([]string{"models", "up", "--startup-timeout", "2m"})); got != 2*time.Minute {
		t.Fatalf("startup timeout from duration flag = %s", got)
	}
	if got := localRunnerStartupTimeout(parseArgs([]string{"models", "up", "--startup-timeout", "0s"})); got != 10*time.Minute {
		t.Fatalf("invalid startup timeout fallback = %s", got)
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
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "false",
		"ABRA_LOCAL_EMBEDDING_IMAGE":           "ghcr.io/ggml-org/llama.cpp:server",
	}); err != nil {
		t.Fatalf("update env error = %v", err)
	}
	if err := validateLocalRunnerImagePolicy(args, embeddingRunner(args)); err == nil || !strings.Contains(err.Error(), "explicit operator approval") {
		t.Fatalf("allow policy error = %v", err)
	}
	if err := updateEnvValues(args, map[string]string{
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "true",
	}); err != nil {
		t.Fatalf("update allow env error = %v", err)
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
	if err := updateEnvValues(args, map[string]string{
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "false",
	}); err != nil {
		t.Fatalf("update disallow env error = %v", err)
	}
	allowedArgs := parseArgs([]string{"models", "up", "--allow-production-local-embeddings"})
	if err := validateLocalRunnerImagePolicy(allowedArgs, embeddingRunner(allowedArgs)); err != nil {
		t.Fatalf("cli allow policy error = %v", err)
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
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER": "qwen3",
	}); err != nil {
		t.Fatalf("update env error = %v", err)
	}
	if !shouldStopLocalModelsForDown(args) {
		t.Fatal("down should stop local models for qwen3 alias")
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

func TestSyncLocalRunnerEnvNormalizesLocalAliases(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init error = %v", err)
	}
	args := parseArgs([]string{"models", "up"})
	if err := updateEnvValues(args, map[string]string{
		"EMBEDDING_PROVIDER":                   "local-smart",
		"EMBEDDING_BASE_URL":                   "http://host.docker.internal:9999/v1",
		"EMBEDDING_MODEL":                      "alias-model",
		"EMBEDDING_DIMENSIONS":                 "1024",
		"ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION": "true",
	}); err != nil {
		t.Fatalf("update env error = %v", err)
	}
	if err := syncLocalRunnerEnv(args); err != nil {
		t.Fatalf("sync local runner env error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["EMBEDDING_PROVIDER"] != "local" {
		t.Fatalf("provider = %q", values["EMBEDDING_PROVIDER"])
	}
	if values["EMBEDDING_MODEL"] != "alias-model" {
		t.Fatalf("model = %q", values["EMBEDDING_MODEL"])
	}
	if values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"] != "true" {
		t.Fatalf("local production guard = %q", values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"])
	}
}

func TestSyncLocalRunnerEnvCanExplicitlyAllowProductionLocalEmbeddings(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	t.Chdir(root)

	if err := run(context.Background(), []string{"init"}); err != nil {
		t.Fatalf("init error = %v", err)
	}
	args := parseArgs([]string{"models", "up", "--allow-production-local-embeddings"})
	if err := syncLocalRunnerEnv(args); err != nil {
		t.Fatalf("sync local runner env error = %v", err)
	}
	values, err := readEnvValues(filepath.Join(home, "quickstart.env"))
	if err != nil {
		t.Fatalf("read env: %v", err)
	}
	if values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"] != "true" {
		t.Fatalf("local production guard = %q", values["ALLOW_LOCAL_EMBEDDINGS_IN_PRODUCTION"])
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

func TestFriendlyProviderErrorUsesStructuredPayload(t *testing.T) {
	err := friendlyProviderError(&httpStatusError{
		Code: 401,
		Body: `{"error_kind":"provider_error"}`,
		Payload: map[string]any{
			"error_kind": "provider_error",
			"provider_error": map[string]any{
				"code":        "auth_failed",
				"status_code": float64(401),
				"retryable":   false,
				"message":     "missing authentication header",
			},
		},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "auth_failed") || !strings.Contains(err.Error(), "base URL, model, and dimensions") {
		t.Fatalf("error = %v", err)
	}
}

func TestLocalPathIngestPostsMatchedFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README.md"), "# Readme\n\nServices must use Abra before release.")
	mustWrite(t, filepath.Join(root, "src", "app.ts"), "export function route() { return '/readyz' }\n")
	mustWrite(t, filepath.Join(root, "node_modules", "ignored.md"), "# Ignored\n")

	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/documents/batch" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		request = body
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": 2,
			"documents": []map[string]any{
				{"index": 0, "document_id": "doc-readme", "source_url": "file://readme", "chunks": 1, "claims": 1},
				{"index": 1, "document_id": "doc-code", "source_url": "file://code", "chunks": 1, "claims": 0},
			},
		})
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
	rawDocs, _ := request["documents"].([]any)
	if len(rawDocs) != 2 {
		t.Fatalf("documents = %d, want 2 (%#v)", len(rawDocs), request)
	}
	first, _ := rawDocs[0].(map[string]any)
	second, _ := rawDocs[1].(map[string]any)
	if first["title"] != "Readme" {
		t.Fatalf("markdown title = %v", first["title"])
	}
	if !strings.HasPrefix(stringValue(first["source_url"], ""), "file://") {
		t.Fatalf("source_url = %v", first["source_url"])
	}
	metadata, _ := second["metadata"].(map[string]any)
	if metadata["content_kind"] != "code" || metadata["ingest_path"] != "src/app.ts" {
		t.Fatalf("code metadata = %#v", metadata)
	}
}

func TestLocalPathShortcutUsesDefaultScope(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README.md"), "# Local Brain\n\nAgents should use Abra.")

	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/documents/batch" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": 1,
			"documents": []map[string]any{
				{"index": 0, "document_id": "doc", "source_url": "file://readme", "chunks": 1, "claims": 1},
			},
		})
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
	rawDocs, _ := request["documents"].([]any)
	if len(rawDocs) != 1 {
		t.Fatalf("documents = %#v", request["documents"])
	}
	doc, _ := rawDocs[0].(map[string]any)
	if doc["scope"] != wantScope {
		t.Fatalf("scope = %v, want %s", doc["scope"], wantScope)
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
		"--max-file-bytes", "123",
		"--include-generated",
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
	if config["max_file_bytes"] != float64(123) || config["include_generated"] != true {
		t.Fatalf("file policy config = %#v", config)
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

func TestLocalPathTrackedWaitJSONWaitsForJob(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README.md"), "# Local Brain\n\nAgents should use Abra.")

	getRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sources/configs":
			_ = json.NewEncoder(w).Encode(map[string]any{"source_config_id": "source-local"})
		case "/ingestion/jobs":
			switch r.Method {
			case http.MethodPost:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ingestion_job": map[string]any{"id": "job-local", "status": "queued"},
				})
			case http.MethodGet:
				getRequests++
				_ = json.NewEncoder(w).Encode(map[string]any{
					"ingestion_jobs": []map[string]any{{
						"id":               "job-local",
						"status":           "succeeded",
						"source_config_id": "source-local",
						"documents_seen":   1,
					}},
				})
			default:
				t.Fatalf("unexpected method %s", r.Method)
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	var runErr error
	output := captureStdout(t, func() {
		runErr = run(context.Background(), []string{
			"ingest", root,
			"--tracked",
			"--wait",
			"--json",
			"--wait-timeout", "2s",
			"--base-url", server.URL,
			"--token", "test-token",
		})
	})
	if runErr != nil {
		t.Fatalf("tracked wait json ingest error = %v", runErr)
	}
	if getRequests == 0 {
		t.Fatal("ingest --tracked --wait --json did not poll job status")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("decode output: %v\n%s", err, output)
	}
	waited, _ := payload["waited_job"].(map[string]any)
	if waited["id"] != "job-local" || waited["status"] != "succeeded" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestSourceMCPQueuesSourceConfig(t *testing.T) {
	var sourceRequest map[string]any
	var jobRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sources/configs":
			if err := json.NewDecoder(r.Body).Decode(&sourceRequest); err != nil {
				t.Fatalf("decode source body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"source_config_id": "source-mcp"})
		case "/ingestion/jobs":
			if err := json.NewDecoder(r.Body).Decode(&jobRequest); err != nil {
				t.Fatalf("decode job body: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ingestion_job": map[string]any{"id": "job-mcp", "status": "queued"},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"source", "mcp",
		"--scope", "team:platform",
		"--mcp-url", "https://mcp.example.local/mcp",
		"--tool", "export_documents",
		"--arguments-json", `{"space":"ENG"}`,
		"--document-source-type", "confluence",
		"--bearer-token-env", "CONFLUENCE_MCP_TOKEN",
		"--header-env", "X-API-Key=CONFLUENCE_API_KEY,X-Team=TEAM_ENV",
		"--freshness-seconds", "600",
		"--schedule", "@every 10m",
		"--base-url", server.URL,
		"--token", "test-token",
	})
	if err != nil {
		t.Fatalf("source mcp error = %v", err)
	}
	if sourceRequest["source_type"] != "mcp" || sourceRequest["connector_kind"] != "mcp" {
		t.Fatalf("source request = %#v", sourceRequest)
	}
	if sourceRequest["base_url"] != "https://mcp.example.local/mcp" {
		t.Fatalf("base_url = %v", sourceRequest["base_url"])
	}
	config, _ := sourceRequest["config"].(map[string]any)
	if config["tool"] != "export_documents" || config["document_source_type"] != "confluence" || config["bearer_token_env"] != "CONFLUENCE_MCP_TOKEN" {
		t.Fatalf("config = %#v", config)
	}
	headerEnv, _ := config["header_env"].(map[string]any)
	if headerEnv["X-API-Key"] != "CONFLUENCE_API_KEY" || headerEnv["X-Team"] != "TEAM_ENV" {
		t.Fatalf("header_env = %#v", headerEnv)
	}
	args, _ := config["arguments"].(map[string]any)
	if args["space"] != "ENG" {
		t.Fatalf("arguments = %#v", args)
	}
	freshness, _ := sourceRequest["freshness_policy"].(map[string]any)
	if freshness["max_age_seconds"] != float64(600) {
		t.Fatalf("freshness_policy = %#v", freshness)
	}
	if sourceRequest["schedule_cron"] != "@every 10m" {
		t.Fatalf("schedule_cron = %v", sourceRequest["schedule_cron"])
	}
	if jobRequest["source_config_id"] != "source-mcp" {
		t.Fatalf("job request = %#v", jobRequest)
	}
}

func TestSourceMCPDryRunValidatesExportedDocuments(t *testing.T) {
	var rpc map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode rpc body: %v", err)
		}
		writeTestJSON(t, w, map[string]any{
			"jsonrpc": "2.0",
			"id":      rpc["id"],
			"result": map[string]any{
				"structuredContent": map[string]any{
					"documents": []map[string]any{{
						"source_type": "confluence",
						"source_url":  "https://wiki.example/pages/1",
						"title":       "Runbook",
						"content":     "Agents should cite this runbook.",
					}},
				},
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"source", "mcp",
			"--scope", "team:platform",
			"--mcp-url", server.URL + "/mcp",
			"--tool", "export_documents",
			"--document-source-type", "confluence",
			"--dry-run",
		})
		if err != nil {
			t.Fatalf("source mcp --dry-run error = %v", err)
		}
	})
	params, _ := rpc["params"].(map[string]any)
	if rpc["method"] != "tools/call" || params["name"] != "export_documents" {
		t.Fatalf("rpc = %#v", rpc)
	}
	if !strings.Contains(output, "MCP source valid: 1 document(s)") || !strings.Contains(output, "Runbook") {
		t.Fatalf("output = %s", output)
	}
}

func TestConnectorsListUsesSourceConfigs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sources/configs" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("scope") != "team:platform" || r.URL.Query().Get("limit") != "2" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		writeTestJSON(t, w, map[string]any{
			"source_configs": []map[string]any{{
				"id":             "source-mcp",
				"status":         "active",
				"source_type":    "mcp",
				"connector_kind": "mcp",
				"name":           "wiki-mcp",
			}},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"connectors", "list",
			"--scope", "team:platform",
			"--limit", "2",
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err != nil {
			t.Fatalf("connectors list error = %v", err)
		}
	})
	if !strings.Contains(output, "Connectors: 1") || !strings.Contains(output, "source-mcp  active  mcp  mcp  wiki-mcp") {
		t.Fatalf("output = %s", output)
	}
}

func TestConnectorsMCPValidateUsesSourceMCPValidation(t *testing.T) {
	var rpc map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode rpc body: %v", err)
		}
		writeTestJSON(t, w, map[string]any{
			"jsonrpc": "2.0",
			"id":      rpc["id"],
			"result": map[string]any{
				"structuredContent": map[string]any{
					"documents": []map[string]any{{
						"source_type": "confluence",
						"source_url":  "https://wiki.example/pages/1",
						"title":       "Runbook",
						"content":     "Agents should cite this runbook.",
					}},
				},
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"connectors", "mcp", "validate", server.URL + "/mcp",
			"--scope", "team:platform",
			"--tool", "export_documents",
			"--document-source-type", "confluence",
		})
		if err != nil {
			t.Fatalf("connectors mcp validate error = %v", err)
		}
	})
	params, _ := rpc["params"].(map[string]any)
	if rpc["method"] != "tools/call" || params["name"] != "export_documents" {
		t.Fatalf("rpc = %#v", rpc)
	}
	if !strings.Contains(output, "MCP source valid: 1 document(s)") || !strings.Contains(output, "Runbook") {
		t.Fatalf("output = %s", output)
	}
}

func TestConnectorsMCPInspectListsUpstreamTools(t *testing.T) {
	var rpc map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
			t.Fatalf("decode rpc body: %v", err)
		}
		writeTestJSON(t, w, map[string]any{
			"jsonrpc": "2.0",
			"id":      rpc["id"],
			"result": map[string]any{
				"tools": []map[string]any{{
					"name":        "export_documents",
					"description": "Export normalized documents",
					"inputSchema": map[string]any{"type": "object"},
				}},
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"connectors", "mcp", "inspect", server.URL + "/mcp",
			"--scope", "team:platform",
		})
		if err != nil {
			t.Fatalf("connectors mcp inspect error = %v", err)
		}
	})
	if rpc["method"] != "tools/list" {
		t.Fatalf("rpc = %#v", rpc)
	}
	if !strings.Contains(output, "MCP tools: 1") || !strings.Contains(output, "export_documents") {
		t.Fatalf("output = %s", output)
	}
}

func TestConnectorsMCPTemplateIncludesACLPassthroughHints(t *testing.T) {
	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"connectors", "mcp", "template",
			"--scope", "team:platform",
			"--connector", "confluence",
			"--owner", "platform",
		})
		if err != nil {
			t.Fatalf("connectors mcp template error = %v", err)
		}
	})
	var manifest map[string]any
	if err := json.Unmarshal([]byte(output), &manifest); err != nil {
		t.Fatalf("template output is not JSON: %v\n%s", err, output)
	}
	metadata, _ := manifest["metadata"].(map[string]any)
	if manifest["scope"] != "team:platform" || manifest["connector_kind"] != "confluence" || metadata["acl_passthrough"] != true {
		t.Fatalf("manifest = %#v", manifest)
	}
	if _, ok := metadata["acl_groups"].([]any); !ok {
		t.Fatalf("acl_groups missing from metadata: %#v", metadata)
	}
}

func TestConnectorsMCPAddInspectsValidatesAndRegisters(t *testing.T) {
	var methods []string
	var sourceRequest map[string]any
	var jobRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			var rpc map[string]any
			if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
				t.Fatalf("decode rpc body: %v", err)
			}
			method := stringValue(rpc["method"], "")
			methods = append(methods, method)
			switch method {
			case "tools/list":
				writeTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      rpc["id"],
					"result": map[string]any{
						"tools": []map[string]any{{
							"name":        "export_documents",
							"description": "Export normalized documents",
						}},
					},
				})
			case "tools/call":
				params, _ := rpc["params"].(map[string]any)
				if params["name"] != "export_documents" {
					t.Fatalf("rpc params = %#v", params)
				}
				writeTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      rpc["id"],
					"result": map[string]any{
						"structuredContent": map[string]any{
							"documents": []map[string]any{{
								"source_type": "confluence",
								"source_url":  "https://wiki.example/pages/1",
								"title":       "Runbook",
								"content":     "Agents should cite this runbook.",
								"metadata": map[string]any{
									"acl_groups": []string{"platform"},
								},
							}},
						},
					},
				})
			default:
				t.Fatalf("unexpected mcp method %s", method)
			}
		case "/sources/configs":
			if err := json.NewDecoder(r.Body).Decode(&sourceRequest); err != nil {
				t.Fatalf("decode source body: %v", err)
			}
			writeTestJSON(t, w, map[string]any{"source_config_id": "source-mcp"})
		case "/ingestion/jobs":
			if err := json.NewDecoder(r.Body).Decode(&jobRequest); err != nil {
				t.Fatalf("decode job body: %v", err)
			}
			writeTestJSON(t, w, map[string]any{
				"ingestion_job": map[string]any{"id": "job-mcp", "status": "queued"},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"connectors", "mcp", "add", server.URL + "/mcp",
			"--scope", "team:platform",
			"--connector", "confluence",
			"--document-source-type", "confluence",
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err != nil {
			t.Fatalf("connectors mcp add error = %v", err)
		}
	})
	if strings.Join(methods, ",") != "tools/list,tools/call" {
		t.Fatalf("mcp methods = %#v", methods)
	}
	config, _ := sourceRequest["config"].(map[string]any)
	if sourceRequest["connector_kind"] != "confluence" || config["tool"] != "export_documents" || config["document_source_type"] != "confluence" {
		t.Fatalf("source request = %#v", sourceRequest)
	}
	if jobRequest["source_config_id"] != "source-mcp" {
		t.Fatalf("job request = %#v", jobRequest)
	}
	for _, want := range []string{"Inspecting MCP connector", "Selected tool: export_documents", "MCP source valid: 1 document(s)", "Source configured: source-mcp"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestConnectorsMCPRegisterQueuesSourceConfig(t *testing.T) {
	var sourceRequest map[string]any
	var jobRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sources/configs":
			if err := json.NewDecoder(r.Body).Decode(&sourceRequest); err != nil {
				t.Fatalf("decode source body: %v", err)
			}
			writeTestJSON(t, w, map[string]any{"source_config_id": "source-mcp"})
		case "/ingestion/jobs":
			if err := json.NewDecoder(r.Body).Decode(&jobRequest); err != nil {
				t.Fatalf("decode job body: %v", err)
			}
			writeTestJSON(t, w, map[string]any{
				"ingestion_job": map[string]any{"id": "job-mcp", "status": "queued"},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"connectors", "mcp", "register",
			"--scope", "team:platform",
			"--mcp-url", "https://mcp.example.local/mcp",
			"--tool", "export_documents",
			"--arguments-json", `{"space":"ENG"}`,
			"--schedule", "@every 10m",
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err != nil {
			t.Fatalf("connectors mcp register error = %v", err)
		}
	})
	if sourceRequest["source_type"] != "mcp" || sourceRequest["connector_kind"] != "mcp" || sourceRequest["base_url"] != "https://mcp.example.local/mcp" {
		t.Fatalf("source request = %#v", sourceRequest)
	}
	config, _ := sourceRequest["config"].(map[string]any)
	args, _ := config["arguments"].(map[string]any)
	if config["tool"] != "export_documents" || args["space"] != "ENG" || sourceRequest["schedule_cron"] != "@every 10m" {
		t.Fatalf("source request = %#v", sourceRequest)
	}
	if jobRequest["source_config_id"] != "source-mcp" {
		t.Fatalf("job request = %#v", jobRequest)
	}
	if !strings.Contains(output, "Source configured: source-mcp") || !strings.Contains(output, "Job queued: job-mcp") {
		t.Fatalf("output = %s", output)
	}
}

func TestConnectorsMCPRegisterUsesManifestAndVerify(t *testing.T) {
	var sourceRequest map[string]any
	var recallRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sources/configs":
			if err := json.NewDecoder(r.Body).Decode(&sourceRequest); err != nil {
				t.Fatalf("decode source body: %v", err)
			}
			writeTestJSON(t, w, map[string]any{"source_config_id": "source-manifest"})
		case "/ingestion/jobs":
			writeTestJSON(t, w, map[string]any{
				"ingestion_job": map[string]any{"id": "job-manifest", "status": "queued"},
			})
		case "/recall":
			if err := json.NewDecoder(r.Body).Decode(&recallRequest); err != nil {
				t.Fatalf("decode recall body: %v", err)
			}
			writeTestJSON(t, w, map[string]any{
				"claims": []map[string]any{{
					"id":         "claim-1",
					"claim_text": "Connector runbook is searchable.",
					"source_url": "https://wiki.example/runbook",
				}},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	manifestPath := filepath.Join(t.TempDir(), "connector.json")
	mustWrite(t, manifestPath, `{
  "id": "source-manifest",
  "name": "Platform Wiki",
  "scope": "team:platform",
  "mcp_url": "https://mcp.example.local/mcp",
  "tool": "export_documents",
  "arguments": {"space": "ENG"},
  "connector_kind": "confluence",
  "document_source_type": "confluence",
  "status": "active",
  "schedule": "@every 10m",
  "verify_query": "Connector runbook",
  "metadata": {"owner": "platform"}
}`)

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"connectors", "mcp", "register",
			"--manifest", manifestPath,
			"--verify",
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err != nil {
			t.Fatalf("connectors mcp register manifest error = %v", err)
		}
	})
	if sourceRequest["id"] != "source-manifest" || sourceRequest["connector_kind"] != "confluence" || sourceRequest["status"] != "active" {
		t.Fatalf("source request = %#v", sourceRequest)
	}
	config, _ := sourceRequest["config"].(map[string]any)
	args, _ := config["arguments"].(map[string]any)
	metadata, _ := sourceRequest["metadata"].(map[string]any)
	if config["tool"] != "export_documents" || config["document_source_type"] != "confluence" || args["space"] != "ENG" || metadata["owner"] != "platform" {
		t.Fatalf("source request = %#v", sourceRequest)
	}
	if recallRequest["query"] != "Connector runbook" || recallRequest["scope"] != "team:platform" {
		t.Fatalf("recall request = %#v", recallRequest)
	}
	if !strings.Contains(output, "Recall verified:") {
		t.Fatalf("output = %s", output)
	}
}

func TestApprovalRequiredErrorIncludesCLIRequest(t *testing.T) {
	err := (&httpStatusError{Code: http.StatusConflict, Body: `{"error":"approval_required"}`, Payload: map[string]any{
		"error":  "approval_required",
		"detail": "approval_id is required",
		"approval": map[string]any{
			"action":      "connector_enable",
			"scope":       "team:platform",
			"target_type": "source_config",
			"target_id":   "source-mcp",
		},
	}}).Error()
	for _, want := range []string{
		"approval required",
		"abra approvals request --scope 'team:platform' --action 'connector_enable'",
		"--target-type 'source_config'",
		"--target-id 'source-mcp'",
		"--approval-id <approval-id>",
	} {
		if !strings.Contains(err, want) {
			t.Fatalf("error missing %q:\n%s", want, err)
		}
	}
}

func TestConnectorsWebhookSignAndTest(t *testing.T) {
	t.Setenv("ABRA_WEBHOOK_SECRET_TEST", "secret")
	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"connectors", "webhook", "sign",
			"--payload-json", `{"scope":"team:platform","source_type":"confluence","source_url":"https://wiki.example/doc","title":"Doc","content":"Body"}`,
			"--secret-env", "ABRA_WEBHOOK_SECRET_TEST",
		})
		if err != nil {
			t.Fatalf("connectors webhook sign error = %v", err)
		}
	})
	if !strings.HasPrefix(strings.TrimSpace(output), "sha256=") {
		t.Fatalf("signature output = %s", output)
	}

	var signature string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/webhooks" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		signature = r.Header.Get("x-abra-signature")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode webhook body: %v", err)
		}
		writeTestJSON(t, w, map[string]any{"accepted": 1, "delivery_id": body["delivery_id"]})
	}))
	defer server.Close()

	output = captureStdout(t, func() {
		err := run(context.Background(), []string{
			"connectors", "webhook", "test",
			"--scope", "team:platform",
			"--connector", "confluence",
			"--secret-env", "ABRA_WEBHOOK_SECRET_TEST",
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err != nil {
			t.Fatalf("connectors webhook test error = %v", err)
		}
	})
	if !strings.HasPrefix(signature, "sha256=") || body["connector_kind"] != "confluence" || body["scope"] != "team:platform" {
		t.Fatalf("signature=%q body=%#v", signature, body)
	}
	if !strings.Contains(output, "Webhook accepted: accepted=1") {
		t.Fatalf("output = %s", output)
	}
}

func TestSourcesSyncQueuesExistingSource(t *testing.T) {
	var jobRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingestion/jobs" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&jobRequest); err != nil {
			t.Fatalf("decode job body: %v", err)
		}
		writeTestJSON(t, w, map[string]any{
			"ingestion_job": map[string]any{
				"id":               "job-sync",
				"scope":            "team:platform",
				"status":           "queued",
				"source_config_id": "source-mcp",
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"sources", "sync", "source-mcp",
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err != nil {
			t.Fatalf("sources sync error = %v", err)
		}
	})
	if jobRequest["source_config_id"] != "source-mcp" || jobRequest["trigger_type"] != "manual" {
		t.Fatalf("job request = %#v", jobRequest)
	}
	metadata, _ := jobRequest["metadata"].(map[string]any)
	if metadata["command"] != "sources sync" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if !strings.Contains(output, "Source queued: source-mcp") || !strings.Contains(output, "Job queued: job-sync") {
		t.Fatalf("output = %s", output)
	}
}

func TestSourcesBackfillQueuesBackfillJob(t *testing.T) {
	var jobRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingestion/jobs" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&jobRequest); err != nil {
			t.Fatalf("decode job body: %v", err)
		}
		writeTestJSON(t, w, map[string]any{
			"ingestion_job": map[string]any{
				"id":               "job-backfill",
				"scope":            "team:platform",
				"status":           "queued",
				"source_config_id": "source-mcp",
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"sources", "backfill", "source-mcp",
			"--approval-id", "approval-backfill",
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err != nil {
			t.Fatalf("sources backfill error = %v", err)
		}
	})
	if jobRequest["source_config_id"] != "source-mcp" || jobRequest["trigger_type"] != "backfill" {
		t.Fatalf("job request = %#v", jobRequest)
	}
	if jobRequest["approval_id"] != "approval-backfill" {
		t.Fatalf("job approval_id = %#v", jobRequest)
	}
	metadata, _ := jobRequest["metadata"].(map[string]any)
	if metadata["command"] != "sources backfill" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if !strings.Contains(output, "Source queued: source-mcp") || !strings.Contains(output, "Job queued: job-backfill") {
		t.Fatalf("output = %s", output)
	}
}

func TestSourcesStatusShowsSourceAndLatestJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sources/configs/source-mcp":
			writeTestJSON(t, w, map[string]any{
				"source_config": map[string]any{
					"id":              "source-mcp",
					"scope":           "team:platform",
					"status":          "active",
					"source_type":     "mcp",
					"name":            "Confluence",
					"authority":       "official-doc",
					"authority_score": 0.9,
					"schedule_cron":   "@every 1h",
					"last_success_at": "2026-06-21T01:02:03Z",
				},
			})
		case "/ingestion/jobs":
			if r.URL.Query().Get("source_config_id") != "source-mcp" || r.URL.Query().Get("limit") != "1" {
				t.Fatalf("jobs query = %s", r.URL.RawQuery)
			}
			writeTestJSON(t, w, map[string]any{
				"ingestion_jobs": []map[string]any{{
					"id":                "job-latest",
					"status":            "succeeded",
					"trigger_type":      "schedule",
					"source_config_id":  "source-mcp",
					"attempts":          1,
					"max_attempts":      3,
					"documents_seen":    4,
					"documents_changed": 2,
					"chunks_written":    8,
					"claims_written":    3,
				}},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"sources", "status", "source-mcp",
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err != nil {
			t.Fatalf("sources status error = %v", err)
		}
	})
	for _, want := range []string{"Source: source-mcp", "status: active", "latest_job: - job-latest  succeeded"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestConnectorsStatusAliasesSourceStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sources/configs/source-mcp":
			writeTestJSON(t, w, map[string]any{
				"source_config": map[string]any{
					"id":              "source-mcp",
					"scope":           "team:platform",
					"status":          "active",
					"source_type":     "mcp",
					"connector_kind":  "confluence",
					"name":            "Confluence",
					"authority":       "official-doc",
					"authority_score": 0.9,
				},
			})
		case "/ingestion/jobs":
			if r.URL.Query().Get("source_config_id") != "source-mcp" || r.URL.Query().Get("limit") != "1" {
				t.Fatalf("jobs query = %s", r.URL.RawQuery)
			}
			writeTestJSON(t, w, map[string]any{
				"ingestion_jobs": []map[string]any{{
					"id":                "job-latest",
					"status":            "succeeded",
					"trigger_type":      "manual",
					"source_config_id":  "source-mcp",
					"attempts":          1,
					"max_attempts":      3,
					"documents_seen":    2,
					"documents_changed": 1,
				}},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"connectors", "status", "source-mcp",
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err != nil {
			t.Fatalf("connectors status error = %v", err)
		}
	})
	for _, want := range []string{"Source: source-mcp", "status: active", "latest_job: - job-latest  succeeded"} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestSourcesLogsListsSourceJobs(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sources/configs/source-mcp":
			writeTestJSON(t, w, map[string]any{
				"source_config": map[string]any{
					"id":    "source-mcp",
					"scope": "team:platform",
				},
			})
			return
		case "/ingestion/jobs":
			if r.URL.Query().Get("scope") != "team:platform" || r.URL.Query().Get("source_config_id") != "source-mcp" || r.URL.Query().Get("limit") != "5" {
				t.Fatalf("jobs query = %s", r.URL.RawQuery)
			}
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		writeTestJSON(t, w, map[string]any{
			"ingestion_jobs": []map[string]any{{
				"id":                "job-2",
				"status":            "failed",
				"trigger_type":      "backfill",
				"source_config_id":  "source-mcp",
				"attempts":          3,
				"max_attempts":      3,
				"documents_seen":    1,
				"documents_changed": 0,
				"chunks_written":    0,
				"claims_written":    0,
				"error_message":     "connector timeout",
			}},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"sources", "logs", "source-mcp",
			"--limit", "5",
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err != nil {
			t.Fatalf("sources logs error = %v", err)
		}
	})
	if !strings.Contains(output, "Source logs: source-mcp  jobs=1") || !strings.Contains(output, "trigger=backfill") || !strings.Contains(output, "error=connector timeout") {
		t.Fatalf("output = %s", output)
	}
}

func TestSourcesSyncWaitsForQueuedJobID(t *testing.T) {
	var jobRequest map[string]any
	getRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingestion/jobs" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		switch r.Method {
		case http.MethodPost:
			if err := json.NewDecoder(r.Body).Decode(&jobRequest); err != nil {
				t.Fatalf("decode job body: %v", err)
			}
			writeTestJSON(t, w, map[string]any{
				"ingestion_job": map[string]any{
					"id":               "job-sync",
					"scope":            "team:platform",
					"status":           "queued",
					"source_config_id": "source-mcp",
				},
			})
		case http.MethodGet:
			getRequests++
			if r.URL.Query().Get("scope") != "team:platform" {
				t.Fatalf("wait should use response scope, query=%s", r.URL.RawQuery)
			}
			if r.URL.Query().Get("limit") != "20" {
				t.Fatalf("wait should request enough jobs to find the queued job id, query=%s", r.URL.RawQuery)
			}
			writeTestJSON(t, w, map[string]any{
				"ingestion_jobs": []map[string]any{
					{"id": "job-newer", "status": "succeeded", "source_config_id": "source-mcp"},
					{"id": "job-sync", "status": "succeeded", "source_config_id": "source-mcp", "documents_seen": 2, "documents_changed": 1, "chunks_written": 3, "claims_written": 4},
				},
			})
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"sources", "sync", "source-mcp",
			"--scope", "stale:scope",
			"--wait",
			"--wait-timeout", "2s",
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err != nil {
			t.Fatalf("sources sync --wait error = %v", err)
		}
	})
	if jobRequest["source_config_id"] != "source-mcp" || getRequests == 0 {
		t.Fatalf("job request = %#v getRequests=%d", jobRequest, getRequests)
	}
	if !strings.Contains(output, "Job succeeded: job-sync") || strings.Contains(output, "Job succeeded: job-newer") {
		t.Fatalf("output = %s", output)
	}
}

func TestSourcesSyncJSONWaitReturnsFinalJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingestion/jobs" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		switch r.Method {
		case http.MethodPost:
			writeTestJSON(t, w, map[string]any{
				"ingestion_job": map[string]any{
					"id":               "job-sync",
					"scope":            "team:platform",
					"status":           "queued",
					"source_config_id": "source-mcp",
				},
			})
		case http.MethodGet:
			writeTestJSON(t, w, map[string]any{
				"ingestion_jobs": []map[string]any{{
					"id":                "job-sync",
					"scope":             "team:platform",
					"status":            "succeeded",
					"source_config_id":  "source-mcp",
					"documents_seen":    2,
					"documents_changed": 1,
				}},
			})
		default:
			t.Fatalf("unexpected method %s", r.Method)
		}
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"sources", "sync", "source-mcp",
			"--wait",
			"--json",
			"--wait-timeout", "2s",
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err != nil {
			t.Fatalf("sources sync --wait --json error = %v", err)
		}
	})
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("expected clean JSON output, got error %v and output:\n%s", err, output)
	}
	job, _ := payload["job"].(map[string]any)
	if job["id"] != "job-sync" || job["status"] != "succeeded" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestSourcesPauseAndResume(t *testing.T) {
	requests := []struct {
		path string
		body map[string]any
	}{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		requests = append(requests, struct {
			path string
			body map[string]any
		}{path: r.URL.Path, body: body})
		status := "paused"
		if strings.HasSuffix(r.URL.Path, "/resume") {
			status = "active"
		}
		writeTestJSON(t, w, map[string]any{
			"source_config": map[string]any{
				"id":     "source-mcp",
				"status": status,
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"sources", "pause", "source-mcp",
			"--base-url", server.URL,
			"--token", "test-token",
		}); err != nil {
			t.Fatalf("sources pause error = %v", err)
		}
		if err := run(context.Background(), []string{
			"sources", "resume", "source-mcp",
			"--approval-id", "approval-1",
			"--base-url", server.URL,
			"--token", "test-token",
		}); err != nil {
			t.Fatalf("sources resume error = %v", err)
		}
	})
	if len(requests) != 2 {
		t.Fatalf("requests = %#v", requests)
	}
	if requests[0].path != "/sources/configs/source-mcp/pause" || requests[1].path != "/sources/configs/source-mcp/resume" {
		t.Fatalf("paths = %#v", requests)
	}
	if requests[1].body["approval_id"] != "approval-1" {
		t.Fatalf("resume body = %#v", requests[1].body)
	}
	if !strings.Contains(output, "Source paused: source-mcp") || !strings.Contains(output, "Source resumed: source-mcp") {
		t.Fatalf("output = %s", output)
	}
}

func TestApprovalsListUsesFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/approvals" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("scope") != "repo:abra" || r.URL.Query().Get("status") != "pending" || r.URL.Query().Get("limit") != "2" {
			t.Fatalf("query = %s", r.URL.RawQuery)
		}
		writeTestJSON(t, w, map[string]any{
			"approvals": []map[string]any{{
				"id":          "approval-1",
				"status":      "pending",
				"action":      "agent_write",
				"scope":       "repo:abra",
				"target_type": "document",
				"target_id":   "doc-1",
			}},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"approvals",
			"--scope", "repo:abra",
			"--status", "pending",
			"--limit", "2",
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err != nil {
			t.Fatalf("approvals list error = %v", err)
		}
	})
	if !strings.Contains(output, "Approvals: 1") || !strings.Contains(output, "approval-1  pending  agent_write  repo:abra  document/doc-1") {
		t.Fatalf("output = %s", output)
	}
}

func TestApprovalsRequestCreatesApproval(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/approvals" || r.Method != http.MethodPost {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		writeTestJSON(t, w, map[string]any{
			"approval": map[string]any{
				"id":     "approval-1",
				"scope":  "repo:abra",
				"action": "agent_write",
				"status": "pending",
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		err := run(context.Background(), []string{
			"approvals", "request",
			"--scope", "repo:abra",
			"--action", "agent_write",
			"--target-type", "document",
			"--target-id", "doc-1",
			"--requested-by", "operator",
			"--reason", "review production ingest",
			"--payload-json", `{"command":"ingest"}`,
			"--metadata-json", `{"ticket":"OPS-1"}`,
			"--base-url", server.URL,
			"--token", "test-token",
		})
		if err != nil {
			t.Fatalf("approvals request error = %v", err)
		}
	})
	if request["action"] != "agent_write" || request["scope"] != "repo:abra" || request["target_type"] != "document" || request["target_id"] != "doc-1" {
		t.Fatalf("request = %#v", request)
	}
	if request["requested_by"] != "operator" || request["reason"] != "review production ingest" {
		t.Fatalf("request actor/reason = %#v", request)
	}
	payload, _ := request["payload"].(map[string]any)
	metadata, _ := request["metadata"].(map[string]any)
	if payload["command"] != "ingest" || metadata["ticket"] != "OPS-1" || metadata["channel"] != "cli" || metadata["command"] != "approvals request" {
		t.Fatalf("payload=%#v metadata=%#v", payload, metadata)
	}
	if !strings.Contains(output, "Approval requested: approval-1") || !strings.Contains(output, "Use: abra approvals approve approval-1") {
		t.Fatalf("output = %s", output)
	}
}

func TestApprovalsApproveAndReject(t *testing.T) {
	requests := []struct {
		path string
		body map[string]any
	}{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		requests = append(requests, struct {
			path string
			body map[string]any
		}{path: r.URL.Path, body: body})
		status := "approved"
		if strings.HasSuffix(r.URL.Path, "/reject") {
			status = "rejected"
		}
		writeTestJSON(t, w, map[string]any{
			"approval": map[string]any{
				"id":     strings.Trim(strings.TrimPrefix(r.URL.Path, "/approvals/"), "/approve/reject"),
				"status": status,
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"approvals", "approve", "approval-1",
			"--decided-by", "operator",
			"--reason", "looks good",
			"--base-url", server.URL,
			"--token", "test-token",
		}); err != nil {
			t.Fatalf("approvals approve error = %v", err)
		}
		if err := run(context.Background(), []string{
			"approvals", "reject",
			"--approval-id", "approval-2",
			"--reason", "missing ticket",
			"--metadata-json", `{"ticket":"OPS-2"}`,
			"--base-url", server.URL,
			"--token", "test-token",
		}); err != nil {
			t.Fatalf("approvals reject error = %v", err)
		}
	})
	if len(requests) != 2 {
		t.Fatalf("requests = %#v", requests)
	}
	if requests[0].path != "/approvals/approval-1/approve" || requests[1].path != "/approvals/approval-2/reject" {
		t.Fatalf("paths = %#v", requests)
	}
	if requests[0].body["decided_by"] != "operator" || requests[0].body["decision_reason"] != "looks good" {
		t.Fatalf("approve body = %#v", requests[0].body)
	}
	metadata, _ := requests[1].body["metadata"].(map[string]any)
	if requests[1].body["decision_reason"] != "missing ticket" || metadata["ticket"] != "OPS-2" || metadata["channel"] != "cli" || metadata["command"] != "approvals reject" {
		t.Fatalf("reject body = %#v", requests[1].body)
	}
	if !strings.Contains(output, "Approval approved: approval-1") || !strings.Contains(output, "Approval rejected: approval-2") {
		t.Fatalf("output = %s", output)
	}
}

func TestLocalPathIngestReportsPreReadSkippedFilesJSON(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README.md"), "# Local Brain\n\nAgents should use Abra.")
	mustWrite(t, filepath.Join(root, "src", "huge.ts"), strings.Repeat("x", 128))
	mustWrite(t, filepath.Join(root, "src", "generated", "client.ts"), "export const generated = true\n")
	if err := os.WriteFile(filepath.Join(root, "src", "binary.ts"), []byte{0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}

	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/documents/batch" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		requests = append(requests, body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": 1,
			"documents": []map[string]any{
				{"index": 0, "document_id": "doc", "source_url": "file://readme", "chunks": 1, "claims": 1},
			},
		})
	}))
	defer server.Close()

	var runErr error
	output := captureStdout(t, func() {
		runErr = run(context.Background(), []string{
			"ingest",
			root,
			"--code",
			"--json",
			"--max-file-bytes", "80",
			"--base-url", server.URL,
			"--token", "test-token",
		})
	})
	if runErr != nil {
		t.Fatalf("shortcut ingest error = %v", runErr)
	}
	rawDocs, _ := requests[0]["documents"].([]any)
	firstDoc, _ := rawDocs[0].(map[string]any)
	if len(requests) != 1 || len(rawDocs) != 1 || firstDoc["title"] != "Local Brain" {
		t.Fatalf("requests = %#v", requests)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("decode output: %v\n%s", err, output)
	}
	skipped, _ := payload["skipped_files"].([]any)
	if len(skipped) != 3 {
		t.Fatalf("skipped_files = %#v", payload["skipped_files"])
	}
	reasons := map[string]bool{}
	for _, item := range skipped {
		entry, _ := item.(map[string]any)
		reasons[stringValue(entry["reason"], "")] = true
	}
	for _, want := range []string{"too_large", "binary", "generated"} {
		if !reasons[want] {
			t.Fatalf("missing reason %s in %#v", want, skipped)
		}
	}
}

func TestLocalPathIngestSkipsEmptyFiles(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "README.md"), "# Local Brain\n\nAgents should use Abra.")
	mustWrite(t, filepath.Join(root, "src", "empty.ts"), "")

	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/documents/batch" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		requests = append(requests, body)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": 1,
			"documents": []map[string]any{
				{"index": 0, "document_id": "doc", "source_url": "file://readme", "chunks": 1, "claims": 1},
			},
		})
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
	rawDocs, _ := requests[0]["documents"].([]any)
	firstDoc, _ := rawDocs[0].(map[string]any)
	if firstDoc["title"] != "Local Brain" {
		t.Fatalf("title = %v", firstDoc["title"])
	}
}

func TestLocalPathIngestContinueOnErrorReportsFailures(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a-ok.md"), "# Alpha\n\nAgents should use Abra.")
	mustWrite(t, filepath.Join(root, "b-fail.md"), "# Broken\n\nThis file triggers a provider failure.")
	mustWrite(t, filepath.Join(root, "c-ok.md"), "# Charlie\n\nRelease checks should pass.")

	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/documents/batch" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		requests = append(requests, body)
		docs, _ := body["documents"].([]any)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": 2,
			"failed":   1,
			"documents": []map[string]any{
				{"index": 0, "status": "ingested", "document_id": "doc-a", "source_url": stringValue(docs[0].(map[string]any)["source_url"], ""), "chunks": 1, "claims": 1},
				{"index": 1, "status": "error", "source_url": stringValue(docs[1].(map[string]any)["source_url"], ""), "error": "ai provider request failed: Post \"http://host.docker.internal:8080/v1/embeddings\": dial tcp: connect: connection refused"},
				{"index": 2, "status": "ingested", "document_id": "doc-c", "source_url": stringValue(docs[2].(map[string]any)["source_url"], ""), "chunks": 1, "claims": 1},
			},
		})
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
	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1 (%#v)", len(requests), requests)
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

func TestLocalPathIngestPrintsHumanProgress(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.md"), "# Alpha\n\nAgents should use Abra.")
	mustWrite(t, filepath.Join(root, "b.md"), "# Bravo\n\nRelease checks should pass.")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/documents/batch" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": 2,
			"documents": []map[string]any{
				{"index": 0, "document_id": "doc-a", "source_url": "file://a", "chunks": 1, "claims": 1},
				{"index": 1, "document_id": "doc-b", "source_url": "file://b", "chunks": 1, "claims": 1},
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"ingest",
			root,
			"--include", "**/*.md",
			"--base-url", server.URL,
			"--token", "test-token",
		}); err != nil {
			t.Fatalf("ingest error = %v", err)
		}
	})
	for _, want := range []string{
		"Ingesting files: 2",
		"model work can take a while",
		"[1-2/2] ingest batch",
		"[1-2/2] ok batch",
		"Ingested files: 2",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output missing %q:\n%s", want, output)
		}
	}
}

func TestLocalPathIngestJSONSuppressesProgress(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.md"), "# Alpha\n\nAgents should use Abra.")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/documents/batch" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": 1,
			"documents": []map[string]any{
				{"index": 0, "document_id": "doc", "source_url": "file://a", "chunks": 1, "claims": 1},
			},
		})
	}))
	defer server.Close()

	output := captureStdout(t, func() {
		if err := run(context.Background(), []string{
			"ingest",
			root,
			"--include", "**/*.md",
			"--json",
			"--base-url", server.URL,
			"--token", "test-token",
		}); err != nil {
			t.Fatalf("ingest error = %v", err)
		}
	})
	if strings.Contains(output, "Ingesting files") || strings.Contains(output, "[1/1]") {
		t.Fatalf("json output included progress:\n%s", output)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("json output is not parseable: %v\n%s", err, output)
	}
	if len(payload["documents"].([]any)) != 1 {
		t.Fatalf("documents payload = %#v", payload["documents"])
	}
}

func TestPlanDirectIngestBatchesHonorsPayloadAndChunkLimits(t *testing.T) {
	payloadDocs := []map[string]any{
		{"content": strings.Repeat("a", 20), "title": "small-a"},
		{"content": strings.Repeat("b", 20), "title": "small-b"},
		{"content": strings.Repeat("c", 20), "title": "small-c"},
	}
	payloadLimit := directIngestBatchBasePayloadBytes() +
		estimateDirectIngestDocumentPayloadBytes(payloadDocs[0]) +
		estimateDirectIngestDocumentPayloadBytes(payloadDocs[1])
	payloadBatches := planDirectIngestBatches(payloadDocs, directIngestBatchLimits{
		MaxDocuments:    50,
		MaxPayloadBytes: payloadLimit,
		MaxChunks:       50,
	})
	if !reflect.DeepEqual(payloadBatches, []directIngestBatch{{Start: 0, End: 2}, {Start: 2, End: 3}}) {
		t.Fatalf("payload batches = %#v", payloadBatches)
	}

	chunkDocs := []map[string]any{
		{"content": "small", "title": "small-a"},
		{"content": strings.Repeat("b", directIngestChunkEstimateChars*2), "title": "large-b"},
		{"content": "small", "title": "small-c"},
	}
	chunkBatches := planDirectIngestBatches(chunkDocs, directIngestBatchLimits{
		MaxDocuments:    50,
		MaxPayloadBytes: 1 << 20,
		MaxChunks:       2,
	})
	if !reflect.DeepEqual(chunkBatches, []directIngestBatch{{Start: 0, End: 1}, {Start: 1, End: 2}, {Start: 2, End: 3}}) {
		t.Fatalf("chunk batches = %#v", chunkBatches)
	}
}

func TestLocalPathIngestChunksLargeBatch(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 51; i++ {
		mustWrite(t, filepath.Join(root, fmt.Sprintf("doc-%02d.md", i)), fmt.Sprintf("# Doc %02d\n\nAgents should use Abra.", i))
	}

	var batchSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/documents/batch" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		rawDocs, _ := body["documents"].([]any)
		batchSizes = append(batchSizes, len(rawDocs))
		results := make([]map[string]any, 0, len(rawDocs))
		for index, raw := range rawDocs {
			doc, _ := raw.(map[string]any)
			results = append(results, map[string]any{
				"index":       index,
				"document_id": fmt.Sprintf("doc-%d", len(batchSizes)*100+index),
				"source_url":  doc["source_url"],
				"chunks":      1,
				"claims":      1,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted": len(results), "documents": results})
	}))
	defer server.Close()

	if err := run(context.Background(), []string{
		"ingest",
		root,
		"--include", "**/*.md",
		"--quiet",
		"--base-url", server.URL,
		"--token", "test-token",
	}); err != nil {
		t.Fatalf("ingest error = %v", err)
	}
	if !reflect.DeepEqual(batchSizes, []int{50, 1}) {
		t.Fatalf("batch sizes = %#v, want [50 1]", batchSizes)
	}
}

func TestLocalPathIngestSplitsMixedFileSizesByEstimatedChunks(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a-small.md"), "# Small A\n\nAgents should use Abra.")
	mustWrite(t, filepath.Join(root, "b-large.md"), "# Large\n\n"+strings.Repeat("x", directIngestChunkEstimateChars*directIngestBatchMaxChunks))
	mustWrite(t, filepath.Join(root, "c-small.md"), "# Small C\n\nRelease checks should pass.")

	var batchPaths [][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/documents/batch" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		rawDocs, _ := body["documents"].([]any)
		paths := make([]string, 0, len(rawDocs))
		results := make([]map[string]any, 0, len(rawDocs))
		for index, raw := range rawDocs {
			doc, _ := raw.(map[string]any)
			metadata, _ := doc["metadata"].(map[string]any)
			paths = append(paths, stringValue(metadata["ingest_path"], ""))
			results = append(results, map[string]any{
				"index":       index,
				"document_id": fmt.Sprintf("doc-%d-%d", len(batchPaths), index),
				"source_url":  doc["source_url"],
				"chunks":      1,
				"claims":      1,
			})
		}
		batchPaths = append(batchPaths, paths)
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted": len(results), "documents": results})
	}))
	defer server.Close()

	if err := run(context.Background(), []string{
		"ingest",
		root,
		"--include", "**/*.md",
		"--quiet",
		"--base-url", server.URL,
		"--token", "test-token",
	}); err != nil {
		t.Fatalf("ingest error = %v", err)
	}
	want := [][]string{{"a-small.md"}, {"b-large.md"}, {"c-small.md"}}
	if !reflect.DeepEqual(batchPaths, want) {
		t.Fatalf("batch paths = %#v, want %#v", batchPaths, want)
	}
}

func TestLocalPathIngestRejectsIncompleteBatchResponse(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "a.md"), "# Alpha\n\nAgents should use Abra.")
	mustWrite(t, filepath.Join(root, "b.md"), "# Bravo\n\nRelease checks should pass.")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ingest/documents/batch" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": 1,
			"documents": []map[string]any{
				{"index": 0, "document_id": "doc-a", "source_url": "file://a", "chunks": 1, "claims": 1},
			},
		})
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"ingest",
		root,
		"--include", "**/*.md",
		"--quiet",
		"--base-url", server.URL,
		"--token", "test-token",
	})
	if err == nil || !strings.Contains(err.Error(), "expected 2") {
		t.Fatalf("error = %v, want incomplete batch response error", err)
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

func TestProductionEnvExampleIncludesCompatibleBatchLimits(t *testing.T) {
	if !strings.Contains(productionEnvExample, "ABRA_EMBEDDING_BATCH_MAX_ITEMS=16\n") ||
		!strings.Contains(productionEnvExample, "ABRA_EMBEDDING_BATCH_MAX_TOKENS=6000\n") {
		t.Fatalf("production env example should include compatible embedding batch limits:\n%s", productionEnvExample)
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

func TestComposeCommandArgsUsesDevOverrideWhenEnabled(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), "services: {}\n")
	mustWrite(t, filepath.Join(root, "docker-compose.dev.yml"), "services: {}\n")

	got := composeCommandArgs(root, "/tmp/abra.env", true, "up", "-d", "api")
	want := []string{
		"compose",
		"--project-name",
		"abra",
		"--env-file",
		"/tmp/abra.env",
		"-f",
		"docker-compose.yml",
		"-f",
		"docker-compose.dev.yml",
		"up",
		"-d",
		"api",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("composeCommandArgs = %#v, want %#v", got, want)
	}
}

func TestComposeCommandArgsUsesBaseComposeWhenDevOverrideDisabled(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), "services: {}\n")
	mustWrite(t, filepath.Join(root, "docker-compose.dev.yml"), "services: {}\n")

	got := composeCommandArgs(root, "/tmp/abra.env", false, "down")
	want := []string{"compose", "--project-name", "abra", "--env-file", "/tmp/abra.env", "down"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("composeCommandArgs = %#v, want %#v", got, want)
	}
}

func TestComposeUpStepsBuildOnlyForSourceCheckout(t *testing.T) {
	checkout := t.TempDir()
	mustWrite(t, filepath.Join(checkout, "docker-compose.yml"), "services: {}\n")
	mustWrite(t, filepath.Join(checkout, "docker-compose.dev.yml"), "services: {}\n")
	mustWrite(t, filepath.Join(checkout, "go.mod"), "module github.com/hermawan22/abra\n")
	mustWrite(t, filepath.Join(checkout, "cmd", "abra", "main.go"), "package main\n")
	mustWrite(t, filepath.Join(checkout, "migrations", "001_init.sql"), "-- init\n")

	devSteps := composeUpSteps(checkout, "/tmp/abra.env")
	if len(devSteps) == 0 || !containsString(devSteps[0], "build") || !containsString(devSteps[0], "docker-compose.dev.yml") {
		t.Fatalf("dev compose steps should build with dev override: %#v", devSteps)
	}

	runtimeDir := t.TempDir()
	mustWrite(t, filepath.Join(runtimeDir, "docker-compose.yml"), "services: {}\n")
	mustWrite(t, filepath.Join(runtimeDir, "docker-compose.dev.yml"), "services: {}\n")
	runtimeSteps := composeUpSteps(runtimeDir, "/tmp/abra.env")
	if len(runtimeSteps) == 0 || !containsString(runtimeSteps[0], "pull") || containsString(runtimeSteps[0], "build") {
		t.Fatalf("runtime compose steps should pull, not build: %#v", runtimeSteps)
	}
	for _, step := range runtimeSteps {
		if containsString(step, "docker-compose.dev.yml") {
			t.Fatalf("runtime compose step used dev override: %#v", runtimeSteps)
		}
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
