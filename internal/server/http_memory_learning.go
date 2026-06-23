package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/hermawan22/abra/internal/brain"
	"github.com/hermawan22/abra/internal/store"
)

func (h *handler) rememberClaim(w http.ResponseWriter, r *http.Request) {
	var input brain.RememberClaimInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, input.Scope) {
		return
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:        "agent_write",
		Scope:         input.Scope,
		TargetType:    "memory_write",
		TargetID:      input.Scope,
		ApprovalID:    input.ApprovalID,
		PrincipalType: "agent",
		PrincipalID:   input.CreatedBy,
	}) {
		return
	}
	result, err := h.brain.RememberClaim(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) captureObservation(w http.ResponseWriter, r *http.Request) {
	var input brain.CaptureObservationInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.Scope = strings.TrimSpace(input.Scope)
	if !h.requireAccess(w, r, authActionWrite, input.Scope) {
		return
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:        "agent_write",
		Scope:         input.Scope,
		TargetType:    "memory_write",
		TargetID:      input.Scope,
		ApprovalID:    input.ApprovalID,
		PrincipalType: "agent",
		PrincipalID:   input.CreatedBy,
	}) {
		return
	}
	result, err := h.brain.CaptureObservation(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) listObservations(w http.ResponseWriter, r *http.Request) {
	input := brain.ListObservationsInput{
		Scope:           strings.TrimSpace(r.URL.Query().Get("scope")),
		Query:           strings.TrimSpace(r.URL.Query().Get("query")),
		ObservationType: strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("observation_type"), r.URL.Query().Get("type"))),
		Status:          strings.TrimSpace(r.URL.Query().Get("status")),
		Since:           strings.TrimSpace(r.URL.Query().Get("since")),
		Until:           strings.TrimSpace(r.URL.Query().Get("until")),
		Limit:           intQuery(r, "limit", 20),
	}
	if !h.requireAccess(w, r, authActionRead, input.Scope) {
		return
	}
	observations, err := h.brain.ListObservations(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"observations": observations})
}

func (h *handler) challengeClaim(w http.ResponseWriter, r *http.Request) {
	var input brain.ChallengeClaimInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.ClaimID = r.PathValue("claimId")
	scope, err := h.db.ClaimScope(r.Context(), strings.TrimSpace(input.ClaimID))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, scope) {
		return
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:        "challenge_claim",
		Scope:         scope,
		TargetType:    "claim",
		TargetID:      input.ClaimID,
		ApprovalID:    input.ApprovalID,
		PrincipalType: "agent",
		PrincipalID:   input.CreatedBy,
	}) {
		return
	}
	result, err := h.brain.ChallengeClaim(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) forgetClaim(w http.ResponseWriter, r *http.Request) {
	var input brain.ForgetClaimInput
	_ = json.NewDecoder(r.Body).Decode(&input)
	input.ClaimID = r.PathValue("claimId")
	scope, err := h.db.ClaimScope(r.Context(), strings.TrimSpace(input.ClaimID))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, scope) {
		return
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:        "forget_claim",
		Scope:         scope,
		TargetType:    "claim",
		TargetID:      input.ClaimID,
		ApprovalID:    input.ApprovalID,
		PrincipalType: "agent",
		PrincipalID:   input.CreatedBy,
	}) {
		return
	}
	result, err := h.brain.ForgetClaim(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) sources(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Query string `json:"query"`
		Scope string `json:"scope"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if !h.requireAccess(w, r, authActionRead, input.Scope) {
		return
	}
	if input.Limit == 0 {
		input.Limit = 5
	}
	docs, err := h.db.Sources(r.Context(), input.Query, input.Scope, input.Limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": docs})
}

func (h *handler) memoryHealth(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if !h.requireAccess(w, r, authActionRead, scope) {
		return
	}
	result, err := h.db.MemoryHealth(r.Context(), scope)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) memorySummaries(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Query string `json:"query"`
		Scope string `json:"scope"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Scope == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scope is required"})
		return
	}
	if !h.requireAccess(w, r, authActionRead, input.Scope) {
		return
	}
	if input.Limit == 0 {
		input.Limit = 10
	}
	result, err := h.db.ListMemorySummaries(r.Context(), input.Query, input.Scope, input.Limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"summaries": result})
}

func (h *handler) rebuildMemorySummaries(w http.ResponseWriter, r *http.Request) {
	var input brain.RebuildSummariesInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Scope == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scope is required"})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, input.Scope) {
		return
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     "backfill",
		Scope:      input.Scope,
		TargetType: "memory_summaries",
		TargetID:   input.Scope,
		ApprovalID: input.ApprovalID,
	}) {
		return
	}
	result, err := h.brain.RebuildSummaries(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) listLearningProposals(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if !h.requireAccess(w, r, authActionRead, scope) {
		return
	}
	proposals, err := h.db.ListLearningProposals(r.Context(), scope, strings.TrimSpace(r.URL.Query().Get("status")), intQuery(r, "limit", 50))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"learning_proposals": proposals})
}

func (h *handler) createLearningProposal(w http.ResponseWriter, r *http.Request) {
	var input store.CreateLearningProposalInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Scope == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scope is required"})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, input.Scope) {
		return
	}
	observation, hasObservationTarget, ok := h.prepareObservationLearningProposal(w, r, &input)
	if !ok {
		return
	}
	var proposal store.LearningProposalRecord
	created := true
	var err error
	if hasObservationTarget {
		proposal, created, err = h.db.CreateLearningProposalOnce(r.Context(), input)
	} else {
		proposal, err = h.db.CreateLearningProposal(r.Context(), input)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if hasObservationTarget {
		h.linkObservationLearningProposal(r.Context(), observation, proposal, input.CreatedBy, "http")
	}
	if created {
		h.auditLearningProposed(r.Context(), proposal, "http")
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"learning_proposal": proposal, "created": created})
}

func (h *handler) prepareObservationLearningProposal(w http.ResponseWriter, r *http.Request, input *store.CreateLearningProposalInput) (store.ObservationResult, bool, bool) {
	observation, hasObservationTarget, err := h.prepareObservationLearningProposalInput(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return store.ObservationResult{}, hasObservationTarget, false
	}
	return observation, hasObservationTarget, true
}

func (h *handler) prepareMCPObservationLearningProposal(ctx context.Context, input *store.CreateLearningProposalInput) (store.ObservationResult, bool, error) {
	return h.prepareObservationLearningProposalInput(ctx, input)
}

func (h *handler) prepareObservationLearningProposalInput(ctx context.Context, input *store.CreateLearningProposalInput) (store.ObservationResult, bool, error) {
	if strings.TrimSpace(input.TargetType) != "observation" {
		return store.ObservationResult{}, false, nil
	}
	observationID := strings.TrimSpace(input.TargetID)
	if observationID == "" && input.Payload != nil {
		if raw, ok := input.Payload["observation_id"].(string); ok {
			observationID = strings.TrimSpace(raw)
		}
	}
	if observationID == "" {
		return store.ObservationResult{}, true, fmt.Errorf("target_id is required when target_type is observation")
	}
	observation, err := h.db.GetObservation(ctx, observationID)
	if err != nil {
		return store.ObservationResult{}, true, err
	}
	if observation.Scope != strings.TrimSpace(input.Scope) {
		return store.ObservationResult{}, true, fmt.Errorf("observation scope does not match proposal scope")
	}
	if observation.Status == "rejected" || observation.Status == "deprecated" || observation.Status == "expired" || observation.Status == "accepted" {
		return store.ObservationResult{}, true, fmt.Errorf("observation status cannot be proposed")
	}
	input.TargetID = observation.ID
	if strings.TrimSpace(input.ProposalType) == "" {
		input.ProposalType = "claim"
	}
	if strings.TrimSpace(input.Title) == "" {
		input.Title = observationLearningTitle(observation)
	}
	if strings.TrimSpace(input.Rationale) == "" {
		input.Rationale = "Review raw observation as a trusted memory candidate."
	}
	if strings.TrimSpace(input.SourceURL) == "" {
		input.SourceURL = observation.SourceURL
	}
	if input.Confidence <= 0 {
		input.Confidence = observation.Confidence
	}
	payload := cloneAnyMap(input.Payload)
	payload["observation_id"] = observation.ID
	payload["observation_text"] = observation.ObservationText
	payload["observation_type"] = observation.ObservationType
	payload["observation_status"] = observation.Status
	payload["promotion_flow"] = "observation_to_" + input.ProposalType
	if _, ok := payload["claim"]; !ok && input.ProposalType == "claim" {
		payload["claim"] = observation.ObservationText
	}
	input.Payload = payload
	return observation, true, nil
}

func (h *handler) linkObservationLearningProposal(ctx context.Context, observation store.ObservationResult, proposal store.LearningProposalRecord, createdBy, channel string) {
	linked, err := h.db.LinkObservationProposal(ctx, observation.ID, proposal.ID, createdBy)
	if err != nil {
		return
	}
	_ = h.db.InsertAuditEvent(ctx, "observation.proposed", "observation", observation.ID, observation.Scope, observation.SourceURL, map[string]any{
		"learning_proposal_id": proposal.ID,
		"proposal_type":        proposal.ProposalType,
		"observation_status":   linked.Status,
		"created_by":           createdBy,
		"channel":              channel,
	})
}

func observationLearningTitle(observation store.ObservationResult) string {
	text := strings.Join(strings.Fields(observation.ObservationText), " ")
	if text == "" {
		text = observation.ID
	}
	runes := []rune(text)
	if len(runes) > 80 {
		text = string(runes[:77]) + "..."
	}
	return "Review observation: " + text
}

func (h *handler) decideLearningProposal(w http.ResponseWriter, r *http.Request) {
	proposal, err := h.db.GetLearningProposal(r.Context(), r.PathValue("proposalId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, proposal.Scope) {
		return
	}
	var input store.DecideLearningProposalInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	decided, err := h.db.DecideLearningProposal(r.Context(), proposal.ID, input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.auditLearningDecided(r.Context(), decided, "http")
	writeJSON(w, http.StatusOK, map[string]any{"learning_proposal": decided, "apply_plan": buildLearningApplyPlan(decided, h.cfg.ApprovalMode)})
}

func (h *handler) applyLearningProposalHTTP(w http.ResponseWriter, r *http.Request) {
	proposal, err := h.db.GetLearningProposal(r.Context(), r.PathValue("proposalId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, proposal.Scope) {
		return
	}
	var input applyLearningProposalInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if !h.requireLearningApplyApproval(w, r, proposal, input) {
		return
	}
	appliedBy := firstNonEmpty(strings.TrimSpace(input.AppliedBy), proposal.ReviewedBy, proposal.CreatedBy, "api")
	approvalID := firstNonEmpty(strings.TrimSpace(input.ApprovalID), proposal.ApprovalID)
	claimed, err := h.db.BeginLearningProposalApply(r.Context(), proposal.ID, store.ApplyLearningProposalInput{
		AppliedBy:  appliedBy,
		ApprovalID: approvalID,
		Metadata:   input.Metadata,
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	applyResult, err := h.applyLearningProposal(r.Context(), claimed, input)
	if err != nil {
		_, _ = h.db.ResetLearningProposalApply(r.Context(), proposal.ID, store.ApplyLearningProposalInput{
			Metadata: map[string]any{"apply_channel": "http"},
		}, err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	applied, err := h.db.MarkLearningProposalApplied(r.Context(), proposal.ID, store.ApplyLearningProposalInput{
		AppliedBy:  appliedBy,
		ApprovalID: approvalID,
		Metadata: mergeWebhookMetadata(input.Metadata, map[string]any{
			"apply_result": applyResult,
		}),
	})
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.auditLearningApplied(r.Context(), applied, "http", applyResult)
	writeJSON(w, http.StatusOK, map[string]any{"learning_proposal": applied, "apply_plan": buildLearningApplyPlan(applied, h.cfg.ApprovalMode), "apply_result": applyResult})
}
