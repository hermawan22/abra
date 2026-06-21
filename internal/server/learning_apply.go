package server

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/hermawan22/abra/internal/brain"
	"github.com/hermawan22/abra/internal/store"
)

type learningApplyPlan struct {
	ProposalID       string         `json:"proposal_id"`
	ProposalType     string         `json:"proposal_type"`
	Status           string         `json:"status"`
	Ready            bool           `json:"ready"`
	Action           string         `json:"action"`
	Method           string         `json:"method,omitempty"`
	Endpoint         string         `json:"endpoint,omitempty"`
	RequiresApproval bool           `json:"requires_approval"`
	ApprovalAction   string         `json:"approval_action,omitempty"`
	TargetType       string         `json:"target_type,omitempty"`
	TargetID         string         `json:"target_id,omitempty"`
	Payload          map[string]any `json:"payload,omitempty"`
	Notes            []string       `json:"notes,omitempty"`
	Warnings         []string       `json:"warnings,omitempty"`
}

type applyLearningProposalInput struct {
	AppliedBy  string         `json:"applied_by"`
	ApprovalID string         `json:"approval_id"`
	Metadata   map[string]any `json:"metadata"`
}

func (h *handler) auditLearningProposed(ctx context.Context, proposal store.LearningProposalRecord, channel string) {
	_ = h.db.InsertAuditEvent(ctx, "learning.proposed", "learning_proposal", proposal.ID, proposal.Scope, proposal.SourceURL, map[string]any{
		"proposal_type": proposal.ProposalType,
		"target_type":   proposal.TargetType,
		"target_id":     proposal.TargetID,
		"created_by":    proposal.CreatedBy,
		"channel":       channel,
	})
}

func (h *handler) auditLearningDecided(ctx context.Context, proposal store.LearningProposalRecord, channel string) {
	_ = h.db.InsertAuditEvent(ctx, "learning.decided", "learning_proposal", proposal.ID, proposal.Scope, proposal.SourceURL, map[string]any{
		"proposal_type": proposal.ProposalType,
		"status":        proposal.Status,
		"reviewed_by":   proposal.ReviewedBy,
		"channel":       channel,
	})
}

func (h *handler) auditLearningApplied(ctx context.Context, proposal store.LearningProposalRecord, channel string, result any) {
	_ = h.db.InsertAuditEvent(ctx, "learning.applied", "learning_proposal", proposal.ID, proposal.Scope, proposal.SourceURL, map[string]any{
		"proposal_type": proposal.ProposalType,
		"target_type":   proposal.TargetType,
		"target_id":     proposal.TargetID,
		"reviewed_by":   proposal.ReviewedBy,
		"channel":       channel,
		"result":        result,
	})
}

func (h *handler) applyLearningProposal(ctx context.Context, proposal store.LearningProposalRecord, input applyLearningProposalInput) (any, error) {
	if proposal.Status != "accepted" && proposal.Status != "applying" {
		return nil, fmt.Errorf("learning proposal %q is %s and cannot be applied", proposal.ID, proposal.Status)
	}
	appliedBy := firstNonEmpty(strings.TrimSpace(input.AppliedBy), proposal.ReviewedBy, proposal.CreatedBy, "api")
	approvalID := firstNonEmpty(strings.TrimSpace(input.ApprovalID), proposal.ApprovalID)
	payload := cloneAnyMap(proposal.Payload)
	switch proposal.ProposalType {
	case "claim":
		claim := firstNonEmpty(payloadString(payload, "claim"), payloadString(payload, "observation_text"), proposal.Title)
		result, err := h.brain.RememberClaim(ctx, brain.RememberClaimInput{
			Claim:      claim,
			Scope:      proposal.Scope,
			SourceURL:  firstNonEmpty(payloadString(payload, "source_url"), proposal.SourceURL),
			SourceType: firstNonEmpty(payloadString(payload, "source_type"), "learning_proposal"),
			Authority:  firstNonEmpty(payloadString(payload, "authority"), "operator-approved"),
			CreatedBy:  appliedBy,
			ApprovalID: approvalID,
			Metadata: mergeWebhookMetadata(map[string]any{
				"learning_proposal_id": proposal.ID,
				"learning_apply":       true,
			}, mapArgFromAny(payload["metadata"])),
		})
		if err != nil {
			return nil, err
		}
		return result, nil
	case "challenge":
		claimID := firstNonEmpty(proposal.TargetID, payloadString(payload, "claim_id"))
		if claimID == "" {
			return nil, fmt.Errorf("claim proposal target_id or payload.claim_id is required")
		}
		claimScope, err := h.db.ClaimScope(ctx, claimID)
		if err != nil {
			return nil, err
		}
		if claimScope != proposal.Scope {
			return nil, fmt.Errorf("claim scope %q does not match proposal scope %q", claimScope, proposal.Scope)
		}
		result, err := h.brain.ChallengeClaim(ctx, brain.ChallengeClaimInput{
			ClaimID:            claimID,
			Reason:             firstNonEmpty(payloadString(payload, "reason"), proposal.Rationale),
			SourceURL:          firstNonEmpty(payloadString(payload, "source_url"), proposal.SourceURL),
			CreatedBy:          appliedBy,
			Verdict:            payloadString(payload, "verdict"),
			ConflictingClaimID: payloadString(payload, "conflicting_claim_id"),
			Severity:           payloadString(payload, "severity"),
			ApprovalID:         approvalID,
		})
		if err != nil {
			return nil, err
		}
		return result, nil
	case "summary_rebuild":
		result, err := h.brain.RebuildSummaries(ctx, brain.RebuildSummariesInput{
			Scope:      proposal.Scope,
			Limit:      payloadInt(payload, "limit", 1000),
			ApprovalID: approvalID,
		})
		if err != nil {
			return nil, err
		}
		return result, nil
	case "ingestion":
		doc := brain.IngestDocumentInput{
			SourceType:      firstNonEmpty(payloadString(payload, "source_type"), "markdown"),
			SourceURL:       firstNonEmpty(payloadString(payload, "source_url"), proposal.SourceURL),
			SourceID:        payloadString(payload, "source_id"),
			Title:           firstNonEmpty(payloadString(payload, "title"), proposal.Title),
			Scope:           proposal.Scope,
			Content:         payloadString(payload, "content"),
			SourceUpdatedAt: payloadString(payload, "source_updated_at"),
			ApprovalID:      approvalID,
			Metadata: mergeWebhookMetadata(map[string]any{
				"learning_proposal_id": proposal.ID,
				"learning_apply":       true,
			}, mapArgFromAny(payload["metadata"])),
		}
		result, err := h.brain.IngestDocument(ctx, doc)
		if err != nil {
			return nil, err
		}
		return result, nil
	case "source_refresh":
		sourceConfigID := firstNonEmpty(proposal.TargetID, payloadString(payload, "source_config_id"))
		if sourceConfigID == "" {
			return nil, fmt.Errorf("source_refresh requires target_id or payload.source_config_id")
		}
		sourceConfig, err := h.db.GetSourceConfig(ctx, sourceConfigID)
		if err != nil {
			return nil, err
		}
		if sourceConfig.Scope != proposal.Scope {
			return nil, fmt.Errorf("source config scope %q does not match proposal scope %q", sourceConfig.Scope, proposal.Scope)
		}
		result, err := h.db.EnqueueIngestionJob(ctx, store.EnqueueIngestionJobInput{
			SourceConfigID: sourceConfigID,
			TriggerType:    firstNonEmpty(payloadString(payload, "trigger_type"), "revalidate"),
			CreatedBy:      appliedBy,
			ApprovalID:     approvalID,
			MaxAttempts:    payloadInt(payload, "max_attempts", 3),
			Metadata: mergeWebhookMetadata(map[string]any{
				"learning_proposal_id": proposal.ID,
				"learning_apply":       true,
			}, mapArgFromAny(payload["metadata"])),
		})
		if err != nil {
			return nil, err
		}
		return result, nil
	case "graph":
		if proposal.TargetType == "conflict" && strings.TrimSpace(proposal.TargetID) != "" {
			conflict, err := h.db.GetConflict(ctx, proposal.TargetID)
			if err != nil {
				return nil, err
			}
			if conflict.Scope != proposal.Scope {
				return nil, fmt.Errorf("conflict scope %q does not match proposal scope %q", conflict.Scope, proposal.Scope)
			}
			result, err := h.db.ResolveConflict(ctx, proposal.TargetID, store.ResolveConflictInput{
				Status:     firstNonEmpty(payloadString(payload, "status"), "reviewing"),
				ResolvedBy: appliedBy,
				Resolution: firstNonEmpty(payloadString(payload, "resolution"), proposal.Rationale),
				Metadata: mergeWebhookMetadata(map[string]any{
					"learning_proposal_id": proposal.ID,
					"learning_apply":       true,
				}, mapArgFromAny(payload["metadata"])),
			})
			if err != nil {
				return nil, err
			}
			return result, nil
		}
		return nil, fmt.Errorf("graph proposal requires target_type=conflict and target_id")
	default:
		return nil, fmt.Errorf("proposal type %q has no first-party apply executor", proposal.ProposalType)
	}
}

func buildLearningApplyPlan(proposal store.LearningProposalRecord, approvalMode string) learningApplyPlan {
	plan := learningApplyPlan{
		ProposalID:   proposal.ID,
		ProposalType: proposal.ProposalType,
		Status:       proposal.Status,
		TargetType:   proposal.TargetType,
		TargetID:     proposal.TargetID,
		Payload:      cloneAnyMap(proposal.Payload),
	}
	if proposal.Status != "accepted" {
		plan.Action = "none"
		plan.Notes = []string{"proposal is not accepted; no apply action is available"}
		return plan
	}
	plan.Ready = true
	switch proposal.ProposalType {
	case "claim":
		plan.Action = "review_claim_promotion"
		plan.Method = http.MethodPost
		plan.Endpoint = "/claims"
		plan.RequiresApproval = approvalMode == "enforce"
		plan.ApprovalAction = "agent_write"
		plan.TargetType = "memory_write"
		plan.TargetID = proposal.Scope
		plan.Notes = []string{"attach durable source evidence before promoting unverified memory to a trusted claim"}
	case "challenge":
		plan.Action = "apply_claim_challenge"
		plan.Method = http.MethodPost
		claimID := firstNonEmpty(proposal.TargetID, payloadString(proposal.Payload, "claim_id"))
		if claimID != "" {
			plan.TargetID = claimID
			plan.Endpoint = "/claims/" + claimID + "/challenge"
		}
		plan.RequiresApproval = approvalMode == "enforce"
		plan.ApprovalAction = "challenge_claim"
		plan.TargetType = nonEmpty(plan.TargetType, "claim")
		plan.Notes = []string{"review source evidence before challenging or deprecating existing memory"}
	case "summary_rebuild":
		plan.Action = "rebuild_summaries"
		plan.Method = http.MethodPost
		plan.Endpoint = "/memory/summaries/rebuild"
		plan.RequiresApproval = approvalMode == "enforce"
		plan.ApprovalAction = "backfill"
		plan.TargetType = nonEmpty(plan.TargetType, "memory_summaries")
		plan.TargetID = nonEmpty(plan.TargetID, proposal.Scope)
	case "source_refresh":
		plan.Action = "refresh_source"
		plan.Method = http.MethodPost
		plan.Endpoint = "/ingestion/jobs"
		plan.RequiresApproval = approvalMode == "enforce"
		plan.ApprovalAction = "backfill"
		plan.TargetType = nonEmpty(plan.TargetType, "source_config")
		plan.TargetID = firstNonEmpty(plan.TargetID, payloadString(proposal.Payload, "source_config_id"))
		plan.Notes = []string{"refresh through the source config or connector that owns the cited source URL"}
	case "ingestion":
		plan.Action = "ingest_stronger_source"
		plan.Method = http.MethodPost
		plan.Endpoint = "/ingest/documents"
		plan.RequiresApproval = approvalMode == "enforce"
		plan.ApprovalAction = "agent_write"
		plan.TargetType = nonEmpty(plan.TargetType, "memory_write")
		plan.TargetID = nonEmpty(plan.TargetID, proposal.Scope)
	case "graph":
		plan.Action = "review_graph_update"
		plan.Method = http.MethodPost
		if proposal.TargetType == "conflict" && strings.TrimSpace(proposal.TargetID) != "" {
			plan.Endpoint = "/conflicts/" + proposal.TargetID + "/resolve"
			plan.Notes = []string{"resolve or suppress the graph conflict before treating the relation as settled memory"}
		} else {
			plan.Endpoint = "/ingest/documents"
			plan.Notes = []string{"re-ingest the owning source to refresh graph entities and relations"}
		}
		plan.RequiresApproval = false
	case "policy":
		plan.Ready = false
		plan.Action = "manual_review"
		plan.RequiresApproval = false
		plan.Warnings = []string{"policy proposals must be applied manually through the policy API after operator review"}
	default:
		plan.Action = "manual_review"
		plan.Warnings = []string{"proposal type has no deterministic apply route yet"}
	}
	if plan.Endpoint == "" && plan.Ready {
		plan.Warnings = append(plan.Warnings, "target_id is required before this proposal can be applied")
	}
	return plan
}

func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func payloadInt(payload map[string]any, key string, fallback int) int {
	switch value := payload[key].(type) {
	case int:
		if value > 0 {
			return value
		}
	case int64:
		if value > 0 {
			return int(value)
		}
	case float64:
		if value > 0 {
			return int(value)
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}

func mapArgFromAny(value any) map[string]any {
	typed, _ := value.(map[string]any)
	return typed
}
