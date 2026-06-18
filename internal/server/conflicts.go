package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

func (h *handler) listConflicts(w http.ResponseWriter, r *http.Request) {
	filter := store.ConflictFilter{
		Scope:      strings.TrimSpace(r.URL.Query().Get("scope")),
		Status:     strings.TrimSpace(r.URL.Query().Get("status")),
		Severity:   strings.TrimSpace(r.URL.Query().Get("severity")),
		ClaimID:    strings.TrimSpace(r.URL.Query().Get("claim_id")),
		RelationID: strings.TrimSpace(r.URL.Query().Get("relation_id")),
		Limit:      intQuery(r, "limit", 50),
	}
	if !h.requireAccess(w, r, authActionRead, filter.Scope) {
		return
	}
	conflicts, err := h.db.ListConflicts(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": conflicts})
}

func (h *handler) resolveConflict(w http.ResponseWriter, r *http.Request) {
	conflictID := strings.TrimSpace(r.PathValue("conflictId"))
	conflict, err := h.db.GetConflict(r.Context(), conflictID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionOps, conflict.Scope) {
		return
	}
	var input store.ResolveConflictInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	resolved, err := h.db.ResolveConflict(r.Context(), conflict.ID, input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := h.db.InsertAuditEvent(r.Context(), "conflict.resolved", "conflict", resolved.ID, resolved.Scope, "", map[string]any{
		"status":      resolved.Status,
		"resolved_by": resolved.ResolvedBy,
		"resolution":  resolved.Resolution,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflict": resolved})
}
