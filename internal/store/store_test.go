package store

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type fakeStoreRunner struct {
	execSQL     []string
	queryRowSQL []string
}

func (r *fakeStoreRunner) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	r.execSQL = append(r.execSQL, compactSQL(sql))
	return pgconn.CommandTag{}, nil
}

func (r *fakeStoreRunner) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	return &fakeRows{}, nil
}

func (r *fakeStoreRunner) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	sql = compactSQL(sql)
	r.queryRowSQL = append(r.queryRowSQL, sql)
	switch {
	case strings.Contains(sql, "UPDATE relations"):
		return fakeRow{err: pgx.ErrNoRows}
	case strings.Contains(sql, "SELECT id FROM documents"):
		return fakeRow{values: []any{"doc-1"}}
	case strings.Contains(sql, "SELECT id FROM claims"):
		return fakeRow{values: []any{"claim-1"}}
	case strings.Contains(sql, "INSERT INTO relations"):
		return fakeRow{values: []any{"relation-1"}}
	case strings.Contains(sql, "SELECT id") && strings.Contains(sql, "memory_summaries"):
		return fakeRow{values: []any{"summary-1"}}
	default:
		return fakeRow{values: []any{"id-1"}}
	}
}

type fakeRow struct {
	values []any
	err    error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	for i := range dest {
		if i >= len(r.values) {
			break
		}
		if target, ok := dest[i].(*string); ok {
			*target, _ = r.values[i].(string)
		}
	}
	return nil
}

type fakeRows struct{}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Next() bool                                   { return false }
func (r *fakeRows) Scan(...any) error                            { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

func compactSQL(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}

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

func TestClaimLifecycleSQLFiltersTemporalAndSupersededClaims(t *testing.T) {
	for name, query := range map[string]string{
		"search":      searchClaimsSQL(),
		"hybrid":      hybridRecallClaimsSQL(activeClaimStatusSQL("c", false), 3, false),
		"active_only": claimEffectiveSQL("c"),
	} {
		for _, fragment := range []string{
			"valid_from IS NULL OR",
			"valid_from <= now()",
			"expires_at IS NULL OR",
			"expires_at > now()",
			"supersedes_claim_id",
		} {
			if !strings.Contains(query, fragment) {
				t.Fatalf("%s lifecycle SQL missing %q:\n%s", name, fragment, query)
			}
		}
	}
	if !strings.Contains(claimEffectiveSQL("c"), "superseding_claim.status NOT IN ('deprecated', 'expired')") {
		t.Fatalf("general lifecycle SQL should hide claims superseded by any active replacement:\n%s", claimEffectiveSQL("c"))
	}
	trustedOnly := trustedClaimEffectiveSQL("c")
	if !strings.Contains(trustedOnly, "superseding_claim.status IN ('verified', 'inferred')") {
		t.Fatalf("trusted lifecycle SQL should only hide claims superseded by trusted replacements:\n%s", trustedOnly)
	}
	includeUnverified := claimEffectiveSQLForRecall("c", true)
	if !strings.Contains(includeUnverified, "superseding_claim.status NOT IN ('deprecated', 'expired')") {
		t.Fatalf("include-unverified lifecycle SQL should hide claims superseded by any active replacement:\n%s", includeUnverified)
	}
	if got := activeClaimStatusSQL("c", true); strings.Contains(got, "'deprecated'") && strings.Contains(got, "'expired'") {
		return
	}
	t.Fatalf("include-unverified status filter should still exclude deprecated and expired claims")
}

func TestGraphLifecycleSQLFiltersTemporalEntitiesAndRelations(t *testing.T) {
	activeRelations := compactSQL(listActiveRelationsFromEntitySQL())
	for _, fragment := range []string{
		"r.valid_from IS NULL OR r.valid_from <= now()",
		"r.expires_at IS NULL OR r.expires_at > now()",
		"src.valid_from IS NULL OR src.valid_from <= now()",
		"dst.valid_from IS NULL OR dst.valid_from <= now()",
	} {
		if !strings.Contains(activeRelations, fragment) {
			t.Fatalf("active relation SQL missing %q:\n%s", fragment, activeRelations)
		}
	}

	related := compactSQL(relatedGraphSQL())
	for _, fragment := range []string{
		"WITH seed_entities AS",
		"seed_edges AS",
		"neighbor_edges AS",
		"e.valid_from IS NULL OR e.valid_from <= now()",
		"r.valid_from IS NULL OR r.valid_from <= now()",
		"src.valid_from IS NULL OR src.valid_from <= now()",
		"dst.valid_from IS NULL OR dst.valid_from <= now()",
		"r.expires_at IS NULL OR r.expires_at > now()",
		"src.expires_at IS NULL OR src.expires_at > now()",
		"dst.expires_at IS NULL OR dst.expires_at > now()",
	} {
		if !strings.Contains(related, fragment) {
			t.Fatalf("related graph SQL missing %q:\n%s", fragment, related)
		}
	}
	if strings.Count(related, "r.valid_from IS NULL OR r.valid_from <= now()") < 3 {
		t.Fatalf("related graph SQL should filter relation lifecycle in seed, neighbor, and final select:\n%s", related)
	}
}

func TestInsertClaimPersistsTemporalLifecycleFields(t *testing.T) {
	runner := &fakeStoreRunner{}
	store := &Store{runner: runner}
	_, err := store.InsertClaim(context.Background(), ClaimRecord{
		ClaimText:           "Use the new source of truth.",
		Scope:               "repo:abra",
		SourceURL:           "file://docs/new.md",
		SourceType:          "markdown",
		Embedding:           []float64{1, 0, 0},
		EmbeddingProvider:   "test",
		EmbeddingModel:      "test-model",
		EmbeddingDimensions: 3,
		ValidFrom:           "2026-01-01T00:00:00Z",
		ExpiresAt:           "2026-12-31T00:00:00Z",
		SupersedesClaimID:   "claim-old",
	})
	if err != nil {
		t.Fatalf("InsertClaim error = %v", err)
	}
	combined := strings.Join(runner.execSQL, "\n")
	for _, fragment := range []string{
		"valid_from, expires_at, supersedes_claim_id",
		"NULLIF($14, '')::timestamptz, NULLIF($15, '')::timestamptz, NULLIF($16, '')",
		"valid_from = COALESCE(NULLIF($4, '')::timestamptz, valid_from)",
		"expires_at = COALESCE(NULLIF($5, '')::timestamptz, expires_at)",
		"supersedes_claim_id = COALESCE(NULLIF($6, ''), supersedes_claim_id)",
	} {
		if !strings.Contains(combined, fragment) {
			t.Fatalf("InsertClaim SQL missing %q:\n%s", fragment, combined)
		}
	}
}

func TestInsertClaimRejectsSelfSupersession(t *testing.T) {
	claim := ClaimRecord{
		ClaimText: "Self supersession is invalid.",
		Scope:     "repo:abra",
		SourceURL: "file://docs/self.md",
	}
	claim.SupersedesClaimID = stableID("claim", claim.Scope, claim.SourceURL, claim.ClaimText)
	_, err := (&Store{runner: &fakeStoreRunner{}}).InsertClaim(context.Background(), claim)
	if err == nil || !strings.Contains(err.Error(), "cannot supersede itself") {
		t.Fatalf("InsertClaim self-supersede error = %v, want rejection", err)
	}
}

func TestCleanStringListTrimsAndDeduplicates(t *testing.T) {
	got := cleanStringList([]string{" a ", "", "b", "a", " b "})
	want := []string{"a", "b"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("cleanStringList = %#v, want %#v", got, want)
	}
}

func TestSourceIngestLockKeyIsStableAndSpecific(t *testing.T) {
	base := sourceIngestLockKey(" repo:abra ", " file://README.md ")
	same := sourceIngestLockKey("repo:abra", "file://README.md")
	otherScope := sourceIngestLockKey("repo:other", "file://README.md")
	otherSource := sourceIngestLockKey("repo:abra", "file://CHANGELOG.md")
	if base != same {
		t.Fatalf("trim-equivalent source lock keys differ: %d != %d", base, same)
	}
	if base == otherScope || base == otherSource {
		t.Fatalf("source lock key should differ by scope/source URL: base=%d scope=%d source=%d", base, otherScope, otherSource)
	}
}

func TestLockSourceIngestRequiresActiveTransaction(t *testing.T) {
	err := (&Store{runner: &fakeStoreRunner{}}).LockSourceIngest(context.Background(), "repo:abra", "file://README.md")
	if err == nil || !strings.Contains(err.Error(), "requires active transaction") {
		t.Fatalf("LockSourceIngest error = %v, want active transaction requirement", err)
	}
}

func TestIngestPersistenceMethodsUseActiveTransactionRunner(t *testing.T) {
	ctx := context.Background()
	runner := &fakeStoreRunner{}
	store := &Store{runner: runner, inTx: true}

	if err := store.LockSourceIngest(ctx, "repo:abra", "file://README.md"); err != nil {
		t.Fatalf("LockSourceIngest error = %v", err)
	}
	docID, err := store.UpsertDocument(ctx, DocumentRecord{
		SourceType:      "markdown",
		SourceURL:       "file://README.md",
		Title:           "README.md",
		Scope:           "repo:abra",
		ContentChecksum: "abc",
	})
	if err != nil || docID != "doc-1" {
		t.Fatalf("UpsertDocument = %q, %v", docID, err)
	}
	if err := store.ReplaceChunks(ctx, docID, "repo:abra", []ChunkRecord{{
		Content:             "Abra uses source-backed memory.",
		Embedding:           []float64{1, 0, 0},
		EmbeddingProvider:   "test",
		EmbeddingModel:      "test-model",
		EmbeddingDimensions: 3,
	}}); err != nil {
		t.Fatalf("ReplaceChunks error = %v", err)
	}
	if _, err := store.BeginSourceGraphRefresh(ctx, "repo:abra", "file://README.md", "job-1"); err != nil {
		t.Fatalf("BeginSourceGraphRefresh error = %v", err)
	}
	if _, err := store.BeginSourceClaimRefresh(ctx, "repo:abra", "markdown", "file://README.md", "job-1"); err != nil {
		t.Fatalf("BeginSourceClaimRefresh error = %v", err)
	}
	claimID, err := store.InsertClaim(ctx, ClaimRecord{
		ClaimText:           "Agents should use Abra before code changes.",
		Scope:               "repo:abra",
		SourceURL:           "file://README.md",
		SourceType:          "markdown",
		Embedding:           []float64{1, 0, 0},
		EmbeddingProvider:   "test",
		EmbeddingModel:      "test-model",
		EmbeddingDimensions: 3,
	})
	if err != nil || claimID != "claim-1" {
		t.Fatalf("InsertClaim = %q, %v", claimID, err)
	}
	if err := store.AddEvidence(ctx, EvidenceRecord{ClaimID: claimID, DocumentID: docID, Quote: "Agents should use Abra before code changes.", SourceURL: "file://README.md", SourceType: "markdown"}); err != nil {
		t.Fatalf("AddEvidence error = %v", err)
	}
	entityID, err := store.UpsertEntity(ctx, EntityRecord{Scope: "repo:abra", EntityType: "component", Name: "Abra", SourceURL: "file://README.md", SourceType: "markdown"})
	if err != nil {
		t.Fatalf("UpsertEntity error = %v", err)
	}
	relationID, err := store.UpsertRelation(ctx, RelationRecord{Scope: "repo:abra", RelationType: "depends_on", SourceEntityID: entityID, TargetEntityID: "entity-target", SourceURL: "file://README.md", SourceType: "markdown"})
	if err != nil || relationID != "relation-1" {
		t.Fatalf("UpsertRelation = %q, %v", relationID, err)
	}
	if _, err := store.ListActiveRelationsFromEntity(ctx, "repo:abra", entityID, 50); err != nil {
		t.Fatalf("ListActiveRelationsFromEntity error = %v", err)
	}
	summaryID, err := store.UpsertMemorySummary(ctx, MemorySummaryRecord{Scope: "repo:abra", Level: "source", Key: "README.md", Title: "README", Summary: "Abra docs", SourceCount: 1})
	if err != nil || summaryID != "summary-1" {
		t.Fatalf("UpsertMemorySummary = %q, %v", summaryID, err)
	}
	if err := store.InsertAuditEvent(ctx, "document.ingested", "document", docID, "repo:abra", "file://README.md", map[string]any{"chunks": 1}); err != nil {
		t.Fatalf("InsertAuditEvent error = %v", err)
	}
	if err := store.MarkDocumentIngestComplete(ctx, docID); err != nil {
		t.Fatalf("MarkDocumentIngestComplete error = %v", err)
	}
	if len(runner.execSQL) == 0 || len(runner.queryRowSQL) == 0 {
		t.Fatalf("fake runner was not exercised: exec=%#v queryRow=%#v", runner.execSQL, runner.queryRowSQL)
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
	claims := hybridRecallClaimsSQL("c.status IN ('verified', 'inferred')", 1024, false)
	for _, fragment := range []string{
		"WITH text_matches AS",
		"vector_matches AS",
		"embedding::vector(1024) <=> $5::vector(1024)",
		"c.embedding_dimensions = $6",
		"UNION",
		"ranked_claims AS",
		"LEFT JOIN documents d",
		"LEFT JOIN source_configs sc",
		"source_freshness.refresh_due",
		"COALESCE(tm.text_score, 0) AS text_score",
		"COALESCE(vm.vector_score, 0) AS vector_score",
		"COALESCE(tm.text_score, 0)",
		"COALESCE(vm.vector_score, 0) * 0.45",
		"CASE freshness",
		"rank_score DESC",
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

	sourceDue := healthy
	sourceDue.Sources.Due = 2
	score, status, reasons = scoreMemoryHealth(sourceDue)
	if status != "needs_review" {
		t.Fatalf("source due status = %s score=%d reasons=%v, want needs_review", status, score, reasons)
	}
	if !strings.Contains(strings.Join(reasons, "\n"), "source refresh is due") {
		t.Fatalf("source due reasons = %v, want due reason", reasons)
	}

	sourceOverdue := healthy
	sourceOverdue.Sources.Overdue = 1
	score, status, reasons = scoreMemoryHealth(sourceOverdue)
	if status != "critical" {
		t.Fatalf("source overdue status = %s score=%d reasons=%v, want critical", status, score, reasons)
	}
	if !strings.Contains(strings.Join(reasons, "\n"), "source refresh is overdue") {
		t.Fatalf("source overdue reasons = %v, want overdue reason", reasons)
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

	refreshDue := healthy
	refreshDue.Sources.Due = 3
	assessment = assessMemoryHealth(refreshDue)
	got, ok := findMemoryHealthSignal(assessment.Signals, "source_refresh_due")
	if !ok {
		t.Fatalf("signals missing source_refresh_due: %#v", assessment.Signals)
	}
	if got.Category != "sources" || got.Severity != "warning" || got.Count != 3 || got.Action == "" {
		t.Fatalf("source_refresh_due signal = %#v, want sources warning count=3 action", got)
	}

	refreshOverdue := healthy
	refreshOverdue.Sources.Overdue = 2
	assessment = assessMemoryHealth(refreshOverdue)
	got, ok = findMemoryHealthSignal(assessment.Signals, "source_refresh_overdue")
	if !ok {
		t.Fatalf("signals missing source_refresh_overdue: %#v", assessment.Signals)
	}
	if got.Category != "sources" || got.Severity != "critical" || got.Count != 2 || got.Action == "" {
		t.Fatalf("source_refresh_overdue signal = %#v, want sources critical count=2 action", got)
	}
}

func TestMemoryHealthSourceDetailJSONFriendly(t *testing.T) {
	lastSuccess := "2026-06-21 10:00:00+00"
	result := MemoryHealthResult{
		Status:    "critical",
		Score:     42,
		CheckedAt: "2026-06-21T10:05:00Z",
		SourceHealth: []MemoryHealthSourceDetail{{
			ID:              "source-1",
			Name:            "Repo",
			Type:            "local_repo",
			Status:          "error",
			LastSuccessAt:   &lastSuccess,
			LastError:       "permission denied",
			Due:             true,
			Overdue:         true,
			FailedJobs:      2,
			RetryJobs:       1,
			LatestJobStatus: "failed",
			RemediationHint: "inspect failed ingestion jobs, fix the source error, then retry",
		}},
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal memory health: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode memory health json: %v", err)
	}
	sourceHealth, ok := decoded["source_health"].([]any)
	if !ok || len(sourceHealth) != 1 {
		t.Fatalf("source_health = %#v, want one detail", decoded["source_health"])
	}
	detail, ok := sourceHealth[0].(map[string]any)
	if !ok {
		t.Fatalf("source_health detail = %#v, want object", sourceHealth[0])
	}
	for _, key := range []string{"id", "name", "type", "status", "last_success_at", "last_error", "due", "overdue", "retry_jobs", "failed_jobs", "running_jobs", "queued_jobs", "latest_job_status", "remediation_hint"} {
		if _, ok := detail[key]; !ok {
			t.Fatalf("source_health detail missing %q: %#v", key, detail)
		}
	}
	if _, ok := detail["last_error_at"]; ok {
		t.Fatalf("empty last_error_at should be omitted: %#v", detail)
	}
}

func TestMemoryHealthSourceRemediationHintPrioritizesAction(t *testing.T) {
	tests := []struct {
		name   string
		source MemoryHealthSourceDetail
		want   string
	}{
		{
			name:   "source error",
			source: MemoryHealthSourceDetail{Status: "error", FailedJobs: 3},
			want:   "fix source configuration or credentials, then retry ingestion",
		},
		{
			name:   "failed jobs",
			source: MemoryHealthSourceDetail{Status: "active", FailedJobs: 1, RetryJobs: 2},
			want:   "inspect failed ingestion jobs, fix the source error, then retry",
		},
		{
			name:   "overdue",
			source: MemoryHealthSourceDetail{Status: "active", Overdue: true, Due: true},
			want:   "refresh this source before relying on affected memory",
		},
		{
			name:   "queued",
			source: MemoryHealthSourceDetail{Status: "active", QueuedJobs: 4},
			want:   "wait for ingestion workers or increase worker capacity if the queue is stuck",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := memoryHealthSourceRemediationHint(tt.source); got != tt.want {
				t.Fatalf("remediation hint = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMemoryHealthSourceDetailsSQLIncludesBoundedDiagnostics(t *testing.T) {
	query := compactSQL(memoryHealthSourceDetailsSQL())
	for _, fragment := range []string{
		"WITH source_intervals AS",
		"source_readiness AS",
		"job_rollup AS",
		"latest_job AS",
		"sr.last_success_at::text",
		"sr.last_error_at::text",
		"COALESCE(sr.last_error, '')",
		"COALESCE(j.retry_jobs, 0)::int",
		"COALESCE(j.failed_jobs, 0)::int",
		"COALESCE(j.running_jobs, 0)::int",
		"COALESCE(j.queued_jobs, 0)::int",
		"COALESCE(lj.status, '')",
		"WHERE sr.status = 'error'",
		"OR sr.refresh_due",
		"OR sr.refresh_overdue",
		"LIMIT $2",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("source health detail SQL missing %q:\n%s", fragment, query)
		}
	}
}

func TestMemoryHealthSourceCountsSQLUsesSharedReadiness(t *testing.T) {
	query := compactSQL(memoryHealthSourceCountsSQL())
	for _, fragment := range []string{
		"WITH source_intervals AS",
		"source_readiness AS",
		"COUNT(*) FILTER (WHERE refresh_due)::int",
		"COUNT(*) FILTER (WHERE refresh_overdue)::int",
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("source health count SQL missing %q:\n%s", fragment, query)
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
