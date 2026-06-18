package memory

import (
	"strings"
	"testing"

	"github.com/hermawan22/abra/internal/store"
)

func TestVerifyPacketStrongWhenSourceBackedAndFresh(t *testing.T) {
	source := "file://source.md"
	report := verifyPacket(
		testSummaries(source),
		[]store.ClaimResult{
			{ID: "claim-1", Claim: "Frontend uses Playwright.", Status: "verified", Source: &source, Rank: 1.2, TextScore: 0.3, VectorScore: 0.2, Freshness: "fresh"},
		},
		[]store.DocumentResult{
			{ID: "doc-1", Title: "Frontend", Source: source, Content: "Frontend uses Playwright.", Rank: 1, TextScore: 0.4, VectorScore: 0.1},
		},
		[]store.RelationResult{
			{FromEntity: "Frontend", ToEntity: "Playwright", Type: "uses", Confidence: 0.8, SourceURL: &source},
		},
		[]EvidenceItem{{SourceURL: source, Count: 2}},
		testRetrievalPlan(1),
		nil,
		nil,
		nil,
	)
	if report.Verdict != "strong" {
		t.Fatalf("verdict = %q, want strong: %#v", report.Verdict, report)
	}
	if report.ActionRequired {
		t.Fatalf("action required for strong packet: %#v", report)
	}
	if report.Score < 0.85 || report.ClaimCoverage != 1 {
		t.Fatalf("unexpected score/coverage: %#v", report)
	}
	if report.RetrievalQuality.ResultCount != 2 || report.RetrievalQuality.TopTextScore == 0 || report.RetrievalQuality.TopVectorScore == 0 {
		t.Fatalf("retrieval quality missing score components: %#v", report.RetrievalQuality)
	}
	if !containsCheck(report.Checks, "retrieval_quality") {
		t.Fatalf("retrieval quality check missing: %#v", report.Checks)
	}
	if !containsCheck(report.Checks, "retrieval_coverage") || !report.RetrievalCoverage.Complete {
		t.Fatalf("retrieval coverage check missing or incomplete: %#v", report)
	}
}

func TestVerifyPacketUnsafeWhenClaimHasNoEvidence(t *testing.T) {
	report := verifyPacket(
		nil,
		[]store.ClaimResult{
			{ID: "claim-1", Claim: "Unbacked memory.", Status: "verified", Freshness: "fresh"},
		},
		nil,
		nil,
		nil,
		testRetrievalPlan(1),
		nil,
		nil,
		nil,
	)
	if report.Verdict != "unsafe" {
		t.Fatalf("verdict = %q, want unsafe: %#v", report.Verdict, report)
	}
	if !report.ActionRequired || len(report.MissingEvidenceClaims) != 1 {
		t.Fatalf("missing evidence was not gated: %#v", report)
	}
}

func TestVerifyPacketUnsafeWhenActiveConflictExists(t *testing.T) {
	source := "file://source.md"
	report := verifyPacket(
		testSummaries(source),
		[]store.ClaimResult{
			{ID: "claim-1", Claim: "Frontend uses Playwright.", Status: "verified", Source: &source, Freshness: "fresh"},
			{ID: "claim-2", Claim: "Frontend uses Cypress.", Status: "verified", Source: &source, Freshness: "fresh"},
		},
		[]store.DocumentResult{{ID: "doc-1", Title: "Frontend", Source: source, Content: "Frontend test guidance.", Rank: 1}},
		nil,
		[]EvidenceItem{{SourceURL: source, Count: 2}},
		testRetrievalPlan(1),
		[]store.ConflictResult{{
			ID:                 "conflict-1",
			Scope:              "team:example",
			ConflictType:       "contradicts",
			Status:             "open",
			Severity:           "high",
			PrimaryClaimID:     "claim-1",
			ConflictingClaimID: "claim-2",
		}},
		nil,
		nil,
	)
	if report.Verdict != "unsafe" || !report.ActionRequired {
		t.Fatalf("active conflict was not unsafe: %#v", report)
	}
	if len(report.ConflictClaims) != 2 || len(report.ActiveConflicts) != 1 {
		t.Fatalf("conflict claims not surfaced: %#v", report)
	}
	if !containsRecommendation(report.Recommendations, "Resolve active memory conflicts") {
		t.Fatalf("conflict recommendation missing: %#v", report.Recommendations)
	}
}

func TestVerifyPacketPartialWhenRetrievalDegraded(t *testing.T) {
	source := "file://source.md"
	report := verifyPacket(
		testSummaries(source),
		[]store.ClaimResult{
			{ID: "claim-1", Claim: "Frontend uses Playwright.", Status: "verified", Source: &source, Freshness: "fresh"},
		},
		[]store.DocumentResult{{ID: "doc-1", Title: "Frontend", Source: source, Content: "Frontend uses Playwright.", Rank: 1}},
		[]store.RelationResult{{FromEntity: "Frontend", ToEntity: "Playwright", Type: "uses", Confidence: 0.8, SourceURL: &source}},
		[]EvidenceItem{{SourceURL: source, Count: 2}},
		testRetrievalPlan(1),
		nil,
		[]RetrievalWarning{{Stage: "retrieval", Operation: "recall", Query: "frontend", Message: "temporary shard unavailable"}},
		nil,
	)
	if report.Verdict != "partial" || !report.ActionRequired {
		t.Fatalf("degraded retrieval should be partial and action-required: %#v", report)
	}
	if len(report.RetrievalWarnings) != 1 {
		t.Fatalf("retrieval warnings not surfaced: %#v", report)
	}
	if !containsRecommendation(report.Recommendations, "Rerun degraded retrieval") {
		t.Fatalf("degraded retrieval recommendation missing: %#v", report.Recommendations)
	}
}

func TestVerifyPacketStrongForCodeContextWhenClaimsAreNotRequired(t *testing.T) {
	source := "file://src/app.tsx"
	report := verifyPacket(
		testSummaries(source),
		nil,
		[]store.DocumentResult{{ID: "doc-1", Title: "src/app.tsx", Source: source, Content: "export function App() {}", Rank: 0.8, TextScore: 0.2, VectorScore: 0.1}},
		[]store.RelationResult{{FromEntity: "src/app.tsx", ToEntity: "App", Type: "exports", Confidence: 0.8, SourceURL: &source}},
		[]EvidenceItem{{SourceURL: source, Count: 2}},
		testRetrievalPlan(0),
		nil,
		nil,
		nil,
	)
	if report.Verdict != "strong" || report.ActionRequired {
		t.Fatalf("code context without claims should be strong when coverage is complete: %#v", report)
	}
	if report.ClaimCoverage != 0 || !report.RetrievalCoverage.Complete {
		t.Fatalf("unexpected coverage for code context: %#v", report)
	}
	if !containsRecommendation(report.Recommendations, "no claim facts by design") {
		t.Fatalf("code-context recommendation missing: %#v", report.Recommendations)
	}
}

func TestVerifyPacketWeakWhenCoverageContractIsMissing(t *testing.T) {
	source := "file://source.md"
	report := verifyPacket(
		testSummaries(source),
		[]store.ClaimResult{
			{ID: "claim-1", Claim: "Frontend uses Playwright.", Status: "verified", Source: &source, Rank: 1.2, TextScore: 0.3, VectorScore: 0.2, Freshness: "fresh"},
		},
		[]store.DocumentResult{{ID: "doc-1", Title: "Frontend", Source: source, Content: "Frontend uses Playwright.", Rank: 1, TextScore: 0.4, VectorScore: 0.1}},
		nil,
		[]EvidenceItem{{SourceURL: source, Count: 2}},
		testRetrievalPlan(1),
		nil,
		nil,
		nil,
	)
	if report.Verdict != "weak" || !report.ActionRequired || report.RetrievalCoverage.Complete {
		t.Fatalf("missing graph coverage should weaken the packet: %#v", report)
	}
	if !contains(report.RetrievalCoverage.Missing, "graph_relations") {
		t.Fatalf("missing graph relation target not surfaced: %#v", report.RetrievalCoverage)
	}
	if !containsRecommendation(report.Recommendations, "missing layers") {
		t.Fatalf("coverage recommendation missing: %#v", report.Recommendations)
	}
}

func TestVerifyPacketPartialWhenGraphWarningExists(t *testing.T) {
	source := "file://source.md"
	warning := GraphWarning{
		WarningType:  "competing_graph_alternatives",
		Severity:     "high",
		Entity:       "Frontend App",
		RelationType: "should_use",
		Message:      "Frontend App has competing browser_test_runner graph relations: Playwright and Cypress.",
	}
	report := verifyPacket(
		testSummaries(source),
		[]store.ClaimResult{
			{ID: "claim-1", Claim: "Frontend App should use Playwright.", Status: "verified", Source: &source, Freshness: "fresh"},
		},
		[]store.DocumentResult{{ID: "doc-1", Title: "Frontend", Source: source, Content: "Frontend App should use Playwright.", Rank: 1}},
		[]store.RelationResult{{FromEntity: "Frontend App", ToEntity: "Playwright", Type: "should_use", Confidence: 0.8, SourceURL: &source}},
		[]EvidenceItem{{SourceURL: source, Count: 2}},
		testRetrievalPlan(1),
		nil,
		nil,
		[]GraphWarning{warning},
	)
	if report.Verdict != "partial" || !report.ActionRequired {
		t.Fatalf("graph warning should be partial and action-required: %#v", report)
	}
	if len(report.GraphWarnings) != 1 {
		t.Fatalf("graph warnings not surfaced: %#v", report)
	}
	if !containsRecommendation(report.Recommendations, "Review graph warnings") {
		t.Fatalf("graph warning recommendation missing: %#v", report.Recommendations)
	}
}

func TestVerifyPacketWeakWhenRetrievalSignalIsLow(t *testing.T) {
	source := "file://source.md"
	report := verifyPacket(
		testSummaries(source),
		[]store.ClaimResult{
			{ID: "claim-1", Claim: "Frontend uses Playwright.", Status: "verified", Source: &source, Rank: 0.02, TextScore: 0, VectorScore: 0.01, Freshness: "fresh"},
		},
		[]store.DocumentResult{{ID: "doc-1", Title: "Frontend", Source: source, Content: "Frontend uses Playwright.", Rank: 0.01, TextScore: 0, VectorScore: 0}},
		[]store.RelationResult{{FromEntity: "Frontend", ToEntity: "Playwright", Type: "uses", Confidence: 0.8, SourceURL: &source}},
		[]EvidenceItem{{SourceURL: source, Count: 2}},
		testRetrievalPlan(1),
		nil,
		nil,
		nil,
	)
	if report.Verdict != "weak" || !report.ActionRequired || !report.RetrievalQuality.LowConfidence {
		t.Fatalf("low retrieval signal should be weak and action-required: %#v", report)
	}
	if !containsRecommendation(report.Recommendations, "Rerun retrieval with a more specific query") {
		t.Fatalf("low-signal recommendation missing: %#v", report.Recommendations)
	}
}

func testRetrievalPlan(facts int) RetrievalPlan {
	return RetrievalPlan{
		CoverageTargets: RetrievalCoverageTarget{
			Summaries:           1,
			Facts:               facts,
			SupportingDocuments: 1,
			GraphRelations:      1,
			EvidenceSources:     1,
		},
	}
}

func testSummaries(source string) []store.MemorySummaryResult {
	return []store.MemorySummaryResult{{ID: "summary-1", Scope: "team:example", Level: "module", Key: "frontend", Title: "Frontend", Summary: "Frontend memory summary.", SourceCount: 1, SourceURLs: []string{source}, Rank: 0.8}}
}

func containsCheck(values []VerificationCheck, name string) bool {
	for _, value := range values {
		if value.Name == name {
			return true
		}
	}
	return false
}

func containsRecommendation(values []string, fragment string) bool {
	for _, value := range values {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}
