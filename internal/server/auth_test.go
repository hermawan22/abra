package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hermawan22/abra/internal/config"
)

func TestLegacyAPIKeyKeepsAdminAccess(t *testing.T) {
	principals := parseAPIKeys([]string{"legacy-token"})
	if len(principals) != 1 {
		t.Fatalf("principals = %d, want 1", len(principals))
	}
	principal := principals[0]
	for _, action := range []authAction{authActionRead, authActionWrite, authActionOps} {
		if !principal.allows(action, "team:anything") {
			t.Fatalf("legacy key should allow %s on scoped resources", action)
		}
		if !principal.allows(action, "") {
			t.Fatalf("legacy key should allow %s on all-scope resources", action)
		}
	}
}

func TestScopedReaderAllowsOnlyReadWithinScope(t *testing.T) {
	principals := parseAPIKeys([]string{"read-token|roles=reader;scopes=team:a team:b*"})
	if len(principals) != 1 {
		t.Fatalf("principals = %d, want 1", len(principals))
	}
	principal := principals[0]
	if !principal.allows(authActionRead, "team:a") {
		t.Fatal("reader should read exact configured scope")
	}
	if !principal.allows(authActionRead, "team:b/frontend") {
		t.Fatal("reader should read wildcard configured scope")
	}
	if principal.allows(authActionWrite, "team:a") {
		t.Fatal("reader should not write")
	}
	if principal.allows(authActionRead, "team:c") {
		t.Fatal("reader should not read another scope")
	}
	if principal.allows(authActionRead, "") {
		t.Fatal("scoped reader should not access all-scope queries")
	}
}

func TestScopedAdminCanWriteOnlyConfiguredScope(t *testing.T) {
	principals := parseAPIKeys([]string{"admin-token|scopes=team:a"})
	if len(principals) != 1 {
		t.Fatalf("principals = %d, want 1", len(principals))
	}
	principal := principals[0]
	if !principal.allows(authActionWrite, "team:a") {
		t.Fatal("scoped admin should write configured scope")
	}
	if principal.allows(authActionWrite, "team:b") {
		t.Fatal("scoped admin should not write another scope")
	}
}

func TestAuthMiddlewareRejectsInvalidToken(t *testing.T) {
	h := handler{cfg: config.Config{APIKeys: []string{"good-token"}}}
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("authorization", "Bearer bad-token")
	recorder := httptest.NewRecorder()

	h.auth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddlewareRejectsMissingKeysWithoutDevBypass(t *testing.T) {
	h := handler{cfg: config.Config{}}
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	recorder := httptest.NewRecorder()

	h.auth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddlewareAllowsExplicitUnauthenticatedDev(t *testing.T) {
	h := handler{cfg: config.Config{AllowUnauthenticatedDev: true}}
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	recorder := httptest.NewRecorder()

	h.auth(func(w http.ResponseWriter, r *http.Request) {
		if !h.requireAccess(w, r, authActionOps, "team:a") {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestAuthMiddlewareAllowsXAPIKeyHeader(t *testing.T) {
	h := handler{cfg: config.Config{APIKeys: []string{"good-token"}}}
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("x-api-key", "good-token")
	recorder := httptest.NewRecorder()

	h.auth(func(w http.ResponseWriter, r *http.Request) {
		if !h.requireAccess(w, r, authActionRead, "team:a") {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestScopedKeyRequiresScopeForListEndpoints(t *testing.T) {
	h := handler{cfg: config.Config{APIKeys: []string{"read-token|roles=reader;scopes=team:a"}}}
	request := httptest.NewRequest(http.MethodGet, "/graph/entities", nil)
	request.Header.Set("authorization", "Bearer read-token")
	recorder := httptest.NewRecorder()

	h.auth(func(w http.ResponseWriter, r *http.Request) {
		if !h.requireAccess(w, r, authActionRead, "") {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusForbidden)
	}
}

func TestAuditEventFilterFromRequest(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/audit/events?scope=team:example&type=claim.remembered&target_type=claim&since=2026-06-16T10:00:00Z&until=2026-06-16T11:00:00Z&limit=25&format=ndjson", nil)

	filter, format, err := auditEventFilterFromRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if filter.Scope != "team:example" {
		t.Fatalf("scope = %q", filter.Scope)
	}
	if filter.EventType != "claim.remembered" {
		t.Fatalf("event type = %q", filter.EventType)
	}
	if filter.TargetType != "claim" {
		t.Fatalf("target type = %q", filter.TargetType)
	}
	if format != "ndjson" {
		t.Fatalf("format = %q", format)
	}
	wantSince := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	if !filter.Since.Equal(wantSince) {
		t.Fatalf("since = %s, want %s", filter.Since, wantSince)
	}
}

func TestAuditEventFilterRejectsInvalidFormat(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/audit/events?format=csv", nil)
	if _, _, err := auditEventFilterFromRequest(request); err == nil {
		t.Fatal("expected invalid format error")
	}
}
