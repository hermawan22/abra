package memory

import (
	"testing"

	"github.com/hermawan22/abra/internal/store"
)

func TestDecideAgentActionAllowsCodeEvidenceWithoutClaimFacts(t *testing.T) {
	result := ComposeResult{
		Verification: VerificationReport{
			Verdict: "strong",
			Score:   0.92,
			RetrievalCoverage: RetrievalCoverage{
				Complete: true,
				Targets:  RetrievalCoverageTarget{Facts: 0, SupportingDocuments: 1, Summaries: 1},
			},
		},
		SupportingDocuments: []store.DocumentResult{{Title: "README.md", Source: "file:///repo/README.md"}},
		Summaries:           []store.MemorySummaryResult{{Title: "Repo overview", Summary: "Source-backed code context."}},
	}

	decision := decideAgentAction(ComposeInput{Hook: "before_task"}, result)
	if decision.Decision == "blocked" || !decision.AutonomousAllowed {
		t.Fatalf("decision = %#v, want source-backed code evidence to proceed", decision)
	}
}

func TestDecideAgentActionBlocksWhenFactCoverageWasRequired(t *testing.T) {
	result := ComposeResult{
		Verification: VerificationReport{
			Verdict: "weak",
			Score:   0.2,
			RetrievalCoverage: RetrievalCoverage{
				Complete: false,
				Targets:  RetrievalCoverageTarget{Facts: 1},
			},
		},
		SupportingDocuments: []store.DocumentResult{{Title: "README.md", Source: "file:///repo/README.md"}},
	}

	decision := decideAgentAction(ComposeInput{Hook: "before_task"}, result)
	if decision.Decision != "blocked" || decision.AutonomousAllowed {
		t.Fatalf("decision = %#v, want blocked when required facts are missing", decision)
	}
}

func TestDecideAgentActionKeepsStrongPacketRecommendationsVisible(t *testing.T) {
	result := ComposeResult{
		Verification: VerificationReport{
			Verdict:         "strong",
			Score:           0.94,
			Recommendations: []string{"Review degraded retrieval branch warnings; required source-backed coverage was still satisfied."},
			RetrievalCoverage: RetrievalCoverage{
				Complete: true,
				Targets:  RetrievalCoverageTarget{Facts: 1, SupportingDocuments: 1},
			},
		},
		Facts:               []store.ClaimResult{{ID: "claim-1", Claim: "Use source-backed memory.", Source: stringPtr("file:///repo/README.md")}},
		SupportingDocuments: []store.DocumentResult{{Title: "README.md", Source: "file:///repo/README.md"}},
	}

	decision := decideAgentAction(ComposeInput{Hook: "before_task"}, result)
	if decision.Decision != "proceed" || !decision.AutonomousAllowed {
		t.Fatalf("decision = %#v, want strong packet to proceed", decision)
	}
	if !containsReason(decision.Reasons, "Review degraded retrieval branch warnings") {
		t.Fatalf("strong packet dropped advisory retrieval warning: %#v", decision)
	}
	if contains(decision.RequiredActions, "rerun_degraded_retrieval") {
		t.Fatalf("advisory retrieval warning should not require rerun: %#v", decision)
	}
}

func stringPtr(value string) *string {
	return &value
}
