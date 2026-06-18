package server

import (
	"net/http"
	"strings"

	"github.com/hermawan22/abra/internal/ingest"
	"github.com/hermawan22/abra/internal/jobs"
	"github.com/hermawan22/abra/internal/store"
)

type approvalRequirement struct {
	Action        string
	Scope         string
	TargetType    string
	TargetID      string
	ApprovalID    string
	PrincipalType string
	PrincipalID   string
}

func (h *handler) requireRiskApproval(w http.ResponseWriter, r *http.Request, requirement approvalRequirement) bool {
	requirement.Action = strings.TrimSpace(requirement.Action)
	requirement.Scope = strings.TrimSpace(requirement.Scope)
	requirement.TargetType = strings.TrimSpace(requirement.TargetType)
	requirement.TargetID = strings.TrimSpace(requirement.TargetID)
	requirement.ApprovalID = strings.TrimSpace(requirement.ApprovalID)
	requirement.PrincipalType = strings.TrimSpace(requirement.PrincipalType)
	requirement.PrincipalID = strings.TrimSpace(requirement.PrincipalID)
	if requirement.Action == "" || requirement.Scope == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "approval action and scope are required"})
		return false
	}
	if requirement.PrincipalType == "" {
		requirement.PrincipalType = "agent"
	}
	if requirement.PrincipalID == "" {
		requirement.PrincipalID = "unknown"
	}
	decision, err := h.db.EvaluateAgentActionPolicy(r.Context(), store.AgentActionDecisionInput{
		Action:        requirement.Action,
		Scope:         requirement.Scope,
		TargetType:    requirement.TargetType,
		TargetID:      requirement.TargetID,
		PrincipalType: requirement.PrincipalType,
		PrincipalID:   requirement.PrincipalID,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return false
	}
	switch decision.Decision {
	case "deny":
		writePolicyDenied(w, requirement, decision)
		return false
	case "allow":
		return true
	case "require_review":
		return h.requireApprovedRisk(w, r, requirement, "agent action policy requires review")
	}
	if h.cfg.ApprovalMode != "enforce" {
		return true
	}
	return h.requireApprovedRisk(w, r, requirement, "approval_id is required")
}

func (h *handler) requireApprovedRisk(w http.ResponseWriter, r *http.Request, requirement approvalRequirement, missingDetail string) bool {
	if requirement.ApprovalID == "" {
		writeApprovalRequired(w, requirement, missingDetail)
		return false
	}
	if _, err := h.db.ApprovedApprovalFor(r.Context(), requirement.ApprovalID, requirement.Action, requirement.Scope, requirement.TargetType, requirement.TargetID); err != nil {
		writeApprovalRequired(w, requirement, err.Error())
		return false
	}
	return true
}

func writePolicyDenied(w http.ResponseWriter, requirement approvalRequirement, decision store.AgentActionDecisionResult) {
	payload := map[string]any{
		"error":   "policy_denied",
		"message": "stored agent action policy denied this operation",
		"policy": map[string]any{
			"action":         requirement.Action,
			"scope":          requirement.Scope,
			"target_type":    requirement.TargetType,
			"target_id":      requirement.TargetID,
			"principal_type": requirement.PrincipalType,
			"principal_id":   requirement.PrincipalID,
			"reason":         decision.Reason,
		},
	}
	if decision.MatchedPolicy != nil {
		payload["matched_policy"] = decision.MatchedPolicy
	}
	writeJSON(w, http.StatusForbidden, payload)
}

func writeApprovalRequired(w http.ResponseWriter, requirement approvalRequirement, detail string) {
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":   "approval_required",
		"message": "create and approve an approval request, then retry the operation with approval_id",
		"detail":  detail,
		"approval": map[string]any{
			"action":      requirement.Action,
			"scope":       requirement.Scope,
			"target_type": requirement.TargetType,
			"target_id":   requirement.TargetID,
		},
	})
}

func sourceAuthorityApprovalRequired(input store.SourceConfigRecord) bool {
	authority := strings.TrimSpace(input.Authority)
	return (authority != "" && authority != "manual-unverified") || input.AuthorityScore > 0.35
}

func sourceConfigApprovalTarget(input store.SourceConfigRecord) string {
	if id := strings.TrimSpace(input.ID); id != "" {
		return id
	}
	return strings.Join([]string{strings.TrimSpace(input.Scope), strings.TrimSpace(input.SourceType), strings.TrimSpace(input.Name)}, "/")
}

func validateSourceConfigInput(input store.SourceConfigRecord) error {
	id := strings.TrimSpace(input.ID)
	if id == "" {
		id = sourceConfigApprovalTarget(input)
	}
	return jobs.SourceConfig{
		ID:             id,
		Scope:          strings.TrimSpace(input.Scope),
		SourceType:     ingest.SourceType(strings.TrimSpace(input.SourceType)),
		Name:           strings.TrimSpace(input.Name),
		BaseURL:        strings.TrimSpace(input.BaseURL),
		Authority:      strings.TrimSpace(input.Authority),
		AuthorityScore: input.AuthorityScore,
		Config:         input.Config,
		Metadata:       input.Metadata,
	}.ValidateIngestContract()
}
