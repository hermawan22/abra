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
