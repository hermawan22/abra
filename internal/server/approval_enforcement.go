package server

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"

	"github.com/hermawan22/abra/internal/brain"
	"github.com/hermawan22/abra/internal/ingest"
	"github.com/hermawan22/abra/internal/jobs"
	"github.com/hermawan22/abra/internal/store"
)

var sourceSchedulePattern = regexp.MustCompile(`^@every[[:space:]]+[1-9][0-9]*[smhd]$`)

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

func (h *handler) requireIngestApproval(w http.ResponseWriter, r *http.Request, scope, approvalID string) bool {
	scope = strings.TrimSpace(scope)
	return h.requireRiskApproval(w, r, approvalRequirement{
		Action:     "agent_write",
		Scope:      scope,
		TargetType: "memory_write",
		TargetID:   scope,
		ApprovalID: strings.TrimSpace(approvalID),
	})
}

func (h *handler) requireIngestDocumentsApproval(w http.ResponseWriter, r *http.Request, docs []brain.IngestDocumentInput, approvalID string) bool {
	seen := map[string]bool{}
	for _, doc := range docs {
		scope := strings.TrimSpace(doc.Scope)
		if scope == "" || seen[scope] {
			continue
		}
		seen[scope] = true
		if !h.requireIngestApproval(w, r, scope, firstNonEmpty(strings.TrimSpace(doc.ApprovalID), approvalID)) {
			return false
		}
	}
	return true
}

func (h *handler) requireLearningApplyApproval(w http.ResponseWriter, r *http.Request, proposal store.LearningProposalRecord, input applyLearningProposalInput) bool {
	plan := buildLearningApplyPlan(proposal, h.cfg.ApprovalMode)
	if !plan.RequiresApproval {
		return true
	}
	payload := cloneAnyMap(proposal.Payload)
	targetType := firstNonEmpty(plan.TargetType, proposal.TargetType, "learning_proposal")
	targetID := firstNonEmpty(plan.TargetID, proposal.TargetID, proposal.ID)
	switch proposal.ProposalType {
	case "challenge":
		targetID = firstNonEmpty(proposal.TargetID, payloadString(payload, "claim_id"), targetID)
	case "source_refresh":
		targetID = firstNonEmpty(proposal.TargetID, payloadString(payload, "source_config_id"), targetID)
	}
	return h.requireRiskApproval(w, r, approvalRequirement{
		Action:        plan.ApprovalAction,
		Scope:         proposal.Scope,
		TargetType:    targetType,
		TargetID:      targetID,
		ApprovalID:    firstNonEmpty(strings.TrimSpace(input.ApprovalID), proposal.ApprovalID),
		PrincipalType: "agent",
		PrincipalID:   firstNonEmpty(strings.TrimSpace(input.AppliedBy), proposal.ReviewedBy, proposal.CreatedBy, "unknown"),
	})
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
	requestCommand := "abra approvals request --scope " + shellQuoteForResponse(requirement.Scope) + " --action " + shellQuoteForResponse(requirement.Action)
	if requirement.TargetType != "" {
		requestCommand += " --target-type " + shellQuoteForResponse(requirement.TargetType)
	}
	if requirement.TargetID != "" {
		requestCommand += " --target-id " + shellQuoteForResponse(requirement.TargetID)
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":   "approval_required",
		"message": "create and approve an approval request, then retry the operation with approval_id",
		"detail":  detail,
		"next_steps": []string{
			requestCommand,
			"retry the original operation with approval_id after approval",
		},
		"approval": map[string]any{
			"action":      requirement.Action,
			"scope":       requirement.Scope,
			"target_type": requirement.TargetType,
			"target_id":   requirement.TargetID,
		},
	})
}

func shellQuoteForResponse(value string) string {
	if value == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func sourceAuthorityApprovalRequired(input store.SourceConfigRecord) bool {
	authority := strings.TrimSpace(input.Authority)
	return (authority != "" && authority != "manual-unverified") || input.AuthorityScore > 0.35
}

func sourceConnectorEnableApprovalRequired(input store.SourceConfigRecord) bool {
	status := strings.TrimSpace(input.Status)
	return status == "" || status == "active"
}

func sourceConfigApprovalAction(input store.SourceConfigRecord) string {
	if sourceAuthorityApprovalRequired(input) {
		return "source_authority_change"
	}
	if sourceConnectorEnableApprovalRequired(input) {
		return "connector_enable"
	}
	return ""
}

func sourceConfigApprovalActionForStatus(input store.SourceConfigRecord, status string) string {
	input.Status = strings.TrimSpace(status)
	return sourceConfigApprovalAction(input)
}

func sourceValidationApprovalAction(input store.SourceConfigRecord) string {
	if sourceValidationUsesServerCredentialEnv(input) {
		return "connector_enable"
	}
	return ""
}

func sourceValidationUsesServerCredentialEnv(input store.SourceConfigRecord) bool {
	return strings.TrimSpace(configString(input.Config, "bearer_token_env")) != "" || len(configStringMap(input.Config, "header_env")) > 0
}

func sourceConfigApprovalTarget(input store.SourceConfigRecord) string {
	if id := strings.TrimSpace(input.ID); id != "" {
		return id
	}
	return strings.Join([]string{strings.TrimSpace(input.Scope), strings.TrimSpace(input.SourceType), strings.TrimSpace(input.Name)}, "/")
}

func validateSourceConfigInput(input store.SourceConfigRecord) error {
	if err := validateSourceFreshness(input.FreshnessPolicy, input.ScheduleCron); err != nil {
		return err
	}
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
		ConnectorKind:  strings.TrimSpace(input.ConnectorKind),
		Authority:      strings.TrimSpace(input.Authority),
		AuthorityScore: input.AuthorityScore,
		Config:         input.Config,
		Metadata:       input.Metadata,
	}.ValidateIngestContract()
}

func configString(config map[string]any, key string) string {
	if config == nil {
		return ""
	}
	value, _ := config[key].(string)
	return strings.TrimSpace(value)
}

func configStringMap(config map[string]any, key string) map[string]string {
	if config == nil {
		return nil
	}
	switch raw := config[key].(type) {
	case map[string]string:
		return raw
	case map[string]any:
		values := map[string]string{}
		for rawKey, rawValue := range raw {
			key := strings.TrimSpace(rawKey)
			value, _ := rawValue.(string)
			value = strings.TrimSpace(value)
			if key != "" && value != "" {
				values[key] = value
			}
		}
		return values
	default:
		return nil
	}
}

func validateSourceFreshness(policy map[string]any, schedule string) error {
	allowedKeys := map[string]struct{}{
		"max_age_seconds": {},
		"max_age_minutes": {},
		"max_age_hours":   {},
		"max_age_days":    {},
	}
	for key, value := range policy {
		if _, ok := allowedKeys[key]; !ok {
			return fmt.Errorf("freshness_policy contains unsupported key %q", key)
		}
		if !positiveJSONNumber(value) {
			return fmt.Errorf("freshness_policy.%s must be a positive number", key)
		}
	}
	schedule = strings.TrimSpace(schedule)
	if schedule == "" || schedule == "@hourly" || schedule == "@daily" || sourceSchedulePattern.MatchString(schedule) {
		return nil
	}
	return fmt.Errorf("schedule_cron must be @hourly, @daily, or @every <positive integer><s|m|h|d>")
}

func positiveJSONNumber(value any) bool {
	switch typed := value.(type) {
	case int:
		return typed > 0
	case int32:
		return typed > 0
	case int64:
		return typed > 0
	case float32:
		return typed > 0 && typed == float32(int64(typed))
	case float64:
		return typed > 0 && typed == math.Trunc(typed)
	case json.Number:
		return positiveNumberText(typed.String())
	default:
		return false
	}
}

func positiveNumberText(value string) bool {
	text := strings.TrimSpace(value)
	if text == "" || strings.HasPrefix(text, "-") || text == "0" {
		return false
	}
	for _, ch := range text {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}
