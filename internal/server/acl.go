package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

func (h *handler) listACLPolicies(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if !h.requireAccess(w, r, authActionOps, scope) {
		return
	}
	policies, err := h.db.ListACLPolicies(
		r.Context(),
		scope,
		strings.TrimSpace(r.URL.Query().Get("subject_type")),
		strings.TrimSpace(r.URL.Query().Get("subject_id")),
		intQuery(r, "limit", 50),
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"acl_policies": policies})
}

func (h *handler) upsertACLPolicy(w http.ResponseWriter, r *http.Request) {
	var input store.ACLPolicyRecord
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
		TargetType: "acl_policy",
		TargetID:   aclPolicyApprovalTarget(input),
		ApprovalID: input.ApprovalID,
	}) {
		return
	}
	policy, err := h.db.UpsertACLPolicy(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.auditACLPolicyUpserted(r.Context(), policy, "http")
	writeJSON(w, http.StatusOK, map[string]any{"acl_policy": policy})
}

func (h *handler) aclDecision(w http.ResponseWriter, r *http.Request) {
	var input store.ACLDecisionInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if !h.requireAccess(w, r, authActionRead, input.Scope) {
		return
	}
	decision, err := h.db.EvaluateACLDecision(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, decision)
}

func aclPolicyApprovalTarget(input store.ACLPolicyRecord) string {
	if id := strings.TrimSpace(input.ID); id != "" {
		return id
	}
	return strings.Join([]string{strings.TrimSpace(input.Scope), strings.TrimSpace(input.Name)}, "/")
}

func (h *handler) auditACLPolicyUpserted(ctx context.Context, policy store.ACLPolicyRecord, channel string) {
	_ = h.db.InsertAuditEvent(ctx, "acl_policy.upserted", "acl_policy", policy.ID, policy.Scope, "", map[string]any{
		"name":         policy.Name,
		"status":       policy.Status,
		"effect":       policy.Effect,
		"priority":     policy.Priority,
		"subject_type": policy.SubjectType,
		"subject_id":   policy.SubjectID,
		"channel":      channel,
	})
}
