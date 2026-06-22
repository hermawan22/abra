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
	if report.RetrievalQuality.UniqueSources != 1 || report.RetrievalQuality.LowSourceDiversity {
		t.Fatalf("small narrow packet should report one source without low-diversity gate: %#v", report.RetrievalQuality)
	}
	if !containsCheck(report.Checks, "retrieval_quality") {
		t.Fatalf("retrieval quality check missing: %#v", report.Checks)
	}
	if !containsCheck(report.Checks, "retrieval_source_diversity") {
		t.Fatalf("retrieval source diversity check missing: %#v", report.Checks)
	}
	if !containsCheck(report.Checks, "retrieval_coverage") || !report.RetrievalCoverage.Complete {
		t.Fatalf("retrieval coverage check missing or incomplete: %#v", report)
	}
	if len(report.RequiredActions) != 1 || report.RequiredActions[0] != "cite_evidence" {
		t.Fatalf("strong packet should expose cite action only: %#v", report.RequiredActions)
	}
}

func TestVerifyPacketSurfacesWeakEvidenceAnchorWithoutBlocking(t *testing.T) {
	source := "file://source.md"
	report := verifyPacket(
		testSummaries(source),
		[]store.ClaimResult{
			{ID: "claim-1", Claim: "Frontend uses Playwright.", Status: "verified", Source: &source, Rank: 1.2, TextScore: 0.3, VectorScore: 0.2, Freshness: "fresh"},
		},
		[]store.DocumentResult{
			{ID: "doc-1", Title: "Frontend", Source: source, Content: "Frontend browser testing guidance.", Rank: 1, TextScore: 0.4, VectorScore: 0.1},
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
	if report.Verdict != "strong" || report.ActionRequired {
		t.Fatalf("weak text anchor should stay advisory when source-backed packet is otherwise healthy: %#v", report)
	}
	if len(report.WeakEvidenceAnchors) != 1 || report.WeakEvidenceAnchors[0] != "claim-1" {
		t.Fatalf("weak evidence anchor claim not surfaced: %#v", report.WeakEvidenceAnchors)
	}
	if check, ok := findCheck(report.Checks, "evidence_anchors"); !ok || check.Status != "advisory" || check.Score != 1 {
		t.Fatalf("evidence anchor check wrong: %#v ok=%v", check, ok)
	}
	if contains(report.RequiredActions, "validate_evidence_anchors") || contains(report.RequiredActions, "retrieve_supporting_source_chunks") {
		t.Fatalf("weak evidence anchors should not be required actions by themselves: %#v", report.RequiredActions)
	}
	if !containsRecommendation(report.Recommendations, "same-source quote or text-span") {
		t.Fatalf("evidence anchor recommendation missing: %#v", report.Recommendations)
	}
}

func TestVerifyPacketReportsRerankQuality(t *testing.T) {
	source := "file://source.md"
	report := verifyPacket(
		testSummaries(source),
		[]store.ClaimResult{
			{ID: "claim-1", Claim: "Frontend uses Playwright.", Status: "verified", Source: &source, Rank: 1.1, BaseRank: 0.9, TextScore: 0.3, VectorScore: 0.2, RerankApplied: true, RerankScore: 1, Freshness: "fresh"},
		},
		[]store.DocumentResult{
			{ID: "doc-1", Title: "Frontend", Source: source, Content: "Frontend uses Playwright.", Rank: 1, BaseRank: 0.9, TextScore: 0.4, VectorScore: 0.1, RerankApplied: true, RerankScore: 0.8},
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
	if report.RetrievalQuality.RerankedResults != 2 || report.RetrievalQuality.TopRerankScore != 1 || report.RetrievalQuality.AverageRerankScore != 0.9 {
		t.Fatalf("rerank quality not reported: %#v", report.RetrievalQuality)
	}
	if report.RetrievalQuality.LowConfidence {
		t.Fatalf("rerank metadata should not create low confidence: %#v", report.RetrievalQuality)
	}
}

func TestVerifyPacketUnsafeWhenMemoryHealthIsCritical(t *testing.T) {
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
		store.MemoryHealthResult{
			Status: "critical",
			Score:  35,
			Signals: []store.MemoryHealthSignal{{
				Code:     "ingestion_jobs_stale_running",
				Severity: "critical",
				Action:   "inspect_ingestion_liveness",
			}},
		},
	)
	if report.Verdict != "unsafe" || !report.ActionRequired {
		t.Fatalf("critical memory health should block strong verification: %#v", report)
	}
	if report.MemoryHealthStatus != "critical" {
		t.Fatalf("memory health status missing: %#v", report)
	}
	if check, ok := findCheck(report.Checks, "memory_health"); !ok || check.Status != "fail" {
		t.Fatalf("memory health check wrong: %#v ok=%v", check, ok)
	}
	if !contains(report.RequiredActions, "inspect_ingestion_liveness") {
		t.Fatalf("memory health required action missing: %#v", report.RequiredActions)
	}
	if !containsRecommendation(report.Recommendations, "Inspect memory health signals") {
		t.Fatalf("memory health recommendation missing: %#v", report.Recommendations)
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
	if !contains(report.RequiredActions, "attach_missing_evidence") {
		t.Fatalf("missing-evidence required action missing: %#v", report.RequiredActions)
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
	if !contains(report.RequiredActions, "resolve_active_conflicts") || !contains(report.RequiredActions, "review_conflict_evidence") {
		t.Fatalf("conflict required actions missing: %#v", report.RequiredActions)
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
	if !contains(report.RequiredActions, "rerun_degraded_retrieval") {
		t.Fatalf("degraded retrieval required action missing: %#v", report.RequiredActions)
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
	if !contains(report.RequiredActions, "cite_source_chunks_and_graph") {
		t.Fatalf("code-context required action missing: %#v", report.RequiredActions)
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
	if !contains(report.RequiredActions, "fill_missing_retrieval_layers") || !contains(report.RequiredActions, "retrieve_graph_relations") {
		t.Fatalf("coverage required actions missing: %#v", report.RequiredActions)
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
	if !contains(report.RequiredActions, "review_graph_warnings") {
		t.Fatalf("graph warning required action missing: %#v", report.RequiredActions)
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
	if !contains(report.RequiredActions, "rerun_with_more_specific_query") || !contains(report.RequiredActions, "check_embeddings_or_reindex") {
		t.Fatalf("low-signal required actions missing: %#v", report.RequiredActions)
	}
}

func TestVerifyPacketWeakWhenBoostedRankHasNoRelevanceSignal(t *testing.T) {
	source := "file://source.md"
	report := verifyPacket(
		testSummaries(source),
		[]store.ClaimResult{
			{ID: "claim-1", Claim: "Boosted but not actually matched.", Status: "verified", Source: &source, Rank: 1.5, TextScore: 0, VectorScore: 0, Freshness: "fresh"},
		},
		[]store.DocumentResult{{ID: "doc-1", Title: "Boosted", Source: source, Content: "Boosted authority result.", Rank: 1.2, TextScore: 0, VectorScore: 0}},
		[]store.RelationResult{{FromEntity: "A", ToEntity: "B", Type: "uses", Confidence: 0.8, SourceURL: &source}},
		[]EvidenceItem{{SourceURL: source, Count: 2}},
		testRetrievalPlan(1),
		nil,
		nil,
		nil,
	)
	if report.Verdict != "weak" || !report.ActionRequired || !report.RetrievalQuality.LowConfidence {
		t.Fatalf("boosted rank without lexical/vector relevance should be weak and action-required: %#v", report)
	}
	if report.RetrievalQuality.TopRankScore < 1 {
		t.Fatalf("test should preserve boosted rank while still flagging low confidence: %#v", report.RetrievalQuality)
	}
	if check, ok := findCheck(report.Checks, "retrieval_quality"); !ok || check.Status != "review" || !strings.Contains(check.Message, "very low lexical and semantic relevance signal") {
		t.Fatalf("boosted low-signal retrieval quality check wrong: %#v ok=%v", check, ok)
	}
	if !contains(report.RequiredActions, "rerun_with_more_specific_query") || !contains(report.RequiredActions, "check_embeddings_or_reindex") {
		t.Fatalf("boosted low-signal required actions missing: %#v", report.RequiredActions)
	}
}

func TestVerifyPacketAllowsModerateRankOnlyCompatibility(t *testing.T) {
	source := "file://source.md"
	report := verifyPacket(
		testSummaries(source),
		[]store.ClaimResult{
			{ID: "claim-1", Claim: "Rank-only result from a compatible retriever.", Status: "verified", Source: &source, Rank: 0.4, TextScore: 0, VectorScore: 0, Freshness: "fresh"},
		},
		[]store.DocumentResult{{ID: "doc-1", Title: "Rank only", Source: source, Content: "Rank-only source chunk.", Rank: 0.4, TextScore: 0, VectorScore: 0}},
		[]store.RelationResult{{FromEntity: "A", ToEntity: "B", Type: "uses", Confidence: 0.8, SourceURL: &source}},
		[]EvidenceItem{{SourceURL: source, Count: 2}},
		testRetrievalPlan(1),
		nil,
		nil,
		nil,
	)
	if report.RetrievalQuality.LowConfidence {
		t.Fatalf("moderate rank-only compatibility path should not be low confidence: %#v", report.RetrievalQuality)
	}
	if check, ok := findCheck(report.Checks, "retrieval_quality"); !ok || check.Status != "pass" || !strings.Contains(check.Message, "ranking signal is strong enough") {
		t.Fatalf("rank-only retrieval quality check wrong: %#v ok=%v", check, ok)
	}
}

func TestVerifyPacketPartialWhenManyResultsComeFromOneSource(t *testing.T) {
	source := "file://single-source.md"
	report := verifyPacket(
		testSummaries(source),
		[]store.ClaimResult{
			{ID: "claim-1", Claim: "First claim.", Status: "verified", Source: &source, Rank: 1.2, TextScore: 0.3, VectorScore: 0.2, Freshness: "fresh"},
			{ID: "claim-2", Claim: "Second claim.", Status: "verified", Source: &source, Rank: 1.1, TextScore: 0.2, VectorScore: 0.2, Freshness: "fresh"},
		},
		[]store.DocumentResult{
			{ID: "doc-1", Title: "Single", Source: source, Content: "First source chunk.", Rank: 1, TextScore: 0.4, VectorScore: 0.1},
			{ID: "doc-2", Title: "Single", Source: source, Content: "Second source chunk.", Rank: 0.9, TextScore: 0.3, VectorScore: 0.1},
		},
		[]store.RelationResult{{FromEntity: "A", ToEntity: "B", Type: "supports", Confidence: 0.8, SourceURL: &source}},
		[]EvidenceItem{{SourceURL: source, Count: 4}},
		testRetrievalPlan(1),
		nil,
		nil,
		nil,
	)
	if report.Verdict != "partial" || !report.ActionRequired || !report.RetrievalQuality.LowSourceDiversity {
		t.Fatalf("single-source dominant packet should be partial/action-required: %#v", report)
	}
	if report.RetrievalQuality.UniqueSources != 1 || report.RetrievalQuality.DominantSourceShare != 1 {
		t.Fatalf("source diversity metrics wrong: %#v", report.RetrievalQuality)
	}
	if !containsRecommendation(report.Recommendations, "Corroborate this packet with another source") {
		t.Fatalf("source diversity recommendation missing: %#v", report.Recommendations)
	}
	if !contains(report.RequiredActions, "corroborate_with_additional_source") {
		t.Fatalf("source diversity required action missing: %#v", report.RequiredActions)
	}
}

func TestVerifyPacketCanonicalizesSourceURLsForDiversity(t *testing.T) {
	claimSourceA := "https://Example.test/docs/runbook.md?utm=one#section-a"
	claimSourceB := "https://example.test/docs/./runbook.md#section-b"
	docSourceA := "https://example.test/docs/runbook.md?utm=two"
	docSourceB := "https://EXAMPLE.test/docs/runbook.md/"
	report := verifyPacket(
		testSummaries(claimSourceA),
		[]store.ClaimResult{
			{ID: "claim-1", Claim: "First claim.", Status: "verified", Source: &claimSourceA, Rank: 1.2, TextScore: 0.3, VectorScore: 0.2, Freshness: "fresh"},
			{ID: "claim-2", Claim: "Second claim.", Status: "verified", Source: &claimSourceB, Rank: 1.1, TextScore: 0.2, VectorScore: 0.2, Freshness: "fresh"},
		},
		[]store.DocumentResult{
			{ID: "doc-1", Title: "Runbook", Source: docSourceA, Content: "First source chunk.", Rank: 1, TextScore: 0.4, VectorScore: 0.1},
			{ID: "doc-2", Title: "Runbook", Source: docSourceB, Content: "Second source chunk.", Rank: 0.9, TextScore: 0.3, VectorScore: 0.1},
		},
		[]store.RelationResult{{FromEntity: "A", ToEntity: "B", Type: "supports", Confidence: 0.8, SourceURL: &claimSourceA}},
		[]EvidenceItem{{SourceURL: claimSourceA, Count: 4}},
		testRetrievalPlan(1),
		nil,
		nil,
		nil,
	)
	if !report.RetrievalQuality.LowSourceDiversity || report.RetrievalQuality.UniqueSources != 1 || report.RetrievalQuality.DominantSourceShare != 1 {
		t.Fatalf("canonical source variants should count as one dominant source: %#v", report.RetrievalQuality)
	}
	if report.EvidenceSources != 1 {
		t.Fatalf("evidence source variants should canonicalize to one source, got %d", report.EvidenceSources)
	}
}

func TestCanonicalSourceIDNormalizesEquivalentURLs(t *testing.T) {
	left := canonicalSourceID("https://EXAMPLE.test/docs/./guide.md/?utm_source=x#L20")
	right := canonicalSourceID("https://example.test/docs/guide.md")
	if left != right {
		t.Fatalf("canonical source IDs differ: %q != %q", left, right)
	}
}

func TestCanonicalSourceIDNormalizesFileURLs(t *testing.T) {
	got := canonicalSourceID("file:///repo//src/../src/app.tsx#L10")
	want := "file:///repo/src/app.tsx"
	if got != want {
		t.Fatalf("canonical file URL = %q, want %q", got, want)
	}
}

func TestVerifyPacketCanonicalEvidenceSourcesDriveCoverage(t *testing.T) {
	sourceA := "https://EXAMPLE.test/docs/./guide.md/?utm_source=x#L20"
	sourceB := "https://example.test/docs/guide.md"
	report := verifyPacket(
		nil,
		nil,
		[]store.DocumentResult{
			{ID: "doc-1", Title: "Guide", Source: sourceA, Content: "Guide chunk.", Rank: 1, TextScore: 0.2, VectorScore: 0.1},
			{ID: "doc-2", Title: "Guide", Source: sourceB, Content: "Guide chunk two.", Rank: 0.9, TextScore: 0.2, VectorScore: 0.1},
		},
		nil,
		[]EvidenceItem{{SourceURL: sourceA, Count: 1}, {SourceURL: sourceB, Count: 1}},
		RetrievalPlan{CoverageTargets: RetrievalCoverageTarget{SupportingDocuments: 1, EvidenceSources: 2}},
		nil,
		nil,
		nil,
	)
	if report.EvidenceSources != 1 || report.RetrievalCoverage.Actual.EvidenceSources != report.EvidenceSources {
		t.Fatalf("canonical source count should drive coverage: report=%d coverage=%d", report.EvidenceSources, report.RetrievalCoverage.Actual.EvidenceSources)
	}
	if report.RetrievalCoverage.Complete || !contains(report.RetrievalCoverage.Missing, "evidence_sources") {
		t.Fatalf("canonical duplicate sources should miss the evidence source target: %#v", report.RetrievalCoverage)
	}
}

func TestVerifyPacketFlagsUnsourcedResultDominance(t *testing.T) {
	sourceA := "file://source-a.md"
	sourceB := "file://source-b.md"
	report := verifyPacket(
		testSummaries(sourceA),
		[]store.ClaimResult{
			{ID: "claim-1", Claim: "First claim.", Status: "verified", Source: &sourceA, Rank: 1.2, TextScore: 0.3, VectorScore: 0.2, Freshness: "fresh"},
			{ID: "claim-2", Claim: "Second claim.", Status: "verified", Source: &sourceB, Rank: 1.1, TextScore: 0.2, VectorScore: 0.2, Freshness: "fresh"},
			{ID: "claim-3", Claim: "Unsourced claim.", Status: "verified", Rank: 1.0, TextScore: 0.2, VectorScore: 0.2, Freshness: "fresh"},
		},
		[]store.DocumentResult{
			{ID: "doc-1", Title: "Unsourced", Content: "Unsourced source chunk.", Rank: 1, TextScore: 0.4, VectorScore: 0.1},
			{ID: "doc-2", Title: "Unsourced", Content: "Another unsourced source chunk.", Rank: 0.9, TextScore: 0.3, VectorScore: 0.1},
		},
		[]store.RelationResult{{FromEntity: "A", ToEntity: "B", Type: "supports", Confidence: 0.8, SourceURL: &sourceA}},
		[]EvidenceItem{{SourceURL: sourceA, Count: 1}, {SourceURL: sourceB, Count: 1}},
		testRetrievalPlan(1),
		nil,
		nil,
		nil,
	)
	if !report.RetrievalQuality.LowSourceDiversity || report.RetrievalQuality.UniqueSources != 2 {
		t.Fatalf("unsourced dominance should require source diversity repair: %#v", report.RetrievalQuality)
	}
	if report.RetrievalQuality.UnsourcedResults != 3 || report.RetrievalQuality.UnsourcedResultShare != 0.6 {
		t.Fatalf("unsourced metrics wrong: %#v", report.RetrievalQuality)
	}
	if !contains(report.RequiredActions, "corroborate_with_additional_source") {
		t.Fatalf("unsourced dominance should require corroboration: %#v", report.RequiredActions)
	}
	if !contains(report.RequiredActions, "attach_missing_source_urls") || !contains(report.RequiredActions, "discard_unsourced_retrieval_results") {
		t.Fatalf("unsourced results should require source repair or discard: %#v", report.RequiredActions)
	}
	if check, ok := findCheck(report.Checks, "retrieval_source_diversity"); !ok || !strings.Contains(check.Message, "without source URLs") {
		t.Fatalf("unsourced diversity check should mention source URLs: %#v ok=%v", check, ok)
	}
}

func TestVerifyPacketActionRequiredForUnsourcedDocuments(t *testing.T) {
	report := verifyPacket(
		nil,
		nil,
		[]store.DocumentResult{
			{ID: "doc-1", Title: "Unsourced", Content: "Unsourced chunk.", Rank: 1, TextScore: 0.4, VectorScore: 0.1},
			{ID: "doc-2", Title: "Unsourced", Content: "Another unsourced chunk.", Rank: 0.9, TextScore: 0.3, VectorScore: 0.1},
		},
		nil,
		nil,
		RetrievalPlan{CoverageTargets: RetrievalCoverageTarget{SupportingDocuments: 2}},
		nil,
		nil,
		nil,
	)
	if report.Verdict != "partial" || !report.ActionRequired {
		t.Fatalf("unsourced documents should require action but remain partial: %#v", report)
	}
	if report.RetrievalQuality.UnsourcedResults != 2 || report.RetrievalQuality.UnsourcedResultShare != 1 {
		t.Fatalf("unsourced document metrics wrong: %#v", report.RetrievalQuality)
	}
	if !contains(report.RequiredActions, "attach_missing_source_urls") || !contains(report.RequiredActions, "discard_unsourced_retrieval_results") {
		t.Fatalf("unsourced document actions missing: %#v", report.RequiredActions)
	}
	if !containsRecommendation(report.Recommendations, "Attach source URLs") {
		t.Fatalf("unsourced document recommendation missing: %#v", report.Recommendations)
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
	_, ok := findCheck(values, name)
	return ok
}

func findCheck(values []VerificationCheck, name string) (VerificationCheck, bool) {
	for _, value := range values {
		if value.Name == name {
			return value, true
		}
	}
	return VerificationCheck{}, false
}

func containsRecommendation(values []string, fragment string) bool {
	for _, value := range values {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}
