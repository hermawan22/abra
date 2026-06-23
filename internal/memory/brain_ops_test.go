package memory

import (
	"context"
	"strings"
	"testing"

	"github.com/hermawan22/abra/internal/store"
)

type brainOpsStore struct {
	fakeStore
	candidates      []store.EvidenceAnchorCandidate
	candidateCount  int
	evalRuns        []store.BrainEvalRunResult
	proposalCreated bool
	proposalInputs  []store.CreateLearningProposalInput
}

func (s *brainOpsStore) ListEvidenceAnchorCandidates(ctx context.Context, scope string, limit int) ([]store.EvidenceAnchorCandidate, error) {
	if limit > 0 && len(s.candidates) > limit {
		return append([]store.EvidenceAnchorCandidate(nil), s.candidates[:limit]...), nil
	}
	return append([]store.EvidenceAnchorCandidate(nil), s.candidates...), nil
}

func (s *brainOpsStore) CountEvidenceAnchorCandidates(ctx context.Context, scope string) (int, error) {
	if s.candidateCount > 0 {
		return s.candidateCount, nil
	}
	return len(s.candidates), nil
}

func (s *brainOpsStore) ListBrainEvalRuns(ctx context.Context, scope string, limit int) ([]store.BrainEvalRunResult, error) {
	if limit > 0 && len(s.evalRuns) > limit {
		return append([]store.BrainEvalRunResult(nil), s.evalRuns[:limit]...), nil
	}
	return append([]store.BrainEvalRunResult(nil), s.evalRuns...), nil
}

func (s *brainOpsStore) CreateLearningProposalOnce(ctx context.Context, input store.CreateLearningProposalInput) (store.LearningProposalRecord, bool, error) {
	s.proposalInputs = append(s.proposalInputs, input)
	created := true
	if !s.proposalCreated {
		created = false
	}
	id := "proposal-" + input.TargetID
	if input.TargetID == "" {
		id = "proposal-scope"
	}
	return store.LearningProposalRecord{
		ID:           id,
		Scope:        input.Scope,
		ProposalType: input.ProposalType,
		Title:        input.Title,
		Rationale:    input.Rationale,
		Status:       "pending",
		TargetType:   input.TargetType,
		TargetID:     input.TargetID,
		SourceURL:    input.SourceURL,
		Confidence:   input.Confidence,
		Payload:      input.Payload,
		CreatedBy:    input.CreatedBy,
	}, created, nil
}

func TestBrainScorecardCountsWeakAnchorsBeyondPageLimit(t *testing.T) {
	db := &brainOpsStore{
		fakeStore: fakeStore{health: store.MemoryHealthResult{
			Scope:  "repo:test",
			Status: "healthy",
			Score:  100,
			Claims: store.MemoryHealthClaim{
				Total:        100,
				Verified:     100,
				WithEvidence: 100,
			},
			Graph:     store.MemoryHealthGraph{Entities: 1, ActiveEntities: 1, Relations: 1, ActiveRelations: 1},
			Summaries: store.MemoryHealthSummary{Total: 1},
		}},
		candidates:     []store.EvidenceAnchorCandidate{{ClaimID: "claim-1"}},
		candidateCount: 75,
	}
	scorecard, err := NewComposerWithOptions(db, ComposerOptions{}).BrainScorecard(context.Background(), BrainScorecardInput{Scope: "repo:test", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if scorecard.Metrics["weak_anchor_claims"] != 75 {
		t.Fatalf("weak anchor count should ignore page limit: %#v", scorecard.Metrics)
	}
}

func TestBrainScorecardAndReviewSurfaceWeakAnchors(t *testing.T) {
	db := &brainOpsStore{
		fakeStore: fakeStore{health: store.MemoryHealthResult{
			Scope:  "repo:test",
			Status: "needs_review",
			Score:  82,
			Claims: store.MemoryHealthClaim{
				Total:        5,
				Verified:     3,
				Inferred:     1,
				WithEvidence: 3,
				Stale:        1,
			},
			Graph: store.MemoryHealthGraph{
				Entities:        4,
				ActiveEntities:  4,
				Relations:       3,
				ActiveRelations: 2,
			},
			Summaries: store.MemoryHealthSummary{Total: 1},
		}},
		candidates: []store.EvidenceAnchorCandidate{
			{ClaimID: "claim-1", Claim: "Retry callbacks must remain idempotent.", Scope: "repo:test"},
		},
	}
	composer := NewComposerWithOptions(db, ComposerOptions{})

	scorecard, err := composer.BrainScorecard(context.Background(), BrainScorecardInput{Scope: "repo:test", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if scorecard.Metrics["weak_anchor_claims"] != 1 {
		t.Fatalf("metrics = %#v", scorecard.Metrics)
	}
	if !hasScorecardCategory(scorecard.Categories, "anchors") {
		t.Fatalf("scorecard categories = %#v", scorecard.Categories)
	}

	review, err := composer.BrainReview(context.Background(), BrainReviewInput{Scope: "repo:test", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !hasReviewItem(review.ReviewItems, "weak_evidence_anchors") || !hasReviewItem(review.ReviewItems, "stale_claims") {
		t.Fatalf("review items = %#v", review.ReviewItems)
	}
	if !containsRecommendation(review.RecommendedActions, "brain_anchor_backfill") {
		t.Fatalf("recommended actions = %#v", review.RecommendedActions)
	}
}

func TestBrainScorecardUsesPersistedEvalHistory(t *testing.T) {
	db := &brainOpsStore{
		fakeStore: fakeStore{health: store.MemoryHealthResult{
			Scope:  "repo:test",
			Status: "healthy",
			Score:  100,
			Claims: store.MemoryHealthClaim{
				Total:        2,
				Verified:     2,
				WithEvidence: 2,
			},
			Graph:     store.MemoryHealthGraph{Entities: 1, ActiveEntities: 1, Relations: 1, ActiveRelations: 1},
			Summaries: store.MemoryHealthSummary{Total: 1},
		}},
		evalRuns: []store.BrainEvalRunResult{{
			ID:        "brain-eval-1",
			Scope:     "repo:test",
			Total:     8,
			Passed:    8,
			Success:   true,
			CreatedAt: "2026-06-22T00:00:00Z",
		}},
	}
	scorecard, err := NewComposerWithOptions(db, ComposerOptions{}).BrainScorecard(context.Background(), BrainScorecardInput{Scope: "repo:test"})
	if err != nil {
		t.Fatal(err)
	}
	category := scorecardCategory(scorecard.Categories, "eval")
	if category.Score != 100 || category.Status != "healthy" || !strings.Contains(category.Message, "latest persisted eval run passed") {
		t.Fatalf("eval category = %#v", category)
	}
	if scorecard.Metrics["eval_latest_id"] != "brain-eval-1" || scorecard.Metrics["eval_latest_success"] != true {
		t.Fatalf("eval metrics = %#v", scorecard.Metrics)
	}
}

func TestAnchorBackfillDryRunAndProposalMode(t *testing.T) {
	db := &brainOpsStore{
		proposalCreated: true,
		candidates: []store.EvidenceAnchorCandidate{
			{
				ClaimID:       "claim-1",
				Claim:         "Retry callbacks must remain idempotent.",
				Scope:         "repo:test",
				SourceURL:     "file://runbook.md",
				DocumentID:    "doc-1",
				DocumentTitle: "Runbook",
				DocumentChunk: "Operators must verify webhook delivery. Retry callbacks must remain idempotent before replay.",
				Freshness:     "fresh",
			},
		},
	}
	composer := NewComposerWithOptions(db, ComposerOptions{})

	dryRun, err := composer.AnchorBackfill(context.Background(), AnchorBackfillInput{Scope: "repo:test", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !dryRun.DryRun || dryRun.Propose || len(dryRun.Proposals) != 0 || len(dryRun.Candidates) != 1 {
		t.Fatalf("dry-run result = %#v", dryRun)
	}
	if !strings.Contains(dryRun.Candidates[0].Quote, "Retry callbacks must remain idempotent") || dryRun.Candidates[0].Action != "propose_text_span_anchor" {
		t.Fatalf("candidate = %#v", dryRun.Candidates[0])
	}

	proposed, err := composer.AnchorBackfill(context.Background(), AnchorBackfillInput{Scope: "repo:test", Limit: 5, Propose: true, DryRun: false, CreatedBy: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if proposed.DryRun || len(proposed.Proposals) != 1 || len(db.proposalInputs) != 1 {
		t.Fatalf("proposal result = %#v inputs=%#v", proposed, db.proposalInputs)
	}
	input := db.proposalInputs[0]
	if input.TargetType != "claim" || input.TargetID != "claim-1" || input.CreatedBy != "codex" {
		t.Fatalf("proposal input = %#v", input)
	}
	if input.Payload["action"] != "add_evidence_anchor" || !strings.Contains(input.Payload["quote"].(string), "Retry callbacks must remain idempotent") {
		t.Fatalf("proposal payload = %#v", input.Payload)
	}
}

func TestAnchorBackfillDoesNotReturnDuplicateProposalsAsNew(t *testing.T) {
	db := &brainOpsStore{
		candidates: []store.EvidenceAnchorCandidate{
			{
				ClaimID:       "claim-1",
				Claim:         "Retry callbacks must remain idempotent.",
				Scope:         "repo:test",
				SourceURL:     "file://runbook.md",
				DocumentChunk: "Retry callbacks must remain idempotent before replay.",
			},
		},
	}
	result, err := NewComposerWithOptions(db, ComposerOptions{}).AnchorBackfill(context.Background(), AnchorBackfillInput{Scope: "repo:test", Limit: 5, Propose: true, DryRun: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Proposals) != 0 || len(result.Candidates) != 1 || result.Candidates[0].ProposalCreated {
		t.Fatalf("duplicate proposal should be linked but not returned as new: %#v", result)
	}
	if result.Candidates[0].ProposalID == "" {
		t.Fatalf("candidate should still expose existing proposal id: %#v", result.Candidates[0])
	}
}

func TestBrainMaintainCreatesReviewableProposalsOnly(t *testing.T) {
	db := &brainOpsStore{
		proposalCreated: true,
		fakeStore: fakeStore{health: store.MemoryHealthResult{
			Scope:     "repo:test",
			Status:    "needs_review",
			Score:     77,
			Claims:    store.MemoryHealthClaim{Total: 2, Verified: 1, WithEvidence: 1, Stale: 1},
			Summaries: store.MemoryHealthSummary{Total: 1},
		}},
		candidates: []store.EvidenceAnchorCandidate{
			{
				ClaimID:       "claim-2",
				Claim:         "Use the current callback replay workflow.",
				Scope:         "repo:test",
				SourceURL:     "file://workflow.md",
				DocumentChunk: "Use the current callback replay workflow when duplicate delivery is observed.",
			},
		},
	}
	composer := NewComposerWithOptions(db, ComposerOptions{})

	result, err := composer.BrainMaintain(context.Background(), BrainMaintenanceInput{Scope: "repo:test", Limit: 5, Propose: true, DryRun: false, CreatedBy: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if result.DryRun || len(result.Proposals) < 2 {
		t.Fatalf("maintenance result = %#v", result)
	}
	if len(db.proposalInputs) < 2 {
		t.Fatalf("proposal inputs = %#v", db.proposalInputs)
	}
	for _, input := range db.proposalInputs {
		if input.CreatedBy != "codex" || input.TargetType == "" {
			t.Fatalf("proposal input should stay reviewable: %#v", input)
		}
	}
}

func hasScorecardCategory(categories []BrainScorecardCategory, name string) bool {
	return scorecardCategory(categories, name).Name != ""
}

func scorecardCategory(categories []BrainScorecardCategory, name string) BrainScorecardCategory {
	for _, category := range categories {
		if category.Name == name {
			return category
		}
	}
	return BrainScorecardCategory{}
}

func hasReviewItem(items []BrainReviewItem, code string) bool {
	for _, item := range items {
		if item.Code == code {
			return true
		}
	}
	return false
}
