package memory

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/hermawan22/abra/internal/policy"
	"github.com/hermawan22/abra/internal/store"
)

type Store interface {
	Recall(ctx context.Context, query, scope string, limit int, includeUnverified bool) (store.RecallResult, error)
	ListMemorySummaries(ctx context.Context, query, scope string, limit int) ([]store.MemorySummaryResult, error)
	RelatedGraph(ctx context.Context, query, scope string, limit int) ([]store.RelationResult, error)
	ListOpenConflictsForClaims(ctx context.Context, scope string, claimIDs []string) ([]store.ConflictResult, error)
	ListOpenConflictsForRelations(ctx context.Context, scope string, relationIDs []string) ([]store.ConflictResult, error)
	EvaluateAgentActionPolicies(ctx context.Context, inputs []store.AgentActionDecisionInput) ([]store.AgentActionDecisionResult, error)
	MemoryHealth(ctx context.Context, scope string) (store.MemoryHealthResult, error)
	InsertAuditEvent(ctx context.Context, eventType, targetType, targetID, scope, sourceURL string, metadata map[string]any) error
}

type recallOptionsStore interface {
	RecallWithOptions(ctx context.Context, query, scope string, limit int, includeUnverified bool, options store.RecallOptions) (store.RecallResult, error)
}

type graphOptionsStore interface {
	RelatedGraphWithOptions(ctx context.Context, query, scope string, limit int, options store.RecallOptions) ([]store.RelationResult, error)
}

type summaryLevelStore interface {
	ListMemorySummariesByLevels(ctx context.Context, query, scope string, levels []string, limit int) ([]store.MemorySummaryResult, error)
}

type evidenceAnchorDocumentStore interface {
	DocumentsBySource(ctx context.Context, scope string, sourceURLs []string, limitPerSource int) ([]store.DocumentResult, error)
}

const (
	defaultRecallConcurrency    = 1
	defaultGraphConcurrency     = 4
	maxStageConcurrency         = 32
	evidenceAnchorSourcesMax    = 8
	evidenceAnchorDocsPerSource = 3
)

type Composer struct {
	store             Store
	synthesizer       Synthesizer
	healthCacheTTL    time.Duration
	recallConcurrency int
	graphConcurrency  int
	healthMu          sync.Mutex
	healthCache       map[string]healthCacheEntry
	healthInflight    map[string]*healthInflight
}

type healthCacheEntry struct {
	health    store.MemoryHealthResult
	expiresAt time.Time
}

type healthInflight struct {
	done   chan struct{}
	health store.MemoryHealthResult
	err    error
}

type ComposerOptions struct {
	HealthCacheTTL    time.Duration
	RecallConcurrency int
	GraphConcurrency  int
	Synthesizer       Synthesizer
}

type ComposeInput struct {
	Task              string                    `json:"task"`
	Scope             string                    `json:"scope"`
	Hook              string                    `json:"hook,omitempty"`
	Agent             string                    `json:"agent,omitempty"`
	Entity            string                    `json:"entity,omitempty"`
	Mode              RetrievalMode             `json:"mode,omitempty"`
	AsOf              string                    `json:"as_of,omitempty"`
	IncludeHistorical bool                      `json:"include_historical,omitempty"`
	Files             []string                  `json:"files,omitempty"`
	ChangedFiles      []string                  `json:"changed_files,omitempty"`
	Language          string                    `json:"language,omitempty"`
	Limit             int                       `json:"limit,omitempty"`
	MaxQueries        int                       `json:"max_queries,omitempty"`
	TokenBudget       int                       `json:"token_budget,omitempty"`
	IncludeUnverified bool                      `json:"include_unverified,omitempty"`
	Diagnostic        bool                      `json:"diagnostic,omitempty"`
	PersistLearning   bool                      `json:"persist_learning,omitempty"`
	AgentProfile      *store.AgentProfileRecord `json:"-"`
}

type ComposeResult struct {
	Task                 string                      `json:"task"`
	Scope                string                      `json:"scope"`
	Intent               string                      `json:"intent"`
	RetrievalMode        RetrievalMode               `json:"mode,omitempty"`
	Strategy             string                      `json:"strategy"`
	Plan                 policy.RecallPlan           `json:"plan"`
	RetrievalPlan        RetrievalPlan               `json:"retrieval_plan"`
	RetrievalTrace       []RetrievalTraceItem        `json:"retrieval_trace"`
	RetrievalWarnings    []RetrievalWarning          `json:"retrieval_warnings,omitempty"`
	Summaries            []store.MemorySummaryResult `json:"summaries"`
	Facts                []store.ClaimResult         `json:"facts"`
	SupportingDocuments  []store.DocumentResult      `json:"supporting_documents"`
	GraphContext         []store.RelationResult      `json:"graph_context"`
	EntityDossiers       []EntityDossier             `json:"entity_dossiers,omitempty"`
	TemporalContext      TemporalContext             `json:"temporal_context,omitempty"`
	GraphWarnings        []GraphWarning              `json:"graph_warnings,omitempty"`
	RetrievalReasons     []store.RetrievalReason     `json:"retrieval_reasons,omitempty"`
	Citations            []Citation                  `json:"citations,omitempty"`
	Conflicts            []store.ConflictResult      `json:"conflicts,omitempty"`
	MemoryHealth         store.MemoryHealthResult    `json:"memory_health"`
	RelevantFiles        []string                    `json:"relevant_files"`
	ImpactMap            []ImpactItem                `json:"impact_map"`
	Risks                []string                    `json:"risks"`
	Evidence             []EvidenceItem              `json:"evidence"`
	EvidenceAnchors      []EvidenceAnchor            `json:"evidence_anchors,omitempty"`
	Verification         VerificationReport          `json:"verification"`
	AgentPolicyDecisions []AgentPolicyDecision       `json:"agent_policy_decisions"`
	AgentProfile         *store.AgentProfileRecord   `json:"agent_profile,omitempty"`
	AgentDecision        AgentDecision               `json:"agent_decision"`
	ContextWindow        ContextWindow               `json:"context_window"`
	ValidationPlan       []ValidationStep            `json:"validation_plan"`
	LearningSuggestions  []LearningSuggestion        `json:"learning_suggestions"`
	SuggestedSteps       []string                    `json:"suggested_steps"`
	Stats                ComposeStats                `json:"stats"`
}

type EvidenceItem struct {
	SourceURL string `json:"source_url"`
	Ref       string `json:"ref,omitempty"`
	Title     string `json:"title,omitempty"`
	Count     int    `json:"count"`
	Anchors   int    `json:"anchors,omitempty"`
}

type EvidenceAnchor struct {
	Ref        string  `json:"ref,omitempty"`
	Kind       string  `json:"kind"`
	SourceURL  string  `json:"source_url"`
	Title      string  `json:"title,omitempty"`
	ClaimID    string  `json:"claim_id,omitempty"`
	DocumentID string  `json:"document_id,omitempty"`
	Quote      string  `json:"quote"`
	StartChar  int     `json:"start_char,omitempty"`
	EndChar    int     `json:"end_char,omitempty"`
	Score      float64 `json:"score,omitempty"`
}

type Citation struct {
	Ref         string           `json:"ref"`
	Kind        string           `json:"kind"`
	SourceURL   string           `json:"source_url"`
	Title       string           `json:"title,omitempty"`
	ClaimID     string           `json:"claim_id,omitempty"`
	DocumentID  string           `json:"document_id,omitempty"`
	ClaimIDs    []string         `json:"claim_ids,omitempty"`
	DocumentIDs []string         `json:"document_ids,omitempty"`
	SummaryIDs  []string         `json:"summary_ids,omitempty"`
	RelationIDs []string         `json:"relation_ids,omitempty"`
	Anchors     []EvidenceAnchor `json:"anchors,omitempty"`
}

type ComposeStats struct {
	QueriesRun           int `json:"queries_run"`
	Summaries            int `json:"summaries"`
	Facts                int `json:"facts"`
	SupportingDocuments  int `json:"supporting_documents"`
	GraphRelations       int `json:"graph_relations"`
	GraphWarnings        int `json:"graph_warnings"`
	Conflicts            int `json:"conflicts"`
	GraphQueries         int `json:"graph_queries"`
	ImpactItems          int `json:"impact_items"`
	ValidationSteps      int `json:"validation_steps"`
	RetrievalTraceItems  int `json:"retrieval_trace_items"`
	RetrievalReasons     int `json:"retrieval_reasons"`
	RetrievalWarnings    int `json:"retrieval_warnings"`
	HealthSignals        int `json:"health_signals"`
	ContextBlocks        int `json:"context_blocks"`
	ContextTokens        int `json:"context_tokens"`
	ContextDroppedBlocks int `json:"context_dropped_blocks"`
	TotalDurationMS      int `json:"total_duration_ms"`
	ParallelQueries      int `json:"parallel_queries"`
	ParallelGraphQueries int `json:"parallel_graph_queries"`
	RecallConcurrency    int `json:"recall_concurrency"`
	GraphConcurrency     int `json:"graph_concurrency"`
}

type RetrievalTraceItem struct {
	Stage       string `json:"stage"`
	Operation   string `json:"operation"`
	Parallel    bool   `json:"parallel"`
	QueryCount  int    `json:"query_count,omitempty"`
	ResultCount int    `json:"result_count,omitempty"`
	DurationMS  int    `json:"duration_ms"`
	Status      string `json:"status"`
	CacheStatus string `json:"cache_status,omitempty"`
	Error       string `json:"error,omitempty"`
}

type RetrievalWarning struct {
	Stage     string `json:"stage"`
	Operation string `json:"operation"`
	Query     string `json:"query,omitempty"`
	Message   string `json:"message"`
}

type ImpactItem struct {
	Kind            string   `json:"kind"`
	Name            string   `json:"name"`
	Confidence      float64  `json:"confidence"`
	Reasons         []string `json:"reasons"`
	EvidenceSources []string `json:"evidence_sources,omitempty"`
	RelationCount   int      `json:"relation_count,omitempty"`
	SummaryCount    int      `json:"summary_count,omitempty"`
	FactCount       int      `json:"fact_count,omitempty"`
}

type ValidationStep struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Command  string   `json:"command,omitempty"`
	Reason   string   `json:"reason"`
	Targets  []string `json:"targets,omitempty"`
	Priority int      `json:"priority"`
	Required bool     `json:"required"`
}

type retrievalResult struct {
	summaries []store.MemorySummaryResult
	recall    store.RecallResult
}

type RetrievalMode string

const (
	RetrievalModeFast     RetrievalMode = "fast"
	RetrievalModeBalanced RetrievalMode = "balanced"
	RetrievalModeDeep     RetrievalMode = "deep"
)

func NormalizeRetrievalMode(value string) RetrievalMode {
	switch RetrievalMode(strings.ToLower(strings.TrimSpace(value))) {
	case RetrievalModeFast:
		return RetrievalModeFast
	case RetrievalModeDeep:
		return RetrievalModeDeep
	default:
		return RetrievalModeBalanced
	}
}

type healthLookup struct {
	CacheStatus string
}

func NewComposerWithOptions(store Store, options ComposerOptions) *Composer {
	ttl := options.HealthCacheTTL
	if ttl < 0 {
		ttl = 0
	}
	recallConcurrency := boundedStageConcurrency(options.RecallConcurrency, defaultRecallConcurrency)
	graphConcurrency := boundedStageConcurrency(options.GraphConcurrency, defaultGraphConcurrency)
	return &Composer{
		store:             store,
		synthesizer:       options.Synthesizer,
		healthCacheTTL:    ttl,
		recallConcurrency: recallConcurrency,
		graphConcurrency:  graphConcurrency,
		healthCache:       map[string]healthCacheEntry{},
		healthInflight:    map[string]*healthInflight{},
	}
}

func boundedStageConcurrency(value, fallback int) int {
	if value <= 0 {
		value = fallback
	}
	if value < 1 {
		return 1
	}
	if value > maxStageConcurrency {
		return maxStageConcurrency
	}
	return value
}

func (c *Composer) Compose(ctx context.Context, input ComposeInput) (ComposeResult, error) {
	started := time.Now()
	trace := []RetrievalTraceItem{}
	warnings := []RetrievalWarning{}
	addTraceWithCache := func(stage, operation string, parallel bool, queryCount, resultCount int, started time.Time, stageWarnings []RetrievalWarning, cacheStatus string) {
		trace = append(trace, RetrievalTraceItem{
			Stage:       stage,
			Operation:   operation,
			Parallel:    parallel,
			QueryCount:  queryCount,
			ResultCount: resultCount,
			DurationMS:  durationMS(started),
			Status:      traceStatus(stageWarnings),
			CacheStatus: cacheStatus,
			Error:       traceError(stageWarnings),
		})
	}
	addTrace := func(stage, operation string, parallel bool, queryCount, resultCount int, started time.Time, stageWarnings []RetrievalWarning) {
		addTraceWithCache(stage, operation, parallel, queryCount, resultCount, started, stageWarnings, "")
	}

	stageStart := time.Now()
	input = normalizeInput(input)
	input = applyRetrievalMode(input)
	intent := classifyIntent(input)
	plan := policy.NewEngine(policy.Config{
		DefaultScope:      input.Scope,
		DefaultLimit:      input.Limit,
		IncludeUnverified: input.IncludeUnverified,
		MaxQueries:        input.MaxQueries,
	}).Plan(policy.Event{
		Hook:         policy.Hook(input.Hook),
		Task:         input.Task,
		Scope:        input.Scope,
		Files:        input.Files,
		ChangedFiles: input.ChangedFiles,
		Language:     input.Language,
		Agent:        input.Agent,
	})
	plan.Queries = mergeQueries(plan.Queries, strategyQueries(input, intent)...)
	if len(plan.Queries) > input.MaxQueries {
		plan.Queries = plan.Queries[:input.MaxQueries]
	}
	plan.Required = len(plan.Queries) > 0
	addTrace("planning", "policy_and_strategy_queries", false, len(plan.Queries), len(plan.Queries), stageStart, nil)

	facts := map[string]store.ClaimResult{}
	docs := map[string]store.DocumentResult{}
	graph := map[string]store.RelationResult{}
	retrievalReasons := map[string]store.RetrievalReason{}
	summaries, summaryTrace, summaryWarnings, err := c.composeSummaryLookups(ctx, input)
	if err != nil {
		return ComposeResult{}, err
	}
	trace = append(trace, summaryTrace...)
	warnings = append(warnings, summaryWarnings...)
	recallOptions, asOfApplied, asOfWarning := recallOptionsFromInput(input)
	if asOfWarning != "" {
		warnings = append(warnings, RetrievalWarning{Stage: "planning", Operation: "temporal_recall_options", Query: input.AsOf, Message: asOfWarning})
	}
	stageStart = time.Now()
	queryResults, retrievalWarnings, err := c.retrieveQueries(ctx, plan.Queries, recallOptions)
	if err != nil {
		return ComposeResult{}, err
	}
	warnings = append(warnings, retrievalWarnings...)
	addTrace("retrieval", "planned_summary_and_recall", true, len(plan.Queries), retrievalResultCount(queryResults), stageStart, retrievalWarnings)
	for _, queryResult := range queryResults {
		for _, summary := range queryResult.summaries {
			if existing, ok := summaries[summary.ID]; !ok || summary.Rank > existing.Rank {
				summaries[summary.ID] = summary
			}
		}
		for _, claim := range queryResult.recall.Claims {
			if existing, ok := facts[claim.ID]; !ok || claimPreferred(claim, existing) {
				facts[claim.ID] = claim
			}
		}
		for _, doc := range queryResult.recall.SupportingDocuments {
			if existing, ok := docs[doc.ID]; !ok || doc.Rank > existing.Rank {
				docs[doc.ID] = doc
			}
		}
		for _, relation := range queryResult.recall.GraphContext {
			graph[relationKey(relation)] = relation
		}
		mergeRetrievalReasons(retrievalReasons, queryResult.recall.RetrievalReasons)
	}

	stageStart = time.Now()
	graphLookupLimit := input.Limit
	if input.Mode == RetrievalModeFast {
		graphLookupLimit = minInt(graphLookupLimit, 2)
	}
	directGraph, err := c.relatedGraph(ctx, input.Task, input.Scope, graphLookupLimit, recallOptions)
	if err != nil {
		if isContextError(err) {
			return ComposeResult{}, err
		}
		graphWarning := RetrievalWarning{
			Stage:     "graph",
			Operation: "direct_task_graph_lookup",
			Query:     input.Task,
			Message:   compactError(err),
		}
		warnings = append(warnings, graphWarning)
		directGraph = nil
	}
	addTrace("graph", "direct_task_graph_lookup", false, 1, len(directGraph), stageStart, warningsFor(warnings, "graph", "direct_task_graph_lookup"))
	for _, relation := range directGraph {
		graph[relationKey(relation)] = relation
	}
	graphQueries := 1

	graphSeeds := graphExpansionSeeds(input, facts, docs, summaries, graph)
	if input.Mode == RetrievalModeFast {
		graphSeeds = nil
	}
	stageStart = time.Now()
	graphResults, graphRetrievalWarnings, err := c.retrieveGraphSeeds(ctx, graphSeeds, input.Scope, minInt(input.Limit, 6), recallOptions)
	if err != nil {
		return ComposeResult{}, err
	}
	warnings = append(warnings, graphRetrievalWarnings...)
	addTrace("graph", "seed_graph_expansion", true, len(graphSeeds), graphResultCount(graphResults), stageStart, graphRetrievalWarnings)
	graphQueries += len(graphSeeds)
	for _, expanded := range graphResults {
		for _, relation := range expanded {
			graph[relationKey(relation)] = relation
		}
	}

	anchorDocs, anchorTrace, anchorWarnings, err := c.composeEvidenceAnchorDocuments(ctx, input.Scope, facts)
	if err != nil {
		return ComposeResult{}, err
	}
	trace = append(trace, anchorTrace)
	warnings = append(warnings, anchorWarnings...)
	for _, doc := range anchorDocs {
		if existing, ok := docs[doc.ID]; !ok || doc.Rank > existing.Rank || len(doc.Content) > len(existing.Content) {
			docs[doc.ID] = doc
		}
	}

	result := ComposeResult{
		Task:                input.Task,
		Scope:               input.Scope,
		Intent:              intent,
		RetrievalMode:       input.Mode,
		Strategy:            strategyDescription(intent),
		Plan:                plan,
		RetrievalTrace:      trace,
		RetrievalWarnings:   warnings,
		Summaries:           sortSummaries(summaries),
		Facts:               sortClaims(facts),
		SupportingDocuments: sortDocuments(docs),
		GraphContext:        sortRelations(graph),
		RetrievalReasons:    sortRetrievalReasons(retrievalReasons),
		AgentProfile:        input.AgentProfile,
	}
	result.RetrievalPlan = buildRetrievalPlan(input, intent, plan.Queries, graphQueries)
	stageStart = time.Now()
	memoryHealth, lookup, err := c.memoryHealth(ctx, input.Scope)
	if err != nil {
		if isContextError(err) {
			return ComposeResult{}, err
		}
		memoryHealth = unavailableMemoryHealth(input.Scope, err)
	}
	addTraceWithCache("health", "memory_health_lookup", false, 1, len(memoryHealth.Signals), stageStart, nil, lookup.CacheStatus)
	result.MemoryHealth = memoryHealth
	stageStart = time.Now()
	conflicts, err := c.activeConflicts(ctx, input.Scope, result.Facts, result.GraphContext)
	if err != nil {
		return ComposeResult{}, err
	}
	addTrace("verification", "active_conflict_lookup", false, len(result.Facts)+len(result.GraphContext), len(conflicts), stageStart, nil)
	result.Conflicts = conflicts
	stageStart = time.Now()
	result.RelevantFiles = relevantFiles(result.Facts, result.SupportingDocuments, input.Files, input.ChangedFiles)
	result.ImpactMap = impactMap(input, result.Summaries, result.Facts, result.SupportingDocuments, result.GraphContext, result.RelevantFiles)
	result.GraphWarnings = graphWarnings(result.GraphContext)
	result.Risks = applyMemoryHealthRisks(risks(result.Facts, result.GraphContext, result.Conflicts, result.RetrievalWarnings, result.GraphWarnings), result.MemoryHealth)
	var citationRefs map[string]string
	result.Citations, citationRefs = buildCitations(result)
	result.EvidenceAnchors = evidenceAnchors(result.Facts, result.SupportingDocuments, citationRefs)
	storedAnchors, err := c.storedEvidenceAnchors(ctx, input.Scope, result.Facts, citationRefs)
	if err != nil {
		if isContextError(err) {
			return ComposeResult{}, err
		}
		result.RetrievalWarnings = append(result.RetrievalWarnings, RetrievalWarning{Stage: "evidence", Operation: "stored_evidence_anchor_lookup", Query: input.Scope, Message: compactError(err)})
	} else {
		result.EvidenceAnchors = mergeEvidenceAnchors(result.EvidenceAnchors, storedAnchors)
	}
	result.Citations = attachCitationAnchors(result.Citations, result.EvidenceAnchors)
	result.Evidence = evidence(result.Facts, result.SupportingDocuments, citationRefs, result.EvidenceAnchors)
	result.TemporalContext = buildTemporalContext(input, result, asOfApplied, asOfWarning)
	result.EntityDossiers = buildEntityDossiers(input, result)
	result.Verification = verifyPacket(result.Summaries, result.Facts, result.SupportingDocuments, result.GraphContext, result.Evidence, result.RetrievalPlan, result.Conflicts, result.RetrievalWarnings, result.GraphWarnings, result.MemoryHealth, result.EvidenceAnchors)
	addTrace("compile", "evidence_impact_and_verification", false, len(result.Facts)+len(result.SupportingDocuments)+len(result.GraphContext), len(result.Evidence)+len(result.ImpactMap)+len(result.Risks), stageStart, nil)
	stageStart = time.Now()
	agentPolicyDecisions, err := c.agentPolicyDecisions(ctx, input)
	if err != nil {
		return ComposeResult{}, err
	}
	addTrace("policy", "agent_action_policy_evaluation", false, 6, len(agentPolicyDecisions), stageStart, nil)
	result.AgentPolicyDecisions = agentPolicyDecisions
	stageStart = time.Now()
	result.LearningSuggestions = learningSuggestions(result)
	result.SuggestedSteps = suggestedSteps(input, intent, result)
	result.AgentDecision = decideAgentAction(input, result)
	result.ValidationPlan = validationPlan(input, result)
	result.ContextWindow = buildContextWindow(input, result)
	addTrace("decision", "agent_decision_and_validation_plan", false, len(result.Risks)+len(result.AgentPolicyDecisions), len(result.ValidationPlan)+len(result.SuggestedSteps), stageStart, nil)
	result.RetrievalTrace = trace
	result.Stats = ComposeStats{
		QueriesRun:           len(plan.Queries),
		Summaries:            len(result.Summaries),
		Facts:                len(result.Facts),
		SupportingDocuments:  len(result.SupportingDocuments),
		GraphRelations:       len(result.GraphContext),
		GraphWarnings:        len(result.GraphWarnings),
		Conflicts:            len(result.Conflicts),
		GraphQueries:         graphQueries,
		ImpactItems:          len(result.ImpactMap),
		ValidationSteps:      len(result.ValidationPlan),
		RetrievalTraceItems:  len(result.RetrievalTrace),
		RetrievalReasons:     len(result.RetrievalReasons),
		RetrievalWarnings:    len(result.RetrievalWarnings),
		HealthSignals:        len(result.MemoryHealth.Signals),
		ContextBlocks:        len(result.ContextWindow.Blocks),
		ContextTokens:        result.ContextWindow.EstimatedTokens,
		ContextDroppedBlocks: len(result.ContextWindow.DroppedBlocks),
		TotalDurationMS:      durationMS(started),
		ParallelQueries:      len(plan.Queries),
		ParallelGraphQueries: len(graphSeeds),
		RecallConcurrency:    c.recallConcurrency,
		GraphConcurrency:     c.graphConcurrency,
	}

	if !input.Diagnostic {
		_ = c.store.InsertAuditEvent(ctx, "memory.composed", "memory_packet", input.Scope+"\x00"+input.Task, input.Scope, "", map[string]any{
			"intent":             intent,
			"agent":              input.Agent,
			"queries":            len(plan.Queries),
			"summaries":          len(result.Summaries),
			"facts":              len(result.Facts),
			"documents":          len(result.SupportingDocuments),
			"relations":          len(result.GraphContext),
			"graph_warnings":     len(result.GraphWarnings),
			"retrieval_reasons":  len(result.RetrievalReasons),
			"conflicts":          len(result.Conflicts),
			"files":              len(result.RelevantFiles),
			"impact":             len(result.ImpactMap),
			"validation":         len(result.ValidationPlan),
			"context_blocks":     len(result.ContextWindow.Blocks),
			"context_tokens":     result.ContextWindow.EstimatedTokens,
			"context_dropped":    len(result.ContextWindow.DroppedBlocks),
			"risk_count":         len(result.Risks),
			"memory_health":      result.MemoryHealth.Status,
			"health_signals":     len(result.MemoryHealth.Signals),
			"verdict":            result.Verification.Verdict,
			"decision":           result.AgentDecision.Decision,
			"score":              result.Verification.Score,
			"learning":           len(result.LearningSuggestions),
			"policy":             len(result.AgentPolicyDecisions),
			"duration_ms":        result.Stats.TotalDurationMS,
			"warnings":           len(result.RetrievalWarnings),
			"recall_concurrency": c.recallConcurrency,
			"graph_concurrency":  c.graphConcurrency,
		})
	}
	return result, nil
}

var (
	filePattern    = regexp.MustCompile("(?:^|[\\s(\"'`])((?:[\\w.-]+/)+(?:[\\w.-]+)(?:\\.(?:go|js|jsx|ts|tsx|md|sql|json|yaml|yml|scss|css))?)")
	fileExtPattern = regexp.MustCompile(`\.(go|js|jsx|ts|tsx|md|sql|json|yaml|yml|scss|css)$`)
)
