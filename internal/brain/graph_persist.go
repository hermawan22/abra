package brain

import (
	"context"
	"strings"

	"github.com/hermawan22/abra/internal/graph"
	"github.com/hermawan22/abra/internal/store"
)

type graphPersistInput struct {
	Scope          string
	SourceURL      string
	SourceType     string
	SourceConfigID string
	IngestionJobID string
	DocumentID     string
	ClaimID        string
	Metadata       map[string]any
	Description    string
	Candidates     graph.CandidateSet
}

type graphRelationConflictCandidate struct {
	ID             string
	Scope          string
	SourceEntityID string
	SourceEntity   string
	TargetEntity   string
	RelationType   string
	SourceURL      string
	DocumentID     string
	Metadata       map[string]any
}

var exclusiveGraphAlternativeGroups = map[string]string{
	"playwright":  "browser_test_runner",
	"cypress":     "browser_test_runner",
	"selenium":    "browser_test_runner",
	"webdriverio": "browser_test_runner",
	"testcafe":    "browser_test_runner",
}

func (s *Service) persistGraphCandidates(ctx context.Context, input graphPersistInput) (int, int, error) {
	if len(input.Candidates.Entities) == 0 && len(input.Candidates.Relations) == 0 {
		return 0, 0, nil
	}
	entityIDs := map[string]string{}
	entityCount := 0
	relationCount := 0
	for _, entity := range input.Candidates.Entities {
		entityID, err := s.db.UpsertEntity(ctx, store.EntityRecord{
			Scope:          input.Scope,
			EntityType:     entity.Type,
			Name:           entity.Name,
			Description:    input.Description,
			SourceURL:      input.SourceURL,
			SourceType:     input.SourceType,
			Confidence:     0.5 + float64(min(entity.Mentions, 5))*0.05,
			SourceConfigID: input.SourceConfigID,
			IngestionJobID: input.IngestionJobID,
			Metadata:       input.Metadata,
		})
		if err != nil {
			return entityCount, relationCount, err
		}
		entityIDs[entity.Name] = entityID
		entityCount++
	}
	for _, relation := range input.Candidates.Relations {
		sourceID := entityIDs[relation.From]
		targetID := entityIDs[relation.To]
		if sourceID == "" || targetID == "" || sourceID == targetID {
			continue
		}
		relationID, err := s.db.UpsertRelation(ctx, store.RelationRecord{
			Scope:          input.Scope,
			RelationType:   relation.Type,
			SourceEntityID: sourceID,
			TargetEntityID: targetID,
			ClaimID:        input.ClaimID,
			SourceURL:      firstNonEmpty(relation.SourceURL, input.SourceURL),
			SourceType:     input.SourceType,
			Confidence:     relation.Confidence,
			SourceConfigID: input.SourceConfigID,
			IngestionJobID: input.IngestionJobID,
			Metadata: mergeMetadata(input.Metadata, map[string]any{
				"document_id": input.DocumentID,
				"evidence":    relation.Evidence,
			}),
		})
		if err != nil {
			return entityCount, relationCount, err
		}
		if err := s.detectGraphRelationConflicts(ctx, graphRelationConflictCandidate{
			ID:             relationID,
			Scope:          input.Scope,
			SourceEntityID: sourceID,
			SourceEntity:   relation.From,
			TargetEntity:   relation.To,
			RelationType:   relation.Type,
			SourceURL:      firstNonEmpty(relation.SourceURL, input.SourceURL),
			DocumentID:     input.DocumentID,
			Metadata:       input.Metadata,
		}); err != nil {
			return entityCount, relationCount, err
		}
		relationCount++
	}
	return entityCount, relationCount, nil
}

func (s *Service) detectGraphRelationConflicts(ctx context.Context, candidate graphRelationConflictCandidate) error {
	relations, err := s.db.ListActiveRelationsFromEntity(ctx, candidate.Scope, candidate.SourceEntityID, 50)
	if err != nil {
		return err
	}
	for _, existing := range relations {
		if existing.ID == candidate.ID {
			continue
		}
		conflictType, severity, reason, ok := graphRelationConflict(candidate, existing)
		if !ok {
			continue
		}
		_, err := s.db.UpsertRelationConflict(ctx, store.ConflictRecord{
			Scope:                 candidate.Scope,
			ConflictType:          conflictType,
			Severity:              severity,
			PrimaryRelationID:     candidate.ID,
			ConflictingRelationID: existing.ID,
			EntityID:              candidate.SourceEntityID,
			DetectedBy:            "auto-graph-detector",
			Authority:             "deterministic-graph-detector",
			Metadata: mergeMetadata(candidate.Metadata, map[string]any{
				"detector":            "graph_relation_contradiction_v1",
				"reason":              reason,
				"document_id":         candidate.DocumentID,
				"new_relation_type":   candidate.RelationType,
				"new_target":          candidate.TargetEntity,
				"new_source_url":      candidate.SourceURL,
				"existing_relation":   existing.ID,
				"existing_type":       existing.Type,
				"existing_target":     existing.ToEntity,
				"existing_source_url": stringPtrValue(existing.SourceURL),
			}),
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func graphRelationConflict(candidate graphRelationConflictCandidate, existing store.GraphRelationResult) (string, string, string, bool) {
	newType := normalizeGraphConflictTerm(candidate.RelationType)
	oldType := normalizeGraphConflictTerm(existing.Type)
	newTarget := normalizeGraphConflictTerm(candidate.TargetEntity)
	oldTarget := normalizeGraphConflictTerm(existing.ToEntity)
	if newTarget == "" || oldTarget == "" {
		return "", "", "", false
	}
	if newTarget == oldTarget && graphOpposingUsePolicy(newType, oldType) {
		return "contradicts", "high", "opposing use policy for " + candidate.TargetEntity, true
	}
	newGroup := exclusiveGraphAlternativeGroups[newTarget]
	oldGroup := exclusiveGraphAlternativeGroups[oldTarget]
	if newGroup != "" && newGroup == oldGroup && newTarget != oldTarget && graphPreferredUseRelation(newType) && graphPreferredUseRelation(oldType) {
		severity := "medium"
		if newType == "should_use" || oldType == "should_use" {
			severity = "high"
		}
		return "competes_with", severity, "competing " + newGroup + " alternatives", true
	}
	return "", "", "", false
}

func graphOpposingUsePolicy(left, right string) bool {
	return (left == "should_not_use" && graphPositiveUseRelation(right)) || (right == "should_not_use" && graphPositiveUseRelation(left))
}

func graphPositiveUseRelation(value string) bool {
	switch value {
	case "should_use", "uses", "depends_on":
		return true
	default:
		return false
	}
}

func graphPreferredUseRelation(value string) bool {
	switch value {
	case "should_use", "uses":
		return true
	default:
		return false
	}
}

func normalizeGraphConflictTerm(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Trim(value, " \t\r\n\"'`.,;:()[]{}")
	return strings.Join(strings.Fields(value), " ")
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
