package memory

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hermawan22/abra/internal/store"
)

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

func (c *Composer) retrieveGraphSeeds(ctx context.Context, seeds []string, scope string, limit int, options store.RecallOptions) ([][]store.RelationResult, []RetrievalWarning, error) {
	if len(seeds) == 0 {
		return nil, nil, nil
	}

	results := make([][]store.RelationResult, len(seeds))
	warningsBySeed := make([][]RetrievalWarning, len(seeds))
	errs := make(chan error, len(seeds))
	sem := make(chan struct{}, minInt(c.graphConcurrency, len(seeds)))
	var wg sync.WaitGroup
	for i, seed := range seeds {
		i, seed := i, seed
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := acquireStageSlot(ctx, sem); err != nil {
				errs <- err
				return
			}
			defer releaseStageSlot(sem)
			expanded, err := c.relatedGraph(ctx, seed, scope, limit, options)
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
