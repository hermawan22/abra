package brain

import (
	"context"
	"fmt"
	"strings"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/store"
)

func (s *Service) RememberClaim(ctx context.Context, input RememberClaimInput) (RememberClaimResult, error) {
	input.Claim = strings.TrimSpace(input.Claim)
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Claim == "" || input.Scope == "" {
		return RememberClaimResult{}, fmt.Errorf("claim and scope are required")
	}
	claimText := input.Claim
	if s.cfg.RedactPII {
		claimText = redact(claimText)
	}
	status := "unverified"
	confidence := 0.25
	if strings.TrimSpace(input.SourceURL) != "" {
		status = "verified"
		confidence = 0.65
	}
	authority := input.Authority
	if authority == "" {
		authority = "manual-unverified"
	}
	embedding, err := s.embed(ctx, ai.EmbeddingRequest{Input: []string{claimText}, Dimensions: s.cfg.Embedding.Dimensions})
	if err != nil {
		return RememberClaimResult{}, err
	}
	claimID, err := s.db.InsertClaim(ctx, store.ClaimRecord{
		ClaimText:           claimText,
		Scope:               input.Scope,
		SourceURL:           input.SourceURL,
		SourceType:          input.SourceType,
		Authority:           authority,
		Status:              status,
		Confidence:          confidence,
		Embedding:           embedding.Embeddings[0].Vector,
		EmbeddingProvider:   s.cfg.Embedding.Provider,
		EmbeddingModel:      embedding.Model,
		EmbeddingDimensions: embedding.Embeddings[0].Dimensions,
		ValidFrom:           input.ValidFrom,
		ExpiresAt:           input.ExpiresAt,
		SupersedesClaimID:   input.SupersedesClaimID,
		Metadata:            input.Metadata,
	})
	if err != nil {
		return RememberClaimResult{}, err
	}
	if input.SourceURL != "" {
		if err := s.db.AddEvidence(ctx, store.EvidenceRecord{ClaimID: claimID, Quote: claimText, SourceURL: input.SourceURL, SourceType: input.SourceType}); err != nil {
			return RememberClaimResult{}, err
		}
	}
	conflicts, err := s.detectClaimConflicts(ctx, claimID, claimText, input.Scope, input.SourceURL, input.Metadata)
	if err != nil {
		return RememberClaimResult{}, err
	}
	_ = s.db.InsertAuditEvent(ctx, "claim.remembered", "claim", claimID, input.Scope, input.SourceURL, map[string]any{"status": status, "created_by": input.CreatedBy, "conflicts": conflicts})
	return RememberClaimResult{ClaimID: claimID, Status: status, Conflicts: conflicts}, nil
}

func (s *Service) CaptureObservation(ctx context.Context, input CaptureObservationInput) (CaptureObservationResult, error) {
	input.Scope = strings.TrimSpace(input.Scope)
	input.ObservationText = strings.TrimSpace(input.ObservationText)
	if input.Scope == "" || input.ObservationText == "" {
		return CaptureObservationResult{}, fmt.Errorf("scope and observation_text are required")
	}
	observationText := input.ObservationText
	value := input.Value
	if s.cfg.RedactPII {
		observationText = redact(observationText)
		value = redactObservationValue(value)
	}
	observation, err := s.db.InsertObservation(ctx, store.ObservationRecord{
		Scope:           input.Scope,
		ObservationType: input.ObservationType,
		ObservationText: observationText,
		Status:          input.Status,
		Authority:       input.Authority,
		AuthorityScore:  input.AuthorityScore,
		Confidence:      input.Confidence,
		FreshnessStatus: input.FreshnessStatus,
		SubjectEntityID: input.SubjectEntityID,
		ObjectEntityID:  input.ObjectEntityID,
		RelationID:      input.RelationID,
		ClaimID:         input.ClaimID,
		DocumentID:      input.DocumentID,
		ChunkID:         input.ChunkID,
		SourceConfigID:  input.SourceConfigID,
		IngestionJobID:  input.IngestionJobID,
		SourceURL:       input.SourceURL,
		SourceType:      input.SourceType,
		SourceID:        input.SourceID,
		ObservedAt:      input.ObservedAt,
		ValidFrom:       input.ValidFrom,
		ExpiresAt:       input.ExpiresAt,
		CreatedBy:       input.CreatedBy,
		Value:           value,
		Metadata: mergeMetadata(map[string]any{
			"channel": "api",
		}, input.Metadata),
	})
	if err != nil {
		return CaptureObservationResult{}, err
	}
	_ = s.db.InsertAuditEvent(ctx, "observation.captured", "observation", observation.ID, observation.Scope, observation.SourceURL, map[string]any{
		"observation_type": observation.ObservationType,
		"status":           observation.Status,
		"created_by":       input.CreatedBy,
	})
	return CaptureObservationResult{Observation: observation}, nil
}

func (s *Service) ListObservations(ctx context.Context, input ListObservationsInput) ([]store.ObservationResult, error) {
	return s.db.ListObservations(ctx, store.ObservationFilter{
		Scope:           input.Scope,
		Query:           input.Query,
		ObservationType: input.ObservationType,
		Status:          input.Status,
		Since:           input.Since,
		Until:           input.Until,
		Limit:           input.Limit,
	})
}

func (s *Service) ChallengeClaim(ctx context.Context, input ChallengeClaimInput) (ChallengeClaimResult, error) {
	input.ClaimID = strings.TrimSpace(input.ClaimID)
	if input.ClaimID == "" {
		return ChallengeClaimResult{}, fmt.Errorf("claim_id is required")
	}
	scope, err := s.db.ClaimScope(ctx, input.ClaimID)
	if err != nil {
		return ChallengeClaimResult{}, err
	}
	if input.Verdict == "" {
		input.Verdict = "incorrect"
	}
	feedbackID, err := s.db.InsertFeedback(ctx, store.FeedbackRecord{
		ClaimID:   input.ClaimID,
		Verdict:   input.Verdict,
		Reason:    input.Reason,
		SourceURL: input.SourceURL,
		CreatedBy: input.CreatedBy,
	})
	if err != nil {
		return ChallengeClaimResult{}, err
	}
	conflictID := ""
	if input.Verdict == "conflict" && strings.TrimSpace(input.ConflictingClaimID) != "" {
		conflictID, err = s.db.UpsertClaimConflict(ctx, store.ConflictRecord{
			Scope:              scope,
			ConflictType:       "contradicts",
			Severity:           input.Severity,
			PrimaryClaimID:     input.ClaimID,
			ConflictingClaimID: input.ConflictingClaimID,
			DetectedBy:         input.CreatedBy,
			Authority:          "challenge",
			Metadata: mergeMetadata(input.Metadata, map[string]any{
				"feedback_id": feedbackID,
				"reason":      input.Reason,
				"source_url":  input.SourceURL,
			}),
		})
		if err != nil {
			return ChallengeClaimResult{}, err
		}
	}
	_ = s.db.InsertAuditEvent(ctx, "claim.challenged", "claim", input.ClaimID, scope, input.SourceURL, map[string]any{"verdict": input.Verdict, "reason": input.Reason, "feedback_id": feedbackID, "conflict_id": conflictID, "conflicting_claim_id": input.ConflictingClaimID})
	return ChallengeClaimResult{FeedbackID: feedbackID, ConflictID: conflictID}, nil
}

func (s *Service) ForgetClaim(ctx context.Context, input ForgetClaimInput) (ForgetClaimResult, error) {
	input.ClaimID = strings.TrimSpace(input.ClaimID)
	if input.ClaimID == "" {
		return ForgetClaimResult{}, fmt.Errorf("claim_id is required")
	}
	scope, err := s.db.ClaimScope(ctx, input.ClaimID)
	if err != nil {
		return ForgetClaimResult{}, err
	}
	forgotten, err := s.db.DeprecateClaim(ctx, input.ClaimID, input.Reason, input.CreatedBy)
	if err != nil {
		return ForgetClaimResult{}, err
	}
	_ = s.db.InsertAuditEvent(ctx, "claim.forgotten", "claim", input.ClaimID, scope, "", map[string]any{"reason": input.Reason, "created_by": input.CreatedBy, "forgotten": forgotten})
	return ForgetClaimResult{ClaimID: input.ClaimID, Forgotten: forgotten}, nil
}
