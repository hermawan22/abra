package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

func (h *handler) listAgentActionPolicies(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if !h.requireAccess(w, r, authActionOps, scope) {
		return
	}
	policies, err := h.db.ListAgentActionPolicies(r.Context(), scope, intQuery(r, "limit", 50))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent_policies": policies})
}

func (h *handler) upsertAgentActionPolicy(w http.ResponseWriter, r *http.Request) {
	var input store.AgentActionPolicyRecord
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.Scope = strings.TrimSpace(input.Scope)
	input.Name = strings.TrimSpace(input.Name)
	if input.Scope == "" || input.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scope and name are required"})
		return
	}
	if !h.requireAccess(w, r, authActionOps, input.Scope) {
		return
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     "acl_change",
		Scope:      input.Scope,
		TargetType: "agent_policy",
		TargetID:   agentActionPolicyApprovalTarget(input),
		ApprovalID: input.ApprovalID,
	}) {
		return
	}
	policy, err := h.db.UpsertAgentActionPolicy(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.auditAgentPolicyUpserted(r.Context(), policy, "http")
	writeJSON(w, http.StatusOK, map[string]any{"agent_policy": policy})
}

func (h *handler) auditAgentPolicyUpserted(ctx context.Context, policy store.AgentActionPolicyRecord, channel string) {
	_ = h.db.InsertAuditEvent(ctx, "agent_policy.upserted", "agent_policy", policy.ID, policy.Scope, "", map[string]any{
		"name":         policy.Name,
		"status":       policy.Status,
		"effect":       policy.Effect,
		"priority":     policy.Priority,
		"subject_type": policy.SubjectType,
		"subject_id":   policy.SubjectID,
		"channel":      channel,
	})
}

func (h *handler) agentActionPolicyDecision(w http.ResponseWriter, r *http.Request) {
	var input store.AgentActionDecisionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if !h.requireAccess(w, r, authActionRead, input.Scope) {
		return
	}
	decision, err := h.db.EvaluateAgentActionPolicy(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.metrics.observeAgentPolicyDecision("decision_api", input.Action, decision.Decision)
	writeJSON(w, http.StatusOK, decision)
}

func agentActionPolicyApprovalTarget(input store.AgentActionPolicyRecord) string {
	if id := strings.TrimSpace(input.ID); id != "" {
		return id
	}
	return strings.Join([]string{strings.TrimSpace(input.Scope), strings.TrimSpace(input.Name)}, "/")
}
