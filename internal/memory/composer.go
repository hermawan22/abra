package memory

import (
	"context"
	"errors"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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

const defaultHealthCacheTTL = 2 * time.Second

type Composer struct {
	store          Store
	healthCacheTTL time.Duration
	healthMu       sync.Mutex
	healthCache    map[string]healthCacheEntry
	healthInflight map[string]*healthInflight
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
	HealthCacheTTL time.Duration
}

type ComposeInput struct {
	Task              string                    `json:"task"`
	Scope             string                    `json:"scope"`
	Hook              string                    `json:"hook,omitempty"`
	Agent             string                    `json:"agent,omitempty"`
	Files             []string                  `json:"files,omitempty"`
	ChangedFiles      []string                  `json:"changed_files,omitempty"`
	Language          string                    `json:"language,omitempty"`
	Limit             int                       `json:"limit,omitempty"`
	MaxQueries        int                       `json:"max_queries,omitempty"`
	TokenBudget       int                       `json:"token_budget,omitempty"`
	IncludeUnverified bool                      `json:"include_unverified,omitempty"`
	AgentProfile      *store.AgentProfileRecord `json:"-"`
}

type ComposeResult struct {
	Task                 string                      `json:"task"`
	Scope                string                      `json:"scope"`
	Intent               string                      `json:"intent"`
	Strategy             string                      `json:"strategy"`
	Plan                 policy.RecallPlan           `json:"plan"`
	RetrievalPlan        RetrievalPlan               `json:"retrieval_plan"`
	RetrievalTrace       []RetrievalTraceItem        `json:"retrieval_trace"`
	RetrievalWarnings    []RetrievalWarning          `json:"retrieval_warnings,omitempty"`
	Summaries            []store.MemorySummaryResult `json:"summaries"`
	Facts                []store.ClaimResult         `json:"facts"`
	SupportingDocuments  []store.DocumentResult      `json:"supporting_documents"`
	GraphContext         []store.RelationResult      `json:"graph_context"`
	GraphWarnings        []GraphWarning              `json:"graph_warnings,omitempty"`
	Conflicts            []store.ConflictResult      `json:"conflicts,omitempty"`
	MemoryHealth         store.MemoryHealthResult    `json:"memory_health"`
	RelevantFiles        []string                    `json:"relevant_files"`
	ImpactMap            []ImpactItem                `json:"impact_map"`
	Risks                []string                    `json:"risks"`
	Evidence             []EvidenceItem              `json:"evidence"`
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
	Title     string `json:"title,omitempty"`
	Count     int    `json:"count"`
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
	RetrievalWarnings    int `json:"retrieval_warnings"`
	HealthSignals        int `json:"health_signals"`
	ContextBlocks        int `json:"context_blocks"`
	ContextTokens        int `json:"context_tokens"`
	ContextDroppedBlocks int `json:"context_dropped_blocks"`
	TotalDurationMS      int `json:"total_duration_ms"`
	ParallelQueries      int `json:"parallel_queries"`
	ParallelGraphQueries int `json:"parallel_graph_queries"`
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

type healthLookup struct {
	CacheStatus string
}

func NewComposer(store Store) *Composer {
	return NewComposerWithOptions(store, ComposerOptions{HealthCacheTTL: defaultHealthCacheTTL})
}

func NewComposerWithOptions(store Store, options ComposerOptions) *Composer {
	ttl := options.HealthCacheTTL
	if ttl < 0 {
		ttl = 0
	}
	return &Composer{
		store:          store,
		healthCacheTTL: ttl,
		healthCache:    map[string]healthCacheEntry{},
		healthInflight: map[string]*healthInflight{},
	}
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
	summaries := map[string]store.MemorySummaryResult{}
	stageStart = time.Now()
	taskSummaries, err := c.store.ListMemorySummaries(ctx, input.Task, input.Scope, input.Limit)
	if err != nil {
		if isContextError(err) {
			return ComposeResult{}, err
		}
		warnings = append(warnings, RetrievalWarning{
			Stage:     "summaries",
			Operation: "task_summary_lookup",
			Query:     input.Task,
			Message:   compactError(err),
		})
		taskSummaries = nil
	}
	addTrace("summaries", "task_summary_lookup", false, 1, len(taskSummaries), stageStart, warningsFor(warnings, "summaries", "task_summary_lookup"))
	for _, summary := range taskSummaries {
		if existing, ok := summaries[summary.ID]; !ok || summary.Rank > existing.Rank {
			summaries[summary.ID] = summary
		}
	}
	stageStart = time.Now()
	queryResults, retrievalWarnings, err := c.retrieveQueries(ctx, plan.Queries)
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
			if existing, ok := facts[claim.ID]; !ok || claim.Rank > existing.Rank {
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
	}

	stageStart = time.Now()
	directGraph, err := c.store.RelatedGraph(ctx, input.Task, input.Scope, input.Limit)
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
	stageStart = time.Now()
	graphResults, graphRetrievalWarnings, err := c.retrieveGraphSeeds(ctx, graphSeeds, input.Scope, minInt(input.Limit, 6))
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

	result := ComposeResult{
		Task:                input.Task,
		Scope:               input.Scope,
		Intent:              intent,
		Strategy:            strategyDescription(intent),
		Plan:                plan,
		RetrievalTrace:      trace,
		RetrievalWarnings:   warnings,
		Summaries:           sortSummaries(summaries),
		Facts:               sortClaims(facts),
		SupportingDocuments: sortDocuments(docs),
		GraphContext:        sortRelations(graph),
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
	result.Evidence = evidence(result.Facts, result.SupportingDocuments)
	result.Verification = verifyPacket(result.Summaries, result.Facts, result.SupportingDocuments, result.GraphContext, result.Evidence, result.RetrievalPlan, result.Conflicts, result.RetrievalWarnings, result.GraphWarnings)
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
		RetrievalWarnings:    len(result.RetrievalWarnings),
		HealthSignals:        len(result.MemoryHealth.Signals),
		ContextBlocks:        len(result.ContextWindow.Blocks),
		ContextTokens:        result.ContextWindow.EstimatedTokens,
		ContextDroppedBlocks: len(result.ContextWindow.DroppedBlocks),
		TotalDurationMS:      durationMS(started),
		ParallelQueries:      len(plan.Queries),
		ParallelGraphQueries: len(graphSeeds),
	}

	_ = c.store.InsertAuditEvent(ctx, "memory.composed", "memory_packet", input.Scope+"\x00"+input.Task, input.Scope, "", map[string]any{
		"intent":          intent,
		"agent":           input.Agent,
		"queries":         len(plan.Queries),
		"summaries":       len(result.Summaries),
		"facts":           len(result.Facts),
		"documents":       len(result.SupportingDocuments),
		"relations":       len(result.GraphContext),
		"graph_warnings":  len(result.GraphWarnings),
		"conflicts":       len(result.Conflicts),
		"files":           len(result.RelevantFiles),
		"impact":          len(result.ImpactMap),
		"validation":      len(result.ValidationPlan),
		"context_blocks":  len(result.ContextWindow.Blocks),
		"context_tokens":  result.ContextWindow.EstimatedTokens,
		"context_dropped": len(result.ContextWindow.DroppedBlocks),
		"risk_count":      len(result.Risks),
		"memory_health":   result.MemoryHealth.Status,
		"health_signals":  len(result.MemoryHealth.Signals),
		"verdict":         result.Verification.Verdict,
		"decision":        result.AgentDecision.Decision,
		"score":           result.Verification.Score,
		"learning":        len(result.LearningSuggestions),
		"policy":          len(result.AgentPolicyDecisions),
		"duration_ms":     result.Stats.TotalDurationMS,
		"warnings":        len(result.RetrievalWarnings),
	})
	return result, nil
}

func (c *Composer) retrieveQueries(ctx context.Context, queries []policy.RecallQuery) ([]retrievalResult, []RetrievalWarning, error) {
	if len(queries) == 0 {
		return nil, nil, nil
	}

	results := make([]retrievalResult, len(queries))
	warningsByQuery := make([][]RetrievalWarning, len(queries))
	errs := make(chan error, len(queries)*2)
	var wg sync.WaitGroup
	for i, query := range queries {
		i, query := i, query
		wg.Add(1)
		go func() {
			defer wg.Done()
			querySummaries, err := c.store.ListMemorySummaries(ctx, query.Query, query.Scope, minInt(query.Limit, 4))
			if err != nil {
				if isContextError(err) {
					errs <- err
					return
				}
				warningsByQuery[i] = append(warningsByQuery[i], RetrievalWarning{
					Stage:     "retrieval",
					Operation: "query_summary_lookup",
					Query:     query.Query,
					Message:   compactError(err),
				})
				querySummaries = nil
			}
			recall, err := c.store.Recall(ctx, query.Query, query.Scope, query.Limit, query.IncludeUnverified)
			if err != nil {
				if isContextError(err) {
					errs <- err
					return
				}
				warningsByQuery[i] = append(warningsByQuery[i], RetrievalWarning{
					Stage:     "retrieval",
					Operation: "recall",
					Query:     query.Query,
					Message:   compactError(err),
				})
				return
			}
			results[i] = retrievalResult{summaries: querySummaries, recall: recall}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return nil, nil, err
		}
	}
	warnings := []RetrievalWarning{}
	for _, queryWarnings := range warningsByQuery {
		warnings = append(warnings, queryWarnings...)
	}
	return results, warnings, nil
}

func (c *Composer) activeConflicts(ctx context.Context, scope string, facts []store.ClaimResult, graph []store.RelationResult) ([]store.ConflictResult, error) {
	claimConflicts, err := c.store.ListOpenConflictsForClaims(ctx, scope, claimIDs(facts))
	if err != nil {
		return nil, err
	}
	relationConflicts, err := c.store.ListOpenConflictsForRelations(ctx, scope, relationIDs(graph))
	if err != nil {
		return nil, err
	}
	return mergeConflicts(claimConflicts, relationConflicts), nil
}

func (c *Composer) memoryHealth(ctx context.Context, scope string) (store.MemoryHealthResult, healthLookup, error) {
	if c.healthCacheTTL <= 0 {
		health, err := c.store.MemoryHealth(ctx, scope)
		return health, healthLookup{CacheStatus: "disabled"}, err
	}
	now := time.Now()
	c.healthMu.Lock()
	if entry, ok := c.healthCache[scope]; ok && now.Before(entry.expiresAt) {
		health := cloneMemoryHealth(entry.health)
		c.healthMu.Unlock()
		return health, healthLookup{CacheStatus: "cache_hit"}, nil
	}
	if call := c.healthInflight[scope]; call != nil {
		c.healthMu.Unlock()
		select {
		case <-call.done:
			if call.err != nil {
				return store.MemoryHealthResult{}, healthLookup{CacheStatus: "coalesced"}, call.err
			}
			return cloneMemoryHealth(call.health), healthLookup{CacheStatus: "coalesced"}, nil
		case <-ctx.Done():
			return store.MemoryHealthResult{}, healthLookup{CacheStatus: "coalesced"}, ctx.Err()
		}
	}
	call := &healthInflight{done: make(chan struct{})}
	c.healthInflight[scope] = call
	c.healthMu.Unlock()

	health, err := c.store.MemoryHealth(ctx, scope)
	if err != nil {
		if isContextError(err) {
			c.finishMemoryHealth(scope, call, store.MemoryHealthResult{}, err, now)
			return store.MemoryHealthResult{}, healthLookup{CacheStatus: "fresh"}, err
		}
		health = unavailableMemoryHealth(scope, err)
		err = nil
	}
	c.finishMemoryHealth(scope, call, health, err, now)
	return cloneMemoryHealth(health), healthLookup{CacheStatus: "fresh"}, nil
}

func (c *Composer) finishMemoryHealth(scope string, call *healthInflight, health store.MemoryHealthResult, err error, now time.Time) {
	c.healthMu.Lock()
	defer c.healthMu.Unlock()
	if err == nil {
		health = cloneMemoryHealth(health)
		c.healthCache[scope] = healthCacheEntry{
			health:    health,
			expiresAt: now.Add(c.healthCacheTTL),
		}
	}
	call.health = health
	call.err = err
	delete(c.healthInflight, scope)
	close(call.done)
}

func cloneMemoryHealth(health store.MemoryHealthResult) store.MemoryHealthResult {
	health.Reasons = append([]string(nil), health.Reasons...)
	health.Signals = append([]store.MemoryHealthSignal(nil), health.Signals...)
	if health.Summaries.Levels != nil {
		levels := make(map[string]int, len(health.Summaries.Levels))
		for key, value := range health.Summaries.Levels {
			levels[key] = value
		}
		health.Summaries.Levels = levels
	}
	if health.LastUpdated != nil {
		lastUpdated := make(map[string]string, len(health.LastUpdated))
		for key, value := range health.LastUpdated {
			lastUpdated[key] = value
		}
		health.LastUpdated = lastUpdated
	}
	return health
}

func mergeConflicts(groups ...[]store.ConflictResult) []store.ConflictResult {
	seen := map[string]struct{}{}
	out := []store.ConflictResult{}
	for _, group := range groups {
		for _, conflict := range group {
			id := strings.TrimSpace(conflict.ID)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, conflict)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := conflictSeverityRank(out[i].Severity)
		right := conflictSeverityRank(out[j].Severity)
		if left != right {
			return left > right
		}
		return out[i].UpdatedAt > out[j].UpdatedAt
	})
	return out
}

func conflictSeverityRank(severity string) int {
	switch strings.ToLower(strings.TrimSpace(severity)) {
	case "blocking":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	default:
		return 0
	}
}

func (c *Composer) retrieveGraphSeeds(ctx context.Context, seeds []string, scope string, limit int) ([][]store.RelationResult, []RetrievalWarning, error) {
	if len(seeds) == 0 {
		return nil, nil, nil
	}

	results := make([][]store.RelationResult, len(seeds))
	warningsBySeed := make([][]RetrievalWarning, len(seeds))
	errs := make(chan error, len(seeds))
	var wg sync.WaitGroup
	for i, seed := range seeds {
		i, seed := i, seed
		wg.Add(1)
		go func() {
			defer wg.Done()
			expanded, err := c.store.RelatedGraph(ctx, seed, scope, limit)
			if err != nil {
				if isContextError(err) {
					errs <- err
					return
				}
				warningsBySeed[i] = append(warningsBySeed[i], RetrievalWarning{
					Stage:     "graph",
					Operation: "seed_graph_expansion",
					Query:     seed,
					Message:   compactError(err),
				})
				return
			}
			results[i] = expanded
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			return nil, nil, err
		}
	}
	warnings := []RetrievalWarning{}
	for _, seedWarnings := range warningsBySeed {
		warnings = append(warnings, seedWarnings...)
	}
	return results, warnings, nil
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func compactError(err error) string {
	message := strings.Join(strings.Fields(err.Error()), " ")
	if len(message) > 180 {
		return message[:177] + "..."
	}
	return message
}

func traceStatus(warnings []RetrievalWarning) string {
	if len(warnings) == 0 {
		return "ok"
	}
	return "degraded"
}

func traceError(warnings []RetrievalWarning) string {
	if len(warnings) == 0 {
		return ""
	}
	if len(warnings) == 1 {
		return warnings[0].Message
	}
	return warnings[0].Message + " and " + strconv.Itoa(len(warnings)-1) + " more warning(s)"
}

func warningsFor(warnings []RetrievalWarning, stage, operation string) []RetrievalWarning {
	out := []RetrievalWarning{}
	for _, warning := range warnings {
		if warning.Stage == stage && warning.Operation == operation {
			out = append(out, warning)
		}
	}
	return out
}

func retrievalResultCount(results []retrievalResult) int {
	total := 0
	for _, result := range results {
		total += len(result.summaries)
		total += len(result.recall.Claims)
		total += len(result.recall.SupportingDocuments)
		total += len(result.recall.GraphContext)
	}
	return total
}

func graphResultCount(results [][]store.RelationResult) int {
	total := 0
	for _, result := range results {
		total += len(result)
	}
	return total
}

func durationMS(started time.Time) int {
	elapsed := time.Since(started).Milliseconds()
	if elapsed < 0 {
		return 0
	}
	return int(elapsed)
}

func (c *Composer) agentPolicyDecisions(ctx context.Context, input ComposeInput) ([]AgentPolicyDecision, error) {
	principalID := strings.TrimSpace(input.Agent)
	if principalID == "" {
		principalID = "unknown"
	}
	actions := []struct {
		action     string
		targetType string
		targetID   string
	}{
		{action: "agent_write", targetType: "memory_write", targetID: input.Scope},
		{action: "challenge_claim", targetType: "claim", targetID: "*"},
		{action: "forget_claim", targetType: "claim", targetID: "*"},
		{action: "backfill", targetType: "memory_summaries", targetID: input.Scope},
		{action: "source_authority_change", targetType: "source_config", targetID: "*"},
		{action: "acl_change", targetType: "policy", targetID: "*"},
	}
	inputs := make([]store.AgentActionDecisionInput, 0, len(actions))
	for _, action := range actions {
		inputs = append(inputs, store.AgentActionDecisionInput{
			Action:        action.action,
			Scope:         input.Scope,
			TargetType:    action.targetType,
			TargetID:      action.targetID,
			PrincipalType: "agent",
			PrincipalID:   principalID,
		})
	}
	results, err := c.store.EvaluateAgentActionPolicies(ctx, inputs)
	if err != nil {
		return nil, err
	}
	decisions := make([]AgentPolicyDecision, 0, len(results))
	for i, result := range results {
		decisions = append(decisions, AgentPolicyDecision{
			Action:        inputs[i].Action,
			TargetType:    inputs[i].TargetType,
			TargetID:      inputs[i].TargetID,
			Allowed:       result.Allowed,
			Decision:      result.Decision,
			Reason:        result.Reason,
			MatchedPolicy: result.MatchedPolicy,
		})
	}
	return decisions, nil
}

func normalizeInput(input ComposeInput) ComposeInput {
	input.Task = strings.Join(strings.Fields(input.Task), " ")
	input.Scope = strings.TrimSpace(input.Scope)
	input.Hook = strings.TrimSpace(input.Hook)
	if input.Hook == "" {
		input.Hook = string(policy.HookBeforeTask)
	}
	if input.Limit < 1 || input.Limit > 20 {
		input.Limit = 6
	}
	if input.MaxQueries < 1 || input.MaxQueries > 12 {
		input.MaxQueries = 6
	}
	if input.TokenBudget < 1 {
		input.TokenBudget = 1600
	}
	if input.TokenBudget < 300 {
		input.TokenBudget = 300
	}
	if input.TokenBudget > 12000 {
		input.TokenBudget = 12000
	}
	input.Files = compactList(input.Files)
	input.ChangedFiles = compactList(input.ChangedFiles)
	return input
}

func classifyIntent(input ComposeInput) string {
	text := strings.ToLower(input.Task + " " + input.Language + " " + strings.Join(input.Files, " ") + " " + strings.Join(input.ChangedFiles, " "))
	switch {
	case containsAny(text, "upgrade", "migration", "migrate", "breaking", "dependency", "version"):
		return "migration"
	case containsAny(text, "bug", "fix", "error", "incident", "regression", "failing", "fail"):
		return "debugging"
	case containsAny(text, "implement", "build", "add", "feature", "refactor", "code"):
		return "implementation"
	case containsAny(text, "architecture", "design", "how", "explain", "overview", "flow"):
		return "architecture"
	default:
		return "general"
	}
}

func strategyQueries(input ComposeInput, intent string) []policy.RecallQuery {
	base := "Task: " + input.Task
	query := func(text, reason string) policy.RecallQuery {
		return policy.RecallQuery{Query: text, Scope: input.Scope, Limit: input.Limit, IncludeUnverified: input.IncludeUnverified, Reason: reason}
	}
	queries := []policy.RecallQuery{
		query("Hierarchical summaries, code intelligence overview, source areas, package operations, and verified facts for "+base, "load compact high-signal memory before detailed retrieval"),
	}
	if len(input.Files)+len(input.ChangedFiles) > 0 {
		queries = append(queries, query("File-specific decisions, symbols, owners, tests, and dependency context for "+strings.Join(append(input.Files, input.ChangedFiles...), " "), "anchor memory to touched files"))
	}
	switch intent {
	case "migration":
		queries = append(queries,
			query("Dependency versions, package scripts, runtime constraints, breaking changes, compatibility risks, and rollout notes for "+base, "plan migration safely"),
			query("Known failures, stale claims, disputed assumptions, and verification gates related to "+base, "avoid unsafe upgrades"),
		)
	case "debugging":
		queries = append(queries,
			query("Known incidents, regression risks, failing tests, error handling, and ownership context for "+base, "prioritize likely failure causes"),
			query("Relevant source files, call paths, data flow, and graph relations for "+base, "trace the issue through the system"),
		)
	case "architecture":
		queries = append(queries,
			query("Architecture summaries, module boundaries, source areas, routes, APIs, entities, and relations for "+base, "answer with global structure"),
			query("Important decisions, constraints, and evidence-backed tradeoffs for "+base, "separate facts from guesses"),
		)
	default:
		queries = append(queries,
			query("Implementation conventions, reusable components, APIs, tests, and validation expectations for "+base, "prepare an agent to change code"),
			query("Graph relations, dependencies, impacted modules, and related symbols for "+base, "find cross-file impact"),
		)
	}
	return queries
}

func mergeQueries(first []policy.RecallQuery, rest ...policy.RecallQuery) []policy.RecallQuery {
	seen := map[string]struct{}{}
	out := []policy.RecallQuery{}
	for _, query := range append(first, rest...) {
		query.Query = strings.Join(strings.Fields(query.Query), " ")
		query.Scope = strings.TrimSpace(query.Scope)
		if query.Query == "" || query.Scope == "" {
			continue
		}
		key := query.Scope + "\x00" + strings.ToLower(query.Query)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, query)
	}
	return out
}

func strategyDescription(intent string) string {
	switch intent {
	case "migration":
		return "migration-aware packet: summaries, dependency facts, compatibility risks, graph impact, and verification gates"
	case "debugging":
		return "debugging packet: known failures, likely impacted files, graph context, stale claims, and test guidance"
	case "architecture":
		return "architecture packet: hierarchical summaries, module boundaries, source areas, graph context, and evidence"
	case "implementation":
		return "implementation packet: conventions, relevant files, reusable components, dependencies, graph impact, and checks"
	default:
		return "general packet: source-backed facts, documents, graph context, risks, and next steps"
	}
}

func sortClaims(in map[string]store.ClaimResult) []store.ClaimResult {
	out := make([]store.ClaimResult, 0, len(in))
	for _, claim := range in {
		out = append(out, claim)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank == out[j].Rank {
			return out[i].ID < out[j].ID
		}
		return out[i].Rank > out[j].Rank
	})
	return out
}

func claimIDs(claims []store.ClaimResult) []string {
	out := make([]string, 0, len(claims))
	for _, claim := range claims {
		if strings.TrimSpace(claim.ID) != "" {
			out = append(out, claim.ID)
		}
	}
	return compactList(out)
}

func relationIDs(relations []store.RelationResult) []string {
	out := make([]string, 0, len(relations))
	for _, relation := range relations {
		if strings.TrimSpace(relation.ID) != "" {
			out = append(out, relation.ID)
		}
	}
	return compactList(out)
}

func sortDocuments(in map[string]store.DocumentResult) []store.DocumentResult {
	out := make([]store.DocumentResult, 0, len(in))
	for _, doc := range in {
		out = append(out, doc)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank == out[j].Rank {
			return out[i].ID < out[j].ID
		}
		return out[i].Rank > out[j].Rank
	})
	return out
}

func sortSummaries(in map[string]store.MemorySummaryResult) []store.MemorySummaryResult {
	out := make([]store.MemorySummaryResult, 0, len(in))
	for _, summary := range in {
		out = append(out, summary)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank == out[j].Rank {
			return out[i].ID < out[j].ID
		}
		return out[i].Rank > out[j].Rank
	})
	return out
}

func sortRelations(in map[string]store.RelationResult) []store.RelationResult {
	out := make([]store.RelationResult, 0, len(in))
	for _, relation := range in {
		out = append(out, relation)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence == out[j].Confidence {
			return relationKey(out[i]) < relationKey(out[j])
		}
		return out[i].Confidence > out[j].Confidence
	})
	return out
}

func relationKey(relation store.RelationResult) string {
	return strings.ToLower(relation.FromEntity + "\x00" + relation.Type + "\x00" + relation.ToEntity)
}

func graphExpansionSeeds(input ComposeInput, facts map[string]store.ClaimResult, docs map[string]store.DocumentResult, summaries map[string]store.MemorySummaryResult, graph map[string]store.RelationResult) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(value string) {
		value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
		if value == "" || strings.EqualFold(value, input.Task) {
			return
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}

	for _, value := range append(input.Files, input.ChangedFiles...) {
		add(value)
	}
	for _, value := range relevantFiles(sortClaims(facts), sortDocuments(docs), nil, nil) {
		add(value)
	}
	for _, relation := range sortRelations(graph) {
		add(relation.FromEntity)
		add(relation.ToEntity)
	}
	for _, summary := range sortSummaries(summaries) {
		add(summary.Key)
		add(summary.Title)
	}
	if len(out) > 4 {
		return out[:4]
	}
	return out
}

var (
	filePattern    = regexp.MustCompile("(?:^|[\\s(\"'`])((?:[\\w.-]+/)+(?:[\\w.-]+)(?:\\.(?:go|js|jsx|ts|tsx|md|sql|json|yaml|yml|scss|css))?)")
	fileExtPattern = regexp.MustCompile(`\.(go|js|jsx|ts|tsx|md|sql|json|yaml|yml|scss|css)$`)
)

func relevantFiles(facts []store.ClaimResult, docs []store.DocumentResult, files []string, changedFiles []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(value string) {
		value = strings.Trim(value, " .,;:)]}\"'")
		if value == "" || strings.Contains(value, "://") || strings.Contains(value, "../") || strings.HasPrefix(value, "/") || strings.HasPrefix(value, ".") {
			return
		}
		if !looksLikeRepoPath(value) {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range append(files, changedFiles...) {
		add(value)
	}
	for _, claim := range facts {
		for _, match := range filePattern.FindAllStringSubmatch(claim.Claim, -1) {
			add(match[1])
		}
	}
	for _, doc := range docs {
		for _, match := range filePattern.FindAllStringSubmatch(doc.Source+" "+doc.Content, -1) {
			add(match[1])
		}
	}
	sort.Strings(out)
	if len(out) > 30 {
		return out[:30]
	}
	return out
}

func looksLikeRepoPath(value string) bool {
	if fileExtPattern.MatchString(value) {
		return true
	}
	for _, prefix := range []string{"src/", "internal/", "cmd/", "frontend/", "migrations/", "scripts/", "deploy/", "examples/", "docs/", "test/", "tests/"} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func risks(facts []store.ClaimResult, graph []store.RelationResult, conflicts []store.ConflictResult, retrievalWarnings []RetrievalWarning, graphWarnings []GraphWarning) []string {
	out := []string{}
	stale := 0
	challenged := 0
	unverified := 0
	for _, fact := range facts {
		if fact.Freshness == "stale" || fact.Freshness == "expired" {
			stale++
		}
		if fact.Status == "challenged" {
			challenged++
		}
		if fact.Status == "unverified" {
			unverified++
		}
	}
	if stale > 0 {
		out = append(out, "Some recalled facts are stale or expired; verify source freshness before acting.")
	}
	if challenged > 0 {
		out = append(out, "Some recalled facts are challenged; do not treat them as authoritative without source review.")
	}
	if unverified > 0 {
		out = append(out, "Unverified claims were included; use them only as leads, not proof.")
	}
	if len(conflicts) > 0 {
		out = append(out, "Active memory conflicts surfaced; treat contradictory claims or graph relations as unsafe until resolved.")
	}
	if len(retrievalWarnings) > 0 {
		out = append(out, "Some retrieval branches failed; treat the packet as degraded and rerun retrieval before autonomous work.")
	}
	if len(graphWarnings) > 0 {
		out = append(out, "Graph warnings surfaced competing or opposing relations; review graph evidence before autonomous work.")
	}
	if len(graph) == 0 {
		out = append(out, "No graph relations matched the task; cross-file impact may be underexplored.")
	}
	if len(out) == 0 {
		out = append(out, "No stale, challenged, or unverified memory surfaced in this packet.")
	}
	return out
}

func unavailableMemoryHealth(scope string, err error) store.MemoryHealthResult {
	message := "memory health could not be checked"
	if err != nil {
		message = "memory health could not be checked: " + compactError(err)
	}
	return store.MemoryHealthResult{
		Scope:   scope,
		Status:  "critical",
		Score:   0,
		Reasons: []string{message},
		Signals: []store.MemoryHealthSignal{
			{
				Code:        "memory_health_unavailable",
				Category:    "readiness",
				Severity:    "critical",
				Count:       1,
				ScoreImpact: 100,
				Message:     message,
				Action:      "check_memory_health_endpoint_and_storage",
			},
		},
	}
}

func applyMemoryHealthRisks(risks []string, health store.MemoryHealthResult) []string {
	status := strings.TrimSpace(health.Status)
	if status == "" || status == "healthy" {
		return risks
	}
	cleaned := risks[:0]
	for _, risk := range risks {
		if risk == "No stale, challenged, or unverified memory surfaced in this packet." {
			continue
		}
		cleaned = append(cleaned, risk)
	}
	for _, signal := range health.Signals {
		if signal.Severity == "critical" || signal.Severity == "warning" {
			cleaned = append(cleaned, "Memory health "+signal.Severity+" signal "+signal.Code+": "+signal.Action+".")
		}
	}
	if len(cleaned) == 0 {
		cleaned = append(cleaned, "Memory health is "+status+"; review health signals before autonomous work.")
	}
	return appendUnique(cleaned)
}

func evidence(facts []store.ClaimResult, docs []store.DocumentResult) []EvidenceItem {
	bySource := map[string]EvidenceItem{}
	for _, fact := range facts {
		if fact.Source == nil || strings.TrimSpace(*fact.Source) == "" {
			continue
		}
		item := bySource[*fact.Source]
		item.SourceURL = *fact.Source
		item.Count++
		bySource[*fact.Source] = item
	}
	for _, doc := range docs {
		if strings.TrimSpace(doc.Source) == "" {
			continue
		}
		item := bySource[doc.Source]
		item.SourceURL = doc.Source
		item.Title = doc.Title
		item.Count++
		bySource[doc.Source] = item
	}
	out := make([]EvidenceItem, 0, len(bySource))
	for _, item := range bySource {
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count == out[j].Count {
			return out[i].SourceURL < out[j].SourceURL
		}
		return out[i].Count > out[j].Count
	})
	if len(out) > 20 {
		return out[:20]
	}
	return out
}

type impactAccumulator struct {
	kind      string
	name      string
	score     float64
	reasons   map[string]struct{}
	sources   map[string]struct{}
	relations int
	summaries int
	facts     int
}

func impactMap(input ComposeInput, summaries []store.MemorySummaryResult, facts []store.ClaimResult, docs []store.DocumentResult, graph []store.RelationResult, relevant []string) []ImpactItem {
	items := map[string]*impactAccumulator{}
	add := func(kind, name, reason string, score float64, sources []string) {
		kind = strings.TrimSpace(kind)
		name = strings.TrimSpace(name)
		reason = strings.TrimSpace(reason)
		if kind == "" || name == "" {
			return
		}
		key := strings.ToLower(kind + "\x00" + name)
		item := items[key]
		if item == nil {
			item = &impactAccumulator{
				kind:    kind,
				name:    name,
				reasons: map[string]struct{}{},
				sources: map[string]struct{}{},
			}
			items[key] = item
		}
		item.score += score
		if reason != "" {
			item.reasons[reason] = struct{}{}
		}
		for _, source := range sources {
			source = strings.TrimSpace(source)
			if source != "" {
				item.sources[source] = struct{}{}
			}
		}
	}
	incRelation := func(kind, name string) {
		if item := items[strings.ToLower(kind+"\x00"+name)]; item != nil {
			item.relations++
		}
	}
	incSummary := func(kind, name string) {
		if item := items[strings.ToLower(kind+"\x00"+name)]; item != nil {
			item.summaries++
		}
	}
	incFact := func(kind, name string) {
		if item := items[strings.ToLower(kind+"\x00"+name)]; item != nil {
			item.facts++
		}
	}

	for _, file := range append(input.Files, input.ChangedFiles...) {
		add("file", file, "provided as task file context", 0.45, nil)
	}
	for _, file := range relevant {
		add("file", file, "mentioned by recalled facts or supporting documents", 0.35, sourcesForName(file, facts, docs))
		incFact("file", file)
	}
	for _, summary := range summaries {
		kind := impactKind(summary.Level, summary.Key)
		score := 0.25 + minFloat(summary.Rank, 1)*0.35
		add(kind, summary.Key, "matched hierarchical summary", score, summary.SourceURLs)
		incSummary(kind, summary.Key)
	}
	for _, relation := range graph {
		source := pointerString(relation.SourceURL)
		fromKind := impactKind("", relation.FromEntity)
		toKind := impactKind("", relation.ToEntity)
		reason := "connected by graph relation " + relation.Type
		score := 0.3 + minFloat(relation.Confidence, 1)*0.45
		add(fromKind, relation.FromEntity, reason, score, []string{source})
		add(toKind, relation.ToEntity, reason, score, []string{source})
		incRelation(fromKind, relation.FromEntity)
		incRelation(toKind, relation.ToEntity)
	}
	for _, fact := range facts {
		source := pointerString(fact.Source)
		for _, match := range filePattern.FindAllStringSubmatch(fact.Claim, -1) {
			if len(match) > 1 && looksLikeRepoPath(match[1]) {
				add("file", match[1], "mentioned by source-backed fact", 0.35, []string{source})
				incFact("file", match[1])
			}
		}
	}

	out := make([]ImpactItem, 0, len(items))
	for _, item := range items {
		out = append(out, ImpactItem{
			Kind:            item.kind,
			Name:            item.name,
			Confidence:      impactConfidence(item),
			Reasons:         sortedSet(item.reasons, 4),
			EvidenceSources: sortedSet(item.sources, 5),
			RelationCount:   item.relations,
			SummaryCount:    item.summaries,
			FactCount:       item.facts,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Confidence == out[j].Confidence {
			if out[i].Kind == out[j].Kind {
				return out[i].Name < out[j].Name
			}
			return out[i].Kind < out[j].Kind
		}
		return out[i].Confidence > out[j].Confidence
	})
	if len(out) > 20 {
		return out[:20]
	}
	return out
}

func impactKind(level, name string) string {
	level = strings.TrimSpace(level)
	switch level {
	case "file", "repo", "module", "route", "component", "symbol", "package", "source", "decision":
		return level
	}
	name = strings.TrimSpace(name)
	switch {
	case strings.HasPrefix(name, "/"):
		return "route"
	case looksLikeImpactFile(name):
		return "file"
	case strings.Contains(name, "/") && !strings.Contains(name, "."):
		return "module"
	default:
		return "entity"
	}
}

func looksLikeImpactFile(name string) bool {
	if !looksLikeRepoPath(name) {
		return false
	}
	if strings.Contains(name, "/") {
		return true
	}
	for _, exact := range []string{"package.json", "go.mod", "go.sum", "Dockerfile"} {
		if name == exact {
			return true
		}
	}
	return false
}

func impactConfidence(item *impactAccumulator) float64 {
	score := item.score
	score += float64(item.relations) * 0.08
	score += float64(item.summaries) * 0.07
	score += float64(item.facts) * 0.06
	if len(item.sources) > 0 {
		score += 0.08
	}
	return round2(minFloat(score, 1))
}

func sourcesForName(name string, facts []store.ClaimResult, docs []store.DocumentResult) []string {
	needle := strings.ToLower(strings.TrimSpace(name))
	if needle == "" {
		return nil
	}
	sources := map[string]struct{}{}
	for _, fact := range facts {
		if strings.Contains(strings.ToLower(fact.Claim), needle) {
			if source := pointerString(fact.Source); source != "" {
				sources[source] = struct{}{}
			}
		}
	}
	for _, doc := range docs {
		if strings.Contains(strings.ToLower(doc.Source+" "+doc.Content), needle) && strings.TrimSpace(doc.Source) != "" {
			sources[doc.Source] = struct{}{}
		}
	}
	return sortedSet(sources, 5)
}

func sortedSet(values map[string]struct{}, limit int) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	if limit > 0 && len(out) > limit {
		return out[:limit]
	}
	return out
}

func pointerString(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func validationPlan(input ComposeInput, result ComposeResult) []ValidationStep {
	targets := validationTargets(input, result)
	steps := []ValidationStep{}
	add := func(step ValidationStep) {
		step.Name = strings.TrimSpace(step.Name)
		step.Type = strings.TrimSpace(step.Type)
		step.Command = strings.TrimSpace(step.Command)
		step.Reason = strings.TrimSpace(step.Reason)
		step.Targets = compactList(step.Targets)
		if step.Name == "" || step.Type == "" || step.Reason == "" {
			return
		}
		for _, existing := range steps {
			if existing.Type == step.Type && existing.Command == step.Command && existing.Name == step.Name {
				return
			}
		}
		steps = append(steps, step)
	}

	if result.Verification.ActionRequired || result.AgentDecision.ReviewRequired {
		add(ValidationStep{
			Name:     "Review memory gate",
			Type:     "memory_gate",
			Reason:   "Verification, memory health, or stored agent policy requires review before autonomous work.",
			Targets:  gateTargets(result),
			Priority: 1,
			Required: true,
		})
	}
	if touchesGo(input, targets) {
		add(ValidationStep{
			Name:     "Run Go tests",
			Type:     "test",
			Command:  "go test ./...",
			Reason:   "Go files or Go service areas appear in the task impact set.",
			Targets:  filterTargets(targets, goTarget),
			Priority: 2,
			Required: true,
		})
	}
	if touchesJavaScript(input, targets) {
		add(ValidationStep{
			Name:     "Run package tests",
			Type:     "test",
			Command:  "npm test",
			Reason:   "JavaScript or TypeScript files appear in the task impact set.",
			Targets:  filterTargets(targets, jsTarget),
			Priority: 2,
			Required: true,
		})
		if result.Intent == "migration" || result.Intent == "implementation" {
			add(ValidationStep{
				Name:     "Run package build",
				Type:     "build",
				Command:  "npm run build",
				Reason:   "Implementation or migration work should verify frontend/package build compatibility when a build script exists.",
				Targets:  filterTargets(targets, jsTarget),
				Priority: 3,
				Required: false,
			})
		}
	}
	if touchesDocker(targets) {
		add(ValidationStep{
			Name:     "Validate Docker Compose config",
			Type:     "config",
			Command:  "docker compose config",
			Reason:   "Docker Compose files appear in the task impact set.",
			Targets:  filterTargets(targets, dockerTarget),
			Priority: 3,
			Required: true,
		})
	}
	if touchesHelm(targets) {
		add(ValidationStep{
			Name:     "Render Helm chart",
			Type:     "config",
			Command:  "helm template abra deploy/helm",
			Reason:   "Helm chart files appear in the task impact set.",
			Targets:  filterTargets(targets, helmTarget),
			Priority: 3,
			Required: true,
		})
	}
	if len(steps) == 0 {
		add(ValidationStep{
			Name:     "Source review",
			Type:     "review",
			Reason:   "No deterministic test command was inferred; inspect cited evidence and impacted files before acting.",
			Targets:  targets,
			Priority: 4,
			Required: true,
		})
	}
	sort.SliceStable(steps, func(i, j int) bool {
		if steps[i].Priority == steps[j].Priority {
			return steps[i].Name < steps[j].Name
		}
		return steps[i].Priority < steps[j].Priority
	})
	if len(steps) > 8 {
		return steps[:8]
	}
	return steps
}

func validationTargets(input ComposeInput, result ComposeResult) []string {
	seen := map[string]struct{}{}
	out := []string{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, value := range append(input.Files, input.ChangedFiles...) {
		add(value)
	}
	for _, value := range result.RelevantFiles {
		add(value)
	}
	for _, item := range result.ImpactMap {
		if item.Kind == "file" || item.Kind == "module" || item.Kind == "repo" {
			add(item.Name)
		}
	}
	sort.Strings(out)
	if len(out) > 30 {
		return out[:30]
	}
	return out
}

func gateTargets(result ComposeResult) []string {
	targets := []string{}
	targets = append(targets, result.Verification.UnverifiedClaims...)
	targets = append(targets, result.Verification.StaleClaims...)
	targets = append(targets, result.Verification.ChallengedClaims...)
	targets = append(targets, result.Verification.MissingEvidenceClaims...)
	targets = append(targets, result.Verification.ConflictClaims...)
	for _, conflict := range result.Conflicts {
		targets = append(targets, conflict.ID)
	}
	for _, warning := range result.GraphWarnings {
		targets = append(targets, warning.Entity)
		for _, relation := range warning.Relations {
			targets = append(targets, relation.FromEntity, relation.ToEntity)
		}
	}
	return compactList(targets)
}

func touchesGo(input ComposeInput, targets []string) bool {
	if strings.Contains(strings.ToLower(input.Language), "go") {
		return true
	}
	for _, target := range targets {
		if goTarget(target) {
			return true
		}
	}
	return false
}

func touchesJavaScript(input ComposeInput, targets []string) bool {
	language := strings.ToLower(input.Language)
	if strings.Contains(language, "javascript") || strings.Contains(language, "typescript") || strings.Contains(language, "react") || strings.Contains(language, "node") {
		return true
	}
	for _, target := range targets {
		if jsTarget(target) {
			return true
		}
	}
	return false
}

func touchesDocker(targets []string) bool {
	for _, target := range targets {
		if dockerTarget(target) {
			return true
		}
	}
	return false
}

func touchesHelm(targets []string) bool {
	for _, target := range targets {
		if helmTarget(target) {
			return true
		}
	}
	return false
}

func filterTargets(targets []string, predicate func(string) bool) []string {
	out := []string{}
	for _, target := range targets {
		if predicate(target) {
			out = append(out, target)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func goTarget(target string) bool {
	return strings.HasSuffix(target, ".go") || target == "go.mod" || target == "go.sum" || strings.HasPrefix(target, "cmd/") || strings.HasPrefix(target, "internal/")
}

func jsTarget(target string) bool {
	switch filepath.Ext(target) {
	case ".js", ".jsx", ".ts", ".tsx", ".json":
		return true
	default:
		return target == "package.json" || strings.HasPrefix(target, "frontend/") || strings.HasPrefix(target, "src/")
	}
}

func dockerTarget(target string) bool {
	base := strings.ToLower(filepath.Base(target))
	return base == "dockerfile" || strings.HasPrefix(base, "docker-compose") || strings.Contains(base, "compose.")
}

func helmTarget(target string) bool {
	return strings.HasPrefix(target, "deploy/helm/") || strings.HasSuffix(target, "Chart.yaml")
}

func suggestedSteps(input ComposeInput, intent string, result ComposeResult) []string {
	steps := []string{"Review the top facts and evidence sources before changing behavior."}
	switch intent {
	case "migration":
		steps = append(steps, "Check package/runtime compatibility and identify dependency blockers before bumping versions.")
	case "debugging":
		steps = append(steps, "Trace the highest-confidence graph relations and inspect the most relevant files first.")
	case "architecture":
		steps = append(steps, "Use hierarchical summaries for the global answer, then cite specific facts and source chunks.")
	default:
		steps = append(steps, "Use the relevant files list as the initial edit/read set, then expand through graph relations.")
	}
	if len(result.Risks) > 0 && !strings.HasPrefix(result.Risks[0], "No stale") {
		steps = append(steps, "Resolve stale, challenged, or unverified memory before presenting conclusions as facts.")
	}
	if result.Verification.ActionRequired {
		steps = append(steps, "Treat the verification report as a gate before using this packet for autonomous changes.")
	}
	if result.MemoryHealth.Status != "" && result.MemoryHealth.Status != "healthy" {
		steps = append(steps, "Inspect memory health signals before using this packet for autonomous changes.")
	}
	if len(input.ChangedFiles) > 0 {
		steps = append(steps, "Run focused validation for changed files and compare against recalled verification expectations.")
	}
	return steps
}

func compactList(values []string) []string {
	out := []string{}
	for _, value := range values {
		value = strings.Join(strings.Fields(value), " ")
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
