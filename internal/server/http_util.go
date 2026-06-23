package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hermawan22/abra/internal/store"
)

func intQuery(r *http.Request, key string, fallback int) int {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed == 0 {
		return fallback
	}
	return parsed
}

func auditEventFilterFromRequest(r *http.Request) (store.AuditEventFilter, string, error) {
	query := r.URL.Query()
	eventType := strings.TrimSpace(query.Get("event_type"))
	if eventType == "" {
		eventType = strings.TrimSpace(query.Get("type"))
	}
	filter := store.AuditEventFilter{
		Scope:      strings.TrimSpace(query.Get("scope")),
		EventType:  eventType,
		TargetType: strings.TrimSpace(query.Get("target_type")),
		Limit:      intQuery(r, "limit", 100),
	}
	var err error
	if filter.Since, err = timeQuery(query.Get("since")); err != nil {
		return store.AuditEventFilter{}, "", fmt.Errorf("invalid since: %w", err)
	}
	if filter.Until, err = timeQuery(query.Get("until")); err != nil {
		return store.AuditEventFilter{}, "", fmt.Errorf("invalid until: %w", err)
	}
	format := strings.ToLower(strings.TrimSpace(query.Get("format")))
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "ndjson" {
		return store.AuditEventFilter{}, "", fmt.Errorf("format must be json or ndjson")
	}
	return filter, format, nil
}

func timeQuery(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("x-request-id")
		if id == "" {
			id = time.Now().UTC().Format("20060102150405.000000000")
		}
		w.Header().Set("x-request-id", id)
		next.ServeHTTP(w, r)
	})
}

func methodNotAllowed(method string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("allow", method)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed", "method": method})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
