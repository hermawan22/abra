package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hermawan22/abra/internal/config"
)

func TestRateLimitBlocksAfterConfiguredLimit(t *testing.T) {
	handler := rateLimit(config.Config{RateLimitMax: 2, RateLimitWindow: time.Minute}, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/recall", nil)
		req.Header.Set("Authorization", "Bearer test")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d status = %d", i, rec.Code)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/recall", nil)
	req.Header.Set("Authorization", "Bearer test")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("retry-after") == "" {
		t.Fatal("missing retry-after header")
	}
}

func TestRateLimitKeyHashesValidBearerToken(t *testing.T) {
	token := "fixture-bearer-token"
	req := httptest.NewRequest(http.MethodPost, "/recall", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	key := rateLimitKey(req, []string{token})
	want := "api-key:sha256:" + sha256Hex(token)
	if key != want {
		t.Fatalf("key = %q, want %q", key, want)
	}
	if strings.Contains(key, token) {
		t.Fatalf("key %q contains raw bearer token", key)
	}
}

func TestRateLimitKeyFallsBackToIPForInvalidBearerToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/recall", nil)
	req.RemoteAddr = "203.0.113.20:54321"
	req.Header.Set("Authorization", "Bearer attacker-rotates-this")

	key := rateLimitKey(req, []string{"real-token"})
	if key != "ip:203.0.113.20" {
		t.Fatalf("key = %q, want invalid tokens rate limited by IP", key)
	}
}

func TestRateLimitKeyFallsBackToIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/recall", nil)
	req.RemoteAddr = "203.0.113.10:54321"

	key := rateLimitKey(req, []string{"real-token"})
	if key != "ip:203.0.113.10" {
		t.Fatalf("key = %q, want ip fallback", key)
	}
}

func TestRateLimitExemptsHealth(t *testing.T) {
	handler := rateLimit(config.Config{RateLimitMax: 1, RateLimitWindow: time.Minute}, nil, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	for _, path := range []string{"/healthz", "/readyz"} {
		for i := 0; i < 3; i++ {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("%s request %d status = %d", path, i, rec.Code)
			}
		}
	}
}

func TestRateLimitUsesSharedStore(t *testing.T) {
	store := &fakeRateLimitStore{allowed: false, resetAt: time.Now().Add(time.Minute)}
	handler := rateLimit(config.Config{APIKeys: []string{"shared-token"}, RateLimitMax: 10, RateLimitWindow: time.Minute}, store, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/recall", nil)
	req.Header.Set("Authorization", "Bearer shared-token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 from shared store decision", rec.Code)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want 1", store.calls)
	}
	if strings.Contains(store.key, "shared-token") {
		t.Fatalf("store key contains raw token: %q", store.key)
	}
}

func TestRateLimitStoreFailureFailsClosed(t *testing.T) {
	store := &fakeRateLimitStore{err: errors.New("database unavailable")}
	handler := rateLimit(config.Config{RateLimitMax: 10, RateLimitWindow: time.Minute}, store, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/recall", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

type fakeRateLimitStore struct {
	allowed bool
	resetAt time.Time
	err     error
	calls   int
	key     string
}

func (f *fakeRateLimitStore) AllowRateLimit(_ context.Context, key string, _ time.Duration, _ int) (bool, time.Time, error) {
	f.calls++
	f.key = key
	if f.err != nil {
		return false, time.Time{}, f.err
	}
	return f.allowed, f.resetAt, nil
}
