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

func writeMCPTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("content-type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write json response: %v", err)
	}
}
