package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemovedBrowserUIReturnsNotFound(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/app/", nil)

	removedBrowserUI(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if !strings.Contains(rec.Body.String(), "browser_ui_not_shipped") {
		t.Fatalf("body = %q, want browser_ui_not_shipped", rec.Body.String())
	}
}

func TestIndexDoesNotAdvertiseRESTCatalog(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	(&handler{}).index(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, ok := body["endpoints"]; ok {
		t.Fatalf("index should not advertise REST endpoint catalog: %v", body["endpoints"])
	}
	if body["product_surface"] != "mcp" || body["mcp"] != "POST /mcp" {
		t.Fatalf("index should advertise MCP as product surface, got %v", body)
	}
}

func TestIndexDoesNotCatchUnknownRoutes(t *testing.T) {
	for _, path := range []string{
		"/brain/review?scope=repo:test",
		"/brain/traces/trace-123",
		"/brain/eval-runs?scope=repo:test",
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)

		(&handler{}).index(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want %d; body=%s", path, rec.Code, http.StatusNotFound, rec.Body.String())
		}
	}
}
