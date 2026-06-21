package store

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestFeedbackIDNormalizesDefaultVerdict(t *testing.T) {
	implicit := FeedbackRecord{
		ClaimID:   "claim-1",
		Reason:    "outdated",
		CreatedBy: "reviewer",
	}
	explicit := implicit
	explicit.Verdict = "incorrect"

	if got, want := feedbackID(implicit), feedbackID(explicit); got != want {
		t.Fatalf("feedbackID implicit incorrect = %q, want %q", got, want)
	}
}

func TestNormalizeConflictDefaultsAndOrdersClaimPair(t *testing.T) {
	conflict := normalizeConflict(ConflictRecord{
		PrimaryClaimID:        " claim-b ",
		ConflictingClaimID:    " claim-a ",
		PrimaryRelationID:     " relation-b ",
		ConflictingRelationID: " relation-a ",
		EntityID:              " entity-1 ",
	})
	if conflict.ConflictType != "contradicts" {
		t.Fatalf("conflict type = %q, want contradicts", conflict.ConflictType)
	}
	if conflict.Severity != "high" {
		t.Fatalf("severity = %q, want high", conflict.Severity)
	}
	if conflict.Authority != "system-detected" {
		t.Fatalf("authority = %q, want system-detected", conflict.Authority)
	}
	left, right := orderedPair(conflict.PrimaryClaimID, conflict.ConflictingClaimID)
	if left != "claim-a" || right != "claim-b" {
		t.Fatalf("ordered pair = %q/%q", left, right)
	}
	left, right = orderedPair(conflict.PrimaryRelationID, conflict.ConflictingRelationID)
	if left != "relation-a" || right != "relation-b" {
		t.Fatalf("ordered relation pair = %q/%q", left, right)
	}
	if conflict.EntityID != "entity-1" {
		t.Fatalf("entity id = %q, want trimmed entity-1", conflict.EntityID)
	}
}

func TestNormalizedConflictStatusAndSeverity(t *testing.T) {
	if got := normalizedConflictStatus(" resolved "); got != "resolved" {
		t.Fatalf("normalizedConflictStatus = %q, want resolved", got)
	}
	if got := normalizedConflictStatus("closed"); got != "" {
		t.Fatalf("unknown status = %q, want empty", got)
	}
	if got := normalizedConflictSeverity(" blocking "); got != "blocking" {
		t.Fatalf("normalizedConflictSeverity = %q, want blocking", got)
	}
	if got := normalizedConflictSeverity("critical"); got != "" {
		t.Fatalf("unknown severity = %q, want empty", got)
	}
}

func TestConflictSelectIncludesResolutionFields(t *testing.T) {
	query := conflictSelectSQL()
	for _, fragment := range []string{
		"COALESCE(resolved_by, '')",
		"COALESCE(resolution, '')",
		"COALESCE(primary_relation_id, '')",
		"COALESCE(conflicting_relation_id, '')",
		"COALESCE(entity_id, '')",
		"resolved_at::text",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("conflict select missing %q:\n%s", fragment, query)
		}
	}
}

func TestCleanStringListTrimsAndDeduplicates(t *testing.T) {
	got := cleanStringList([]string{" a ", "", "b", "a", " b "})
	want := []string{"a", "b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("cleanStringList = %#v, want %#v", got, want)
	}
}

func TestWebhookIngestionJobIDIsStableForDelivery(t *testing.T) {
	base := StartWebhookIngestionJobInput{
		Scope:         " repo:abra ",
		SourceType:    " jira ",
		SourceURL:     " https://jira.example.local/browse/ABRA-1 ",
		ConnectorKind: " jira ",
		EventType:     " issue.updated ",
		DeliveryID:    " delivery-1 ",
		DocumentIndex: 0,
	}
	same := base
	same.Scope = "repo:abra"
	same.SourceType = "jira"
	same.SourceURL = "https://jira.example.local/browse/ABRA-1"
	same.ConnectorKind = "jira"
	same.EventType = "issue.updated"

	if got, want := webhookIngestionJobID(base), webhookIngestionJobID(same); got != want {
		t.Fatalf("webhookIngestionJobID not stable for same delivery: %q != %q", got, want)
	}
}

func TestWebhookIngestionJobIDDifferentiatesBatchDocumentIndex(t *testing.T) {
	first := StartWebhookIngestionJobInput{
		Scope:         "repo:abra",
		SourceType:    "jira",
		SourceURL:     "https://jira.example.local/browse/ABRA-1",
		ConnectorKind: "jira",
		EventType:     "issue.updated",
		DeliveryID:    "delivery-1",
		DocumentIndex: 0,
	}
	second := first
	second.DocumentIndex = 1

	if got, other := webhookIngestionJobID(first), webhookIngestionJobID(second); got == other {
		t.Fatalf("webhookIngestionJobID should differ by document index: %q", got)
	}
}

func TestAuditEventsQueryAppliesFilters(t *testing.T) {
	since := time.Date(2026, 6, 16, 10, 0, 0, 0, time.UTC)
	until := since.Add(time.Hour)
	query, args := auditEventsQuery(AuditEventFilter{
		Scope:      "team:example",
		EventType:  "claim.remembered",
		TargetType: "claim",
		Since:      since,
		Until:      until,
		Limit:      250,
	})

	for _, fragment := range []string{
		"scope = $1",
		"event_type = $2",
		"target_type = $3",
		"created_at >= $4",
		"created_at <= $5",
		"LIMIT $6",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("query missing %q:\n%s", fragment, query)
		}
	}
	if len(args) != 6 {
		t.Fatalf("args = %d, want 6", len(args))
	}
	if got := args[0]; got != "team:example" {
		t.Fatalf("scope arg = %#v", got)
	}
	if got := args[1]; got != "claim.remembered" {
		t.Fatalf("event_type arg = %#v", got)
	}
	if got := args[2]; got != "claim" {
		t.Fatalf("target_type arg = %#v", got)
	}
	if got := args[3].(time.Time); !got.Equal(since) {
		t.Fatalf("since arg = %s, want %s", got, since)
	}
	if got := args[4].(time.Time); !got.Equal(until) {
		t.Fatalf("until arg = %s, want %s", got, until)
	}
	if got := args[5]; got != 250 {
		t.Fatalf("limit arg = %#v", got)
	}
}

func TestAuditEventsQueryClampsLimit(t *testing.T) {
	query, args := auditEventsQuery(AuditEventFilter{Limit: 5000})
	if !strings.Contains(query, "LIMIT $1") {
		t.Fatalf("query should only bind limit:\n%s", query)
	}
	if got := args[0]; got != 100 {
		t.Fatalf("default limit = %#v, want 100", got)
	}
}

func TestFullTextAnyQuerySanitizesTerms(t *testing.T) {
	got := fullTextAnyQuery("web-app repository source areas features components api")
	want := "web | app | repository | source | areas | features | components | api"
	if got != want {
		t.Fatalf("fullTextAnyQuery = %q, want %q", got, want)
	}
}

func TestFullTextAnyQueryHandlesEmptyInput(t *testing.T) {
	if got := fullTextAnyQuery("! ?"); got != "__abra_no_match__" {
		t.Fatalf("empty fullTextAnyQuery = %q", got)
	}
}

func TestSearchClaimsQueryCanUseClaimSearchVector(t *testing.T) {
	query := searchClaimsSQL()
	for _, fragment := range []string{
		"FROM claims",
		"status NOT IN ('deprecated', 'expired')",
		"search_vector @@ plainto_tsquery('simple', $1)",
		"search_vector @@ to_tsquery('simple', $5)",
		"LIMIT $4",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("search claims query missing %q:\n%s", fragment, query)
		}
	}
}

func TestHybridRecallQueriesUseVectorAndTextCandidates(t *testing.T) {
	claims := hybridRecallClaimsSQL("status IN ('verified', 'inferred')", 1024)
	for _, fragment := range []string{
		"WITH text_matches AS",
		"vector_matches AS",
		"embedding::vector(1024) <=> $5::vector(1024)",
		"embedding_dimensions = $6",
		"UNION",
		"COALESCE(tm.text_score, 0) AS text_score",
		"COALESCE(vm.vector_score, 0) AS vector_score",
		"COALESCE(tm.text_score, 0)",
		"COALESCE(vm.vector_score, 0) * 0.45",
		"ORDER BY rank_score DESC",
	} {
		if !strings.Contains(claims, fragment) {
			t.Fatalf("hybrid claims query missing %q:\n%s", fragment, claims)
		}
	}

	docs := hybridRecallDocumentsSQL(1024)
	for _, fragment := range []string{
		"WITH text_matches AS",
		"vector_matches AS",
		"ch.embedding::vector(1024) <=> $5::vector(1024)",
		"ch.embedding_dimensions = $6",
		"UNION",
		"COALESCE(tm.text_score, 0) AS text_score",
		"COALESCE(vm.vector_score, 0) AS vector_score",
		"COALESCE(tm.text_score, 0)",
		"COALESCE(vm.vector_score, 0) * 0.45",
		"ORDER BY rank_score DESC",
	} {
		if !strings.Contains(docs, fragment) {
			t.Fatalf("hybrid documents query missing %q:\n%s", fragment, docs)
		}
	}
}

func TestRecallRetrievalReasonsExplainSignals(t *testing.T) {
	result := RecallResult{
		RetrievalMode: "hybrid",
		Claims: []ClaimResult{
			{ID: "claim-text", TextScore: 0.8},
			{ID: "claim-vector", VectorScore: 0.7},
		},
		SupportingDocuments: []DocumentResult{
			{ID: "doc-both", TextScore: 0.4, VectorScore: 0.6},
		},
		GraphContext: []RelationResult{
			{ID: "relation-1", FromEntity: "A", Type: "depends_on", ToEntity: "B"},
		},
	}

	reasons := recallRetrievalReasons(result)
	if !hasRetrievalReason(reasons, "text", "hybrid", 2) {
		t.Fatalf("text retrieval reason missing: %#v", reasons)
	}
	if !hasRetrievalReason(reasons, "vector", "hybrid", 2) {
		t.Fatalf("vector retrieval reason missing: %#v", reasons)
	}
	if !hasRetrievalReason(reasons, "graph", "entity_local", 1) {
		t.Fatalf("graph retrieval reason missing: %#v", reasons)
	}
}

func TestRecallRetrievalReasonsFallbackWhenScoresHidden(t *testing.T) {
	reasons := recallRetrievalReasons(RecallResult{
		RetrievalMode: "full_text",
		Claims:        []ClaimResult{{ID: "claim-1"}},
	})
	if len(reasons) != 1 || reasons[0].Signal != "rank" || reasons[0].Count != 1 {
		t.Fatalf("fallback retrieval reason = %#v", reasons)
	}
}

func TestApplyBaseRankScoresDefaultsToRankScore(t *testing.T) {
	result := RecallResult{
		Claims: []ClaimResult{
			{ID: "claim-1", Rank: 0.7},
			{ID: "claim-2", Rank: 0.5, BaseRank: 0.4},
		},
		SupportingDocuments: []DocumentResult{
			{ID: "doc-1", Rank: 0.6},
			{ID: "doc-2", Rank: 0.5, BaseRank: 0.3},
		},
	}
	applyBaseRankScores(&result)
	if result.Claims[0].BaseRank != 0.7 || result.Claims[1].BaseRank != 0.4 {
		t.Fatalf("claim base ranks = %#v", result.Claims)
	}
	if result.SupportingDocuments[0].BaseRank != 0.6 || result.SupportingDocuments[1].BaseRank != 0.3 {
		t.Fatalf("document base ranks = %#v", result.SupportingDocuments)
	}
}

func hasRetrievalReason(reasons []RetrievalReason, signal, mode string, count int) bool {
	for _, reason := range reasons {
		if reason.Signal == signal && reason.Mode == mode && reason.Count == count {
			return true
		}
	}
	return false
}

func TestMemorySummaryRankingBoostsCodeIntelligenceLevels(t *testing.T) {
	query := memorySummarySelectSQL()
	for _, fragment := range []string{
		"WHEN 'repo' THEN 0.35",
		"WHEN 'module' THEN 0.3",
		"WHEN 'route' THEN 0.34",
		"WHEN 'component' THEN 0.34",
		"WHEN 'symbol' THEN 0.34",
		"WHEN 'package' THEN 0.34",
		"WHEN 'file' THEN 0.2",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("memory summary ranking missing %q:\n%s", fragment, query)
		}
	}
}

func TestPendingLearningProposalQueryDeduplicatesReviewQueue(t *testing.T) {
	query := pendingLearningProposalSelectSQL()
	for _, fragment := range []string{
		"status = 'pending'",
		"scope = $1",
		"proposal_type = $2",
		"title = $3",
		"COALESCE(target_type, '') = $4",
		"COALESCE(target_id, '') = $5",
		"COALESCE(source_url, '') = $6",
		"LIMIT 1",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("pending learning proposal query missing %q:\n%s", fragment, query)
		}
	}
}

func TestLearningProposalPendingDedupMigrationAddsDatabaseGuard(t *testing.T) {
	migration, err := os.ReadFile("../../migrations/010_learning_proposal_pending_dedup.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	query := string(migration)
	for _, fragment := range []string{
		"CREATE UNIQUE INDEX IF NOT EXISTS learning_proposals_pending_dedup_idx",
		"WHERE status = 'pending'",
		"COALESCE(target_type, '')",
		"COALESCE(target_id, '')",
		"COALESCE(source_url, '')",
		"status = 'canceled'",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("pending dedup migration missing %q:\n%s", fragment, query)
		}
	}
}

func TestCodeDocumentClaimCleanupMigrationDeprecatesTrustedCodeClaims(t *testing.T) {
	migration, err := os.ReadFile("../../migrations/011_deprecate_code_document_claims.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	query := string(migration)
	for _, fragment := range []string{
		"UPDATE claims c",
		"FROM documents d",
		"d.metadata->>'content_kind' = 'code'",
		"c.status NOT IN ('deprecated', 'expired')",
		"status = 'deprecated'",
		"deprecated_by_migration",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("code-claim cleanup migration missing %q:\n%s", fragment, query)
		}
	}
}

func TestScoreMemoryHealthStatuses(t *testing.T) {
	healthy := MemoryHealthResult{
		Documents: MemoryHealthDocument{Total: 3, Active: 3},
		Claims:    MemoryHealthClaim{Total: 4, Verified: 3, Inferred: 1, WithEvidence: 4},
		Graph:     MemoryHealthGraph{ActiveRelations: 2},
		Summaries: MemoryHealthSummary{Total: 5},
	}
	score, status, reasons := scoreMemoryHealth(healthy)
	if status != "healthy" || score != 100 {
		t.Fatalf("healthy score/status = %d/%s reasons=%v, want 100/healthy", score, status, reasons)
	}

	review := healthy
	review.Learning.Pending = 2
	score, status, _ = scoreMemoryHealth(review)
	if status != "needs_review" || score >= 100 {
		t.Fatalf("review score/status = %d/%s, want needs_review below 100", score, status)
	}

	critical := healthy
	critical.Conflicts.Blocking = 1
	score, status, _ = scoreMemoryHealth(critical)
	if status != "critical" || score >= 80 {
		t.Fatalf("critical score/status = %d/%s, want critical below 80", score, status)
	}

	codePollution := healthy
	codePollution.Claims.TrustedFromCodeDocuments = 1
	score, status, reasons = scoreMemoryHealth(codePollution)
	if status != "critical" {
		t.Fatalf("code pollution status = %s reasons=%v, want critical", status, reasons)
	}
	if !strings.Contains(strings.Join(reasons, "\n"), "trusted claims from code documents need cleanup") {
		t.Fatalf("code pollution reasons = %v, want cleanup reason", reasons)
	}

	learningDuplicates := healthy
	learningDuplicates.Learning.DuplicatePendingGroups = 1
	score, status, reasons = scoreMemoryHealth(learningDuplicates)
	if status != "critical" {
		t.Fatalf("learning duplicate status = %s reasons=%v, want critical", status, reasons)
	}
	if !strings.Contains(strings.Join(reasons, "\n"), "duplicate pending learning proposals need cleanup") {
		t.Fatalf("learning duplicate reasons = %v, want cleanup reason", reasons)
	}

	staleRunning := healthy
	staleRunning.Ingestion.StaleRunningJobs = 1
	score, status, reasons = scoreMemoryHealth(staleRunning)
	if status != "critical" {
		t.Fatalf("stale running status = %s reasons=%v, want critical", status, reasons)
	}
	if !strings.Contains(strings.Join(reasons, "\n"), "ingestion jobs are stale while running") {
		t.Fatalf("stale running reasons = %v, want cleanup reason", reasons)
	}

	retryBacklog := healthy
	retryBacklog.Ingestion.RetryJobs = 1
	score, status, reasons = scoreMemoryHealth(retryBacklog)
	if status != "needs_review" {
		t.Fatalf("retry backlog status = %s reasons=%v, want needs_review", status, reasons)
	}
	if !strings.Contains(strings.Join(reasons, "\n"), "ingestion jobs are waiting to retry") {
		t.Fatalf("retry backlog reasons = %v, want retry reason", reasons)
	}
}

func TestAssessMemoryHealthSignals(t *testing.T) {
	healthy := MemoryHealthResult{
		Documents: MemoryHealthDocument{Total: 3, Active: 3},
		Claims:    MemoryHealthClaim{Total: 4, Verified: 3, Inferred: 1, WithEvidence: 4},
		Graph:     MemoryHealthGraph{ActiveRelations: 2},
		Summaries: MemoryHealthSummary{Total: 5},
	}
	assessment := assessMemoryHealth(healthy)
	if assessment.Status != "healthy" || assessment.Score != 100 {
		t.Fatalf("healthy assessment = %d/%s, want 100/healthy", assessment.Score, assessment.Status)
	}
	if len(assessment.Signals) != 1 || assessment.Signals[0].Code != "memory_ready" || assessment.Signals[0].Action != "proceed" {
		t.Fatalf("healthy signals = %#v, want memory_ready proceed signal", assessment.Signals)
	}

	unhealthy := healthy
	unhealthy.Claims.TrustedFromCodeDocuments = 2
	unhealthy.Learning.DuplicatePendingGroups = 1
	unhealthy.Ingestion.StaleRunningJobs = 1
	assessment = assessMemoryHealth(unhealthy)
	if assessment.Status != "critical" {
		t.Fatalf("unhealthy status = %s signals=%#v, want critical", assessment.Status, assessment.Signals)
	}
	wantSignals := map[string]struct {
		category string
		severity string
		count    int
	}{
		"trusted_claims_from_code_documents": {category: "trust_guard", severity: "critical", count: 2},
		"learning_duplicate_pending_groups":  {category: "trust_guard", severity: "critical", count: 1},
		"ingestion_jobs_stale_running":       {category: "ingestion", severity: "critical", count: 1},
	}
	for code, want := range wantSignals {
		got, ok := findMemoryHealthSignal(assessment.Signals, code)
		if !ok {
			t.Fatalf("signals missing %q: %#v", code, assessment.Signals)
		}
		if got.Category != want.category || got.Severity != want.severity || got.Count != want.count || got.Action == "" || got.ScoreImpact <= 0 {
			t.Fatalf("signal %q = %#v, want category=%s severity=%s count=%d action and score impact", code, got, want.category, want.severity, want.count)
		}
	}
}

func findMemoryHealthSignal(signals []MemoryHealthSignal, code string) (MemoryHealthSignal, bool) {
	for _, signal := range signals {
		if signal.Code == code {
			return signal, true
		}
	}
	return MemoryHealthSignal{}, false
}
