package jobs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hermawan22/abra/internal/ingest"
)

func TestValidateMCPSourceCallsToolAndNormalizesStructuredDocuments(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("content-type"); got != "application/json" {
			t.Fatalf("content-type = %q, want application/json", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["method"] != "tools/call" {
			t.Fatalf("method = %v, want tools/call", body["method"])
		}
		params, _ := body["params"].(map[string]any)
		if params["name"] != "export_documents" {
			t.Fatalf("tool = %v, want export_documents", params["name"])
		}
		args, _ := params["arguments"].(map[string]any)
		if args["space"] != "ENG" {
			t.Fatalf("arguments = %#v", args)
		}

		writeMCPTestJSON(t, w, map[string]any{
			"jsonrpc": "2.0",
			"id":      body["id"],
			"result": map[string]any{
				"structuredContent": map[string]any{
					"documents": []map[string]any{{
						"source_url": "https://wiki.example/pages/123",
						"source_id":  "123",
						"title":      "Platform Decision",
						"content":    "Use Abra for governed agent memory.",
					}},
				},
			},
		})
	}))
	defer server.Close()

	docs, err := ValidateMCPSource(context.Background(), SourceConfig{
		ID:            "mcp-confluence",
		Scope:         "repo:abra",
		SourceType:    ingest.SourceTypeMCP,
		Name:          "Confluence MCP",
		BaseURL:       server.URL,
		ConnectorKind: "confluence",
		Config: map[string]any{
			"tool":      "export_documents",
			"arguments": map[string]any{"space": "ENG"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("expected ValidateMCPSource to call mock MCP endpoint")
	}
	if len(docs) != 1 {
		t.Fatalf("documents = %d, want 1", len(docs))
	}
	doc := docs[0]
	if doc.SourceType != "confluence" || doc.Scope != "repo:abra" {
		t.Fatalf("normalized document = %+v", doc)
	}
	if doc.SourceURL != "https://wiki.example/pages/123" || doc.SourceID != "123" || doc.Title != "Platform Decision" {
		t.Fatalf("document = %+v", doc)
	}
	if doc.ContentBytes != len([]byte("Use Abra for governed agent memory.")) {
		t.Fatalf("content bytes = %d", doc.ContentBytes)
	}
}

func TestListMCPToolsCallsUpstreamToolsList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if body["method"] != "tools/list" {
			t.Fatalf("method = %v, want tools/list", body["method"])
		}
		writeMCPTestJSON(t, w, map[string]any{
			"jsonrpc": "2.0",
			"id":      body["id"],
			"result": map[string]any{
				"tools": []map[string]any{{
					"name":        "export_documents",
					"description": "Export normalized documents.",
					"inputSchema": map[string]any{"type": "object"},
				}},
			},
		})
	}))
	defer server.Close()

	tools, err := ListMCPTools(context.Background(), SourceConfig{
		ID:         "mcp-confluence",
		Scope:      "repo:abra",
		SourceType: ingest.SourceTypeMCP,
		BaseURL:    server.URL,
		Config:     map[string]any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 1 || tools[0].Name != "export_documents" || tools[0].Description == "" {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestValidateMCPSourceReportReturnsWarnings(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeMCPTestJSON(t, w, map[string]any{
			"jsonrpc": "2.0",
			"id":      body["id"],
			"result": map[string]any{
				"structuredContent": map[string]any{
					"documents": []map[string]any{
						{
							"scope":             "repo:other",
							"source_url":        "https://wiki.example/pages/123",
							"title":             "Platform Decision",
							"content":           "Use Abra for governed agent memory.",
							"source_updated_at": "not-a-date",
						},
						{
							"source_url": "https://wiki.example/pages/123",
							"title":      "Platform Decision Copy",
							"content":    "Use Abra for governed agent memory.",
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	report, err := ValidateMCPSourceReport(context.Background(), SourceConfig{
		ID:            "mcp-confluence",
		Scope:         "repo:abra",
		SourceType:    ingest.SourceTypeMCP,
		BaseURL:       server.URL,
		ConnectorKind: "confluence",
		Config:        map[string]any{"tool": "export_documents"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || report.Count != 2 || len(report.Documents) != 2 {
		t.Fatalf("report = %#v", report)
	}
	for _, want := range []string{"scope_mismatch", "invalid_source_updated_at", "duplicate_source_url", "missing_source_updated_at"} {
		found := false
		for _, warning := range report.Warnings {
			if warning.Code == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("warnings missing %s: %#v", want, report.Warnings)
		}
	}
}

func TestValidateMCPSourceRejectsStructuredDocumentsMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{
			name: "source_url",
			mutate: func(doc map[string]any) {
				delete(doc, "source_url")
			},
			wantErr: "without source_url",
		},
		{
			name: "title",
			mutate: func(doc map[string]any) {
				delete(doc, "title")
			},
			wantErr: "without title",
		},
		{
			name: "content",
			mutate: func(doc map[string]any) {
				doc["content"] = " \t\n"
			},
			wantErr: "without content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			document := map[string]any{
				"source_url": "https://wiki.example/pages/123",
				"title":      "Platform Decision",
				"content":    "Use Abra for governed agent memory.",
			}
			tt.mutate(document)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				writeMCPTestJSON(t, w, map[string]any{
					"jsonrpc": "2.0",
					"id":      body["id"],
					"result": map[string]any{
						"structuredContent": map[string]any{
							"documents": []map[string]any{document},
						},
					},
				})
			}))
			defer server.Close()

			_, err := ValidateMCPSource(context.Background(), SourceConfig{
				ID:            "mcp-confluence",
				Scope:         "repo:abra",
				SourceType:    ingest.SourceTypeMCP,
				BaseURL:       server.URL,
				ConnectorKind: "confluence",
				Config:        map[string]any{"tool": "export_documents"},
			})
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateMCPSourceSendsConfiguredCredentialHeaders(t *testing.T) {
	t.Setenv("CONFLUENCE_MCP_TOKEN", "mcp-token")
	t.Setenv("CONFLUENCE_TENANT_ID", "tenant-42")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("authorization"); got != "Bearer mcp-token" {
			t.Fatalf("authorization = %q, want bearer token", got)
		}
		if got := r.Header.Get("x-tenant-id"); got != "tenant-42" {
			t.Fatalf("x-tenant-id = %q, want tenant-42", got)
		}
		if got := r.Header.Get("accept"); got != "application/json" {
			t.Fatalf("accept = %q, want application/json", got)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeMCPTestJSON(t, w, map[string]any{
			"jsonrpc": "2.0",
			"id":      body["id"],
			"result": map[string]any{
				"structuredContent": map[string]any{
					"documents": []map[string]any{{
						"source_url": "https://wiki.example/pages/credentialed",
						"title":      "Credentialed Export",
						"content":    "Credentialed MCP exports are accepted.",
					}},
				},
			},
		})
	}))
	defer server.Close()

	if _, err := ValidateMCPSource(context.Background(), SourceConfig{
		ID:            "mcp-confluence",
		Scope:         "repo:abra",
		SourceType:    ingest.SourceTypeMCP,
		BaseURL:       server.URL,
		ConnectorKind: "confluence",
		Config: map[string]any{
			"tool":             "export_documents",
			"bearer_token_env": "CONFLUENCE_MCP_TOKEN",
			"header_env": map[string]any{
				"X-Tenant-ID": "CONFLUENCE_TENANT_ID",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateMCPSourceRejectsMissingCredentialEnv(t *testing.T) {
	tests := []struct {
		name    string
		config  map[string]any
		wantErr string
	}{
		{
			name: "bearer token",
			config: map[string]any{
				"tool":             "export_documents",
				"bearer_token_env": "MISSING_MCP_TOKEN",
			},
			wantErr: `bearer_token_env "MISSING_MCP_TOKEN" is empty`,
		},
		{
			name: "custom header",
			config: map[string]any{
				"tool": "export_documents",
				"header_env": map[string]any{
					"X-Tenant-ID": "MISSING_TENANT_ID",
				},
			},
			wantErr: `header env "MISSING_TENANT_ID" for "X-Tenant-ID" is empty`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				t.Fatal("MCP endpoint should not be called when credential env is missing")
			}))
			defer server.Close()

			_, err := ValidateMCPSource(context.Background(), SourceConfig{
				ID:         "mcp-confluence",
				Scope:      "repo:abra",
				SourceType: ingest.SourceTypeMCP,
				BaseURL:    server.URL,
				Config:     tt.config,
			})
			if err == nil {
				t.Fatal("expected missing credential env error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateMCPSourceRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(strings.Repeat("x", defaultMCPSourceMaxResponseBytes+1)))
	}))
	defer server.Close()

	_, err := ValidateMCPSource(context.Background(), SourceConfig{
		ID:            "mcp-confluence",
		Scope:         "repo:abra",
		SourceType:    ingest.SourceTypeMCP,
		BaseURL:       server.URL,
		ConnectorKind: "confluence",
		Config:        map[string]any{"tool": "export_documents"},
	})
	if err == nil {
		t.Fatal("expected oversized response error")
	}
	if !strings.Contains(err.Error(), "response exceeds") {
		t.Fatalf("error = %q, want response exceeds", err)
	}
}

func TestValidateMCPSourceRejectsTooManyDocuments(t *testing.T) {
	documents := make([]map[string]any, 0, defaultMCPSourceMaxDocuments+1)
	for index := 0; index < defaultMCPSourceMaxDocuments+1; index++ {
		documents = append(documents, map[string]any{
			"source_url": "https://wiki.example/pages/doc",
			"title":      "Platform Decision",
			"content":    "Use Abra for governed agent memory.",
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeMCPTestJSON(t, w, map[string]any{
			"jsonrpc": "2.0",
			"id":      body["id"],
			"result": map[string]any{
				"structuredContent": map[string]any{"documents": documents},
			},
		})
	}))
	defer server.Close()

	_, err := ValidateMCPSource(context.Background(), SourceConfig{
		ID:            "mcp-confluence",
		Scope:         "repo:abra",
		SourceType:    ingest.SourceTypeMCP,
		BaseURL:       server.URL,
		ConnectorKind: "confluence",
		Config:        map[string]any{"tool": "export_documents"},
	})
	if err == nil {
		t.Fatal("expected document limit error")
	}
	if !strings.Contains(err.Error(), "returned 51 documents; limit is 50") {
		t.Fatalf("error = %q, want document limit", err)
	}
}

func TestValidateMCPSourceRejectsOversizedDocumentContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		writeMCPTestJSON(t, w, map[string]any{
			"jsonrpc": "2.0",
			"id":      body["id"],
			"result": map[string]any{
				"structuredContent": map[string]any{
					"documents": []map[string]any{{
						"source_url": "https://wiki.example/pages/large",
						"title":      "Large Export",
						"content":    strings.Repeat("x", defaultMCPSourceMaxDocumentContentBytes+1),
					}},
				},
			},
		})
	}))
	defer server.Close()

	_, err := ValidateMCPSource(context.Background(), SourceConfig{
		ID:            "mcp-confluence",
		Scope:         "repo:abra",
		SourceType:    ingest.SourceTypeMCP,
		BaseURL:       server.URL,
		ConnectorKind: "confluence",
		Config:        map[string]any{"tool": "export_documents"},
	})
	if err == nil {
		t.Fatal("expected content limit error")
	}
	if !strings.Contains(err.Error(), "content with") || !strings.Contains(err.Error(), "limit is") {
		t.Fatalf("error = %q, want content limit", err)
	}
}

func writeMCPTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("content-type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write json response: %v", err)
	}
}
