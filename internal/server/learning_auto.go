package server

import (
	"context"
	"strings"

	"github.com/hermawan22/abra/internal/memory"
	"github.com/hermawan22/abra/internal/store"
)

func (h *handler) persistComposeLearningSuggestions(ctx context.Context, result *memory.ComposeResult, createdBy string) {
	if result == nil || len(result.LearningSuggestions) == 0 {
		return
	}
	createdBy = strings.TrimSpace(createdBy)
	if createdBy == "" {
		createdBy = "working-memory-compose"
	}
	for i := range result.LearningSuggestions {
		suggestion := result.LearningSuggestions[i]
		if !shouldPersistLearningSuggestion(suggestion) {
			continue
		}
		payload := cloneAnyMap(suggestion.Payload)
		payload["auto_persisted"] = true
		payload["memory_task"] = result.Task
		payload["memory_verdict"] = result.Verification.Verdict
		payload["agent_decision"] = result.AgentDecision.Decision
		proposal, created, err := h.db.CreateLearningProposalOnce(ctx, store.CreateLearningProposalInput{
			Scope:        result.Scope,
			ProposalType: suggestion.ProposalType,
			Title:        suggestion.Title,
			Rationale:    suggestion.Rationale,
			TargetType:   suggestion.TargetType,
			TargetID:     suggestion.TargetID,
			SourceURL:    suggestion.SourceURL,
			Confidence:   suggestion.Confidence,
			Payload:      payload,
			CreatedBy:    createdBy,
		})
		if err != nil {
			if result.LearningSuggestions[i].Payload == nil {
				result.LearningSuggestions[i].Payload = map[string]any{}
			}
			result.LearningSuggestions[i].Payload["persistence_error"] = err.Error()
			continue
		}
		result.LearningSuggestions[i].ProposalID = proposal.ID
		result.LearningSuggestions[i].Persisted = true
		result.LearningSuggestions[i].PersistedNew = created
		if created {
			_ = h.db.InsertAuditEvent(ctx, "learning.auto_proposed", "learning_proposal", proposal.ID, proposal.Scope, proposal.SourceURL, map[string]any{
				"proposal_type": proposal.ProposalType,
				"target_type":   proposal.TargetType,
				"target_id":     proposal.TargetID,
				"created_by":    proposal.CreatedBy,
				"memory_task":   result.Task,
				"verdict":       result.Verification.Verdict,
			})
		}
	}
}

func shouldPersistLearningSuggestion(suggestion memory.LearningSuggestion) bool {
	if strings.TrimSpace(suggestion.ProposalType) == "" || strings.TrimSpace(suggestion.Title) == "" || strings.TrimSpace(suggestion.Rationale) == "" {
		return false
	}
	if suggestion.ProposalType == "other" {
		return false
	}
	return !strings.EqualFold(strings.TrimSpace(suggestion.Title), "No learning action required")
}

func cloneAnyMap(input map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range input {
		out[key] = value
	}
	return out
}
