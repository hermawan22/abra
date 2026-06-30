package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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
			"--allow-private-network",
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
			"--allow-private-network",
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
			"--allow-private-network",
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
			"--allow-private-network",
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
	if sourceRequest["connector_kind"] != "confluence" || config["tool"] != "export_documents" || config["document_source_type"] != "confluence" || config["allow_private_network"] != true {
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
		case "/mcp":
			recallRequest = decodeMCPToolCall(t, r, "recall")
			writeMCPToolResult(t, w, 1, map[string]any{
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
		id := strings.TrimPrefix(r.URL.Path, "/approvals/")
		id = strings.TrimSuffix(strings.TrimSuffix(id, "/approve"), "/reject")
		writeTestJSON(t, w, map[string]any{
			"approval": map[string]any{
				"id":     id,
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
	home := t.TempDir()
	t.Setenv("ABRA_HOME", home)
	mustWrite(t, filepath.Join(home, "quickstart.env"), "EMBEDDING_PROVIDER=local\n")

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
		"abra model up",
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
