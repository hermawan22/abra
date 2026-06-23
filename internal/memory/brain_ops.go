package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

type BrainReviewInput struct {
	Scope string `json:"scope"`
	Limit int    `json:"limit,omitempty"`
}

type BrainReviewResult struct {
	Scope              string               `json:"scope"`
	Status             string               `json:"status"`
	Score              int                  `json:"score"`
	ReviewItems        []BrainReviewItem    `json:"review_items"`
	RecommendedActions []string             `json:"recommended_actions"`
	Metrics            map[string]any       `json:"metrics"`
	Scorecard          BrainScorecardResult `json:"scorecard"`
}

type BrainReviewItem struct {
	Code            string `json:"code"`
	Category        string `json:"category"`
	Severity        string `json:"severity"`
	Count           int    `json:"count,omitempty"`
	Message         string `json:"message"`
	SuggestedAction string `json:"suggested_action,omitempty"`
}

type BrainScorecardInput struct {
	Scope string `json:"scope"`
	Limit int    `json:"limit,omitempty"`
}

type BrainScorecardResult struct {
	Scope      string                   `json:"scope"`
	Status     string                   `json:"status"`
	Score      int                      `json:"score"`
	Categories []BrainScorecardCategory `json:"categories"`
	Metrics    map[string]any           `json:"metrics"`
	Health     store.MemoryHealthResult `json:"memory_health"`
}

type BrainScorecardCategory struct {
	Name    string `json:"name"`
	Score   int    `json:"score"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type AnchorBackfillInput struct {
	Scope     string `json:"scope"`
	Limit     int    `json:"limit,omitempty"`
	DryRun    bool   `json:"dry_run"`
	Propose   bool   `json:"propose,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
}

type AnchorBackfillResult struct {
	Scope              string                         `json:"scope"`
	Status             string                         `json:"status"`
	Score              int                            `json:"score"`
	DryRun             bool                           `json:"dry_run"`
	Propose            bool                           `json:"propose"`
	Checked            int                            `json:"checked"`
	CheckedClaims      int                            `json:"checked_claims"`
	Candidates         []AnchorBackfillCandidate      `json:"candidates"`
	Proposals          []store.LearningProposalRecord `json:"proposals"`
	RecommendedActions []string                       `json:"recommended_actions,omitempty"`
	Warning            string                         `json:"warning,omitempty"`
}

type AnchorBackfillCandidate struct {
	ClaimID         string  `json:"claim_id"`
	Claim           string  `json:"claim_text"`
	SourceURL       string  `json:"source_url,omitempty"`
	DocumentID      string  `json:"document_id,omitempty"`
	DocumentTitle   string  `json:"document_title,omitempty"`
	Freshness       string  `json:"freshness,omitempty"`
	Quote           string  `json:"quote,omitempty"`
	StartChar       int     `json:"start_char,omitempty"`
	EndChar         int     `json:"end_char,omitempty"`
	Score           float64 `json:"score,omitempty"`
	Action          string  `json:"action"`
	ProposalID      string  `json:"proposal_id,omitempty"`
	ProposalCreated bool    `json:"proposal_created,omitempty"`
}

type BrainMaintenanceInput struct {
	Scope     string `json:"scope"`
	Limit     int    `json:"limit,omitempty"`
	DryRun    bool   `json:"dry_run"`
	Propose   bool   `json:"propose,omitempty"`
	CreatedBy string `json:"created_by,omitempty"`
}

type BrainMaintenanceResult struct {
	Scope              string                         `json:"scope"`
	Status             string                         `json:"status"`
	Score              int                            `json:"score"`
	DryRun             bool                           `json:"dry_run"`
	Propose            bool                           `json:"propose"`
	Review             BrainReviewResult              `json:"review"`
	AnchorBackfill     AnchorBackfillResult           `json:"anchor_backfill,omitempty"`
	Proposals          []store.LearningProposalRecord `json:"proposals"`
	RecommendedActions []string                       `json:"recommended_actions"`
	Warning            string                         `json:"warning,omitempty"`
}

type evidenceAnchorCandidateLister interface {
	ListEvidenceAnchorCandidates(ctx context.Context, scope string, limit int) ([]store.EvidenceAnchorCandidate, error)
}

type evidenceAnchorCandidateCounter interface {
	CountEvidenceAnchorCandidates(ctx context.Context, scope string) (int, error)
}

type learningProposalCreator interface {
	CreateLearningProposalOnce(ctx context.Context, input store.CreateLearningProposalInput) (store.LearningProposalRecord, bool, error)
}

type brainEvalRunLister interface {
	ListBrainEvalRuns(ctx context.Context, scope string, limit int) ([]store.BrainEvalRunResult, error)
}

func (c *Composer) BrainReview(ctx context.Context, input BrainReviewInput) (BrainReviewResult, error) {
	scope := strings.TrimSpace(input.Scope)
	if scope == "" {
		return BrainReviewResult{}, fmt.Errorf("scope is required")
	}
	scorecard, err := c.BrainScorecard(ctx, BrainScorecardInput{Scope: scope, Limit: input.Limit})
	if err != nil {
		return BrainReviewResult{}, err
	}
	health := scorecard.Health
	items := reviewItemsFromHealth(health)
	if weak := weakAnchorCount(scorecard.Metrics); weak > 0 {
		items = append(items, BrainReviewItem{
			Code:            "weak_evidence_anchors",
			Category:        "anchors",
			Severity:        "medium",
			Count:           weak,
			Message:         "verified or inferred claims are missing same-source quote evidence; anchor missing, synthesis blocked",
			SuggestedAction: "call MCP brain_anchor_backfill with dry_run=true for scope " + scope,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if severityWeight(items[i].Severity) != severityWeight(items[j].Severity) {
			return severityWeight(items[i].Severity) > severityWeight(items[j].Severity)
		}
		return items[i].Code < items[j].Code
	})
	actions := recommendedBrainActions(scope, items)
	status := scorecard.Status
	return BrainReviewResult{
		Scope:              scope,
		Status:             status,
		Score:              scorecard.Score,
		ReviewItems:        items,
		RecommendedActions: actions,
		Metrics:            scorecard.Metrics,
		Scorecard:          scorecard,
	}, nil
}

func (c *Composer) BrainScorecard(ctx context.Context, input BrainScorecardInput) (BrainScorecardResult, error) {
	scope := strings.TrimSpace(input.Scope)
	if scope == "" {
		return BrainScorecardResult{}, fmt.Errorf("scope is required")
	}
	health, _, err := c.memoryHealth(ctx, scope)
	if err != nil {
		return BrainScorecardResult{}, err
	}
	if strings.TrimSpace(health.Scope) == "" {
		health.Scope = scope
	}
	limit := input.Limit
	if limit < 1 {
		limit = 50
	}
	weakAnchors := 0
	if counter, ok := c.store.(evidenceAnchorCandidateCounter); ok {
		count, err := counter.CountEvidenceAnchorCandidates(ctx, scope)
		if err != nil {
			return BrainScorecardResult{}, err
		}
		weakAnchors = count
	} else if lister, ok := c.store.(evidenceAnchorCandidateLister); ok {
		candidates, err := lister.ListEvidenceAnchorCandidates(ctx, scope, limit)
		if err != nil {
			return BrainScorecardResult{}, err
		}
		weakAnchors = len(candidates)
	}
	evalCategory, evalMetrics, err := c.evalScorecardCategory(ctx, scope)
	if err != nil {
		return BrainScorecardResult{}, err
	}
	verifiedLike := health.Claims.Verified + health.Claims.Inferred
	categories := []BrainScorecardCategory{
		brainScorecardCategory("evidence", ratioScore(health.Claims.WithEvidence, maxInt(verifiedLike, 1)), "claims with stored evidence"),
		brainScorecardCategory("anchors", anchorScore(verifiedLike, weakAnchors), "same-source quote/span anchor coverage"),
		brainScorecardCategory("retrieval", health.Score, "memory health and retrieval readiness"),
		brainScorecardCategory("freshness", freshnessScore(health), "stale or expired memory pressure"),
		brainScorecardCategory("conflicts", conflictScore(health), "open or blocking conflicts"),
		brainScorecardCategory("graph", graphScore(health), "active temporal entity graph coverage"),
		brainScorecardCategory("learning", learningScore(health), "pending and duplicate learning proposals"),
		evalCategory,
	}
	total := 0
	for _, category := range categories {
		total += category.Score
	}
	score := total / len(categories)
	status := scoreStatus(score)
	if health.Status == "critical" {
		status = "critical"
	}
	metrics := map[string]any{
		"verified_claims":        health.Claims.Verified,
		"inferred_claims":        health.Claims.Inferred,
		"claims_with_evidence":   health.Claims.WithEvidence,
		"weak_anchor_claims":     weakAnchors,
		"stale_claims":           health.Claims.Stale,
		"expired_claims":         health.Claims.Expired,
		"open_conflicts":         health.Conflicts.Open,
		"blocking_conflicts":     health.Conflicts.Blocking,
		"active_entities":        health.Graph.ActiveEntities,
		"active_relations":       health.Graph.ActiveRelations,
		"stale_relations":        health.Graph.StaleRelations,
		"source_due":             health.Sources.Due,
		"source_overdue":         health.Sources.Overdue,
		"pending_learning":       health.Learning.Pending,
		"duplicate_learning":     health.Learning.DuplicatePendingGroups,
		"summary_token_estimate": health.Summaries.TokenEstimate,
	}
	for key, value := range evalMetrics {
		metrics[key] = value
	}
	return BrainScorecardResult{
		Scope:      scope,
		Status:     status,
		Score:      score,
		Categories: categories,
		Metrics:    metrics,
		Health:     health,
	}, nil
}

func (c *Composer) evalScorecardCategory(ctx context.Context, scope string) (BrainScorecardCategory, map[string]any, error) {
	lister, ok := c.store.(brainEvalRunLister)
	if !ok {
		return brainScorecardCategory("eval", 80, "eval runner is available; persisted eval history is not stored"), map[string]any{"eval_runs": 0}, nil
	}
	runs, err := lister.ListBrainEvalRuns(ctx, scope, 10)
	if err != nil {
		return BrainScorecardCategory{}, nil, err
	}
	if len(runs) == 0 {
		return brainScorecardCategory("eval", 80, "eval runner is available; persisted eval history is not stored"), map[string]any{"eval_runs": 0}, nil
	}
	latest := runs[0]
	score := ratioScore(latest.Passed, maxInt(latest.Total, 1))
	message := "latest persisted eval run passed"
	if !latest.Success || latest.Passed < latest.Total {
		message = "latest persisted eval run has failures"
	}
	return brainScorecardCategory("eval", score, message), map[string]any{
		"eval_runs":           len(runs),
		"eval_latest_id":      latest.ID,
		"eval_latest_total":   latest.Total,
		"eval_latest_passed":  latest.Passed,
		"eval_latest_success": latest.Success,
		"eval_latest_at":      latest.CreatedAt,
	}, nil
}

func (c *Composer) AnchorBackfill(ctx context.Context, input AnchorBackfillInput) (AnchorBackfillResult, error) {
	scope := strings.TrimSpace(input.Scope)
	if scope == "" {
		return AnchorBackfillResult{}, fmt.Errorf("scope is required")
	}
	limit := input.Limit
	if limit < 1 || limit > 200 {
		limit = 50
	}
	result := AnchorBackfillResult{
		Scope:      scope,
		Status:     "healthy",
		Score:      100,
		DryRun:     input.DryRun || !input.Propose,
		Propose:    input.Propose,
		Candidates: []AnchorBackfillCandidate{},
		Proposals:  []store.LearningProposalRecord{},
	}
	lister, ok := c.store.(evidenceAnchorCandidateLister)
	if !ok {
		result.Warning = "anchor candidate lookup is unavailable for this store"
		result.Status = "needs_review"
		result.Score = 80
		return result, nil
	}
	candidates, err := lister.ListEvidenceAnchorCandidates(ctx, scope, limit)
	if err != nil {
		return AnchorBackfillResult{}, err
	}
	result.Checked = len(candidates)
	result.CheckedClaims = len(candidates)
	creator, canPropose := c.store.(learningProposalCreator)
	if input.Propose && !canPropose {
		result.Warning = "learning proposal writer is unavailable; returning dry-run candidates"
		result.Status = "needs_review"
		result.Score = 80
		result.DryRun = true
	}
	for _, candidate := range candidates {
		quote, start, end, score := anchorQuoteForClaim(candidate.Claim, candidate.DocumentChunk)
		item := AnchorBackfillCandidate{
			ClaimID:       candidate.ClaimID,
			Claim:         candidate.Claim,
			SourceURL:     candidate.SourceURL,
			DocumentID:    candidate.DocumentID,
			DocumentTitle: candidate.DocumentTitle,
			Freshness:     candidate.Freshness,
			Quote:         quote,
			StartChar:     start,
			EndChar:       end,
			Score:         round2(score),
			Action:        "propose_text_span_anchor",
		}
		if quote == "" {
			item.Action = "needs_source_text"
		}
		if input.Propose && canPropose && quote != "" && !result.DryRun {
			proposal, created, err := creator.CreateLearningProposalOnce(ctx, store.CreateLearningProposalInput{
				Scope:        scope,
				ProposalType: "claim",
				Title:        "Add evidence anchor for claim " + candidate.ClaimID,
				Rationale:    "A verified or inferred claim is missing same-source quote evidence. Proposal ready for review; do not promote truth without approval.",
				TargetType:   "claim",
				TargetID:     candidate.ClaimID,
				SourceURL:    candidate.SourceURL,
				Confidence:   score,
				CreatedBy:    input.CreatedBy,
				Payload: map[string]any{
					"action":      "add_evidence_anchor",
					"claim_id":    candidate.ClaimID,
					"source_url":  candidate.SourceURL,
					"document_id": candidate.DocumentID,
					"quote":       quote,
					"start_char":  start,
					"end_char":    end,
					"score":       round2(score),
				},
			})
			if err != nil {
				return AnchorBackfillResult{}, err
			}
			item.ProposalID = proposal.ID
			item.ProposalCreated = created
			if created {
				result.Proposals = append(result.Proposals, proposal)
			}
		}
		result.Candidates = append(result.Candidates, item)
	}
	if len(result.Candidates) > 0 {
		result.Status = "needs_review"
		result.Score = 75
		result.RecommendedActions = append(result.RecommendedActions, "review anchor candidates before enabling synthesis-heavy flows")
		if !input.Propose {
			result.RecommendedActions = append(result.RecommendedActions, "call MCP brain_anchor_backfill with propose=true after operator review")
		}
	}
	return result, nil
}

func (c *Composer) BrainMaintain(ctx context.Context, input BrainMaintenanceInput) (BrainMaintenanceResult, error) {
	scope := strings.TrimSpace(input.Scope)
	if scope == "" {
		return BrainMaintenanceResult{}, fmt.Errorf("scope is required")
	}
	review, err := c.BrainReview(ctx, BrainReviewInput{Scope: scope, Limit: input.Limit})
	if err != nil {
		return BrainMaintenanceResult{}, err
	}
	result := BrainMaintenanceResult{
		Scope:              scope,
		Status:             review.Status,
		Score:              review.Score,
		DryRun:             input.DryRun || !input.Propose,
		Propose:            input.Propose,
		Review:             review,
		Proposals:          []store.LearningProposalRecord{},
		RecommendedActions: append([]string(nil), review.RecommendedActions...),
	}
	anchorResult, err := c.AnchorBackfill(ctx, AnchorBackfillInput{
		Scope:     scope,
		Limit:     input.Limit,
		DryRun:    result.DryRun,
		Propose:   input.Propose,
		CreatedBy: input.CreatedBy,
	})
	if err != nil {
		return BrainMaintenanceResult{}, err
	}
	result.AnchorBackfill = anchorResult
	result.Proposals = append(result.Proposals, anchorResult.Proposals...)
	if input.Propose && !result.DryRun {
		creator, ok := c.store.(learningProposalCreator)
		if !ok {
			result.Warning = "learning proposal writer is unavailable; maintenance stayed dry-run"
			result.DryRun = true
			return result, nil
		}
		for _, item := range review.ReviewItems {
			if item.Code == "weak_evidence_anchors" {
				continue
			}
			proposal, created, err := creator.CreateLearningProposalOnce(ctx, store.CreateLearningProposalInput{
				Scope:        scope,
				ProposalType: maintenanceProposalType(item),
				Title:        "Brain maintenance: " + item.Code,
				Rationale:    item.Message + " Proposal ready for review.",
				TargetType:   "memory_scope",
				TargetID:     scope,
				Confidence:   0.75,
				CreatedBy:    input.CreatedBy,
				Payload: map[string]any{
					"action":           "review_memory_health",
					"review_item":      item,
					"suggested_action": item.SuggestedAction,
				},
			})
			if err != nil {
				return BrainMaintenanceResult{}, err
			}
			if created {
				result.Proposals = append(result.Proposals, proposal)
			}
		}
	}
	return result, nil
}

func reviewItemsFromHealth(health store.MemoryHealthResult) []BrainReviewItem {
	items := []BrainReviewItem{}
	add := func(code, category, severity string, count int, message, action string) {
		if count <= 0 {
			return
		}
		items = append(items, BrainReviewItem{Code: code, Category: category, Severity: severity, Count: count, Message: message, SuggestedAction: action})
	}
	for _, signal := range health.Signals {
		if strings.TrimSpace(signal.Code) == "memory_ready" {
			continue
		}
		severity := firstNonEmptyString(signal.Severity, "medium")
		items = append(items, BrainReviewItem{
			Code:            signal.Code,
			Category:        firstNonEmptyString(signal.Category, "health"),
			Severity:        severity,
			Count:           signal.Count,
			Message:         signal.Message,
			SuggestedAction: signal.Action,
		})
	}
	add("stale_claims", "freshness", "medium", health.Claims.Stale, "claim is stale or expired; refresh source before autonomous use", "call MCP brain_maintain with dry_run=true for scope "+health.Scope)
	add("expired_claims", "freshness", "high", health.Claims.Expired, "claim is stale or expired; expired memory should not guide action", "call MCP brain_maintain with propose=true after operator review for scope "+health.Scope)
	add("challenged_claims", "governance", "high", health.Claims.Challenged, "challenged claims need review before use", "abra govern doctor --scope "+shellScope(health.Scope))
	add("unverified_claims", "governance", "medium", health.Claims.Unverified, "unverified claims are leads, not trusted memory", "abra govern observe --scope "+shellScope(health.Scope))
	add("stale_relations", "graph", "medium", health.Graph.StaleRelations, "graph relation is stale; temporal entity graph needs refresh", "call MCP brain_maintain with propose=true after operator review for scope "+health.Scope)
	add("challenged_relations", "graph", "high", health.Graph.ChallengedRelations, "conflict blocks autonomous use for challenged graph relations", "call MCP brain_review for scope "+health.Scope)
	add("open_conflicts", "conflicts", "high", health.Conflicts.Open, "conflict blocks autonomous use until resolved", "abra govern doctor --scope "+shellScope(health.Scope))
	add("blocking_conflicts", "conflicts", "high", health.Conflicts.Blocking, "blocking conflict prevents safe agent handoff", "abra govern doctor --scope "+shellScope(health.Scope))
	add("source_refresh_due", "sources", "medium", health.Sources.Due, "source needs refresh", "abra sync status --scope "+shellScope(health.Scope))
	add("source_refresh_overdue", "sources", "high", health.Sources.Overdue, "source needs refresh and is overdue", "abra sync status --scope "+shellScope(health.Scope))
	add("source_errors", "sources", "high", health.Sources.Error, "source connector has errors", "abra brain doctor --scope "+shellScope(health.Scope))
	add("learning_duplicate_pending_groups", "learning", "high", health.Learning.DuplicatePendingGroups, "duplicate learning proposals need cleanup", "abra govern doctor --scope "+shellScope(health.Scope))
	if health.Summaries.Total == 0 {
		add("missing_summaries", "summaries", "medium", 1, "memory summaries are missing; context may be less token efficient", "abra govern approvals request --scope "+shellScope(health.Scope)+" --action backfill")
	}
	return items
}

func recommendedBrainActions(scope string, items []BrainReviewItem) []string {
	actions := []string{"call MCP brain_scorecard for scope " + scope}
	for _, item := range items {
		if strings.TrimSpace(item.SuggestedAction) != "" {
			actions = appendUnique(actions, item.SuggestedAction)
		}
	}
	if len(items) == 0 {
		actions = append(actions, "call MCP brain_think for task-specific recall")
	}
	return actions
}

func brainScorecardCategory(name string, score int, message string) BrainScorecardCategory {
	return BrainScorecardCategory{Name: name, Score: clampScore(score), Status: scoreStatus(score), Message: message}
}

func ratioScore(numerator, denominator int) int {
	if denominator <= 0 {
		return 100
	}
	return clampScore((numerator * 100) / denominator)
}

func anchorScore(verifiedLike, weak int) int {
	if verifiedLike <= 0 {
		return 100
	}
	return clampScore(100 - ((weak * 100) / verifiedLike))
}

func freshnessScore(health store.MemoryHealthResult) int {
	total := maxInt(health.Claims.Total+health.Graph.Relations+health.Sources.Total, 1)
	bad := health.Claims.Stale + health.Claims.Expired + health.Graph.StaleRelations + health.Sources.Overdue + health.Sources.Error
	return clampScore(100 - ((bad * 100) / total))
}

func conflictScore(health store.MemoryHealthResult) int {
	if health.Conflicts.Blocking > 0 {
		return 0
	}
	if health.Conflicts.Open > 0 || health.Conflicts.Reviewing > 0 {
		return 50
	}
	return 100
}

func graphScore(health store.MemoryHealthResult) int {
	if health.Graph.Relations == 0 && health.Graph.Entities == 0 {
		return 70
	}
	return ratioScore(health.Graph.ActiveRelations+health.Graph.ActiveEntities, maxInt(health.Graph.Relations+health.Graph.Entities, 1))
}

func learningScore(health store.MemoryHealthResult) int {
	if health.Learning.DuplicatePendingGroups > 0 {
		return 0
	}
	if health.Learning.Pending > 20 {
		return 60
	}
	if health.Learning.Pending > 0 {
		return 85
	}
	return 100
}

func scoreStatus(score int) string {
	switch {
	case score >= 90:
		return "healthy"
	case score >= 70:
		return "needs_review"
	default:
		return "critical"
	}
}

func clampScore(score int) int {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func weakAnchorCount(metrics map[string]any) int {
	return intFromAny(metrics["weak_anchor_claims"])
}

func intFromAny(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func severityWeight(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "critical", "high", "blocking":
		return 3
	case "medium", "warn", "warning":
		return 2
	case "low", "info":
		return 1
	default:
		return 0
	}
}

func maintenanceProposalType(item BrainReviewItem) string {
	switch item.Category {
	case "sources", "freshness":
		return "source_refresh"
	case "summaries":
		return "summary_rebuild"
	case "graph":
		return "graph"
	case "conflicts", "governance":
		return "challenge"
	default:
		return "other"
	}
}

func shellScope(scope string) string {
	if strings.ContainsAny(scope, " \t\n'\"") {
		return "'" + strings.ReplaceAll(scope, "'", "'\"'\"'") + "'"
	}
	return scope
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
