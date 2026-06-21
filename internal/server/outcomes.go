package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hermawan22/abra/internal/memory"
	"github.com/hermawan22/abra/internal/store"
)

const (
	repeatedOutcomeObservationLimit = 10
	repeatedOutcomeProposalLimit    = 5
)

func (h *handler) captureTaskOutcomeHTTP(w http.ResponseWriter, r *http.Request) {
	var input memory.TaskOutcomeInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input = memory.NormalizeTaskOutcome(input)
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
		PrincipalID:   firstNonEmpty(input.CreatedBy, input.Agent),
	}) {
		return
	}
	result, err := h.captureTaskOutcome(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (h *handler) captureTaskOutcome(ctx context.Context, input memory.TaskOutcomeInput) (memory.TaskOutcomeCaptureResult, error) {
	input = memory.NormalizeTaskOutcome(input)
	if input.Scope == "" {
		return memory.TaskOutcomeCaptureResult{}, fmt.Errorf("scope is required")
	}
	if input.Task == "" {
		return memory.TaskOutcomeCaptureResult{}, fmt.Errorf("task is required")
	}
	sourceID := memory.TaskOutcomeSourceID(input)
	sourceURL := memory.TaskOutcomeSourceURL(input)
	createdBy := strings.TrimSpace(input.CreatedBy)
	if createdBy == "" {
		createdBy = firstNonEmpty(input.Agent, "task-outcome")
	}
	observation, err := h.db.InsertObservation(ctx, store.ObservationRecord{
		ID:              sourceID,
		Scope:           input.Scope,
		ObservationType: "task_outcome",
		ObservationText: memory.TaskOutcomeObservationText(input),
		Status:          "raw",
		Authority:       "agent-reported",
		AuthorityScore:  0.45,
		Confidence:      outcomeConfidence(input),
		FreshnessStatus: "fresh",
		SourceURL:       sourceURL,
		SourceType:      "task_outcome",
		SourceID:        sourceID,
		ObservedAt:      input.CompletedAt,
		CreatedBy:       createdBy,
		Value:           memory.TaskOutcomeValue(input),
		Metadata:        memory.TaskOutcomeMetadata(input),
	})
	if err != nil {
		return memory.TaskOutcomeCaptureResult{}, err
	}
	_ = h.db.InsertAuditEvent(ctx, "memory.task_outcome_captured", "observation", observation.ID, observation.Scope, observation.SourceURL, map[string]any{
		"task":                   input.Task,
		"hook":                   input.Hook,
		"agent":                  input.Agent,
		"outcome":                input.Outcome,
		"files_changed":          input.FilesChanged,
		"commands_run":           memory.TaskOutcomeValue(input)["commands_run"],
		"tests_result":           memory.TaskOutcomeValue(input)["tests_result"],
		"missing_context":        input.MissingContext,
		"memory_refs_used":       memory.TaskOutcomeValue(input)["memory_refs_used"],
		"bounded":                true,
		"auto_promoted":          false,
		"trusted_claim_created":  false,
		"source_backed_observe":  true,
		"learning_review_needed": true,
		"created_by":             createdBy,
	})
	proposals, patterns := h.proposeRepeatedOutcomeLearning(ctx, input, observation, createdBy)
	return memory.TaskOutcomeCaptureResult{
		Observation:         observation,
		LearningProposals:   proposals,
		LearningProposalNew: len(proposals),
		PatternsConsidered:  patterns,
	}, nil
}

func outcomeConfidence(input memory.TaskOutcomeInput) float64 {
	confidence := 0.45
	if len(input.MemoryRefsUsed) > 0 {
		confidence += 0.1
	}
	if input.Tests.Status == "passed" || input.Tests.Status == "failed" {
		confidence += 0.1
	}
	if len(input.CommandsRun) > 0 {
		confidence += 0.05
	}
	if confidence > 0.75 {
		return 0.75
	}
	return confidence
}

func (h *handler) proposeRepeatedOutcomeLearning(ctx context.Context, input memory.TaskOutcomeInput, current store.ObservationResult, createdBy string) ([]store.LearningProposalRecord, []memory.OutcomePattern) {
	proposals := []store.LearningProposalRecord{}
	patterns := []memory.OutcomePattern{}
	addProposal := func(pattern memory.OutcomePattern, proposalType, title, rationale string, payload map[string]any) {
		if len(proposals) >= repeatedOutcomeProposalLimit {
			return
		}
		patternID := memory.OutcomePatternID(input.Scope, pattern.Kind, pattern.Key)
		if payload == nil {
			payload = map[string]any{}
		}
		payload["pattern"] = pattern
		payload["current_observation_id"] = current.ID
		payload["task_outcome_feedback"] = true
		payload["auto_promoted"] = false
		proposal, created, err := h.db.CreateLearningProposalOnce(ctx, store.CreateLearningProposalInput{
			Scope:        input.Scope,
			ProposalType: proposalType,
			Title:        title,
			Rationale:    rationale,
			TargetType:   "task_outcome_pattern",
			TargetID:     patternID,
			SourceURL:    "abra://task-outcome-pattern/" + patternID,
			Confidence:   0.68,
			Payload:      payload,
			CreatedBy:    createdBy,
		})
		if err != nil || !created {
			return
		}
		proposals = append(proposals, proposal)
		_ = h.db.InsertAuditEvent(ctx, "learning.outcome_pattern_proposed", "learning_proposal", proposal.ID, proposal.Scope, proposal.SourceURL, map[string]any{
			"proposal_type": proposal.ProposalType,
			"target_type":   proposal.TargetType,
			"target_id":     proposal.TargetID,
			"pattern":       pattern,
			"created_by":    createdBy,
		})
	}

	for _, missing := range input.MissingContext {
		observations, err := h.db.ListObservations(ctx, store.ObservationFilter{
			Scope:           input.Scope,
			Query:           missing,
			ObservationType: "task_outcome",
			Limit:           repeatedOutcomeObservationLimit,
		})
		if err != nil {
			continue
		}
		pattern := repeatedMissingContextPattern(missing, observations)
		if pattern.Occurrences < 2 {
			continue
		}
		patterns = append(patterns, pattern)
		addProposal(pattern, "ingestion",
			"Add source coverage for repeated missing context",
			"Task outcome captures repeatedly reported the same missing context. Add or refresh source-backed documentation before treating the gap as resolved.",
			map[string]any{
				"missing_context": missing,
				"suggested_actions": []string{
					"ingest_source_for_missing_context",
					"refresh_related_documents",
					"rerun_working_memory_compose_after_ingestion",
				},
			},
		)
	}

	for _, command := range failedOutcomeCommands(input) {
		observations, err := h.db.ListObservations(ctx, store.ObservationFilter{
			Scope:           input.Scope,
			Query:           command,
			ObservationType: "task_outcome",
			Limit:           repeatedOutcomeObservationLimit,
		})
		if err != nil {
			continue
		}
		pattern := repeatedFailedCommandPattern(command, observations)
		if pattern.Occurrences < 2 {
			continue
		}
		patterns = append(patterns, pattern)
		addProposal(pattern, "policy",
			"Review repeated failing validation command",
			"Task outcome captures repeatedly reported the same failed validation command. Review the local workflow or policy guidance before agents rely on that command as a clean validation path.",
			map[string]any{
				"command": command,
				"suggested_actions": []string{
					"inspect_repeated_failure_logs",
					"document_required_setup_or_alternative_validation",
					"update_agent_policy_after_review",
				},
			},
		)
	}

	return proposals, patterns
}

func repeatedMissingContextPattern(missing string, observations []store.ObservationResult) memory.OutcomePattern {
	pattern := memory.OutcomePattern{Kind: "missing_context", Key: missing}
	seen := map[string]bool{}
	for _, observation := range observations {
		if !memory.ObservationHasMissingContext(observation, missing) || seen[observation.ID] {
			continue
		}
		seen[observation.ID] = true
		pattern.Occurrences++
		pattern.ObservationIDs = append(pattern.ObservationIDs, observation.ID)
	}
	return pattern
}

func repeatedFailedCommandPattern(command string, observations []store.ObservationResult) memory.OutcomePattern {
	pattern := memory.OutcomePattern{Kind: "failed_command", Key: command}
	seen := map[string]bool{}
	for _, observation := range observations {
		if !memory.ObservationHasFailedCommand(observation, command) || seen[observation.ID] {
			continue
		}
		seen[observation.ID] = true
		pattern.Occurrences++
		pattern.ObservationIDs = append(pattern.ObservationIDs, observation.ID)
	}
	return pattern
}

func failedOutcomeCommands(input memory.TaskOutcomeInput) []string {
	commands := []string{}
	seen := map[string]bool{}
	add := func(command string) {
		command = strings.TrimSpace(command)
		key := strings.ToLower(command)
		if command == "" || seen[key] {
			return
		}
		seen[key] = true
		commands = append(commands, command)
	}
	if input.Tests.Status == "failed" {
		for _, command := range input.Tests.Commands {
			add(command)
		}
	}
	for _, command := range input.CommandsRun {
		if command.Status == "failed" || command.ExitCode != 0 {
			add(command.Command)
		}
	}
	return commands
}
