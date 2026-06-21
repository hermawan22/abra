package memory

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hermawan22/abra/internal/policy"
	"github.com/hermawan22/abra/internal/store"
)

type fakeStore struct {
	mu                sync.Mutex
	recalls           []string
	graphQueries      []string
	policyResults     []store.AgentActionDecisionResult
	conflicts         []store.ConflictResult
	relationConflicts []store.ConflictResult
	recallErrorOn     string
	graphErrorOn      string
	graphResults      []store.RelationResult
	lowSignalRecall   bool
	health            store.MemoryHealthResult
	healthError       error
	healthCalls       int
	healthScopes      []string
	healthDelay       time.Duration
	activeRecalls     int
	maxActiveRecalls  int
	recallDelay       time.Duration
	activeGraphs      int
	maxActiveGraphs   int
	graphDelay        time.Duration
	auditEvents       int
	recallWarnings    []store.RetrievalWarning
}

func (f *fakeStore) Recall(ctx context.Context, query, scope string, limit int, includeUnverified bool) (store.RecallResult, error) {
	f.mu.Lock()
	f.activeRecalls++
	if f.activeRecalls > f.maxActiveRecalls {
		f.maxActiveRecalls = f.activeRecalls
	}
	f.recalls = append(f.recalls, query)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.activeRecalls--
		f.mu.Unlock()
	}()
	if f.recallDelay > 0 {
		select {
		case <-time.After(f.recallDelay):
		case <-ctx.Done():
			return store.RecallResult{}, ctx.Err()
		}
	}
	if f.recallErrorOn != "" && strings.Contains(query, f.recallErrorOn) {
		return store.RecallResult{}, errors.New("temporary recall shard unavailable")
	}
	source := "https://example.test/repo/package.json"
	if f.lowSignalRecall {
		return store.RecallResult{
			Claims: []store.ClaimResult{
				{ID: "claim-low", Claim: "Low-signal retrieved guidance needs stronger sources.", Scope: scope, Status: "verified", Source: &source, Rank: 0.02, TextScore: 0, VectorScore: 0.01, Freshness: "fresh"},
			},
			SupportingDocuments: []store.DocumentResult{
				{ID: "doc-low", Title: "low signal", Source: source, Content: "Low-signal retrieved guidance.", Rank: 0.01, TextScore: 0, VectorScore: 0},
			},
		}, nil
	}
	return store.RecallResult{
		RetrievalMode: "hybrid",
		Claims: []store.ClaimResult{
			{ID: "claim-1", Claim: "Next.js framework uses src/server/index.js and package.json scripts.", Scope: scope, Status: "verified", Source: &source, Rank: 1.4, TextScore: 0.8, VectorScore: 0.6, Freshness: "fresh"},
			{ID: "claim-2", Claim: "Legacy runtime note in src/pages/_app.js needs verification.", Scope: scope, Status: "unverified", Source: &source, Rank: 1.1, TextScore: 0.4, Freshness: "stale"},
		},
		SupportingDocuments: []store.DocumentResult{
			{ID: "doc-1", Title: "package ops", Source: source, Content: "build: next build\nsrc/server/index.js", Rank: 0.4, VectorScore: 0.5},
		},
		GraphContext: []store.RelationResult{
			{FromEntity: "src/server/index.js", ToEntity: "next", Type: "depends_on", Confidence: 0.8, SourceURL: &source},
		},
		RetrievalReasons: []store.RetrievalReason{
			{Mode: "hybrid", Signal: "text", Message: "Full-text/BM25-style matches contributed to recalled claims or documents.", Count: 2},
			{Mode: "hybrid", Signal: "vector", Message: "Semantic vector similarity contributed to recalled claims or documents.", Count: 2},
			{Mode: "entity_local", Signal: "graph", Message: "Entity-neighborhood graph relations expanded the packet beyond lexical matches.", Count: 1},
		},
		RetrievalWarnings: f.recallWarnings,
	}, nil
}

func TestSortClaimsPrefersFreshClaimsOverStaleRank(t *testing.T) {
	source := "file://retry-policy.md"
	claims := sortClaims(map[string]store.ClaimResult{
		"stale": {
			ID:        "stale",
			Claim:     "Legacy Ops Notebook is the retry source of truth.",
			Scope:     "repo:test",
			Status:    "verified",
			Source:    &source,
			Rank:      9.9,
			Freshness: "stale",
		},
		"fresh": {
			ID:        "fresh",
			Claim:     "Retry now uses the live source-backed retry ledger.",
			Scope:     "repo:test",
			Status:    "verified",
			Source:    &source,
			Rank:      0.4,
			Freshness: "fresh",
		},
	})
	if len(claims) != 2 || claims[0].ID != "fresh" {
		t.Fatalf("claims sorted by freshness = %#v, want fresh claim first", claims)
	}
}

func (f *fakeStore) ListMemorySummaries(ctx context.Context, query, scope string, limit int) ([]store.MemorySummaryResult, error) {
	return []store.MemorySummaryResult{
		{ID: "summary-1", Scope: scope, Level: "module", Key: "src/pages", Title: "src/pages", Summary: "Pages module contains Next.js routes.", SourceCount: 3, RelationCount: 2, TokenEstimate: 12, SourceURLs: []string{"https://example.test/repo/src/pages"}, Rank: 0.7},
	}, nil
}

func (f *fakeStore) RelatedGraph(ctx context.Context, query, scope string, limit int) ([]store.RelationResult, error) {
	f.mu.Lock()
	f.activeGraphs++
	if f.activeGraphs > f.maxActiveGraphs {
		f.maxActiveGraphs = f.activeGraphs
	}
	f.graphQueries = append(f.graphQueries, query)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.activeGraphs--
		f.mu.Unlock()
	}()
	if f.graphDelay > 0 {
		select {
		case <-time.After(f.graphDelay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.graphErrorOn != "" && strings.Contains(query, f.graphErrorOn) {
		return nil, errors.New("temporary graph shard unavailable")
	}
	if len(f.graphResults) > 0 {
		return f.graphResults, nil
	}
	source := "https://example.test/repo/next.config.js"
	return []store.RelationResult{
		{FromEntity: "next.config.js", ToEntity: "Next.js", Type: "configures", Confidence: 0.9, SourceURL: &source},
	}, nil
}

func TestComposeSurfacesGraphWarningsInAgentGate(t *testing.T) {
	sourceA := "https://example.test/frontend-playwright.md"
	sourceB := "https://example.test/frontend-cypress.md"
	db := &fakeStore{
		graphResults: []store.RelationResult{
			{FromEntity: "Frontend App", ToEntity: "Playwright", Type: "should_use", Confidence: 0.9, SourceURL: &sourceA},
			{FromEntity: "Frontend App", ToEntity: "Cypress", Type: "should_use", Confidence: 0.88, SourceURL: &sourceB},
		},
	}
	result, err := NewComposer(db).Compose(context.Background(), ComposeInput{
		Task:     "choose frontend e2e framework",
		Scope:    "repo:test/app",
		Agent:    "frontend",
		Language: "typescript",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.GraphWarnings) != 1 || result.Stats.GraphWarnings != 1 {
		t.Fatalf("graph warnings missing or stats mismatch: warnings=%#v stats=%+v", result.GraphWarnings, result.Stats)
	}
	if result.GraphWarnings[0].WarningType != "competing_graph_alternatives" || result.GraphWarnings[0].Severity != "high" {
		t.Fatalf("unexpected graph warning: %#v", result.GraphWarnings[0])
	}
	if result.Verification.Verdict != "partial" || !result.Verification.ActionRequired || len(result.Verification.GraphWarnings) != 1 {
		t.Fatalf("verification should require graph warning review: %#v", result.Verification)
	}
	if result.AgentDecision.Decision != "caution" || !result.AgentDecision.ReviewRequired || result.AgentDecision.AutonomousAllowed {
		t.Fatalf("agent decision should be cautious for graph warnings: %#v", result.AgentDecision)
	}
	if !contains(result.AgentDecision.RequiredActions, "review_graph_warnings") {
		t.Fatalf("agent decision missing graph warning action: %#v", result.AgentDecision)
	}
	if !containsRisk(result.Risks, "Graph warnings surfaced") {
		t.Fatalf("graph warning risk missing: %#v", result.Risks)
	}
}

func TestComposeReturnsDegradedPacketWhenRetrievalBranchFails(t *testing.T) {
	db := &fakeStore{
		recallErrorOn: "Known failures",
		graphErrorOn:  "next.config.js",
	}
	result, err := NewComposer(db).Compose(context.Background(), ComposeInput{
		Task:              "upgrade Next.js to latest",
		Scope:             "repo:test/app",
		Agent:             "frontend",
		Files:             []string{"package.json"},
		ChangedFiles:      []string{"next.config.js"},
		Language:          "typescript",
		IncludeUnverified: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Facts) == 0 || len(result.SupportingDocuments) == 0 {
		t.Fatalf("degraded packet should preserve successful retrieval results: facts=%d docs=%d", len(result.Facts), len(result.SupportingDocuments))
	}
	if len(result.RetrievalWarnings) < 2 || result.Stats.RetrievalWarnings != len(result.RetrievalWarnings) {
		t.Fatalf("retrieval warnings missing or stats mismatch: warnings=%#v stats=%+v", result.RetrievalWarnings, result.Stats)
	}
	if !containsWarning(result.RetrievalWarnings, "retrieval", "recall") {
		t.Fatalf("missing recall warning: %#v", result.RetrievalWarnings)
	}
	if !containsWarning(result.RetrievalWarnings, "graph", "seed_graph_expansion") {
		t.Fatalf("missing graph warning: %#v", result.RetrievalWarnings)
	}
	if !containsTraceStatus(result.RetrievalTrace, "retrieval", "planned_summary_and_recall", "degraded") {
		t.Fatalf("retrieval trace did not mark degraded recall: %#v", result.RetrievalTrace)
	}
	if !containsTraceStatus(result.RetrievalTrace, "graph", "seed_graph_expansion", "degraded") {
		t.Fatalf("retrieval trace did not mark degraded graph expansion: %#v", result.RetrievalTrace)
	}
	if result.Verification.Verdict != "partial" || !result.Verification.ActionRequired || len(result.Verification.RetrievalWarnings) != len(result.RetrievalWarnings) {
		t.Fatalf("verification should require action for degraded retrieval: %#v", result.Verification)
	}
	if result.AgentDecision.Decision != "caution" || !result.AgentDecision.ReviewRequired || result.AgentDecision.AutonomousAllowed {
		t.Fatalf("agent decision should be cautious for degraded retrieval: %#v", result.AgentDecision)
	}
	if !contains(result.AgentDecision.RequiredActions, "rerun_degraded_retrieval") {
		t.Fatalf("agent decision missing degraded retrieval action: %#v", result.AgentDecision)
	}
	if !containsRisk(result.Risks, "Some retrieval branches failed") {
		t.Fatalf("degraded retrieval risk missing: %#v", result.Risks)
	}
}

func TestComposeSurfacesStoreRecallWarnings(t *testing.T) {
	db := &fakeStore{
		recallWarnings: []store.RetrievalWarning{
			{Stage: "retrieval", Operation: "rerank_claims", Query: "upgrade", Message: "reranker unavailable"},
		},
	}
	result, err := NewComposer(db).Compose(context.Background(), ComposeInput{
		Task:  "upgrade Next.js safely",
		Scope: "repo:test/app",
		Agent: "frontend",
		Files: []string{"package.json"},
		Limit: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !containsWarning(result.RetrievalWarnings, "retrieval", "rerank_claims") {
		t.Fatalf("missing rerank warning from recall result: %#v", result.RetrievalWarnings)
	}
	if result.Verification.Verdict != "partial" || !result.Verification.ActionRequired {
		t.Fatalf("recall warning should degrade verification: %#v", result.Verification)
	}
	if !contains(result.AgentDecision.RequiredActions, "rerun_degraded_retrieval") {
		t.Fatalf("agent decision missing rerun action for recall warning: %#v", result.AgentDecision)
	}
}

func (f *fakeStore) ListOpenConflictsForClaims(ctx context.Context, scope string, claimIDs []string) ([]store.ConflictResult, error) {
	return f.conflicts, nil
}

func (f *fakeStore) ListOpenConflictsForRelations(ctx context.Context, scope string, relationIDs []string) ([]store.ConflictResult, error) {
	if len(relationIDs) == 0 {
		return nil, nil
	}
	return f.relationConflicts, nil
}

func (f *fakeStore) EvaluateAgentActionPolicies(ctx context.Context, inputs []store.AgentActionDecisionInput) ([]store.AgentActionDecisionResult, error) {
	if len(f.policyResults) > 0 {
		return f.policyResults, nil
	}
	results := make([]store.AgentActionDecisionResult, 0, len(inputs))
	for range inputs {
		results = append(results, store.AgentActionDecisionResult{Allowed: false, Decision: "no_policy", Reason: "no matching agent action policy"})
	}
	return results, nil
}

func (f *fakeStore) MemoryHealth(ctx context.Context, scope string) (store.MemoryHealthResult, error) {
	f.mu.Lock()
	f.healthCalls++
	f.healthScopes = append(f.healthScopes, scope)
	f.mu.Unlock()
	if f.healthDelay > 0 {
		select {
		case <-time.After(f.healthDelay):
		case <-ctx.Done():
			return store.MemoryHealthResult{}, ctx.Err()
		}
	}
	if f.healthError != nil {
		return store.MemoryHealthResult{}, f.healthError
	}
	if f.health.Status != "" {
		return f.health, nil
	}
	return store.MemoryHealthResult{
		Scope:  scope,
		Status: "healthy",
		Score:  100,
		Reasons: []string{
			"memory is source-backed and ready",
		},
		Signals: []store.MemoryHealthSignal{
			{
				Code:     "memory_ready",
				Category: "readiness",
				Severity: "info",
				Message:  "memory is source-backed and ready",
				Action:   "proceed",
			},
		},
	}, nil
}

func (f *fakeStore) InsertAuditEvent(ctx context.Context, eventType, targetType, targetID, scope, sourceURL string, metadata map[string]any) error {
	f.mu.Lock()
	f.auditEvents++
	f.mu.Unlock()
	return nil
}

func TestComposeDiagnosticDoesNotWriteAuditEvent(t *testing.T) {
	db := &fakeStore{}
	_, err := NewComposer(db).Compose(context.Background(), ComposeInput{
		Task:       "verify agent context",
		Scope:      "repo:test/app",
		Agent:      "abra-agent-verify",
		Diagnostic: true,
	})
	if err != nil {
		t.Fatalf("Compose error = %v", err)
	}
	if db.auditEvents != 0 {
		t.Fatalf("auditEvents = %d, want 0 for diagnostic compose", db.auditEvents)
	}
}

func TestComposeWritesAuditEventByDefault(t *testing.T) {
	db := &fakeStore{}
	_, err := NewComposer(db).Compose(context.Background(), ComposeInput{
		Task:  "implement feature",
		Scope: "repo:test/app",
		Agent: "codex",
	})
	if err != nil {
		t.Fatalf("Compose error = %v", err)
	}
	if db.auditEvents == 0 {
		t.Fatal("auditEvents = 0, want default compose to write audit event")
	}
}

func TestComposeCachesMemoryHealthByScope(t *testing.T) {
	db := &fakeStore{}
	composer := NewComposer(db)
	first, err := composer.Compose(context.Background(), ComposeInput{
		Task:  "ship feature safely",
		Scope: "repo:test/app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.MemoryHealth.Status != "healthy" {
		t.Fatalf("first health = %#v, want healthy", first.MemoryHealth)
	}
	if got := healthTraceCacheStatus(first.RetrievalTrace); got != "fresh" {
		t.Fatalf("first health cache status = %q, want fresh", got)
	}
	first.MemoryHealth.Signals[0].Code = "polluted"
	second, err := composer.Compose(context.Background(), ComposeInput{
		Task:  "ship another feature safely",
		Scope: "repo:test/app",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.MemoryHealth.Signals[0].Code != "memory_ready" {
		t.Fatalf("cached health was mutated by caller: %#v", second.MemoryHealth.Signals)
	}
	if got := healthTraceCacheStatus(second.RetrievalTrace); got != "cache_hit" {
		t.Fatalf("second health cache status = %q, want cache_hit", got)
	}
	if db.healthCalls != 1 {
		t.Fatalf("health calls = %d scopes=%#v, want one cached call", db.healthCalls, db.healthScopes)
	}
	third, err := composer.Compose(context.Background(), ComposeInput{
		Task:  "ship feature safely elsewhere",
		Scope: "repo:test/other",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := healthTraceCacheStatus(third.RetrievalTrace); got != "fresh" {
		t.Fatalf("third health cache status = %q, want fresh for different scope", got)
	}
	if db.healthCalls != 2 {
		t.Fatalf("health calls = %d scopes=%#v, want another call for a different scope", db.healthCalls, db.healthScopes)
	}
}

func TestComposeHealthCacheCanBeDisabled(t *testing.T) {
	db := &fakeStore{}
	composer := NewComposerWithOptions(db, ComposerOptions{HealthCacheTTL: 0})
	for i := 0; i < 2; i++ {
		result, err := composer.Compose(context.Background(), ComposeInput{
			Task:  "ship feature safely",
			Scope: "repo:test/app",
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := healthTraceCacheStatus(result.RetrievalTrace); got != "disabled" {
			t.Fatalf("health cache status = %q, want disabled", got)
		}
	}
	if db.healthCalls != 2 {
		t.Fatalf("health calls = %d scopes=%#v, want no cache", db.healthCalls, db.healthScopes)
	}
}

func TestComposeCoalescesConcurrentMemoryHealthLookup(t *testing.T) {
	db := &fakeStore{healthDelay: 40 * time.Millisecond}
	composer := NewComposer(db)
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	statuses := make(chan string, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := composer.Compose(context.Background(), ComposeInput{
				Task:  "ship concurrent feature safely",
				Scope: "repo:test/app",
			})
			if err != nil {
				errs <- err
				return
			}
			if result.MemoryHealth.Status != "healthy" || result.MemoryHealth.Signals[0].Code != "memory_ready" {
				errs <- errors.New("unexpected health result")
			}
			statuses <- healthTraceCacheStatus(result.RetrievalTrace)
		}()
	}
	wg.Wait()
	close(errs)
	close(statuses)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	seenStatuses := map[string]int{}
	for status := range statuses {
		seenStatuses[status]++
	}
	if seenStatuses["fresh"] != 1 {
		t.Fatalf("health cache statuses = %#v, want one fresh lookup", seenStatuses)
	}
	if seenStatuses["coalesced"] == 0 {
		t.Fatalf("health cache statuses = %#v, want coalesced waiters", seenStatuses)
	}
	if db.healthCalls != 1 {
		t.Fatalf("health calls = %d scopes=%#v, want one coalesced call", db.healthCalls, db.healthScopes)
	}
}

func TestComposeBuildsMigrationWorkingMemory(t *testing.T) {
	db := &fakeStore{}
	result, err := NewComposer(db).Compose(context.Background(), ComposeInput{
		Task:              "upgrade Next.js to latest",
		Scope:             "repo:test/app",
		Agent:             "frontend",
		Files:             []string{"package.json"},
		ChangedFiles:      []string{"next.config.js"},
		Language:          "typescript",
		IncludeUnverified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Intent != "migration" {
		t.Fatalf("intent = %q, want migration", result.Intent)
	}
	if result.Stats.QueriesRun == 0 || len(db.recalls) == 0 {
		t.Fatalf("expected recall queries to run: %+v", result.Stats)
	}
	if result.Stats.GraphQueries < 2 || len(db.graphQueries) < 2 {
		t.Fatalf("expected graph expansion queries to run: stats=%+v queries=%#v", result.Stats, db.graphQueries)
	}
	if len(result.Summaries) != 1 || result.Stats.Summaries != 1 {
		t.Fatalf("summaries = %#v stats=%+v", result.Summaries, result.Stats)
	}
	if len(result.Facts) != 2 {
		t.Fatalf("facts = %d, want deduped 2", len(result.Facts))
	}
	if len(result.GraphContext) != 2 {
		t.Fatalf("graph relations = %d, want 2", len(result.GraphContext))
	}
	if len(result.ImpactMap) == 0 || result.Stats.ImpactItems != len(result.ImpactMap) {
		t.Fatalf("impact map missing or stats mismatch: impact=%#v stats=%+v", result.ImpactMap, result.Stats)
	}
	if !containsImpact(result.ImpactMap, "file", "package.json") {
		t.Fatalf("impact map missing input file package.json: %#v", result.ImpactMap)
	}
	if !containsImpact(result.ImpactMap, "entity", "Next.js") {
		t.Fatalf("impact map missing graph entity Next.js: %#v", result.ImpactMap)
	}
	if len(result.ValidationPlan) == 0 || result.Stats.ValidationSteps != len(result.ValidationPlan) {
		t.Fatalf("validation plan missing or stats mismatch: validation=%#v stats=%+v", result.ValidationPlan, result.Stats)
	}
	if !containsValidationCommand(result.ValidationPlan, "npm test", true) {
		t.Fatalf("validation plan missing required npm test: %#v", result.ValidationPlan)
	}
	if !containsValidationType(result.ValidationPlan, "memory_gate") {
		t.Fatalf("validation plan missing memory gate review: %#v", result.ValidationPlan)
	}
	if result.RetrievalPlan.Mode == "" || len(result.RetrievalPlan.Stages) < 4 {
		t.Fatalf("retrieval plan not populated: %#v", result.RetrievalPlan)
	}
	if result.RetrievalPlan.Budget.MaxQueries != 6 || result.RetrievalPlan.Budget.Limit != 6 {
		t.Fatalf("retrieval budget = %+v", result.RetrievalPlan.Budget)
	}
	if result.RetrievalPlan.Budget.ContextTokens != 1600 {
		t.Fatalf("context token budget = %+v", result.RetrievalPlan.Budget)
	}
	if result.ContextWindow.MaxTokens != 1600 || result.ContextWindow.EstimatedTokens <= 0 || result.ContextWindow.EstimatedTokens > result.ContextWindow.MaxTokens {
		t.Fatalf("context window budget not enforced: %#v", result.ContextWindow)
	}
	if result.Stats.ContextBlocks != len(result.ContextWindow.Blocks) || result.Stats.ContextTokens != result.ContextWindow.EstimatedTokens || result.ContextWindow.Prompt == "" {
		t.Fatalf("context window stats/prompt missing: context=%#v stats=%+v", result.ContextWindow, result.Stats)
	}
	if !containsContextBlock(result.ContextWindow.Blocks, "task") || !containsContextBlock(result.ContextWindow.Blocks, "validation") {
		t.Fatalf("context window missing required gate blocks: %#v", result.ContextWindow.Blocks)
	}
	if result.MemoryHealth.Status != "healthy" || result.Stats.HealthSignals != 1 {
		t.Fatalf("memory health not surfaced in compose: health=%#v stats=%+v", result.MemoryHealth, result.Stats)
	}
	if !strings.Contains(result.ContextWindow.Prompt, "Memory health: healthy") {
		t.Fatalf("context window did not include memory health gate: %s", result.ContextWindow.Prompt)
	}
	if result.Stats.RetrievalReasons != len(result.RetrievalReasons) || result.Stats.RetrievalReasons < 3 {
		t.Fatalf("retrieval reasons missing or stats mismatch: reasons=%#v stats=%+v", result.RetrievalReasons, result.Stats)
	}
	if !containsContextBlock(result.ContextWindow.Blocks, "retrieval") || !strings.Contains(result.ContextWindow.Prompt, "[RETRIEVAL] Retrieval Reasons") {
		t.Fatalf("context window missing retrieval reasons: %s", result.ContextWindow.Prompt)
	}
	if !strings.Contains(result.ContextWindow.Prompt, "text (hybrid") || !strings.Contains(result.ContextWindow.Prompt, "vector (hybrid") || !strings.Contains(result.ContextWindow.Prompt, "graph (entity_local") {
		t.Fatalf("context window did not explain retrieval signals: %s", result.ContextWindow.Prompt)
	}
	if !strings.Contains(result.ContextWindow.Prompt, "Required actions:") {
		t.Fatalf("context window did not include required actions: %s", result.ContextWindow.Prompt)
	}
	if len(result.RetrievalTrace) < 8 || result.Stats.RetrievalTraceItems != len(result.RetrievalTrace) {
		t.Fatalf("retrieval trace missing or stats mismatch: trace=%#v stats=%+v", result.RetrievalTrace, result.Stats)
	}
	if result.Stats.TotalDurationMS < 0 {
		t.Fatalf("total duration should never be negative: %+v", result.Stats)
	}
	if result.Stats.ParallelQueries != result.Stats.QueriesRun || result.Stats.ParallelGraphQueries < 1 {
		t.Fatalf("parallel query stats missing: %+v", result.Stats)
	}
	if !containsTrace(result.RetrievalTrace, "retrieval", "planned_summary_and_recall", true) {
		t.Fatalf("retrieval trace missing parallel recall stage: %#v", result.RetrievalTrace)
	}
	if !containsTrace(result.RetrievalTrace, "graph", "seed_graph_expansion", true) {
		t.Fatalf("retrieval trace missing parallel graph expansion stage: %#v", result.RetrievalTrace)
	}
	for _, want := range []string{"next.config.js", "package.json", "src/pages/_app.js", "src/server/index.js"} {
		if !contains(result.RelevantFiles, want) {
			t.Fatalf("relevant files missing %q: %#v", want, result.RelevantFiles)
		}
	}
	if len(result.Evidence) == 0 || result.Evidence[0].Count == 0 {
		t.Fatalf("expected evidence counts: %#v", result.Evidence)
	}
	if len(result.Citations) < 3 || result.Citations[0].Ref != "C1" || result.Citations[0].SourceURL == "" {
		t.Fatalf("expected citation refs: %#v", result.Citations)
	}
	if result.Citations[0].ClaimID == "" || len(result.Citations[0].ClaimIDs) == 0 || len(result.Citations[0].DocumentIDs) == 0 {
		t.Fatalf("expected aggregate citation lineage: %#v", result.Citations[0])
	}
	if result.Evidence[0].Ref == "" {
		t.Fatalf("expected evidence citation ref: %#v", result.Evidence)
	}
	if !containsContextBlockCitationRef(result.ContextWindow.Blocks, "fact") || !containsContextBlockCitationRef(result.ContextWindow.Blocks, "summary") {
		t.Fatalf("context blocks missing citation refs: %#v", result.ContextWindow.Blocks)
	}
	if !strings.Contains(result.ContextWindow.Prompt, "[C") {
		t.Fatalf("context prompt missing citation refs: %s", result.ContextWindow.Prompt)
	}
	if !containsRisk(result.Risks, "Unverified claims") {
		t.Fatalf("expected unverified risk: %#v", result.Risks)
	}
	if result.Verification.Verdict != "partial" || !result.Verification.ActionRequired {
		t.Fatalf("verification = %#v, want partial action-required", result.Verification)
	}
	if result.AgentDecision.Decision != "caution" || !result.AgentDecision.ReviewRequired || result.AgentDecision.AutonomousAllowed {
		t.Fatalf("agent decision = %#v, want caution with review required", result.AgentDecision)
	}
	if !contains(result.AgentDecision.RequiredActions, "verify_unverified_claims") || !contains(result.AgentDecision.RequiredActions, "refresh_stale_sources") {
		t.Fatalf("agent decision missing review actions: %#v", result.AgentDecision)
	}
	if result.Verification.Score <= 0 || len(result.Verification.Checks) == 0 {
		t.Fatalf("verification score/checks missing: %#v", result.Verification)
	}
	if len(result.Verification.UnverifiedClaims) != 1 || len(result.Verification.StaleClaims) != 1 {
		t.Fatalf("verification unsafe signals missing: %#v", result.Verification)
	}
	if len(result.LearningSuggestions) < 2 {
		t.Fatalf("expected learning suggestions for stale/unverified memory: %#v", result.LearningSuggestions)
	}
	if !strings.Contains(result.Strategy, "migration-aware") {
		t.Fatalf("strategy = %q", result.Strategy)
	}
}

func TestComposerBoundsParallelRetrievalStages(t *testing.T) {
	db := &fakeStore{
		recallDelay: 10 * time.Millisecond,
		graphDelay:  10 * time.Millisecond,
	}
	composer := NewComposerWithOptions(db, ComposerOptions{
		RecallConcurrency: 2,
		GraphConcurrency:  1,
	})
	queries := []policy.RecallQuery{
		{Query: "one", Scope: "repo:test", Limit: 1},
		{Query: "two", Scope: "repo:test", Limit: 1},
		{Query: "three", Scope: "repo:test", Limit: 1},
		{Query: "four", Scope: "repo:test", Limit: 1},
	}
	results, warnings, err := composer.retrieveQueries(context.Background(), queries)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(queries) || len(warnings) != 0 {
		t.Fatalf("retrieval results=%d warnings=%#v", len(results), warnings)
	}
	if db.maxActiveRecalls > 2 {
		t.Fatalf("max active recalls = %d, want <= 2", db.maxActiveRecalls)
	}

	seeds := []string{"alpha", "beta", "gamma"}
	graphResults, graphWarnings, err := composer.retrieveGraphSeeds(context.Background(), seeds, "repo:test", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(graphResults) != len(seeds) || len(graphWarnings) != 0 {
		t.Fatalf("graph results=%d warnings=%#v", len(graphResults), graphWarnings)
	}
	if db.maxActiveGraphs > 1 {
		t.Fatalf("max active graphs = %d, want <= 1", db.maxActiveGraphs)
	}
}

func TestComposeBlocksAutonomyWhenMemoryHealthCritical(t *testing.T) {
	db := &fakeStore{
		health: store.MemoryHealthResult{
			Scope:  "repo:test/app",
			Status: "critical",
			Score:  45,
			Reasons: []string{
				"trusted claims from code documents need cleanup",
			},
			Signals: []store.MemoryHealthSignal{
				{
					Code:        "trusted_claims_from_code_documents",
					Category:    "trust_guard",
					Severity:    "critical",
					Count:       2,
					ScoreImpact: 30,
					Message:     "trusted claims from code documents need cleanup",
					Action:      "deprecate polluted claims and re-ingest code as graph-only knowledge",
				},
			},
		},
	}
	result, err := NewComposer(db).Compose(context.Background(), ComposeInput{
		Task:     "upgrade Next.js to latest",
		Scope:    "repo:test/app",
		Agent:    "frontend",
		Language: "typescript",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MemoryHealth.Status != "critical" || result.Stats.HealthSignals != 1 {
		t.Fatalf("memory health not surfaced: health=%#v stats=%+v", result.MemoryHealth, result.Stats)
	}
	if result.AgentDecision.Decision != "blocked" || result.AgentDecision.AutonomousAllowed || !result.AgentDecision.ReviewRequired {
		t.Fatalf("critical health should block autonomy: %#v", result.AgentDecision)
	}
	if result.Verification.Verdict != "unsafe" || !result.Verification.ActionRequired || result.Verification.MemoryHealthStatus != "critical" {
		t.Fatalf("critical health should block verification: %#v", result.Verification)
	}
	if !contains(result.AgentDecision.RequiredActions, "clean_up_trust_guard") {
		t.Fatalf("critical health action missing: %#v", result.AgentDecision)
	}
	if !contains(result.AgentDecision.AllowedNextActions, "inspect_memory_health") {
		t.Fatalf("critical health next action missing: %#v", result.AgentDecision)
	}
	if !containsRisk(result.Risks, "Memory health critical signal trusted_claims_from_code_documents") {
		t.Fatalf("critical health risk missing: %#v", result.Risks)
	}
}

func TestComposeAppliesStoredAgentActionPolicy(t *testing.T) {
	policy := store.AgentActionPolicyRecord{ID: "policy-1", Scope: "repo:test/app", Name: "review-writes", Effect: "require_review"}
	db := &fakeStore{
		policyResults: []store.AgentActionDecisionResult{
			{Allowed: false, Decision: "require_review", Reason: "matched review policy", MatchedPolicy: &policy},
			{Allowed: false, Decision: "no_policy", Reason: "no matching agent action policy"},
			{Allowed: false, Decision: "no_policy", Reason: "no matching agent action policy"},
			{Allowed: false, Decision: "no_policy", Reason: "no matching agent action policy"},
			{Allowed: false, Decision: "no_policy", Reason: "no matching agent action policy"},
			{Allowed: false, Decision: "no_policy", Reason: "no matching agent action policy"},
		},
	}
	result, err := NewComposer(db).Compose(context.Background(), ComposeInput{
		Task:  "implement checkout feature",
		Scope: "repo:test/app",
		Agent: "frontend-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.AgentPolicyDecisions) != 6 {
		t.Fatalf("agent policy decisions = %d, want 6", len(result.AgentPolicyDecisions))
	}
	if result.AgentPolicyDecisions[0].Decision != "require_review" || result.AgentPolicyDecisions[0].MatchedPolicy == nil {
		t.Fatalf("agent write policy decision not preserved: %#v", result.AgentPolicyDecisions[0])
	}
	if result.AgentDecision.Decision != "needs_review" || !result.AgentDecision.ReviewRequired || result.AgentDecision.AutonomousAllowed {
		t.Fatalf("agent decision = %#v, want needs_review with autonomous disabled", result.AgentDecision)
	}
	if !contains(result.AgentDecision.RequiredActions, "request_approval_for_agent_write") {
		t.Fatalf("agent decision missing policy approval action: %#v", result.AgentDecision)
	}
}

func TestComposeBuildsBudgetedContextWindow(t *testing.T) {
	db := &fakeStore{}
	result, err := NewComposer(db).Compose(context.Background(), ComposeInput{
		Task:        "upgrade Next.js to latest and explain risks",
		Scope:       "repo:test/app",
		Agent:       "frontend-agent",
		Files:       []string{"package.json"},
		Language:    "typescript",
		TokenBudget: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ContextWindow.MaxTokens != 300 {
		t.Fatalf("context max tokens = %d, want 300", result.ContextWindow.MaxTokens)
	}
	if result.ContextWindow.EstimatedTokens <= 0 || result.ContextWindow.EstimatedTokens > 300 {
		t.Fatalf("context token estimate not bounded: %#v", result.ContextWindow)
	}
	if len(result.ContextWindow.Blocks) == 0 || result.ContextWindow.Prompt == "" {
		t.Fatalf("context window should be prompt-ready: %#v", result.ContextWindow)
	}
	if !containsContextBlock(result.ContextWindow.Blocks, "task") {
		t.Fatalf("task/gate block should survive tight budgets: %#v", result.ContextWindow.Blocks)
	}
	if len(result.ContextWindow.DroppedBlocks) == 0 || result.Stats.ContextDroppedBlocks != len(result.ContextWindow.DroppedBlocks) {
		t.Fatalf("tight budget should report dropped blocks: context=%#v stats=%+v", result.ContextWindow, result.Stats)
	}
	if len(result.ContextWindow.Warnings) == 0 {
		t.Fatalf("context window should warn when constrained or gated: %#v", result.ContextWindow)
	}
}

func TestComposeContextWindowPreservesSafetyGateForLongTaskAtMinimumBudget(t *testing.T) {
	db := &fakeStore{
		health: store.MemoryHealthResult{
			Scope:  "repo:test/app",
			Status: "critical",
			Score:  35,
			Signals: []store.MemoryHealthSignal{
				{Code: "source_refresh_overdue", Category: "sources", Severity: "critical", Count: 1, Action: "refresh_stale_sources"},
			},
		},
	}
	longTask := strings.Repeat("upgrade the architecture without losing safety gates ", 80)
	result, err := NewComposer(db).Compose(context.Background(), ComposeInput{
		Task:        longTask,
		Scope:       "repo:test/app",
		Agent:       "frontend-agent",
		TokenBudget: 300,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ContextWindow.EstimatedTokens <= 0 || result.ContextWindow.EstimatedTokens > result.ContextWindow.MaxTokens {
		t.Fatalf("context token estimate not bounded: %#v", result.ContextWindow)
	}
	prompt := result.ContextWindow.Prompt
	for _, want := range []string{
		"[GATE] Memory Gate",
		"Memory health: critical",
		"Verification: unsafe",
		"Required actions:",
		"Agent decision: blocked; autonomous_allowed=false",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("context prompt lost safety gate field %q:\n%s", want, prompt)
		}
	}
}

func TestComposeSuggestsLearningForLowConfidenceRetrieval(t *testing.T) {
	db := &fakeStore{lowSignalRecall: true}
	result, err := NewComposer(db).Compose(context.Background(), ComposeInput{
		Task:  "find obscure deployment convention",
		Scope: "repo:test/app",
		Agent: "frontend-agent",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Verification.Verdict != "weak" || !result.Verification.RetrievalQuality.LowConfidence || !result.Verification.ActionRequired {
		t.Fatalf("expected weak low-confidence verification: %#v", result.Verification)
	}
	if result.AgentDecision.Decision != "needs_review" || result.AgentDecision.AutonomousAllowed {
		t.Fatalf("agent decision should require review for low-confidence retrieval: %#v", result.AgentDecision)
	}
	if !contains(result.AgentDecision.RequiredActions, "rerun_with_more_specific_query") {
		t.Fatalf("agent decision missing low-confidence action: %#v", result.AgentDecision)
	}
	if !containsLearningTitle(result.LearningSuggestions, "Improve low-confidence retrieval") {
		t.Fatalf("low-confidence learning suggestion missing: %#v", result.LearningSuggestions)
	}
	if !containsLearningType(result.LearningSuggestions, "ingestion") {
		t.Fatalf("low-confidence learning suggestion should be ingestion typed: %#v", result.LearningSuggestions)
	}
}

func TestLearningSuggestionsIncludeLowSourceDiversityRepair(t *testing.T) {
	result := ComposeResult{
		Task:  "decide deployment convention",
		Scope: "repo:test/app",
		Verification: VerificationReport{
			Verdict: "partial",
			RetrievalQuality: RetrievalQuality{
				ResultCount:         4,
				UniqueSources:       1,
				DominantSourceShare: 1,
				LowSourceDiversity:  true,
			},
		},
	}
	suggestions := learningSuggestions(result)
	if !containsLearningTitle(suggestions, "Corroborate single-source retrieval") {
		t.Fatalf("low-source-diversity learning suggestion missing: %#v", suggestions)
	}
	if !containsLearningType(suggestions, "ingestion") {
		t.Fatalf("low-source-diversity suggestion should be ingestion typed: %#v", suggestions)
	}
}

func TestComposeBlocksAutonomyWhenActiveConflictSurfaces(t *testing.T) {
	db := &fakeStore{
		conflicts: []store.ConflictResult{{
			ID:                 "conflict-1",
			Scope:              "repo:test/app",
			ConflictType:       "contradicts",
			Status:             "open",
			Severity:           "blocking",
			PrimaryClaimID:     "claim-1",
			ConflictingClaimID: "claim-2",
		}},
	}
	result, err := NewComposer(db).Compose(context.Background(), ComposeInput{
		Task:              "choose frontend e2e framework",
		Scope:             "repo:test/app",
		Agent:             "frontend-agent",
		IncludeUnverified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 1 || result.Stats.Conflicts != 1 {
		t.Fatalf("conflicts not surfaced: conflicts=%#v stats=%+v", result.Conflicts, result.Stats)
	}
	if result.Verification.Verdict != "unsafe" || len(result.Verification.ActiveConflicts) != 1 {
		t.Fatalf("verification did not flag active conflict: %#v", result.Verification)
	}
	if result.AgentDecision.Decision != "blocked" || result.AgentDecision.AutonomousAllowed {
		t.Fatalf("agent decision = %#v, want blocked autonomous=false", result.AgentDecision)
	}
	if !contains(result.AgentDecision.RequiredActions, "resolve_active_conflicts") || !contains(result.AgentDecision.AllowedNextActions, "list_conflicts") || contains(result.AgentDecision.AllowedNextActions, "request_approval") {
		t.Fatalf("active conflict gate should require conflict review without approval bypass: %#v", result.AgentDecision)
	}
	if len(result.LearningSuggestions) == 0 || !containsLearningTitle(result.LearningSuggestions, "Resolve active claim conflict") {
		t.Fatalf("conflict learning suggestion missing: %#v", result.LearningSuggestions)
	}
}

func TestComposeBlocksAutonomyWhenActiveRelationConflictSurfaces(t *testing.T) {
	source := "https://example.test/frontend.md"
	db := &fakeStore{
		graphResults: []store.RelationResult{
			{ID: "relation-playwright", FromEntity: "Frontend App", ToEntity: "Playwright", Type: "should_use", Confidence: 0.9, SourceURL: &source},
			{ID: "relation-cypress", FromEntity: "Frontend App", ToEntity: "Cypress", Type: "should_use", Confidence: 0.88, SourceURL: &source},
		},
		relationConflicts: []store.ConflictResult{{
			ID:                    "relation-conflict-1",
			Scope:                 "repo:test/app",
			ConflictType:          "competes_with",
			Status:                "open",
			Severity:              "high",
			PrimaryRelationID:     "relation-playwright",
			ConflictingRelationID: "relation-cypress",
		}},
	}
	result, err := NewComposer(db).Compose(context.Background(), ComposeInput{
		Task:              "choose frontend e2e framework",
		Scope:             "repo:test/app",
		Agent:             "frontend-agent",
		IncludeUnverified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Conflicts) != 1 || result.Conflicts[0].PrimaryRelationID == "" || result.Stats.Conflicts != 1 {
		t.Fatalf("relation conflict not surfaced: conflicts=%#v stats=%+v", result.Conflicts, result.Stats)
	}
	if result.Verification.Verdict != "unsafe" || len(result.Verification.ActiveConflicts) != 1 {
		t.Fatalf("verification did not flag active relation conflict: %#v", result.Verification)
	}
	if result.AgentDecision.Decision != "blocked" || result.AgentDecision.AutonomousAllowed {
		t.Fatalf("agent decision = %#v, want blocked autonomous=false", result.AgentDecision)
	}
	if !contains(result.AgentDecision.RequiredActions, "review_relation_conflicts") || !contains(result.AgentDecision.AllowedNextActions, "list_conflicts") || contains(result.AgentDecision.AllowedNextActions, "request_approval") {
		t.Fatalf("relation conflict gate should require relation review without approval bypass: %#v", result.AgentDecision)
	}
	if len(result.LearningSuggestions) == 0 || !containsLearningTitle(result.LearningSuggestions, "Resolve active graph relation conflict") {
		t.Fatalf("relation conflict learning suggestion missing: %#v", result.LearningSuggestions)
	}
	if !containsRisk(result.Risks, "Active memory conflicts surfaced") {
		t.Fatalf("relation conflict risk missing: %#v", result.Risks)
	}
}

func TestNormalizeInputDefaults(t *testing.T) {
	input := normalizeInput(ComposeInput{Task: "  build   feature  ", Scope: " repo:test "})
	if input.Task != "build feature" {
		t.Fatalf("task = %q", input.Task)
	}
	if input.Scope != "repo:test" {
		t.Fatalf("scope = %q", input.Scope)
	}
	if input.Hook != "before_task" {
		t.Fatalf("hook = %q", input.Hook)
	}
	if input.Limit != 6 || input.MaxQueries != 6 {
		t.Fatalf("defaults = limit %d max %d", input.Limit, input.MaxQueries)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsRisk(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func containsLearningTitle(values []LearningSuggestion, title string) bool {
	for _, value := range values {
		if value.Title == title {
			return true
		}
	}
	return false
}

func containsLearningType(values []LearningSuggestion, proposalType string) bool {
	for _, value := range values {
		if value.ProposalType == proposalType {
			return true
		}
	}
	return false
}

func containsImpact(values []ImpactItem, kind, name string) bool {
	for _, value := range values {
		if value.Kind == kind && value.Name == name && value.Confidence > 0 && len(value.Reasons) > 0 {
			return true
		}
	}
	return false
}

func containsValidationCommand(values []ValidationStep, command string, required bool) bool {
	for _, value := range values {
		if value.Command == command && value.Required == required && value.Priority > 0 && value.Reason != "" {
			return true
		}
	}
	return false
}

func containsValidationType(values []ValidationStep, typ string) bool {
	for _, value := range values {
		if value.Type == typ && value.Priority > 0 && value.Reason != "" {
			return true
		}
	}
	return false
}

func containsTrace(values []RetrievalTraceItem, stage, operation string, parallel bool) bool {
	for _, value := range values {
		if value.Stage == stage && value.Operation == operation && value.Parallel == parallel && value.QueryCount >= 0 && value.ResultCount >= 0 && value.DurationMS >= 0 {
			return true
		}
	}
	return false
}

func containsTraceStatus(values []RetrievalTraceItem, stage, operation, status string) bool {
	for _, value := range values {
		if value.Stage == stage && value.Operation == operation && value.Status == status && value.Error != "" {
			return true
		}
	}
	return false
}

func healthTraceCacheStatus(values []RetrievalTraceItem) string {
	for _, value := range values {
		if value.Stage == "health" && value.Operation == "memory_health_lookup" {
			return value.CacheStatus
		}
	}
	return ""
}

func containsWarning(values []RetrievalWarning, stage, operation string) bool {
	for _, value := range values {
		if value.Stage == stage && value.Operation == operation && value.Message != "" {
			return true
		}
	}
	return false
}

func containsContextBlock(values []ContextBlock, typ string) bool {
	for _, value := range values {
		if value.Type == typ && value.Title != "" && value.Content != "" && value.Tokens > 0 {
			return true
		}
	}
	return false
}

func containsContextBlockCitationRef(values []ContextBlock, typ string) bool {
	for _, value := range values {
		if value.Type == typ && len(value.CitationRefs) > 0 {
			return true
		}
	}
	return false
}
