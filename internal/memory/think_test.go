package memory

import (
	"strings"
	"testing"

	"github.com/hermawan22/abra/internal/store"
)

func TestBuildThinkResultIncludesCitationsGapsAndDecision(t *testing.T) {
	source := "file://docs/adr.md"
	packet := ComposeResult{
		Task:  "Which runtime should agents use?",
		Scope: "repo:example",
		Facts: []store.ClaimResult{
			{
				ID:        "claim-1",
				Claim:     "Agents must use Abra before autonomous code changes.",
				Scope:     "repo:example",
				Status:    "verified",
				Source:    &source,
				Freshness: "fresh",
				Rank:      1.2,
			},
		},
		SupportingDocuments: []store.DocumentResult{
			{ID: "doc-1", Title: "ADR", Source: source, Content: "Use Abra first.", Rank: 0.9},
		},
		GraphContext: []store.RelationResult{
			{ID: "rel-1", FromEntity: "Agent", Type: "uses", ToEntity: "Abra", Confidence: 0.91, SourceURL: &source},
		},
		RetrievalReasons: []store.RetrievalReason{
			{Mode: "hybrid", Signal: "text", Message: "Full-text matches contributed.", Count: 1},
			{Mode: "hybrid", Signal: "vector", Message: "Semantic matches contributed.", Count: 1},
		},
		MemoryHealth: store.MemoryHealthResult{Status: "healthy"},
		Verification: VerificationReport{
			Verdict:           "strong",
			Score:             0.94,
			RetrievalCoverage: RetrievalCoverage{Complete: true},
			RetrievalQuality:  RetrievalQuality{ResultCount: 2, TopRankScore: 1.2},
			Recommendations:   []string{"keep citing source evidence"},
		},
		AgentDecision: AgentDecision{
			Decision:           "proceed",
			AutonomousAllowed:  true,
			AllowedNextActions: []string{"cite_evidence"},
		},
		Stats: ComposeStats{Facts: 1, SupportingDocuments: 1, GraphRelations: 1},
	}

	result := BuildThinkResult(packet)
	if !strings.Contains(result.Answer, "Abra's governed answer") {
		t.Fatalf("answer did not look synthesized:\n%s", result.Answer)
	}
	if !strings.Contains(result.Answer, "[C1]") {
		t.Fatalf("answer missing citation ref:\n%s", result.Answer)
	}
	if len(result.Citations) != 1 || result.Citations[0].SourceURL != source {
		t.Fatalf("citations = %#v", result.Citations)
	}
	if len(result.GraphPaths) != 1 || result.GraphPaths[0].CitationRef != "C1" {
		t.Fatalf("graph paths = %#v", result.GraphPaths)
	}
	if len(result.Gaps) != 0 {
		t.Fatalf("unexpected gaps = %#v", result.Gaps)
	}
	if result.AgentDecision.Decision != "proceed" {
		t.Fatalf("decision = %#v", result.AgentDecision)
	}
	if len(result.RetrievalReasons) != 2 || result.RetrievalReasons[0].Signal != "text" {
		t.Fatalf("retrieval reasons = %#v", result.RetrievalReasons)
	}
}

func TestBuildThinkResultSurfacesGovernanceGaps(t *testing.T) {
	packet := ComposeResult{
		Task:         "What changed?",
		Scope:        "repo:example",
		MemoryHealth: store.MemoryHealthResult{Status: "needs_review"},
		Verification: VerificationReport{
			Verdict: "weak",
			RetrievalCoverage: RetrievalCoverage{
				Complete: false,
				Missing:  []string{"facts", "evidence_sources"},
			},
			RetrievalQuality: RetrievalQuality{LowConfidence: true},
			UnverifiedClaims: []string{"claim-unverified"},
		},
		AgentDecision: AgentDecision{
			Decision:        "needs_review",
			ReviewRequired:  true,
			RequiredActions: []string{"add_evidence"},
		},
	}

	result := BuildThinkResult(packet)
	if !strings.Contains(result.Answer, "cannot answer this with source-backed memory") {
		t.Fatalf("answer should refuse unsupported certainty:\n%s", result.Answer)
	}
	codes := map[string]bool{}
	for _, gap := range result.Gaps {
		codes[gap.Code] = true
	}
	for _, want := range []string{"no_source_backed_facts", "coverage_facts", "coverage_evidence_sources", "low_confidence_retrieval", "unverified_claims", "memory_health_needs_review"} {
		if !codes[want] {
			t.Fatalf("gap %q missing from %#v", want, result.Gaps)
		}
	}
	if result.AgentDecision.Decision != "needs_review" {
		t.Fatalf("decision = %#v", result.AgentDecision)
	}
}
