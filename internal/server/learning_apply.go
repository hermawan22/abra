package server

import (
	"context"
	"net/http"
	"strings"

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
		if strings.TrimSpace(proposal.TargetID) != "" {
			plan.Endpoint = "/claims/" + proposal.TargetID + "/challenge"
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
		plan.Endpoint = "/sources/configs"
		plan.RequiresApproval = approvalMode == "enforce"
		plan.ApprovalAction = "source_authority_change"
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
		plan.Action = "review_policy_change"
		plan.Method = http.MethodPost
		plan.Endpoint = "/agent/policies"
		plan.RequiresApproval = approvalMode == "enforce"
		plan.ApprovalAction = "acl_change"
		plan.TargetType = nonEmpty(plan.TargetType, "policy")
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
