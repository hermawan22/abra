package memory

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hermawan22/abra/internal/policy"
	"github.com/hermawan22/abra/internal/store"
)

func (c *Composer) composeSummaryLookups(ctx context.Context, input ComposeInput) (map[string]store.MemorySummaryResult, []RetrievalTraceItem, []RetrievalWarning, error) {
	summaries := map[string]store.MemorySummaryResult{}
	trace := []RetrievalTraceItem{}
	warnings := []RetrievalWarning{}

	stageStart := time.Now()
	coreSummaries, err := c.coreMemorySummaries(ctx, input)
	if err != nil {
		if isContextError(err) {
			return nil, nil, nil, err
		}
		warnings = append(warnings, RetrievalWarning{
			Stage:     "summaries",
			Operation: "core_memory_lookup",
			Query:     input.Agent,
			Message:   compactError(err),
		})
		coreSummaries = nil
	}
	trace = append(trace, newRetrievalTraceItem("summaries", "core_memory_lookup", false, 1, len(coreSummaries), stageStart, warningsFor(warnings, "summaries", "core_memory_lookup"), ""))
	mergeSummaryResults(summaries, coreSummaries)

	stageStart = time.Now()
	taskSummaries, err := c.store.ListMemorySummaries(ctx, input.Task, input.Scope, input.Limit)
	if err != nil {
		if isContextError(err) {
			return nil, nil, nil, err
		}
		warnings = append(warnings, RetrievalWarning{
			Stage:     "summaries",
			Operation: "task_summary_lookup",
			Query:     input.Task,
			Message:   compactError(err),
		})
		taskSummaries = nil
	}
	trace = append(trace, newRetrievalTraceItem("summaries", "task_summary_lookup", false, 1, len(taskSummaries), stageStart, warningsFor(warnings, "summaries", "task_summary_lookup"), ""))
	mergeSummaryResults(summaries, taskSummaries)
	return summaries, trace, warnings, nil
}

func (c *Composer) composeEvidenceAnchorDocuments(ctx context.Context, scope string, facts map[string]store.ClaimResult) ([]store.DocumentResult, RetrievalTraceItem, []RetrievalWarning, error) {
	stageStart := time.Now()
	anchorDocs, err := c.evidenceAnchorDocuments(ctx, scope, facts)
	warnings := []RetrievalWarning{}
	if err != nil {
		if isContextError(err) {
			return nil, RetrievalTraceItem{}, nil, err
		}
		warnings = append(warnings, RetrievalWarning{
			Stage:     "evidence",
			Operation: "source_anchor_lookup",
			Query:     scope,
			Message:   compactError(err),
		})
		anchorDocs = nil
	}
	trace := newRetrievalTraceItem("evidence", "source_anchor_lookup", false, len(facts), len(anchorDocs), stageStart, warnings, "")
	return anchorDocs, trace, warnings, nil
}

func mergeSummaryResults(target map[string]store.MemorySummaryResult, summaries []store.MemorySummaryResult) {
	for _, summary := range summaries {
		if existing, ok := target[summary.ID]; !ok || summary.Rank > existing.Rank {
			target[summary.ID] = summary
		}
	}
}

func newRetrievalTraceItem(stage, operation string, parallel bool, queryCount, resultCount int, started time.Time, stageWarnings []RetrievalWarning, cacheStatus string) RetrievalTraceItem {
	return RetrievalTraceItem{
		Stage:       stage,
		Operation:   operation,
		Parallel:    parallel,
		QueryCount:  queryCount,
		ResultCount: resultCount,
		DurationMS:  durationMS(started),
		Status:      traceStatus(stageWarnings),
		CacheStatus: cacheStatus,
		Error:       traceError(stageWarnings),
	}
}

func (c *Composer) retrieveQueries(ctx context.Context, queries []policy.RecallQuery, options store.RecallOptions) ([]retrievalResult, []RetrievalWarning, error) {
	if len(queries) == 0 {
		return nil, nil, nil
	}

	results := make([]retrievalResult, len(queries))
	warningsByQuery := make([][]RetrievalWarning, len(queries))
	errs := make(chan error, len(queries)*2)
	sem := make(chan struct{}, minInt(c.recallConcurrency, len(queries)))
	var wg sync.WaitGroup
	for i, query := range queries {
		i, query := i, query
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := acquireStageSlot(ctx, sem); err != nil {
				errs <- err
				return
			}
			defer releaseStageSlot(sem)
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
			recall, err := c.recall(ctx, query.Query, query.Scope, query.Limit, query.IncludeUnverified, options)
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
			warningsByQuery[i] = append(warningsByQuery[i], recallWarnings(recall.RetrievalWarnings)...)
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

func recallWarnings(values []store.RetrievalWarning) []RetrievalWarning {
	if len(values) == 0 {
		return nil
	}
	warnings := make([]RetrievalWarning, 0, len(values))
	for _, value := range values {
		warnings = append(warnings, RetrievalWarning{
			Stage:     strings.TrimSpace(value.Stage),
			Operation: strings.TrimSpace(value.Operation),
			Query:     strings.TrimSpace(value.Query),
			Message:   strings.TrimSpace(value.Message),
		})
	}
	return warnings
}

func (c *Composer) recall(ctx context.Context, query, scope string, limit int, includeUnverified bool, options store.RecallOptions) (store.RecallResult, error) {
	if optionStore, ok := c.store.(recallOptionsStore); ok {
		return optionStore.RecallWithOptions(ctx, query, scope, limit, includeUnverified, options)
	}
	return c.store.Recall(ctx, query, scope, limit, includeUnverified)
}

func (c *Composer) relatedGraph(ctx context.Context, query, scope string, limit int, options store.RecallOptions) ([]store.RelationResult, error) {
	if optionStore, ok := c.store.(graphOptionsStore); ok {
		return optionStore.RelatedGraphWithOptions(ctx, query, scope, limit, options)
	}
	return c.store.RelatedGraph(ctx, query, scope, limit)
}

func (c *Composer) coreMemorySummaries(ctx context.Context, input ComposeInput) ([]store.MemorySummaryResult, error) {
	levelStore, ok := c.store.(summaryLevelStore)
	if !ok {
		return nil, nil
	}
	limit := 4
	if input.Mode == RetrievalModeDeep {
		limit = 8
	}
	return levelStore.ListMemorySummariesByLevels(ctx, "", input.Scope, []string{"agent_core", "core", "shared"}, limit)
}

func (c *Composer) evidenceAnchorDocuments(ctx context.Context, scope string, facts map[string]store.ClaimResult) ([]store.DocumentResult, error) {
	sourceStore, ok := c.store.(evidenceAnchorDocumentStore)
	if !ok || len(facts) == 0 {
		return nil, nil
	}
	sourceByCanonical := map[string]string{}
	for _, fact := range facts {
		if fact.Source == nil || strings.TrimSpace(fact.Claim) == "" {
			continue
		}
		source := strings.TrimSpace(*fact.Source)
		canonical := canonicalSourceID(source)
		if canonical == "" {
			continue
		}
		sourceByCanonical[canonical] = source
	}
	if len(sourceByCanonical) == 0 {
		return nil, nil
	}
	sources := make([]string, 0, len(sourceByCanonical))
	for _, source := range sourceByCanonical {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	if len(sources) > evidenceAnchorSourcesMax {
		sources = sources[:evidenceAnchorSourcesMax]
	}
	return sourceStore.DocumentsBySource(ctx, scope, sources, evidenceAnchorDocsPerSource)
}

func recallOptionsFromInput(input ComposeInput) (store.RecallOptions, bool, string) {
	options := store.RecallOptions{IncludeHistorical: input.IncludeHistorical || input.Mode == RetrievalModeDeep}
	rawAsOf := strings.TrimSpace(input.AsOf)
	if rawAsOf == "" {
		return options, false, ""
	}
	asOf, err := time.Parse(time.RFC3339, rawAsOf)
	if err != nil {
		return options, false, "as_of must be RFC3339; temporal recall used current-time lifecycle filters"
	}
	options.AsOf = asOf.UTC()
	return options, true, ""
}

func acquireStageSlot(ctx context.Context, sem chan struct{}) error {
	select {
	case sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func releaseStageSlot(sem chan struct{}) {
	select {
	case <-sem:
	default:
	}
}
