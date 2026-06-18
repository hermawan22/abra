package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

func (h *handler) listAgentProfiles(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if !h.requireAccess(w, r, authActionOps, scope) {
		return
	}
	profiles, err := h.db.ListAgentProfiles(
		r.Context(),
		scope,
		strings.TrimSpace(r.URL.Query().Get("status")),
		intQuery(r, "limit", 50),
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent_profiles": profiles})
}

func (h *handler) upsertAgentProfile(w http.ResponseWriter, r *http.Request) {
	var input store.AgentProfileRecord
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if !h.requireAccess(w, r, authActionOps, input.Scope) {
		return
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     "acl_change",
		Scope:      input.Scope,
		TargetType: "agent_profile",
		TargetID:   agentProfileApprovalTarget(input),
		ApprovalID: input.ApprovalID,
	}) {
		return
	}
	profile, err := h.db.UpsertAgentProfile(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	_ = h.db.InsertAuditEvent(r.Context(), "agent_profile.upserted", "agent_profile", profile.ID, profile.Scope, "", map[string]any{
		"profile_key":    profile.ProfileKey,
		"status":         profile.Status,
		"principal_ref":  profile.PrincipalRef,
		"default_scope":  profile.DefaultScope,
		"allowed_scopes": profile.AllowedScopes,
		"denied_scopes":  profile.DeniedScopes,
		"channel":        "http",
	})
	writeJSON(w, http.StatusOK, map[string]any{"agent_profile": profile})
}

func agentProfileApprovalTarget(input store.AgentProfileRecord) string {
	if id := strings.TrimSpace(input.ID); id != "" {
		return id
	}
	return strings.Join([]string{strings.TrimSpace(input.Scope), strings.TrimSpace(input.ProfileKey)}, "/")
}
