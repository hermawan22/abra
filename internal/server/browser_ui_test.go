package server

import (
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
