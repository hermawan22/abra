package server

import (
	"testing"

	"github.com/hermawan22/abra/internal/memory"
	"github.com/hermawan22/abra/internal/store"
)

func TestShouldPersistLearningSuggestionSkipsNoop(t *testing.T) {
	if shouldPersistLearningSuggestion(memory.LearningSuggestion{
		ProposalType: "other",
		Title:        "No learning action required",
		Rationale:    "Packet is already strong.",
	}) {
		t.Fatal("noop learning suggestion should not be persisted")
	}
}

func TestShouldPersistLearningSuggestionKeepsActionableSuggestion(t *testing.T) {
	if !shouldPersistLearningSuggestion(memory.LearningSuggestion{
		ProposalType: "ingestion",
		Title:        "Improve low-confidence retrieval",
		Rationale:    "Recall evidence is too weak for autonomous use.",
	}) {
		t.Fatal("actionable learning suggestion should be persisted")
	}
}

func TestShouldAutoPersistComposeLearningRequiresExplicitOptIn(t *testing.T) {
	if shouldAutoPersistComposeLearning(memory.ComposeInput{Diagnostic: true}) {
		t.Fatal("diagnostic compose must not persist learning suggestions")
	}
	if shouldAutoPersistComposeLearning(memory.ComposeInput{}) {
		t.Fatal("default compose must remain read-only")
	}
	if !shouldAutoPersistComposeLearning(memory.ComposeInput{PersistLearning: true}) {
		t.Fatal("persist_learning compose should persist actionable learning suggestions")
	}
	if shouldAutoPersistComposeLearning(memory.ComposeInput{PersistLearning: true, Diagnostic: true}) {
		t.Fatal("diagnostic compose must override persist_learning")
	}
}

func TestBuildLearningApplyPlanForAcceptedClaimRequiresApprovalWhenEnforced(t *testing.T) {
	plan := buildLearningApplyPlan(store.LearningProposalRecord{
		ID:           "proposal-1",
		Scope:        "team:example",
		ProposalType: "claim",
		Status:       "accepted",
		Payload:      map[string]any{"claim": "Frontend should use Playwright."},
	}, "enforce")
	if !plan.Ready || plan.Action != "review_claim_promotion" || plan.Endpoint != "/claims" {
		t.Fatalf("unexpected apply plan: %#v", plan)
	}
	if !plan.RequiresApproval || plan.ApprovalAction != "agent_write" || plan.TargetID != "team:example" {
		t.Fatalf("claim apply plan did not require enforced approval: %#v", plan)
	}
}

func TestBuildLearningApplyPlanForObservationClaimTargetsMemoryWrite(t *testing.T) {
	plan := buildLearningApplyPlan(store.LearningProposalRecord{
		ID:           "proposal-1",
		Scope:        "repo:app",
		ProposalType: "claim",
		Status:       "accepted",
		TargetType:   "observation",
		TargetID:     "observation-1",
		Payload: map[string]any{
			"observation_id": "observation-1",
			"claim":          "Agents should verify memory before trusting it.",
		},
	}, "enforce")
	if !plan.Ready || plan.Action != "review_claim_promotion" || plan.Endpoint != "/claims" {
		t.Fatalf("unexpected apply plan: %#v", plan)
	}
	if !plan.RequiresApproval || plan.ApprovalAction != "agent_write" {
		t.Fatalf("observation claim promotion should require agent_write approval in enforce mode: %#v", plan)
	}
	if plan.TargetType != "memory_write" || plan.TargetID != "repo:app" {
		t.Fatalf("claim apply plan should target scoped memory write, got %#v", plan)
	}
}

func TestBuildLearningApplyPlanForSummaryRebuild(t *testing.T) {
	plan := buildLearningApplyPlan(store.LearningProposalRecord{
		ID:           "proposal-1",
		Scope:        "repo:app",
		ProposalType: "summary_rebuild",
		Status:       "accepted",
		Payload:      map[string]any{"limit": 100},
	}, "advisory")
	if !plan.Ready || plan.Action != "rebuild_summaries" || plan.Endpoint != "/memory/summaries/rebuild" {
		t.Fatalf("unexpected summary rebuild apply plan: %#v", plan)
	}
	if plan.RequiresApproval || plan.ApprovalAction != "backfill" {
		t.Fatalf("summary rebuild advisory approval flags unexpected: %#v", plan)
	}
}

func TestBuildLearningApplyPlanForGraphConflict(t *testing.T) {
	plan := buildLearningApplyPlan(store.LearningProposalRecord{
		ID:           "proposal-1",
		Scope:        "repo:app",
		ProposalType: "graph",
		Status:       "accepted",
		TargetType:   "conflict",
		TargetID:     "conflict-1",
	}, "enforce")
	if !plan.Ready || plan.Action != "review_graph_update" || plan.Endpoint != "/conflicts/conflict-1/resolve" {
		t.Fatalf("unexpected graph conflict apply plan: %#v", plan)
	}
	if plan.RequiresApproval {
		t.Fatalf("graph conflict resolution should use conflict review, not approval gate: %#v", plan)
	}
}

func TestBuildLearningApplyPlanForNonAcceptedProposalHasNoAction(t *testing.T) {
	plan := buildLearningApplyPlan(store.LearningProposalRecord{
		ID:           "proposal-1",
		Scope:        "repo:app",
		ProposalType: "claim",
		Status:       "rejected",
	}, "enforce")
	if plan.Ready || plan.Action != "none" || len(plan.Notes) == 0 {
		t.Fatalf("non-accepted proposal should not be ready: %#v", plan)
	}
}
