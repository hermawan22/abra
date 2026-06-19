package memory

type LearningSuggestion struct {
	ProposalType string         `json:"proposal_type"`
	Title        string         `json:"title"`
	Rationale    string         `json:"rationale"`
	TargetType   string         `json:"target_type,omitempty"`
	TargetID     string         `json:"target_id,omitempty"`
	SourceURL    string         `json:"source_url,omitempty"`
	Confidence   float64        `json:"confidence"`
	Payload      map[string]any `json:"payload"`
	ProposalID   string         `json:"proposal_id,omitempty"`
	Persisted    bool           `json:"persisted,omitempty"`
	PersistedNew bool           `json:"persisted_new"`
}

func learningSuggestions(result ComposeResult) []LearningSuggestion {
	out := []LearningSuggestion{}
	add := func(item LearningSuggestion) {
		if item.Confidence <= 0 {
			item.Confidence = 0.5
		}
		if item.Payload == nil {
			item.Payload = map[string]any{}
		}
		out = append(out, item)
	}
	for _, claimID := range result.Verification.MissingEvidenceClaims {
		add(LearningSuggestion{
			ProposalType: "challenge",
			Title:        "Review claim without source evidence",
			Rationale:    "A recalled claim has no source URL and should not be trusted until evidence is attached or the claim is deprecated.",
			TargetType:   "claim",
			TargetID:     claimID,
			Confidence:   0.85,
			Payload: map[string]any{
				"task":    result.Task,
				"verdict": result.Verification.Verdict,
			},
		})
	}
	for _, claimID := range result.Verification.StaleClaims {
		add(LearningSuggestion{
			ProposalType: "source_refresh",
			Title:        "Refresh stale source-backed claim",
			Rationale:    "A recalled claim is stale or expired; refresh its source before using it as settled memory.",
			TargetType:   "claim",
			TargetID:     claimID,
			Confidence:   0.75,
			Payload: map[string]any{
				"task":    result.Task,
				"verdict": result.Verification.Verdict,
			},
		})
	}
	for _, claimID := range result.Verification.ChallengedClaims {
		add(LearningSuggestion{
			ProposalType: "challenge",
			Title:        "Resolve challenged claim",
			Rationale:    "A recalled claim is already challenged and needs operator or source review before reuse.",
			TargetType:   "claim",
			TargetID:     claimID,
			Confidence:   0.8,
			Payload: map[string]any{
				"task":    result.Task,
				"verdict": result.Verification.Verdict,
			},
		})
	}
	for _, claimID := range result.Verification.ConflictClaims {
		add(LearningSuggestion{
			ProposalType: "challenge",
			Title:        "Resolve active claim conflict",
			Rationale:    "A recalled claim is part of an active conflict; resolve the contradiction before an agent treats it as settled memory.",
			TargetType:   "claim",
			TargetID:     claimID,
			Confidence:   0.9,
			Payload: map[string]any{
				"task":      result.Task,
				"verdict":   result.Verification.Verdict,
				"conflicts": result.Conflicts,
			},
		})
	}
	for _, conflict := range result.Conflicts {
		if conflict.PrimaryRelationID == "" && conflict.ConflictingRelationID == "" {
			continue
		}
		add(LearningSuggestion{
			ProposalType: "graph",
			Title:        "Resolve active graph relation conflict",
			Rationale:    "A graph relation conflict is active; resolve the graph contradiction before an agent treats impact or policy context as settled memory.",
			TargetType:   "conflict",
			TargetID:     conflict.ID,
			Confidence:   0.9,
			Payload: map[string]any{
				"task":                     result.Task,
				"verdict":                  result.Verification.Verdict,
				"primary_relation_id":      conflict.PrimaryRelationID,
				"conflicting_relation_id":  conflict.ConflictingRelationID,
				"conflict_type":            conflict.ConflictType,
				"conflict_resolution_path": "POST /conflicts/" + conflict.ID + "/resolve",
			},
		})
	}
	if len(result.Verification.UnverifiedClaims) > 0 {
		add(LearningSuggestion{
			ProposalType: "claim",
			Title:        "Promote or reject unverified memory",
			Rationale:    "Unverified claims were useful enough to surface; attach evidence or reject them so future agents get clearer memory.",
			TargetType:   "claim_set",
			Confidence:   0.65,
			Payload: map[string]any{
				"task":              result.Task,
				"unverified_claims": result.Verification.UnverifiedClaims,
			},
		})
	}
	if result.Verification.RetrievalQuality.LowConfidence {
		add(LearningSuggestion{
			ProposalType: "ingestion",
			Title:        "Improve low-confidence retrieval",
			Rationale:    "Working-memory retrieval returned source-backed results, but rank, text, and vector signals were too weak for autonomous use.",
			TargetType:   "scope",
			TargetID:     result.Scope,
			Confidence:   0.78,
			Payload: map[string]any{
				"task":              result.Task,
				"verdict":           result.Verification.Verdict,
				"retrieval_quality": result.Verification.RetrievalQuality,
				"suggested_actions": []string{
					"rerun_with_more_specific_query",
					"ingest_stronger_sources",
					"rebuild_embeddings_or_reindex",
				},
			},
		})
	}
	if result.Verification.RetrievalQuality.LowSourceDiversity {
		add(LearningSuggestion{
			ProposalType: "ingestion",
			Title:        "Corroborate single-source retrieval",
			Rationale:    "Working-memory retrieval returned several results, but they were dominated by one source; add or refresh corroborating sources before treating the packet as settled.",
			TargetType:   "scope",
			TargetID:     result.Scope,
			Confidence:   0.72,
			Payload: map[string]any{
				"task":              result.Task,
				"verdict":           result.Verification.Verdict,
				"retrieval_quality": result.Verification.RetrievalQuality,
				"suggested_actions": []string{
					"ingest_corroborating_sources",
					"rerun_with_source_diversity",
					"challenge_single_source_assumptions",
				},
			},
		})
	}
	if len(result.Summaries) == 0 && len(result.Facts)+len(result.SupportingDocuments) > 0 {
		add(LearningSuggestion{
			ProposalType: "summary_rebuild",
			Title:        "Build hierarchy summaries for recalled sources",
			Rationale:    "Recall found source-backed context but no hierarchy summaries; rebuild summaries to improve future working-memory packets.",
			TargetType:   "scope",
			TargetID:     result.Scope,
			Confidence:   0.7,
			Payload: map[string]any{
				"task": result.Task,
			},
		})
	}
	if len(result.GraphContext) == 0 && len(result.RelevantFiles) > 0 {
		add(LearningSuggestion{
			ProposalType: "graph",
			Title:        "Extract graph context for relevant files",
			Rationale:    "The packet found relevant files but no graph relations; structural extraction can improve impact analysis.",
			TargetType:   "scope",
			TargetID:     result.Scope,
			Confidence:   0.6,
			Payload: map[string]any{
				"task":           result.Task,
				"relevant_files": result.RelevantFiles,
			},
		})
	}
	if len(out) == 0 && result.Verification.Verdict == "strong" {
		add(LearningSuggestion{
			ProposalType: "other",
			Title:        "No learning action required",
			Rationale:    "The packet is source-backed and did not surface stale, challenged, unverified, or missing-evidence memory.",
			Confidence:   1,
			Payload: map[string]any{
				"task":    result.Task,
				"verdict": result.Verification.Verdict,
			},
		})
	}
	if len(out) > 10 {
		return out[:10]
	}
	return out
}
