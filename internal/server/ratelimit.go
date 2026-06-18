package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/hermawan22/abra/internal/config"
)

type rateLimitStore interface {
	AllowRateLimit(ctx context.Context, key string, window time.Duration, limit int) (bool, time.Time, error)
}

type rateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	buckets map[string]rateBucket
}

type rateBucket struct {
	ResetAt time.Time
	Count   int
}

func rateLimit(cfg config.Config, store rateLimitStore, next http.Handler) http.Handler {
	limiter := &rateLimiter{
		limit:   cfg.RateLimitMax,
		window:  cfg.RateLimitWindow,
		buckets: map[string]rateBucket{},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rateLimitExempt(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		allowed, resetAt, err := allowRateLimit(r.Context(), store, limiter, rateLimitKey(r, cfg.APIKeys), cfg.RateLimitWindow, cfg.RateLimitMax)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rate_limit_unavailable"})
			return
		}
		if !allowed {
			w.Header().Set("retry-after", secondsUntil(resetAt))
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "rate_limited"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func allowRateLimit(ctx context.Context, store rateLimitStore, limiter *rateLimiter, key string, window time.Duration, limit int) (bool, time.Time, error) {
	if store != nil {
		return store.AllowRateLimit(ctx, key, window, limit)
	}
	allowed, resetAt := limiter.allow(key)
	return allowed, resetAt, nil
}

func (l *rateLimiter) allow(key string) (bool, time.Time) {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	bucket := l.buckets[key]
	if bucket.ResetAt.IsZero() || !now.Before(bucket.ResetAt) {
		bucket = rateBucket{ResetAt: now.Add(l.window)}
	}
	bucket.Count++
	l.buckets[key] = bucket
	return bucket.Count <= l.limit, bucket.ResetAt
}

func rateLimitKey(r *http.Request, apiKeys []string) string {
	if principal, ok := authenticate(r, apiKeys, false); ok && principal != nil && principal.token != "" {
		return hashedRateLimitKey("api-key", principal.token)
	}
	return rateLimitIPKey(r)
}

func hashedRateLimitKey(prefix, value string) string {
	sum := sha256.Sum256([]byte(value))
	return prefix + ":sha256:" + hex.EncodeToString(sum[:])
}

func rateLimitIPKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return "ip:" + r.RemoteAddr
	}
	return "ip:" + host
}

func rateLimitExempt(path string) bool {
	return path == "/" || path == "/healthz" || path == "/readyz"
}

func secondsUntil(t time.Time) string {
	seconds := int(time.Until(t).Seconds())
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}
