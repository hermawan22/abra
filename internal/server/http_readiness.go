package server

import (
	"context"
	"errors"
	"net/http"
	"time"
)

func removedBrowserUI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "browser_ui_not_shipped"})
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service":          "abra",
		"status":           "ok",
		"stack":            "go-v1",
		"product_surface":  "mcp",
		"mcp":              "POST /mcp",
		"health":           "GET /healthz",
		"readiness":        "GET /readyz",
		"operator_metrics": "GET /metrics",
		"note":             "Abra is MCP-first for agents. REST routes are internal service transport for the CLI, MCP server, gateways, and operators; they are intentionally not advertised as a public API catalog.",
	})
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *handler) ready(w http.ResponseWriter, r *http.Request) {
	if err := h.db.Ready(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	result := map[string]any{
		"ok":                   true,
		"auth_required":        len(h.cfg.APIKeys) > 0,
		"embedding_provider":   h.cfg.Embedding.Provider,
		"approval_mode":        h.cfg.ApprovalMode,
		"approval_enforcement": h.cfg.ApprovalMode == "enforce",
		"redaction_enabled":    h.cfg.RedactPII,
		"tracing_enabled":      h.cfg.Tracing.Enabled,
	}
	if r.URL.Query().Get("deep") == "1" || r.URL.Query().Get("deep") == "true" {
		checkTimeout := minDuration(10*time.Second, h.cfg.Embedding.Timeout)
		checkCtx, cancel := context.WithTimeout(r.Context(), checkTimeout)
		defer cancel()
		started := time.Now()
		if err := h.brain.CheckEmbeddingReady(checkCtx); err != nil {
			result["ok"] = false
			result["embedding_ready"] = false
			result["embedding_error"] = err.Error()
			result["embedding_status"] = embeddingReadinessStatus(checkCtx, err)
			result["embedding_check_timeout"] = checkTimeout.String()
			result["embedding_provider_timeout"] = h.cfg.Embedding.Timeout.String()
			result["embedding_elapsed_ms"] = time.Since(started).Milliseconds()
			writeJSON(w, http.StatusServiceUnavailable, result)
			return
		}
		result["embedding_ready"] = true
	}
	writeJSON(w, http.StatusOK, result)
}

func embeddingReadinessStatus(ctx context.Context, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	return "error"
}

func (h *handler) metricsText(w http.ResponseWriter, r *http.Request) {
	if !h.requireAccess(w, r, authActionOps, "") {
		return
	}
	w.Header().Set("content-type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(h.metrics.prometheus()))
}
