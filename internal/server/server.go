package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/brain"
	"github.com/hermawan22/abra/internal/config"
	"github.com/hermawan22/abra/internal/memory"
	"github.com/hermawan22/abra/internal/observability"
	"github.com/hermawan22/abra/internal/policy"
	"github.com/hermawan22/abra/internal/store"
	"github.com/hermawan22/abra/internal/version"
	"go.opentelemetry.io/otel/attribute"
)

func New(cfg config.Config, db *store.Store) (http.Handler, error) {
	brainService, err := brain.New(cfg, db)
	if err != nil {
		return nil, err
	}
	memoryComposer := memory.NewComposerWithOptions(&composerStore{Store: db, brain: brainService}, memory.ComposerOptions{
		HealthCacheTTL:    cfg.ComposeHealthCacheTTL,
		RecallConcurrency: cfg.ComposeRecallConcurrency,
		GraphConcurrency:  cfg.ComposeGraphConcurrency,
	})
	mux := http.NewServeMux()
	metrics := newMetricsCollector()
	handler := &handler{cfg: cfg, db: db, brain: brainService, memory: memoryComposer, metrics: metrics}

	mux.HandleFunc("GET /", handler.index)
	mux.HandleFunc("GET /app", removedBrowserUI)
	mux.HandleFunc("GET /app/", removedBrowserUI)
	mux.HandleFunc("GET /healthz", handler.health)
	mux.HandleFunc("GET /readyz", handler.ready)
	mux.HandleFunc("GET /metrics", handler.auth(handler.metricsText))
	mux.HandleFunc("GET /audit/events", handler.auth(handler.auditEvents))
	mux.HandleFunc("POST /ingest/documents", handler.auth(handler.ingestDocument))
	mux.HandleFunc("GET /ingest/documents", methodNotAllowed("POST"))
	mux.HandleFunc("POST /ingest/webhooks", handler.auth(handler.ingestWebhook))
	mux.HandleFunc("GET /ingest/webhooks", methodNotAllowed("POST"))
	mux.HandleFunc("POST /recall", handler.auth(handler.recall))
	mux.HandleFunc("GET /recall", methodNotAllowed("POST"))
	mux.HandleFunc("POST /claims", handler.auth(handler.rememberClaim))
	mux.HandleFunc("GET /claims", methodNotAllowed("POST"))
	mux.HandleFunc("POST /observations", handler.auth(handler.captureObservation))
	mux.HandleFunc("GET /observations", handler.auth(handler.listObservations))
	mux.HandleFunc("POST /claims/{claimId}/challenge", handler.auth(handler.challengeClaim))
	mux.HandleFunc("POST /claims/{claimId}/forget", handler.auth(handler.forgetClaim))
	mux.HandleFunc("GET /conflicts", handler.auth(handler.listConflicts))
	mux.HandleFunc("POST /conflicts/{conflictId}/resolve", handler.auth(handler.resolveConflict))
	mux.HandleFunc("POST /sources", handler.auth(handler.sources))
	mux.HandleFunc("GET /sources", methodNotAllowed("POST"))
	mux.HandleFunc("POST /brain/think", handler.auth(handler.brainThink))
	mux.HandleFunc("GET /brain/think", methodNotAllowed("POST"))
	mux.HandleFunc("POST /memory/compose", handler.auth(handler.composeMemory))
	mux.HandleFunc("GET /memory/compose", methodNotAllowed("POST"))
	mux.HandleFunc("GET /memory/health", handler.auth(handler.memoryHealth))
	mux.HandleFunc("POST /memory/summaries", handler.auth(handler.memorySummaries))
	mux.HandleFunc("GET /memory/summaries", methodNotAllowed("POST"))
	mux.HandleFunc("POST /memory/summaries/rebuild", handler.auth(handler.rebuildMemorySummaries))
	mux.HandleFunc("GET /memory/summaries/rebuild", methodNotAllowed("POST"))
	mux.HandleFunc("GET /learning/proposals", handler.auth(handler.listLearningProposals))
	mux.HandleFunc("POST /learning/proposals", handler.auth(handler.createLearningProposal))
	mux.HandleFunc("POST /learning/proposals/{proposalId}/decide", handler.auth(handler.decideLearningProposal))
	mux.HandleFunc("GET /sources/configs", handler.auth(handler.listSourceConfigs))
	mux.HandleFunc("POST /sources/configs", handler.auth(handler.upsertSourceConfig))
	mux.HandleFunc("GET /ingestion/jobs", handler.auth(handler.listIngestionJobs))
	mux.HandleFunc("POST /ingestion/jobs", handler.auth(handler.enqueueIngestionJob))
	mux.HandleFunc("POST /ingestion/jobs/{jobId}/retry", handler.auth(handler.retryIngestionJob))
	mux.HandleFunc("POST /ingestion/jobs/{jobId}/cancel", handler.auth(handler.cancelIngestionJob))
	mux.HandleFunc("GET /approvals", handler.auth(handler.listApprovals))
	mux.HandleFunc("POST /approvals", handler.auth(handler.createApproval))
	mux.HandleFunc("POST /approvals/{approvalId}/approve", handler.auth(handler.approveApproval))
	mux.HandleFunc("POST /approvals/{approvalId}/reject", handler.auth(handler.rejectApproval))
	mux.HandleFunc("GET /acl/policies", handler.auth(handler.listACLPolicies))
	mux.HandleFunc("POST /acl/policies", handler.auth(handler.upsertACLPolicy))
	mux.HandleFunc("POST /acl/decision", handler.auth(handler.aclDecision))
	mux.HandleFunc("GET /agent/profiles", handler.auth(handler.listAgentProfiles))
	mux.HandleFunc("POST /agent/profiles", handler.auth(handler.upsertAgentProfile))
	mux.HandleFunc("GET /agent/policies", handler.auth(handler.listAgentActionPolicies))
	mux.HandleFunc("POST /agent/policies", handler.auth(handler.upsertAgentActionPolicy))
	mux.HandleFunc("POST /agent/policy/decision", handler.auth(handler.agentActionPolicyDecision))
	mux.HandleFunc("GET /graph/entities", handler.auth(handler.graphEntities))
	mux.HandleFunc("GET /graph/relations", handler.auth(handler.graphRelations))
	mux.HandleFunc("POST /policy/plan", handler.auth(handler.policyPlan))
	mux.HandleFunc("POST /mcp", handler.auth(handler.mcp))
	mux.HandleFunc("GET /mcp", methodNotAllowed("POST"))

	return requestID(observability.TraceHTTP(authGate(cfg, metrics.wrap(rateLimit(cfg, db, mux))))), nil
}

func removedBrowserUI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "browser_ui_not_shipped"})
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

type handler struct {
	cfg     config.Config
	db      *store.Store
	brain   *brain.Service
	memory  *memory.Composer
	metrics *metricsCollector
}

const (
	defaultScopeDiscoveryLimit    = 50
	maxScopeDiscoveryLimit        = 100
	maxScopeDiscoveryCandidateCap = 10000
)

func scopeDiscoveryLimits(requested int, principal *apiPrincipal) (int, int) {
	limit := requested
	if limit < 1 {
		limit = defaultScopeDiscoveryLimit
	}
	if limit > maxScopeDiscoveryLimit {
		limit = maxScopeDiscoveryLimit
	}
	candidateLimit := limit
	if principal != nil && !principal.allScopes {
		candidateLimit = maxScopeDiscoveryCandidateCap
	}
	return limit, candidateLimit
}

func rankScopeSummaries(scopes []store.ScopeSummary, expectedScope, query string) ([]store.ScopeSummary, []store.ScopeSummary, string) {
	expectedScope = strings.ToLower(strings.TrimSpace(expectedScope))
	query = strings.ToLower(strings.TrimSpace(query))
	type rankedScope struct {
		scope store.ScopeSummary
		score int
	}
	ranked := make([]rankedScope, 0, len(scopes))
	for _, scope := range scopes {
		name := strings.ToLower(scope.Scope)
		score := 0
		if expectedScope != "" {
			switch {
			case name == expectedScope:
				score += 1000
			case strings.Contains(name, expectedScope):
				score += 800
			}
		}
		if query != "" {
			for _, term := range strings.Fields(query) {
				if strings.Contains(name, term) {
					score += 100
				}
			}
			if strings.Contains(name, query) {
				score += 300
			}
		}
		ranked = append(ranked, rankedScope{scope: scope, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		left := ranked[i].scope
		right := ranked[j].scope
		leftTotal := left.Documents + left.Claims + left.Observations + left.Summaries + left.Entities + left.Relations + left.Conflicts + left.Sources + left.Jobs
		rightTotal := right.Documents + right.Claims + right.Observations + right.Summaries + right.Entities + right.Relations + right.Conflicts + right.Sources + right.Jobs
		if leftTotal != rightTotal {
			return leftTotal > rightTotal
		}
		return left.Scope < right.Scope
	})
	ordered := make([]store.ScopeSummary, 0, len(ranked))
	matches := []store.ScopeSummary{}
	for _, item := range ranked {
		ordered = append(ordered, item.scope)
		if item.score > 0 {
			matches = append(matches, item.scope)
		}
	}
	recommended := ""
	if len(matches) > 0 {
		recommended = ordered[0].Scope
	}
	return ordered, matches, recommended
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service": "abra",
		"status":  "ok",
		"stack":   "go-v1",
		"endpoints": map[string]string{
			"health":        "GET /healthz",
			"readiness":     "GET /readyz",
			"metrics":       "GET /metrics",
			"audit":         "GET /audit/events",
			"ingest":        "POST /ingest/documents",
			"webhooks":      "POST /ingest/webhooks",
			"recall":        "POST /recall",
			"claims":        "POST /claims",
			"observations":  "GET|POST /observations",
			"challenge":     "POST /claims/{claimId}/challenge",
			"forget":        "POST /claims/{claimId}/forget",
			"conflicts":     "GET /conflicts",
			"resolve":       "POST /conflicts/{conflictId}/resolve",
			"sources":       "POST /sources",
			"brain_think":   "POST /brain/think",
			"memory":        "POST /memory/compose",
			"memory_health": "GET /memory/health",
			"summaries":     "POST /memory/summaries",
			"rebuild":       "POST /memory/summaries/rebuild",
			"learning":      "GET|POST /learning/proposals",
			"configs":       "GET|POST /sources/configs",
			"jobs":          "GET|POST /ingestion/jobs",
			"job_retry":     "POST /ingestion/jobs/{jobId}/retry",
			"job_cancel":    "POST /ingestion/jobs/{jobId}/cancel",
			"approvals":     "GET|POST /approvals",
			"acl":           "GET|POST /acl/policies",
			"acl_decide":    "POST /acl/decision",
			"agent":         "GET|POST /agent/policies",
			"agent_decide":  "POST /agent/policy/decision",
			"entities":      "GET /graph/entities",
			"relations":     "GET /graph/relations",
			"policy":        "POST /policy/plan",
			"mcp":           "POST /mcp",
		},
	})
}

func (h *handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *handler) ready(w http.ResponseWriter, r *http.Request) {
	if err := h.db.Ready(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	result := map[string]any{
		"ok":                   true,
		"auth_required":        len(h.cfg.APIKeys) > 0,
		"embedding_provider":   h.cfg.Embedding.Provider,
		"approval_mode":        h.cfg.ApprovalMode,
		"approval_enforcement": h.cfg.ApprovalMode == "enforce",
		"redaction_enabled":    h.cfg.RedactPII,
		"tracing_enabled":      h.cfg.Tracing.Enabled,
	}
	if r.URL.Query().Get("deep") == "1" || r.URL.Query().Get("deep") == "true" {
		checkTimeout := minDuration(10*time.Second, h.cfg.Embedding.Timeout)
		checkCtx, cancel := context.WithTimeout(r.Context(), checkTimeout)
		defer cancel()
		started := time.Now()
		if err := h.brain.CheckEmbeddingReady(checkCtx); err != nil {
			result["ok"] = false
			result["embedding_ready"] = false
			result["embedding_error"] = err.Error()
			result["embedding_status"] = embeddingReadinessStatus(checkCtx, err)
			result["embedding_check_timeout"] = checkTimeout.String()
			result["embedding_provider_timeout"] = h.cfg.Embedding.Timeout.String()
			result["embedding_elapsed_ms"] = time.Since(started).Milliseconds()
			writeJSON(w, http.StatusServiceUnavailable, result)
			return
		}
		result["embedding_ready"] = true
	}
	writeJSON(w, http.StatusOK, result)
}

func embeddingReadinessStatus(ctx context.Context, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	return "error"
}

func (h *handler) metricsText(w http.ResponseWriter, r *http.Request) {
	if !h.requireAccess(w, r, authActionOps, "") {
		return
	}
	w.Header().Set("content-type", "text/plain; version=0.0.4; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(h.metrics.prometheus()))
}

func (h *handler) auditEvents(w http.ResponseWriter, r *http.Request) {
	filter, format, err := auditEventFilterFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionOps, filter.Scope) {
		return
	}
	events, err := h.db.ListAuditEvents(r.Context(), filter)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if format == "ndjson" {
		w.Header().Set("content-type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		encoder := json.NewEncoder(w)
		for _, event := range events {
			_ = encoder.Encode(event)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit_events": events})
}

func (h *handler) ingestDocument(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.MaxRequestBodyBytes)
	var input brain.IngestDocumentInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, input.Scope) {
		return
	}
	result, err := h.brain.IngestDocument(r.Context(), input)
	if err != nil {
		if providerErr, ok := ai.ProviderErrorInfo(err); ok {
			writeJSON(w, providerErr.HTTPStatus(), providerErrorPayload(err, providerErr))
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func providerErrorPayload(err error, providerErr *ai.ProviderError) map[string]any {
	details := map[string]any{
		"operation": providerErr.Operation,
		"code":      providerErr.Code,
		"retryable": providerErr.Retryable,
		"attempts":  providerErr.Attempts,
	}
	if providerErr.Provider != "" {
		details["provider"] = providerErr.Provider
	}
	if providerErr.Model != "" {
		details["model"] = providerErr.Model
	}
	if providerErr.Status > 0 {
		details["status_code"] = providerErr.Status
	}
	if providerErr.Message != "" {
		details["message"] = providerErr.Message
	}
	if providerErr.BatchSize > 0 {
		details["batch_size"] = providerErr.BatchSize
		details["batch_start"] = providerErr.BatchStart
		details["batch_end"] = providerErr.BatchEnd
	}
	if providerErr.BatchTokens > 0 {
		details["batch_tokens"] = providerErr.BatchTokens
	}
	return map[string]any{
		"error":          err.Error(),
		"error_kind":     "provider_error",
		"provider_error": details,
	}
}

func (h *handler) recall(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Query             string `json:"query"`
		Scope             string `json:"scope"`
		Limit             int    `json:"limit"`
		IncludeUnverified bool   `json:"include_unverified"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.Query = strings.TrimSpace(input.Query)
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Query == "" || input.Scope == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "query and scope are required"})
		return
	}
	if !h.requireAccess(w, r, authActionRead, input.Scope) {
		return
	}
	if input.Limit == 0 {
		input.Limit = 5
	}
	started := time.Now()
	ctx, span := observability.Start(r.Context(), "abra.recall",
		attribute.Int("abra.limit", input.Limit),
		attribute.Bool("abra.include_unverified", input.IncludeUnverified),
	)
	result, err := h.brain.Recall(ctx, input.Query, input.Scope, input.Limit, input.IncludeUnverified)
	if err != nil {
		h.metrics.observeRecall("error", time.Since(started), store.RecallResult{})
		observability.End(span, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	span.SetAttributes(
		attribute.String("abra.retrieval.mode", result.RetrievalMode),
		attribute.Int("abra.recall.claims", len(result.Claims)),
		attribute.Int("abra.recall.documents", len(result.SupportingDocuments)),
		attribute.Int("abra.recall.graph_relations", len(result.GraphContext)),
	)
	observability.End(span, nil)
	h.metrics.observeRecall("ok", time.Since(started), result)
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) rememberClaim(w http.ResponseWriter, r *http.Request) {
	var input brain.RememberClaimInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, input.Scope) {
		return
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:        "agent_write",
		Scope:         input.Scope,
		TargetType:    "memory_write",
		TargetID:      input.Scope,
		ApprovalID:    input.ApprovalID,
		PrincipalType: "agent",
		PrincipalID:   input.CreatedBy,
	}) {
		return
	}
	result, err := h.brain.RememberClaim(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) captureObservation(w http.ResponseWriter, r *http.Request) {
	var input brain.CaptureObservationInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.Scope = strings.TrimSpace(input.Scope)
	if !h.requireAccess(w, r, authActionWrite, input.Scope) {
		return
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:        "agent_write",
		Scope:         input.Scope,
		TargetType:    "memory_write",
		TargetID:      input.Scope,
		ApprovalID:    input.ApprovalID,
		PrincipalType: "agent",
		PrincipalID:   input.CreatedBy,
	}) {
		return
	}
	result, err := h.brain.CaptureObservation(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) listObservations(w http.ResponseWriter, r *http.Request) {
	input := brain.ListObservationsInput{
		Scope:           strings.TrimSpace(r.URL.Query().Get("scope")),
		Query:           strings.TrimSpace(r.URL.Query().Get("query")),
		ObservationType: strings.TrimSpace(firstNonEmpty(r.URL.Query().Get("observation_type"), r.URL.Query().Get("type"))),
		Status:          strings.TrimSpace(r.URL.Query().Get("status")),
		Since:           strings.TrimSpace(r.URL.Query().Get("since")),
		Until:           strings.TrimSpace(r.URL.Query().Get("until")),
		Limit:           intQuery(r, "limit", 20),
	}
	if !h.requireAccess(w, r, authActionRead, input.Scope) {
		return
	}
	observations, err := h.brain.ListObservations(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"observations": observations})
}

func (h *handler) challengeClaim(w http.ResponseWriter, r *http.Request) {
	var input brain.ChallengeClaimInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.ClaimID = r.PathValue("claimId")
	scope, err := h.db.ClaimScope(r.Context(), strings.TrimSpace(input.ClaimID))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, scope) {
		return
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:        "challenge_claim",
		Scope:         scope,
		TargetType:    "claim",
		TargetID:      input.ClaimID,
		ApprovalID:    input.ApprovalID,
		PrincipalType: "agent",
		PrincipalID:   input.CreatedBy,
	}) {
		return
	}
	result, err := h.brain.ChallengeClaim(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) forgetClaim(w http.ResponseWriter, r *http.Request) {
	var input brain.ForgetClaimInput
	_ = json.NewDecoder(r.Body).Decode(&input)
	input.ClaimID = r.PathValue("claimId")
	scope, err := h.db.ClaimScope(r.Context(), strings.TrimSpace(input.ClaimID))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, scope) {
		return
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:        "forget_claim",
		Scope:         scope,
		TargetType:    "claim",
		TargetID:      input.ClaimID,
		ApprovalID:    input.ApprovalID,
		PrincipalType: "agent",
		PrincipalID:   input.CreatedBy,
	}) {
		return
	}
	result, err := h.brain.ForgetClaim(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) sources(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Query string `json:"query"`
		Scope string `json:"scope"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if !h.requireAccess(w, r, authActionRead, input.Scope) {
		return
	}
	if input.Limit == 0 {
		input.Limit = 5
	}
	docs, err := h.db.Sources(r.Context(), input.Query, input.Scope, input.Limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sources": docs})
}

func (h *handler) composeMemory(w http.ResponseWriter, r *http.Request) {
	var input memory.ComposeInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.Task = strings.TrimSpace(input.Task)
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Task == "" || input.Scope == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "task and scope are required"})
		return
	}
	if !h.requireAccess(w, r, authActionRead, input.Scope) {
		return
	}
	input, _, profileErr := h.applyAgentProfileToCompose(r.Context(), input)
	if profileErr != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "agent_profile_denied", "detail": profileErr.Error()})
		return
	}
	started := time.Now()
	ctx, span := observability.Start(r.Context(), "abra.memory.compose",
		attribute.Int("abra.limit", input.Limit),
		attribute.Int("abra.max_queries", input.MaxQueries),
		attribute.Int("abra.token_budget", input.TokenBudget),
		attribute.Bool("abra.include_unverified", input.IncludeUnverified),
		attribute.Bool("abra.diagnostic", input.Diagnostic),
	)
	result, err := h.memory.Compose(ctx, input)
	if err != nil {
		h.metrics.observeMemory("error", time.Since(started), memory.ComposeResult{})
		observability.End(span, err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	span.SetAttributes(
		attribute.String("abra.memory.intent", result.Intent),
		attribute.String("abra.memory.verdict", result.Verification.Verdict),
		attribute.String("abra.agent.decision", result.AgentDecision.Decision),
		attribute.Bool("abra.agent.autonomous_allowed", result.AgentDecision.AutonomousAllowed),
		attribute.Int("abra.memory.facts", len(result.Facts)),
		attribute.Int("abra.memory.documents", len(result.SupportingDocuments)),
		attribute.Int("abra.memory.graph_relations", len(result.GraphContext)),
		attribute.Int("abra.memory.retrieval_warnings", len(result.RetrievalWarnings)),
		attribute.Int("abra.memory.graph_warnings", len(result.GraphWarnings)),
		attribute.Int("abra.memory.context_tokens", result.ContextWindow.EstimatedTokens),
	)
	observability.End(span, nil)
	if shouldAutoPersistComposeLearning(input) {
		h.persistComposeLearningSuggestions(ctx, &result, input.Agent)
	}
	h.metrics.observeMemory("ok", time.Since(started), result)
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) brainThink(w http.ResponseWriter, r *http.Request) {
	var input memory.ThinkInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.Question = strings.TrimSpace(input.Question)
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Question == "" || input.Scope == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "question and scope are required"})
		return
	}
	if !h.requireAccess(w, r, authActionRead, input.Scope) {
		return
	}
	composeInput := memory.ComposeInput{
		Task:              input.Question,
		Scope:             input.Scope,
		Hook:              "before_task",
		Agent:             input.Agent,
		Limit:             input.Limit,
		MaxQueries:        input.MaxQueries,
		TokenBudget:       input.TokenBudget,
		IncludeUnverified: input.IncludeUnverified,
	}
	var profileErr error
	composeInput, _, profileErr = h.applyAgentProfileToCompose(r.Context(), composeInput)
	if profileErr != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "agent_profile_denied", "detail": profileErr.Error()})
		return
	}
	input.Limit = composeInput.Limit
	input.MaxQueries = composeInput.MaxQueries
	input.TokenBudget = composeInput.TokenBudget
	input.IncludeUnverified = composeInput.IncludeUnverified
	input.AgentProfile = composeInput.AgentProfile
	started := time.Now()
	result, err := h.memory.Think(r.Context(), input)
	if err != nil {
		h.metrics.observeBrainThink("error", time.Since(started), memory.ThinkResult{})
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	h.metrics.observeBrainThink("ok", time.Since(started), result)
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) memoryHealth(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if !h.requireAccess(w, r, authActionRead, scope) {
		return
	}
	result, err := h.db.MemoryHealth(r.Context(), scope)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) memorySummaries(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Query string `json:"query"`
		Scope string `json:"scope"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Scope == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scope is required"})
		return
	}
	if !h.requireAccess(w, r, authActionRead, input.Scope) {
		return
	}
	if input.Limit == 0 {
		input.Limit = 10
	}
	result, err := h.db.ListMemorySummaries(r.Context(), input.Query, input.Scope, input.Limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"summaries": result})
}

func (h *handler) rebuildMemorySummaries(w http.ResponseWriter, r *http.Request) {
	var input brain.RebuildSummariesInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Scope == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scope is required"})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, input.Scope) {
		return
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     "backfill",
		Scope:      input.Scope,
		TargetType: "memory_summaries",
		TargetID:   input.Scope,
		ApprovalID: input.ApprovalID,
	}) {
		return
	}
	result, err := h.brain.RebuildSummaries(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *handler) listLearningProposals(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if !h.requireAccess(w, r, authActionRead, scope) {
		return
	}
	proposals, err := h.db.ListLearningProposals(r.Context(), scope, strings.TrimSpace(r.URL.Query().Get("status")), intQuery(r, "limit", 50))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"learning_proposals": proposals})
}

func (h *handler) createLearningProposal(w http.ResponseWriter, r *http.Request) {
	var input store.CreateLearningProposalInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.Scope = strings.TrimSpace(input.Scope)
	if input.Scope == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scope is required"})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, input.Scope) {
		return
	}
	observation, hasObservationTarget, ok := h.prepareObservationLearningProposal(w, r, &input)
	if !ok {
		return
	}
	var proposal store.LearningProposalRecord
	created := true
	var err error
	if hasObservationTarget {
		proposal, created, err = h.db.CreateLearningProposalOnce(r.Context(), input)
	} else {
		proposal, err = h.db.CreateLearningProposal(r.Context(), input)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if hasObservationTarget {
		h.linkObservationLearningProposal(r.Context(), observation, proposal, input.CreatedBy, "http")
	}
	if created {
		h.auditLearningProposed(r.Context(), proposal, "http")
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"learning_proposal": proposal, "created": created})
}

func (h *handler) prepareObservationLearningProposal(w http.ResponseWriter, r *http.Request, input *store.CreateLearningProposalInput) (store.ObservationResult, bool, bool) {
	observation, hasObservationTarget, err := h.prepareObservationLearningProposalInput(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return store.ObservationResult{}, hasObservationTarget, false
	}
	return observation, hasObservationTarget, true
}

func (h *handler) prepareMCPObservationLearningProposal(ctx context.Context, input *store.CreateLearningProposalInput) (store.ObservationResult, bool, error) {
	return h.prepareObservationLearningProposalInput(ctx, input)
}

func (h *handler) prepareObservationLearningProposalInput(ctx context.Context, input *store.CreateLearningProposalInput) (store.ObservationResult, bool, error) {
	if strings.TrimSpace(input.TargetType) != "observation" {
		return store.ObservationResult{}, false, nil
	}
	observationID := strings.TrimSpace(input.TargetID)
	if observationID == "" && input.Payload != nil {
		if raw, ok := input.Payload["observation_id"].(string); ok {
			observationID = strings.TrimSpace(raw)
		}
	}
	if observationID == "" {
		return store.ObservationResult{}, true, fmt.Errorf("target_id is required when target_type is observation")
	}
	observation, err := h.db.GetObservation(ctx, observationID)
	if err != nil {
		return store.ObservationResult{}, true, err
	}
	if observation.Scope != strings.TrimSpace(input.Scope) {
		return store.ObservationResult{}, true, fmt.Errorf("observation scope does not match proposal scope")
	}
	if observation.Status == "rejected" || observation.Status == "deprecated" || observation.Status == "expired" || observation.Status == "accepted" {
		return store.ObservationResult{}, true, fmt.Errorf("observation status cannot be proposed")
	}
	input.TargetID = observation.ID
	if strings.TrimSpace(input.ProposalType) == "" {
		input.ProposalType = "claim"
	}
	if strings.TrimSpace(input.Title) == "" {
		input.Title = observationLearningTitle(observation)
	}
	if strings.TrimSpace(input.Rationale) == "" {
		input.Rationale = "Review raw observation as a trusted memory candidate."
	}
	if strings.TrimSpace(input.SourceURL) == "" {
		input.SourceURL = observation.SourceURL
	}
	if input.Confidence <= 0 {
		input.Confidence = observation.Confidence
	}
	payload := cloneAnyMap(input.Payload)
	payload["observation_id"] = observation.ID
	payload["observation_text"] = observation.ObservationText
	payload["observation_type"] = observation.ObservationType
	payload["observation_status"] = observation.Status
	payload["promotion_flow"] = "observation_to_" + input.ProposalType
	if _, ok := payload["claim"]; !ok && input.ProposalType == "claim" {
		payload["claim"] = observation.ObservationText
	}
	input.Payload = payload
	return observation, true, nil
}

func (h *handler) linkObservationLearningProposal(ctx context.Context, observation store.ObservationResult, proposal store.LearningProposalRecord, createdBy, channel string) {
	linked, err := h.db.LinkObservationProposal(ctx, observation.ID, proposal.ID, createdBy)
	if err != nil {
		return
	}
	_ = h.db.InsertAuditEvent(ctx, "observation.proposed", "observation", observation.ID, observation.Scope, observation.SourceURL, map[string]any{
		"learning_proposal_id": proposal.ID,
		"proposal_type":        proposal.ProposalType,
		"observation_status":   linked.Status,
		"created_by":           createdBy,
		"channel":              channel,
	})
}

func observationLearningTitle(observation store.ObservationResult) string {
	text := strings.Join(strings.Fields(observation.ObservationText), " ")
	if text == "" {
		text = observation.ID
	}
	runes := []rune(text)
	if len(runes) > 80 {
		text = string(runes[:77]) + "..."
	}
	return "Review observation: " + text
}

func (h *handler) decideLearningProposal(w http.ResponseWriter, r *http.Request) {
	proposal, err := h.db.GetLearningProposal(r.Context(), r.PathValue("proposalId"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, proposal.Scope) {
		return
	}
	var input store.DecideLearningProposalInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	decided, err := h.db.DecideLearningProposal(r.Context(), proposal.ID, input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	h.auditLearningDecided(r.Context(), decided, "http")
	writeJSON(w, http.StatusOK, map[string]any{"learning_proposal": decided, "apply_plan": buildLearningApplyPlan(decided, h.cfg.ApprovalMode)})
}

func (h *handler) listSourceConfigs(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if !h.requireAccess(w, r, authActionRead, scope) {
		return
	}
	sources, err := h.db.ListSourceConfigs(r.Context(), scope, intQuery(r, "limit", 50))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source_configs": sources})
}

func (h *handler) upsertSourceConfig(w http.ResponseWriter, r *http.Request) {
	var input store.SourceConfigRecord
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	input.Scope = strings.TrimSpace(input.Scope)
	input.SourceType = strings.TrimSpace(input.SourceType)
	input.Name = strings.TrimSpace(input.Name)
	if input.Scope == "" || input.SourceType == "" || input.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "scope, source_type, and name are required"})
		return
	}
	if err := validateSourceConfigInput(input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, input.Scope) {
		return
	}
	if sourceAuthorityApprovalRequired(input) && !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     "source_authority_change",
		Scope:      input.Scope,
		TargetType: "source_config",
		TargetID:   sourceConfigApprovalTarget(input),
		ApprovalID: input.ApprovalID,
	}) {
		return
	}
	id, err := h.db.UpsertSourceConfig(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := h.db.InsertAuditEvent(r.Context(), "source_config.upserted", "source_config", id, input.Scope, input.BaseURL, map[string]any{
		"name":            input.Name,
		"source_type":     input.SourceType,
		"connector_kind":  input.ConnectorKind,
		"status":          input.Status,
		"authority":       input.Authority,
		"authority_score": input.AuthorityScore,
		"created_by":      input.CreatedBy,
		"approval_id":     input.ApprovalID,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source_config_id": id, "status": "upserted"})
}

func (h *handler) listIngestionJobs(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if !h.requireAccess(w, r, authActionRead, scope) {
		return
	}
	jobs, err := h.db.ListIngestionJobs(r.Context(), scope, strings.TrimSpace(r.URL.Query().Get("source_config_id")), intQuery(r, "limit", 50))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ingestion_jobs": jobs})
}

func (h *handler) graphEntities(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if !h.requireAccess(w, r, authActionRead, scope) {
		return
	}
	entities, err := h.db.ListGraphEntities(r.Context(), scope, intQuery(r, "limit", 50))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entities": entities})
}

func (h *handler) graphRelations(w http.ResponseWriter, r *http.Request) {
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	if !h.requireAccess(w, r, authActionRead, scope) {
		return
	}
	relations, err := h.db.ListGraphRelations(r.Context(), scope, intQuery(r, "limit", 50))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"relations": relations})
}

func (h *handler) policyPlan(w http.ResponseWriter, r *http.Request) {
	var input policy.Event
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	if strings.TrimSpace(input.Scope) != "" && !h.requireAccess(w, r, authActionRead, input.Scope) {
		return
	}
	config := policy.Config{}
	var profileErr error
	input, config, _, profileErr = h.applyAgentProfileToPolicy(r.Context(), input, config)
	if profileErr != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "agent_profile_denied", "detail": profileErr.Error()})
		return
	}
	engine := policy.NewEngine(config)
	writeJSON(w, http.StatusOK, engine.Plan(input))
}

func (h *handler) mcp(w http.ResponseWriter, r *http.Request) {
	var rpc struct {
		JSONRPC string         `json:"jsonrpc"`
		ID      any            `json:"id"`
		Method  string         `json:"method"`
		Params  map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "parse error"}})
		return
	}
	switch rpc.Method {
	case "initialize":
		writeJSON(w, http.StatusOK, map[string]any{
			"jsonrpc": "2.0",
			"id":      rpc.ID,
			"result": map[string]any{
				"protocolVersion": "2025-06-18",
				"serverInfo":      map[string]any{"name": "abra", "version": version.Version},
				"capabilities":    mcpCapabilities(),
			},
		})
	case "tools/list":
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{"tools": mcpTools()}})
	case "tools/call":
		h.mcpToolCall(w, r, rpc.ID, rpc.Params)
	case "resources/list":
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{"resources": mcpResources()}})
	case "resources/templates/list":
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{"resourceTemplates": mcpResourceTemplates()}})
	case "resources/read":
		h.mcpResourceRead(w, r, rpc.ID, rpc.Params)
	case "prompts/list":
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{"prompts": mcpPrompts()}})
	case "prompts/get":
		h.mcpPromptGet(w, rpc.ID, rpc.Params)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
	}
}

func (h *handler) mcpToolCall(w http.ResponseWriter, r *http.Request, id any, params map[string]any) {
	name, _ := params["name"].(string)
	args, _ := params["arguments"].(map[string]any)
	var result any
	var err error
	ctx, span := observability.Start(r.Context(), "abra.mcp.tool",
		attribute.String("mcp.tool.name", mcpToolTraceName(name)),
	)
	r = r.WithContext(ctx)
	defer func() {
		observability.End(span, err)
	}()
	switch name {
	case "recall":
		query, _ := args["query"].(string)
		scope, _ := args["scope"].(string)
		if !h.requireAccess(w, r, authActionRead, scope) {
			return
		}
		limit := intArg(args, "limit", 5)
		includeUnverified, _ := args["include_unverified"].(bool)
		result, err = h.brain.Recall(r.Context(), query, scope, limit, includeUnverified)
	case "ingest_document":
		doc := mcpDocumentInput(args, nil)
		if !h.requireAccess(w, r, authActionWrite, doc.Scope) {
			return
		}
		result, err = h.brain.IngestDocument(r.Context(), doc)
	case "ingest_documents":
		docs, parseErr := mcpDocumentInputs(args)
		if parseErr != nil {
			err = parseErr
			break
		}
		continueOnError, _ := args["continue_on_error"].(bool)
		results := make([]map[string]any, 0, len(docs))
		accepted := 0
		failed := 0
		for index, doc := range docs {
			if !h.requireAccess(w, r, authActionWrite, doc.Scope) {
				return
			}
			ingested, ingestErr := h.brain.IngestDocument(r.Context(), doc)
			if ingestErr != nil {
				if continueOnError {
					failed++
					results = append(results, mcpIngestDocumentError(index, doc, ingestErr))
					continue
				}
				err = fmt.Errorf("document %d: %w", index, ingestErr)
				break
			}
			accepted++
			results = append(results, mcpIngestDocumentSuccess(index, doc, ingested, continueOnError))
		}
		if err == nil {
			response := map[string]any{"accepted": accepted, "documents": results}
			if continueOnError {
				response["failed"] = failed
			}
			result = response
		}
	case "remember_claim":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionWrite, scope) {
			return
		}
		if !h.requireRiskApproval(w, r, approvalRequirement{
			Action:        "agent_write",
			Scope:         scope,
			TargetType:    "memory_write",
			TargetID:      scope,
			ApprovalID:    stringArg(args, "approval_id"),
			PrincipalType: "agent",
			PrincipalID:   stringArg(args, "created_by"),
		}) {
			return
		}
		result, err = h.brain.RememberClaim(r.Context(), brain.RememberClaimInput{
			Claim:      stringArg(args, "claim"),
			Scope:      scope,
			SourceURL:  stringArg(args, "source_url"),
			SourceType: stringArg(args, "source_type"),
			Authority:  stringArg(args, "authority"),
			CreatedBy:  stringArg(args, "created_by"),
			ApprovalID: stringArg(args, "approval_id"),
		})
	case "capture_observation":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionWrite, scope) {
			return
		}
		if !h.requireRiskApproval(w, r, approvalRequirement{
			Action:        "agent_write",
			Scope:         scope,
			TargetType:    "memory_write",
			TargetID:      scope,
			ApprovalID:    stringArg(args, "approval_id"),
			PrincipalType: "agent",
			PrincipalID:   stringArg(args, "created_by"),
		}) {
			return
		}
		result, err = h.brain.CaptureObservation(r.Context(), brain.CaptureObservationInput{
			Scope:           scope,
			ObservationText: stringArg(args, "observation_text"),
			ObservationType: stringArg(args, "observation_type"),
			Status:          stringArg(args, "status"),
			Authority:       stringArg(args, "authority"),
			AuthorityScore:  floatArg(args, "authority_score", 0),
			Confidence:      floatArg(args, "confidence", 0),
			FreshnessStatus: stringArg(args, "freshness_status"),
			SubjectEntityID: stringArg(args, "subject_entity_id"),
			ObjectEntityID:  stringArg(args, "object_entity_id"),
			RelationID:      stringArg(args, "relation_id"),
			ClaimID:         stringArg(args, "claim_id"),
			DocumentID:      stringArg(args, "document_id"),
			ChunkID:         stringArg(args, "chunk_id"),
			SourceConfigID:  stringArg(args, "source_config_id"),
			IngestionJobID:  stringArg(args, "ingestion_job_id"),
			SourceURL:       stringArg(args, "source_url"),
			SourceType:      stringArg(args, "source_type"),
			SourceID:        stringArg(args, "source_id"),
			ObservedAt:      stringArg(args, "observed_at"),
			ValidFrom:       stringArg(args, "valid_from"),
			ExpiresAt:       stringArg(args, "expires_at"),
			CreatedBy:       stringArg(args, "created_by"),
			ApprovalID:      stringArg(args, "approval_id"),
			Value:           mapArg(args, "value"),
			Metadata:        mapArg(args, "metadata"),
		})
	case "list_observations":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionRead, scope) {
			return
		}
		result, err = h.brain.ListObservations(r.Context(), brain.ListObservationsInput{
			Scope:           scope,
			Query:           stringArg(args, "query"),
			ObservationType: stringArg(args, "observation_type"),
			Status:          stringArg(args, "status"),
			Since:           stringArg(args, "since"),
			Until:           stringArg(args, "until"),
			Limit:           intArg(args, "limit", 20),
		})
	case "challenge":
		claimID := stringArg(args, "claim_id")
		scope, scopeErr := h.db.ClaimScope(r.Context(), strings.TrimSpace(claimID))
		if scopeErr != nil {
			err = scopeErr
			break
		}
		if !h.requireAccess(w, r, authActionWrite, scope) {
			return
		}
		if !h.requireRiskApproval(w, r, approvalRequirement{
			Action:        "challenge_claim",
			Scope:         scope,
			TargetType:    "claim",
			TargetID:      claimID,
			ApprovalID:    stringArg(args, "approval_id"),
			PrincipalType: "agent",
			PrincipalID:   stringArg(args, "created_by"),
		}) {
			return
		}
		result, err = h.brain.ChallengeClaim(r.Context(), brain.ChallengeClaimInput{
			ClaimID:            claimID,
			Reason:             stringArg(args, "reason"),
			SourceURL:          stringArg(args, "source_url"),
			CreatedBy:          stringArg(args, "created_by"),
			Verdict:            stringArg(args, "verdict"),
			ConflictingClaimID: stringArg(args, "conflicting_claim_id"),
			Severity:           stringArg(args, "severity"),
			ApprovalID:         stringArg(args, "approval_id"),
		})
	case "forget":
		claimID := stringArg(args, "claim_id")
		scope, scopeErr := h.db.ClaimScope(r.Context(), strings.TrimSpace(claimID))
		if scopeErr != nil {
			err = scopeErr
			break
		}
		if !h.requireAccess(w, r, authActionWrite, scope) {
			return
		}
		if !h.requireRiskApproval(w, r, approvalRequirement{
			Action:        "forget_claim",
			Scope:         scope,
			TargetType:    "claim",
			TargetID:      claimID,
			ApprovalID:    stringArg(args, "approval_id"),
			PrincipalType: "agent",
			PrincipalID:   stringArg(args, "created_by"),
		}) {
			return
		}
		result, err = h.brain.ForgetClaim(r.Context(), brain.ForgetClaimInput{
			ClaimID:    claimID,
			Reason:     stringArg(args, "reason"),
			CreatedBy:  stringArg(args, "created_by"),
			ApprovalID: stringArg(args, "approval_id"),
		})
	case "brain_sources":
		if !h.requireAccess(w, r, authActionRead, stringArg(args, "scope")) {
			return
		}
		result, err = h.db.Sources(r.Context(), stringArg(args, "query"), stringArg(args, "scope"), intArg(args, "limit", 5))
	case "brain_summaries":
		if !h.requireAccess(w, r, authActionRead, stringArg(args, "scope")) {
			return
		}
		result, err = h.db.ListMemorySummaries(r.Context(), stringArg(args, "query"), stringArg(args, "scope"), intArg(args, "limit", 10))
	case "brain_think":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionRead, scope) {
			return
		}
		input := memory.ThinkInput{
			Question:          stringArg(args, "question"),
			Scope:             scope,
			Agent:             stringArg(args, "agent"),
			Limit:             intArg(args, "limit", 0),
			MaxQueries:        intArg(args, "max_queries", 0),
			TokenBudget:       intArg(args, "token_budget", 0),
			IncludeUnverified: boolArg(args, "include_unverified", false),
		}
		composeInput := memory.ComposeInput{
			Task:              input.Question,
			Scope:             input.Scope,
			Hook:              "before_task",
			Agent:             input.Agent,
			Limit:             input.Limit,
			MaxQueries:        input.MaxQueries,
			TokenBudget:       input.TokenBudget,
			IncludeUnverified: input.IncludeUnverified,
		}
		var profileErr error
		composeInput, _, profileErr = h.applyAgentProfileToCompose(r.Context(), composeInput)
		if profileErr != nil {
			err = profileErr
			break
		}
		input.Limit = composeInput.Limit
		input.MaxQueries = composeInput.MaxQueries
		input.TokenBudget = composeInput.TokenBudget
		input.IncludeUnverified = composeInput.IncludeUnverified
		input.AgentProfile = composeInput.AgentProfile
		result, err = h.memory.Think(r.Context(), input)
	case "memory_health":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionRead, scope) {
			return
		}
		result, err = h.db.MemoryHealth(r.Context(), scope)
	case "discover_scopes":
		principal := principalFromContext(r.Context())
		if principal == nil || !principal.allowsAction(authActionRead) {
			err = fmt.Errorf("forbidden: read role required")
			break
		}
		expectedScope := strings.TrimSpace(stringArg(args, "expected_scope"))
		query := strings.TrimSpace(stringArg(args, "query"))
		limit, candidateLimit := scopeDiscoveryLimits(intArg(args, "limit", defaultScopeDiscoveryLimit), principal)
		if expectedScope != "" || query != "" {
			candidateLimit = maxScopeDiscoveryCandidateCap
		}
		scopes, listErr := h.db.ListScopes(r.Context(), candidateLimit)
		if listErr != nil {
			err = listErr
			break
		}
		visible := make([]store.ScopeSummary, 0, len(scopes))
		for _, scope := range scopes {
			if principal.allows(authActionRead, scope.Scope) {
				visible = append(visible, scope)
			}
		}
		visible, matches, recommendedScope := rankScopeSummaries(visible, expectedScope, query)
		if len(visible) > limit {
			visible = visible[:limit]
		}
		if len(matches) > limit {
			matches = matches[:limit]
		}
		result = map[string]any{
			"scopes":              visible,
			"returned":            len(visible),
			"limit":               limit,
			"query":               query,
			"expected_scope":      expectedScope,
			"recommended_scope":   recommendedScope,
			"matches":             matches,
			"candidate_count":     len(scopes),
			"candidate_limit":     candidateLimit,
			"candidate_truncated": len(scopes) >= candidateLimit,
			"filtered_by_token":   !principal.allScopes,
			"hint":                "Use one exact scope value with brain_think, recall, policy_plan, and working_memory_compose. When you already know the project scope from `abra scope`, call discover_scopes with expected_scope set to that exact value. If the expected project is missing or candidate_truncated is true, run `abra scope` in that project, then `abra ingest . --code --scope <scope>` with the printed scope.",
		}
	case "rebuild_summaries":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionWrite, scope) {
			return
		}
		if !h.requireRiskApproval(w, r, approvalRequirement{
			Action:     "backfill",
			Scope:      scope,
			TargetType: "memory_summaries",
			TargetID:   scope,
			ApprovalID: stringArg(args, "approval_id"),
		}) {
			return
		}
		result, err = h.brain.RebuildSummaries(r.Context(), brain.RebuildSummariesInput{
			Scope:      scope,
			Limit:      intArg(args, "limit", 1000),
			ApprovalID: stringArg(args, "approval_id"),
		})
	case "policy_plan":
		if scope := stringArg(args, "scope"); strings.TrimSpace(scope) != "" && !h.requireAccess(w, r, authActionRead, scope) {
			return
		}
		event := policy.Event{
			Hook:         policy.Hook(stringArg(args, "hook")),
			Task:         stringArg(args, "task"),
			Scope:        stringArg(args, "scope"),
			Files:        stringListArg(args, "files"),
			Language:     stringArg(args, "language"),
			Agent:        stringArg(args, "agent"),
			ChangedFiles: stringListArg(args, "changed_files"),
		}
		config := policy.Config{DefaultLimit: intArg(args, "limit", 0), MaxQueries: intArg(args, "max_queries", 0)}
		var appliedErr error
		event, config, _, appliedErr = h.applyAgentProfileToPolicy(r.Context(), event, config)
		if appliedErr != nil {
			err = appliedErr
			break
		}
		engine := policy.NewEngine(config)
		result = engine.Plan(event)
	case "working_memory_compose":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionRead, scope) {
			return
		}
		input := memory.ComposeInput{
			Task:              stringArg(args, "task"),
			Scope:             scope,
			Hook:              stringArg(args, "hook"),
			Agent:             stringArg(args, "agent"),
			Files:             stringListArg(args, "files"),
			ChangedFiles:      stringListArg(args, "changed_files"),
			Language:          stringArg(args, "language"),
			Limit:             intArg(args, "limit", 0),
			MaxQueries:        intArg(args, "max_queries", 0),
			TokenBudget:       intArg(args, "token_budget", 0),
			IncludeUnverified: boolArg(args, "include_unverified", false),
			Diagnostic:        boolArg(args, "diagnostic", false),
		}
		var profileErr error
		input, _, profileErr = h.applyAgentProfileToCompose(r.Context(), input)
		if profileErr != nil {
			err = profileErr
			break
		}
		packet, composeErr := h.memory.Compose(r.Context(), input)
		if composeErr == nil {
			if shouldAutoPersistComposeLearning(input) {
				h.persistComposeLearningSuggestions(r.Context(), &packet, stringArg(args, "agent"))
			}
			result = packet
		}
		err = composeErr
	case "list_conflicts":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionRead, scope) {
			return
		}
		result, err = h.db.ListConflicts(r.Context(), store.ConflictFilter{
			Scope:      scope,
			Status:     stringArg(args, "status"),
			Severity:   stringArg(args, "severity"),
			ClaimID:    stringArg(args, "claim_id"),
			RelationID: stringArg(args, "relation_id"),
			Limit:      intArg(args, "limit", 50),
		})
	case "resolve_conflict":
		conflictID := stringArg(args, "conflict_id")
		conflict, getErr := h.db.GetConflict(r.Context(), conflictID)
		if getErr != nil {
			err = getErr
			break
		}
		if !h.requireAccess(w, r, authActionOps, conflict.Scope) {
			return
		}
		result, err = h.db.ResolveConflict(r.Context(), conflict.ID, store.ResolveConflictInput{
			Status:     stringArg(args, "status"),
			ResolvedBy: stringArg(args, "resolved_by"),
			Resolution: stringArg(args, "resolution"),
			Metadata:   mapArg(args, "metadata"),
		})
		if err == nil {
			resolved := result.(store.ConflictResult)
			err = h.db.InsertAuditEvent(r.Context(), "conflict.resolved", "conflict", resolved.ID, resolved.Scope, "", map[string]any{
				"status":      resolved.Status,
				"resolved_by": resolved.ResolvedBy,
				"resolution":  resolved.Resolution,
			})
		}
	case "upsert_acl_policy":
		policyRecord := store.ACLPolicyRecord{
			Scope:       stringArg(args, "scope"),
			Name:        stringArg(args, "name"),
			Status:      stringArg(args, "status"),
			Priority:    intArg(args, "priority", 100),
			SubjectType: stringArg(args, "subject_type"),
			SubjectID:   stringArg(args, "subject_id"),
			Effect:      stringArg(args, "effect"),
			Rule:        mapArg(args, "rule"),
			CreatedBy:   stringArg(args, "created_by"),
			Metadata:    mapArg(args, "metadata"),
			ApprovalID:  stringArg(args, "approval_id"),
		}
		if !h.requireAccess(w, r, authActionOps, policyRecord.Scope) {
			return
		}
		if !h.requireRiskApproval(w, r, approvalRequirement{
			Action:     "acl_change",
			Scope:      policyRecord.Scope,
			TargetType: "acl_policy",
			TargetID:   aclPolicyApprovalTarget(policyRecord),
			ApprovalID: policyRecord.ApprovalID,
		}) {
			return
		}
		created, createErr := h.db.UpsertACLPolicy(r.Context(), policyRecord)
		if createErr != nil {
			err = createErr
			break
		}
		h.auditACLPolicyUpserted(r.Context(), created, "mcp")
		result = created
	case "list_acl_policies":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionOps, scope) {
			return
		}
		result, err = h.db.ListACLPolicies(r.Context(), scope, stringArg(args, "subject_type"), stringArg(args, "subject_id"), intArg(args, "limit", 50))
	case "acl_decision":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionRead, scope) {
			return
		}
		result, err = h.db.EvaluateACLDecision(r.Context(), store.ACLDecisionInput{
			Scope:         scope,
			Action:        stringArg(args, "action"),
			PrincipalType: stringArg(args, "principal_type"),
			PrincipalID:   stringArg(args, "principal_id"),
			ResourceType:  stringArg(args, "resource_type"),
			ResourceID:    stringArg(args, "resource_id"),
			Context:       mapArg(args, "context"),
		})
	case "upsert_agent_policy":
		policyRecord := store.AgentActionPolicyRecord{
			Scope:       stringArg(args, "scope"),
			Name:        stringArg(args, "name"),
			Status:      stringArg(args, "status"),
			Priority:    intArg(args, "priority", 100),
			SubjectType: stringArg(args, "subject_type"),
			SubjectID:   stringArg(args, "subject_id"),
			Effect:      stringArg(args, "effect"),
			Rule:        mapArg(args, "rule"),
			CreatedBy:   stringArg(args, "created_by"),
			Metadata:    mapArg(args, "metadata"),
			ApprovalID:  stringArg(args, "approval_id"),
		}
		if !h.requireAccess(w, r, authActionOps, policyRecord.Scope) {
			return
		}
		if !h.requireRiskApproval(w, r, approvalRequirement{
			Action:     "acl_change",
			Scope:      policyRecord.Scope,
			TargetType: "agent_policy",
			TargetID:   agentActionPolicyApprovalTarget(policyRecord),
			ApprovalID: policyRecord.ApprovalID,
		}) {
			return
		}
		created, createErr := h.db.UpsertAgentActionPolicy(r.Context(), policyRecord)
		if createErr != nil {
			err = createErr
			break
		}
		h.auditAgentPolicyUpserted(r.Context(), created, "mcp")
		result = created
	case "list_agent_policies":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionOps, scope) {
			return
		}
		result, err = h.db.ListAgentActionPolicies(r.Context(), scope, intArg(args, "limit", 50))
	case "agent_policy_decision":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionRead, scope) {
			return
		}
		input := store.AgentActionDecisionInput{
			Scope:         scope,
			Action:        stringArg(args, "action"),
			TargetType:    stringArg(args, "target_type"),
			TargetID:      stringArg(args, "target_id"),
			PrincipalType: stringArg(args, "principal_type"),
			PrincipalID:   stringArg(args, "principal_id"),
			Context:       mapArg(args, "context"),
		}
		decision, decisionErr := h.db.EvaluateAgentActionPolicy(r.Context(), input)
		if decisionErr != nil {
			err = decisionErr
			break
		}
		h.metrics.observeAgentPolicyDecision("mcp_decision", input.Action, decision.Decision)
		result = decision
	case "upsert_agent_profile":
		profile := store.AgentProfileRecord{
			Scope:             stringArg(args, "scope"),
			ProfileKey:        stringArg(args, "profile_key"),
			DisplayName:       stringArg(args, "display_name"),
			AgentType:         stringArg(args, "agent_type"),
			Status:            stringArg(args, "status"),
			PrincipalRef:      stringArg(args, "principal_ref"),
			DefaultScope:      stringArg(args, "default_scope"),
			AllowedScopes:     stringListArg(args, "allowed_scopes"),
			DeniedScopes:      stringListArg(args, "denied_scopes"),
			Permissions:       mapArg(args, "permissions"),
			MemoryPreferences: mapArg(args, "memory_preferences"),
			CreatedBy:         stringArg(args, "created_by"),
			Metadata:          mapArg(args, "metadata"),
			ApprovalID:        stringArg(args, "approval_id"),
		}
		if !h.requireAccess(w, r, authActionOps, profile.Scope) {
			return
		}
		if !h.requireRiskApproval(w, r, approvalRequirement{
			Action:     "acl_change",
			Scope:      profile.Scope,
			TargetType: "agent_profile",
			TargetID:   agentProfileApprovalTarget(profile),
			ApprovalID: profile.ApprovalID,
		}) {
			return
		}
		created, createErr := h.db.UpsertAgentProfile(r.Context(), profile)
		if createErr != nil {
			err = createErr
			break
		}
		_ = h.db.InsertAuditEvent(r.Context(), "agent_profile.upserted", "agent_profile", created.ID, created.Scope, "", map[string]any{
			"profile_key":    created.ProfileKey,
			"status":         created.Status,
			"principal_ref":  created.PrincipalRef,
			"default_scope":  created.DefaultScope,
			"allowed_scopes": created.AllowedScopes,
			"denied_scopes":  created.DeniedScopes,
			"channel":        "mcp",
		})
		result = created
	case "list_agent_profiles":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionOps, scope) {
			return
		}
		result, err = h.db.ListAgentProfiles(r.Context(), scope, stringArg(args, "status"), intArg(args, "limit", 50))
	case "upsert_source_config":
		sourceConfig := store.SourceConfigRecord{
			ID:             stringArg(args, "id"),
			Scope:          stringArg(args, "scope"),
			SourceType:     stringArg(args, "source_type"),
			Name:           stringArg(args, "name"),
			BaseURL:        stringArg(args, "base_url"),
			ConnectorKind:  stringArg(args, "connector_kind"),
			Status:         stringArg(args, "status"),
			Authority:      stringArg(args, "authority"),
			AuthorityScore: floatArg(args, "authority_score", 0),
			Config:         mapArg(args, "config"),
			Metadata:       mapArg(args, "metadata"),
			CreatedBy:      stringArg(args, "created_by"),
			ApprovalID:     stringArg(args, "approval_id"),
		}
		sourceConfig.Scope = strings.TrimSpace(sourceConfig.Scope)
		sourceConfig.SourceType = strings.TrimSpace(sourceConfig.SourceType)
		sourceConfig.Name = strings.TrimSpace(sourceConfig.Name)
		if sourceConfig.Scope == "" || sourceConfig.SourceType == "" || sourceConfig.Name == "" {
			err = fmt.Errorf("scope, source_type, and name are required")
			break
		}
		if validateErr := validateSourceConfigInput(sourceConfig); validateErr != nil {
			err = validateErr
			break
		}
		if !h.requireAccess(w, r, authActionWrite, sourceConfig.Scope) {
			return
		}
		if sourceAuthorityApprovalRequired(sourceConfig) && !h.requireRiskApproval(w, r, approvalRequirement{
			Action:     "source_authority_change",
			Scope:      sourceConfig.Scope,
			TargetType: "source_config",
			TargetID:   sourceConfigApprovalTarget(sourceConfig),
			ApprovalID: sourceConfig.ApprovalID,
		}) {
			return
		}
		id, upsertErr := h.db.UpsertSourceConfig(r.Context(), sourceConfig)
		if upsertErr != nil {
			err = upsertErr
			break
		}
		if auditErr := h.db.InsertAuditEvent(r.Context(), "source_config.upserted", "source_config", id, sourceConfig.Scope, sourceConfig.BaseURL, map[string]any{
			"name":            sourceConfig.Name,
			"source_type":     sourceConfig.SourceType,
			"connector_kind":  sourceConfig.ConnectorKind,
			"status":          sourceConfig.Status,
			"authority":       sourceConfig.Authority,
			"authority_score": sourceConfig.AuthorityScore,
			"created_by":      sourceConfig.CreatedBy,
			"approval_id":     sourceConfig.ApprovalID,
			"channel":         "mcp",
		}); auditErr != nil {
			err = auditErr
			break
		}
		result = map[string]any{"source_config_id": id, "status": "upserted"}
	case "list_source_configs":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionRead, scope) {
			return
		}
		result, err = h.db.ListSourceConfigs(r.Context(), scope, intArg(args, "limit", 50))
	case "enqueue_ingestion_job":
		sourceConfigID := stringArg(args, "source_config_id")
		source, getErr := h.db.GetSourceConfig(r.Context(), sourceConfigID)
		if getErr != nil {
			err = getErr
			break
		}
		if !h.requireAccess(w, r, authActionWrite, source.Scope) {
			return
		}
		result, err = h.db.EnqueueIngestionJob(r.Context(), store.EnqueueIngestionJobInput{
			SourceConfigID: sourceConfigID,
			TriggerType:    stringArg(args, "trigger_type"),
			CreatedBy:      stringArg(args, "created_by"),
			MaxAttempts:    intArg(args, "max_attempts", 0),
			Metadata:       mapArg(args, "metadata"),
		})
	case "list_ingestion_jobs":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionRead, scope) {
			return
		}
		result, err = h.db.ListIngestionJobs(r.Context(), scope, stringArg(args, "source_config_id"), intArg(args, "limit", 50))
	case "retry_ingestion_job":
		jobID := stringArg(args, "job_id")
		current, getErr := h.db.GetIngestionJob(r.Context(), jobID)
		if getErr != nil {
			err = getErr
			break
		}
		if !h.requireAccess(w, r, authActionWrite, current.Scope) {
			return
		}
		result, err = h.db.RetryIngestionJob(r.Context(), jobID, store.RetryIngestionJobInput{
			CreatedBy:   stringArg(args, "created_by"),
			MaxAttempts: intArg(args, "max_attempts", 0),
			Metadata:    mapArg(args, "metadata"),
		})
	case "cancel_ingestion_job":
		jobID := stringArg(args, "job_id")
		current, getErr := h.db.GetIngestionJob(r.Context(), jobID)
		if getErr != nil {
			err = getErr
			break
		}
		if !h.requireAccess(w, r, authActionWrite, current.Scope) {
			return
		}
		result, err = h.db.CancelIngestionJob(r.Context(), jobID, store.CancelIngestionJobInput{
			Reason:    stringArg(args, "reason"),
			CreatedBy: stringArg(args, "created_by"),
			Metadata:  mapArg(args, "metadata"),
		})
	case "propose_learning":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionWrite, scope) {
			return
		}
		input := store.CreateLearningProposalInput{
			Scope:        scope,
			ProposalType: stringArg(args, "proposal_type"),
			Title:        stringArg(args, "title"),
			Rationale:    stringArg(args, "rationale"),
			TargetType:   stringArg(args, "target_type"),
			TargetID:     stringArg(args, "target_id"),
			SourceURL:    stringArg(args, "source_url"),
			Confidence:   floatArg(args, "confidence", 0.5),
			Payload:      mapArg(args, "payload"),
			CreatedBy:    stringArg(args, "created_by"),
			ApprovalID:   stringArg(args, "approval_id"),
		}
		observation, hasObservationTarget, prepareErr := h.prepareMCPObservationLearningProposal(r.Context(), &input)
		if prepareErr != nil {
			err = prepareErr
			break
		}
		var proposal store.LearningProposalRecord
		created := true
		var createErr error
		if hasObservationTarget {
			proposal, created, createErr = h.db.CreateLearningProposalOnce(r.Context(), input)
		} else {
			proposal, createErr = h.db.CreateLearningProposal(r.Context(), input)
		}
		if createErr != nil {
			err = createErr
			break
		}
		if hasObservationTarget {
			h.linkObservationLearningProposal(r.Context(), observation, proposal, input.CreatedBy, "mcp")
		}
		if created {
			h.auditLearningProposed(r.Context(), proposal, "mcp")
		}
		result = proposal
	case "list_learning_proposals":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionRead, scope) {
			return
		}
		result, err = h.db.ListLearningProposals(r.Context(), scope, stringArg(args, "status"), intArg(args, "limit", 50))
	case "decide_learning_proposal":
		proposalID := stringArg(args, "proposal_id")
		proposal, getErr := h.db.GetLearningProposal(r.Context(), proposalID)
		if getErr != nil {
			err = getErr
			break
		}
		if !h.requireAccess(w, r, authActionWrite, proposal.Scope) {
			return
		}
		decided, decideErr := h.db.DecideLearningProposal(r.Context(), proposalID, store.DecideLearningProposalInput{
			Status:       stringArg(args, "status"),
			ReviewedBy:   stringArg(args, "reviewed_by"),
			ReviewReason: stringArg(args, "review_reason"),
			ApprovalID:   stringArg(args, "approval_id"),
			Metadata:     mapArg(args, "metadata"),
		})
		if decideErr != nil {
			err = decideErr
			break
		}
		h.auditLearningDecided(r.Context(), decided, "mcp")
		result = map[string]any{"learning_proposal": decided, "apply_plan": buildLearningApplyPlan(decided, h.cfg.ApprovalMode)}
	case "request_approval":
		scope := stringArg(args, "scope")
		if !h.requireAccess(w, r, authActionWrite, scope) {
			return
		}
		result, err = h.db.CreateApprovalRequest(r.Context(), store.CreateApprovalRequestInput{
			Action:      stringArg(args, "action"),
			Scope:       scope,
			TargetType:  stringArg(args, "target_type"),
			TargetID:    stringArg(args, "target_id"),
			RequestedBy: stringArg(args, "requested_by"),
			Reason:      stringArg(args, "reason"),
			Payload:     mapArg(args, "payload"),
			Metadata:    mapArg(args, "metadata"),
			ExpiresAt:   stringArg(args, "expires_at"),
		})
	default:
		err = fmt.Errorf("unsupported mcp tool %q", name)
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": "unsupported tool"}})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": err.Error()}})
		return
	}
	span.SetAttributes(attribute.Bool("mcp.tool.success", true))
	raw, _ := json.MarshalIndent(result, "", "  ")
	writeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result":  map[string]any{"content": []map[string]any{{"type": "text", "text": string(raw)}}},
	})
}

func mcpTools() []map[string]any {
	return []map[string]any{
		{
			"name":        "recall",
			"description": "Recall source-cited claims and supporting documents from Abra.",
			"inputSchema": map[string]any{
				"type":       "object",
				"required":   []string{"query", "scope"},
				"properties": map[string]any{"query": map[string]any{"type": "string"}, "scope": map[string]any{"type": "string"}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}, "include_unverified": map[string]any{"type": "boolean"}},
			},
		},
		{
			"name":        "ingest_document",
			"description": "Ingest one normalized source document into Abra. Requires write access to the document scope.",
			"inputSchema": objectSchema([]string{"source_type", "source_url", "title", "scope", "content"}, documentSchemaProperties()),
		},
		{
			"name":        "ingest_documents",
			"description": "Ingest a normalized batch of up to 50 source documents into Abra. Top-level scope/source_type/authority/metadata may be used as defaults. Defaults to fail-fast; set continue_on_error to return per-document status for ingest failures.",
			"inputSchema": objectSchema([]string{"documents"}, map[string]any{
				"scope":             stringSchema(),
				"source_type":       stringSchema(),
				"authority":         stringSchema(),
				"authority_score":   map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				"metadata":          map[string]any{"type": "object"},
				"source_updated_at": stringSchema(),
				"continue_on_error": map[string]any{"type": "boolean"},
				"documents": map[string]any{
					"type":     "array",
					"minItems": 1,
					"maxItems": 50,
					"items":    objectSchema([]string{"source_url", "title", "content"}, documentSchemaProperties()),
				},
			}),
		},
		{
			"name":        "remember_claim",
			"description": "Store a claim. Claims without source_url are unverified by default.",
			"inputSchema": objectSchema([]string{"claim", "scope"}, map[string]any{"claim": stringSchema(), "scope": stringSchema(), "source_url": stringSchema(), "source_type": stringSchema(), "authority": stringSchema(), "created_by": stringSchema(), "approval_id": stringSchema()}),
		},
		{
			"name":        "capture_observation",
			"description": "Capture raw episodic or procedural memory without promoting it to a trusted claim. Requires write access and agent-write approval when enforcement is enabled.",
			"inputSchema": objectSchema([]string{"scope", "observation_text"}, map[string]any{
				"scope":             stringSchema(),
				"observation_text":  stringSchema(),
				"observation_type":  stringSchema(),
				"status":            map[string]any{"type": "string", "enum": []string{"raw", "proposed", "accepted", "rejected", "challenged", "deprecated", "expired"}},
				"authority":         stringSchema(),
				"authority_score":   map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				"confidence":        map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				"freshness_status":  map[string]any{"type": "string", "enum": []string{"fresh", "stale", "expired", "unknown"}},
				"source_url":        stringSchema(),
				"source_type":       stringSchema(),
				"source_id":         stringSchema(),
				"observed_at":       stringSchema(),
				"valid_from":        stringSchema(),
				"expires_at":        stringSchema(),
				"created_by":        stringSchema(),
				"approval_id":       stringSchema(),
				"subject_entity_id": stringSchema(),
				"object_entity_id":  stringSchema(),
				"relation_id":       stringSchema(),
				"claim_id":          stringSchema(),
				"document_id":       stringSchema(),
				"chunk_id":          stringSchema(),
				"source_config_id":  stringSchema(),
				"ingestion_job_id":  stringSchema(),
				"value":             map[string]any{"type": "object"},
				"metadata":          map[string]any{"type": "object"},
			}),
		},
		{
			"name":        "list_observations",
			"description": "List or search raw observations for a scope. Observations are not trusted claims unless promoted through review.",
			"inputSchema": objectSchema([]string{"scope"}, map[string]any{
				"scope":            stringSchema(),
				"query":            stringSchema(),
				"observation_type": stringSchema(),
				"status":           stringSchema(),
				"since":            stringSchema(),
				"until":            stringSchema(),
				"limit":            map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}),
		},
		{
			"name":        "challenge",
			"description": "Challenge or correct an existing claim.",
			"inputSchema": objectSchema([]string{"claim_id", "reason"}, map[string]any{"claim_id": stringSchema(), "reason": stringSchema(), "source_url": stringSchema(), "created_by": stringSchema(), "approval_id": stringSchema(), "conflicting_claim_id": stringSchema(), "severity": map[string]any{"type": "string", "enum": []string{"low", "medium", "high", "blocking"}}, "verdict": map[string]any{"type": "string", "enum": []string{"correct", "incorrect", "stale", "conflict", "useful", "not_useful"}}}),
		},
		{
			"name":        "forget",
			"description": "Deprecate a claim so it is no longer returned as trusted memory.",
			"inputSchema": objectSchema([]string{"claim_id"}, map[string]any{"claim_id": stringSchema(), "reason": stringSchema(), "created_by": stringSchema(), "approval_id": stringSchema()}),
		},
		{
			"name":        "brain_sources",
			"description": "Find supporting source chunks for a query.",
			"inputSchema": objectSchema([]string{"query", "scope"}, map[string]any{"query": stringSchema(), "scope": stringSchema(), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 20}}),
		},
		{
			"name":        "brain_summaries",
			"description": "Find hierarchical memory summaries for a scope and query.",
			"inputSchema": objectSchema([]string{"scope"}, map[string]any{"query": stringSchema(), "scope": stringSchema(), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 50}}),
		},
		{
			"name":        "brain_think",
			"description": "Return a governed brain answer with citations, gap analysis, graph paths, memory-health status, conflicts, verification, and an agent decision gate.",
			"inputSchema": objectSchema([]string{"question", "scope"}, map[string]any{
				"question":           stringSchema(),
				"scope":              stringSchema(),
				"agent":              stringSchema(),
				"limit":              map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
				"max_queries":        map[string]any{"type": "integer", "minimum": 1, "maximum": 12},
				"token_budget":       map[string]any{"type": "integer", "minimum": 300, "maximum": 12000},
				"include_unverified": map[string]any{"type": "boolean"},
			}),
		},
		{
			"name":        "memory_health",
			"description": "Return an aggregate health score and operational memory quality counts for a scope.",
			"inputSchema": objectSchema([]string{"scope"}, map[string]any{"scope": stringSchema()}),
		},
		{
			"name":        "discover_scopes",
			"description": "List memory scopes visible to the current API token with counts, so AI agents can choose the right scope before recall or working-memory composition. Pass expected_scope or query when the project scope is known or discovery is crowded.",
			"inputSchema": objectSchema(nil, map[string]any{
				"limit":          map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
				"query":          stringSchema(),
				"expected_scope": stringSchema(),
			}),
		},
		{
			"name":        "rebuild_summaries",
			"description": "Rebuild hierarchical memory summaries for existing documents in a scope. Requires a backfill approval when approval enforcement is enabled.",
			"inputSchema": objectSchema([]string{"scope"}, map[string]any{"scope": stringSchema(), "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 10000}, "approval_id": stringSchema()}),
		},
		{
			"name":        "policy_plan",
			"description": "Plan which Abra recall queries an agent should run for a task hook.",
			"inputSchema": objectSchema([]string{"hook", "task", "scope"}, map[string]any{
				"hook":          map[string]any{"type": "string", "enum": []string{"before_task", "before_code", "after_task"}},
				"task":          stringSchema(),
				"scope":         stringSchema(),
				"files":         map[string]any{"type": "array", "items": stringSchema()},
				"changed_files": map[string]any{"type": "array", "items": stringSchema()},
				"language":      stringSchema(),
				"agent":         stringSchema(),
				"limit":         map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
				"max_queries":   map[string]any{"type": "integer", "minimum": 1, "maximum": 10},
			}),
		},
		{
			"name":        "working_memory_compose",
			"description": "Compose a health-aware, source-backed working-memory packet for an agent task using policy planning, retrieval planning, recall, graph context, risks, evidence verification, memory health signals, decision gates, and next-step hints.",
			"inputSchema": objectSchema([]string{"task", "scope"}, map[string]any{
				"task":               stringSchema(),
				"scope":              stringSchema(),
				"hook":               map[string]any{"type": "string", "enum": []string{"before_task", "before_code", "after_task"}},
				"agent":              stringSchema(),
				"files":              map[string]any{"type": "array", "items": stringSchema()},
				"changed_files":      map[string]any{"type": "array", "items": stringSchema()},
				"language":           stringSchema(),
				"limit":              map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
				"max_queries":        map[string]any{"type": "integer", "minimum": 1, "maximum": 12},
				"token_budget":       map[string]any{"type": "integer", "minimum": 300, "maximum": 12000},
				"include_unverified": map[string]any{"type": "boolean"},
				"diagnostic":         map[string]any{"type": "boolean", "description": "Read-only compose for health checks; suppresses memory.composed audit events and automatic learning proposal persistence."},
			}),
		},
		{
			"name":        "list_conflicts",
			"description": "List claim or graph relation conflicts that may block autonomous agent work.",
			"inputSchema": objectSchema([]string{"scope"}, map[string]any{
				"scope":       stringSchema(),
				"status":      map[string]any{"type": "string", "enum": []string{"open", "reviewing", "resolved", "suppressed"}},
				"severity":    map[string]any{"type": "string", "enum": []string{"low", "medium", "high", "blocking"}},
				"claim_id":    stringSchema(),
				"relation_id": stringSchema(),
				"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}),
		},
		{
			"name":        "resolve_conflict",
			"description": "Resolve, suppress, reopen, or mark a claim conflict as reviewing. Requires ops access.",
			"inputSchema": objectSchema([]string{"conflict_id", "status"}, map[string]any{
				"conflict_id": stringSchema(),
				"status":      map[string]any{"type": "string", "enum": []string{"open", "reviewing", "resolved", "suppressed"}},
				"resolved_by": stringSchema(),
				"resolution":  stringSchema(),
				"metadata":    map[string]any{"type": "object"},
			}),
		},
		{
			"name":        "upsert_acl_policy",
			"description": "Create or update a scoped ACL policy for identity gateways, connectors, and agent access overlays. Requires ops access and approval when enforcement is active.",
			"inputSchema": objectSchema([]string{"scope", "name", "subject_type", "subject_id", "effect", "rule"}, map[string]any{
				"scope":        stringSchema(),
				"name":         stringSchema(),
				"status":       map[string]any{"type": "string", "enum": []string{"active", "paused", "disabled", "deleted"}},
				"priority":     map[string]any{"type": "integer", "minimum": 1, "maximum": 1000},
				"subject_type": stringSchema(),
				"subject_id":   stringSchema(),
				"effect":       map[string]any{"type": "string", "enum": []string{"allow", "deny", "require_review"}},
				"rule":         map[string]any{"type": "object"},
				"created_by":   stringSchema(),
				"metadata":     map[string]any{"type": "object"},
				"approval_id":  stringSchema(),
			}),
		},
		{
			"name":        "list_acl_policies",
			"description": "List scoped ACL policies for an operator scope, optionally filtered by subject.",
			"inputSchema": objectSchema([]string{"scope"}, map[string]any{
				"scope":        stringSchema(),
				"subject_type": stringSchema(),
				"subject_id":   stringSchema(),
				"limit":        map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}),
		},
		{
			"name":        "acl_decision",
			"description": "Evaluate scoped ACL policies for an identity gateway, connector, or agent principal.",
			"inputSchema": objectSchema([]string{"scope", "principal_type", "principal_id", "action"}, map[string]any{
				"scope":          stringSchema(),
				"principal_type": stringSchema(),
				"principal_id":   stringSchema(),
				"action":         stringSchema(),
				"resource_type":  stringSchema(),
				"resource_id":    stringSchema(),
				"context":        map[string]any{"type": "object"},
			}),
		},
		{
			"name":        "upsert_agent_policy",
			"description": "Create or update a stored agent-action policy for autonomous agent gates. Requires ops access and approval when enforcement is active.",
			"inputSchema": objectSchema([]string{"scope", "name", "effect", "rule"}, map[string]any{
				"scope":        stringSchema(),
				"name":         stringSchema(),
				"status":       map[string]any{"type": "string", "enum": []string{"active", "paused", "disabled", "deleted"}},
				"priority":     map[string]any{"type": "integer", "minimum": 1, "maximum": 1000},
				"subject_type": stringSchema(),
				"subject_id":   stringSchema(),
				"effect":       map[string]any{"type": "string", "enum": []string{"allow", "deny", "require_review"}},
				"rule":         map[string]any{"type": "object"},
				"created_by":   stringSchema(),
				"metadata":     map[string]any{"type": "object"},
				"approval_id":  stringSchema(),
			}),
		},
		{
			"name":        "list_agent_policies",
			"description": "List stored agent-action policies for an operator scope.",
			"inputSchema": objectSchema([]string{"scope"}, map[string]any{
				"scope": stringSchema(),
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}),
		},
		{
			"name":        "agent_policy_decision",
			"description": "Evaluate stored agent-action policies for a proposed risky agent action.",
			"inputSchema": objectSchema([]string{"scope", "action"}, map[string]any{
				"scope":          stringSchema(),
				"action":         stringSchema(),
				"target_type":    stringSchema(),
				"target_id":      stringSchema(),
				"principal_type": stringSchema(),
				"principal_id":   stringSchema(),
				"context":        map[string]any{"type": "object"},
			}),
		},
		{
			"name":        "upsert_agent_profile",
			"description": "Create or update a configurable agent profile with default scope, allowed scopes, denied scopes, permissions, and memory preferences. Requires ops access and approval when enforcement is active.",
			"inputSchema": objectSchema([]string{"scope", "profile_key", "display_name"}, map[string]any{
				"scope":              stringSchema(),
				"profile_key":        stringSchema(),
				"display_name":       stringSchema(),
				"agent_type":         stringSchema(),
				"status":             map[string]any{"type": "string", "enum": []string{"active", "disabled", "deleted"}},
				"principal_ref":      stringSchema(),
				"default_scope":      stringSchema(),
				"allowed_scopes":     map[string]any{"type": "array", "items": stringSchema()},
				"denied_scopes":      map[string]any{"type": "array", "items": stringSchema()},
				"permissions":        map[string]any{"type": "object"},
				"memory_preferences": map[string]any{"type": "object"},
				"created_by":         stringSchema(),
				"metadata":           map[string]any{"type": "object"},
				"approval_id":        stringSchema(),
			}),
		},
		{
			"name":        "list_agent_profiles",
			"description": "List configurable agent profiles for an operator scope.",
			"inputSchema": objectSchema([]string{"scope"}, map[string]any{
				"scope":  stringSchema(),
				"status": map[string]any{"type": "string", "enum": []string{"active", "disabled", "deleted"}},
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}),
		},
		{
			"name":        "upsert_source_config",
			"description": "Create or update a source config. Core worker scheduling supports markdown, local_repo, and git_repo; deployment overlays may store other source types and own their scheduling. Trusted authority changes require approval when enforcement is active.",
			"inputSchema": objectSchema([]string{"scope", "source_type", "name"}, map[string]any{
				"id":              stringSchema(),
				"scope":           stringSchema(),
				"source_type":     stringSchema(),
				"name":            stringSchema(),
				"base_url":        stringSchema(),
				"connector_kind":  stringSchema(),
				"status":          map[string]any{"type": "string", "enum": []string{"active", "paused", "disabled", "deleted", "error"}},
				"authority":       stringSchema(),
				"authority_score": map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				"config":          map[string]any{"type": "object"},
				"metadata":        map[string]any{"type": "object"},
				"created_by":      stringSchema(),
				"approval_id":     stringSchema(),
			}),
		},
		{
			"name":        "list_source_configs",
			"description": "List source configs for a scope.",
			"inputSchema": objectSchema([]string{"scope"}, map[string]any{
				"scope": stringSchema(),
				"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}),
		},
		{
			"name":        "enqueue_ingestion_job",
			"description": "Queue an ingestion job for an active source config. Requires write access to the source scope.",
			"inputSchema": objectSchema([]string{"source_config_id"}, map[string]any{
				"source_config_id": stringSchema(),
				"trigger_type":     map[string]any{"type": "string", "enum": []string{"manual", "schedule", "webhook", "backfill", "revalidate"}},
				"created_by":       stringSchema(),
				"max_attempts":     map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
				"metadata":         map[string]any{"type": "object"},
			}),
		},
		{
			"name":        "list_ingestion_jobs",
			"description": "List ingestion jobs for a scope, optionally filtered by source config.",
			"inputSchema": objectSchema([]string{"scope"}, map[string]any{
				"scope":            stringSchema(),
				"source_config_id": stringSchema(),
				"limit":            map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}),
		},
		{
			"name":        "retry_ingestion_job",
			"description": "Retry a failed or canceled ingestion job. Requires write access to the job scope.",
			"inputSchema": objectSchema([]string{"job_id"}, map[string]any{
				"job_id":       stringSchema(),
				"created_by":   stringSchema(),
				"max_attempts": map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
				"metadata":     map[string]any{"type": "object"},
			}),
		},
		{
			"name":        "cancel_ingestion_job",
			"description": "Cancel a queued or retrying ingestion job. Requires write access to the job scope.",
			"inputSchema": objectSchema([]string{"job_id"}, map[string]any{
				"job_id":     stringSchema(),
				"reason":     stringSchema(),
				"created_by": stringSchema(),
				"metadata":   map[string]any{"type": "object"},
			}),
		},
		{
			"name":        "propose_learning",
			"description": "Create a reviewable learning proposal without promoting it to trusted memory.",
			"inputSchema": objectSchema([]string{"scope", "proposal_type", "title", "rationale"}, map[string]any{
				"scope":         stringSchema(),
				"proposal_type": map[string]any{"type": "string", "enum": []string{"claim", "challenge", "source_refresh", "summary_rebuild", "ingestion", "policy", "graph", "other"}},
				"title":         stringSchema(),
				"rationale":     stringSchema(),
				"target_type":   stringSchema(),
				"target_id":     stringSchema(),
				"source_url":    stringSchema(),
				"confidence":    map[string]any{"type": "number", "minimum": 0, "maximum": 1},
				"payload":       map[string]any{"type": "object"},
				"created_by":    stringSchema(),
				"approval_id":   stringSchema(),
			}),
		},
		{
			"name":        "list_learning_proposals",
			"description": "List reviewable learning proposals for a scope.",
			"inputSchema": objectSchema([]string{"scope"}, map[string]any{
				"scope":  stringSchema(),
				"status": map[string]any{"type": "string", "enum": []string{"pending", "accepted", "rejected", "applied", "canceled"}},
				"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
			}),
		},
		{
			"name":        "decide_learning_proposal",
			"description": "Accept, reject, mark applied, or cancel a learning proposal. Accepted proposals return an apply_plan with the deterministic next operation; they are not auto-promoted to trusted memory.",
			"inputSchema": objectSchema([]string{"proposal_id", "status"}, map[string]any{
				"proposal_id":   stringSchema(),
				"status":        map[string]any{"type": "string", "enum": []string{"accepted", "rejected", "applied", "canceled"}},
				"reviewed_by":   stringSchema(),
				"review_reason": stringSchema(),
				"approval_id":   stringSchema(),
				"metadata":      map[string]any{"type": "object"},
			}),
		},
		{
			"name":        "request_approval",
			"description": "Create an operator approval request for risky agent writes or source changes.",
			"inputSchema": objectSchema([]string{"action", "scope", "reason"}, map[string]any{
				"action":       map[string]any{"type": "string", "enum": []string{"agent_write", "forget_claim", "challenge_claim", "source_authority_change", "scope_expansion", "backfill", "connector_enable", "acl_change", "other"}},
				"scope":        stringSchema(),
				"reason":       stringSchema(),
				"target_type":  stringSchema(),
				"target_id":    stringSchema(),
				"requested_by": stringSchema(),
				"expires_at":   stringSchema(),
				"payload":      map[string]any{"type": "object"},
				"metadata":     map[string]any{"type": "object"},
			}),
		},
	}
}

func mcpToolTraceName(name string) string {
	switch name {
	case "recall", "ingest_document", "ingest_documents", "remember_claim", "capture_observation", "list_observations", "challenge", "forget", "brain_sources", "brain_summaries", "brain_think", "memory_health", "discover_scopes", "rebuild_summaries", "policy_plan", "working_memory_compose", "list_conflicts", "resolve_conflict", "upsert_acl_policy", "list_acl_policies", "acl_decision", "upsert_agent_policy", "list_agent_policies", "agent_policy_decision", "upsert_agent_profile", "list_agent_profiles", "upsert_source_config", "list_source_configs", "enqueue_ingestion_job", "list_ingestion_jobs", "retry_ingestion_job", "cancel_ingestion_job", "propose_learning", "list_learning_proposals", "decide_learning_proposal", "request_approval":
		return name
	default:
		return "unknown"
	}
}

func mcpIngestDocumentSuccess(index int, doc brain.IngestDocumentInput, ingested brain.IngestDocumentResult, includeStatus bool) map[string]any {
	result := map[string]any{
		"index":       index,
		"document_id": ingested.DocumentID,
		"chunks":      ingested.Chunks,
		"claims":      ingested.Claims,
		"entities":    ingested.Entities,
		"relations":   ingested.Relations,
		"source_url":  doc.SourceURL,
		"scope":       doc.Scope,
	}
	if includeStatus {
		result["status"] = "ingested"
	}
	return result
}

func mcpIngestDocumentError(index int, doc brain.IngestDocumentInput, err error) map[string]any {
	result := map[string]any{
		"index":      index,
		"status":     "error",
		"error":      err.Error(),
		"source_url": doc.SourceURL,
		"scope":      doc.Scope,
	}
	if providerErr, ok := ai.ProviderErrorInfo(err); ok {
		result["error_kind"] = "provider_error"
		result["provider_error"] = providerErrorPayload(err, providerErr)["provider_error"]
	}
	return result
}

func documentSchemaProperties() map[string]any {
	return map[string]any{
		"source_type":       stringSchema(),
		"source_url":        stringSchema(),
		"source_id":         stringSchema(),
		"title":             stringSchema(),
		"scope":             stringSchema(),
		"content":           stringSchema(),
		"source_updated_at": stringSchema(),
		"authority":         stringSchema(),
		"authority_score":   map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"metadata":          map[string]any{"type": "object"},
	}
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "required": required, "properties": properties}
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func stringListArg(args map[string]any, key string) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	values := make([]string, 0, len(raw))
	for _, value := range raw {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			values = append(values, text)
		}
	}
	return values
}

func mapArg(args map[string]any, key string) map[string]any {
	raw, ok := args[key].(map[string]any)
	if !ok {
		return nil
	}
	return raw
}

func mcpDocumentInput(args map[string]any, defaults map[string]any) brain.IngestDocumentInput {
	metadata := mergeWebhookMetadata(mapArg(defaults, "metadata"), mapArg(args, "metadata"))
	authority := firstNonEmpty(stringArg(args, "authority"), stringArg(defaults, "authority"))
	if authority != "" {
		metadata["authority"] = authority
	}
	authorityScore := floatArg(args, "authority_score", floatArg(defaults, "authority_score", 0))
	if authorityScore > 0 {
		metadata["authority_score"] = authorityScore
	}
	return brain.IngestDocumentInput{
		SourceType:      firstNonEmpty(stringArg(args, "source_type"), stringArg(defaults, "source_type")),
		SourceURL:       stringArg(args, "source_url"),
		SourceID:        stringArg(args, "source_id"),
		Title:           stringArg(args, "title"),
		Scope:           firstNonEmpty(stringArg(args, "scope"), stringArg(defaults, "scope")),
		Content:         stringArg(args, "content"),
		SourceUpdatedAt: firstNonEmpty(stringArg(args, "source_updated_at"), stringArg(defaults, "source_updated_at")),
		Metadata:        metadata,
	}
}

func mcpDocumentInputs(args map[string]any) ([]brain.IngestDocumentInput, error) {
	rawDocs, ok := args["documents"].([]any)
	if !ok || len(rawDocs) == 0 {
		return nil, fmt.Errorf("documents must contain at least one document")
	}
	if len(rawDocs) > 50 {
		return nil, fmt.Errorf("documents batch limit is 50")
	}
	docs := make([]brain.IngestDocumentInput, 0, len(rawDocs))
	for index, raw := range rawDocs {
		docArgs, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("document %d must be an object", index)
		}
		docs = append(docs, mcpDocumentInput(docArgs, args))
	}
	return docs, nil
}

func intArg(args map[string]any, key string, fallback int) int {
	raw, ok := args[key].(float64)
	if !ok || raw == 0 {
		return fallback
	}
	return int(raw)
}

func floatArg(args map[string]any, key string, fallback float64) float64 {
	raw, ok := args[key].(float64)
	if !ok || raw == 0 {
		return fallback
	}
	return raw
}

func boolArg(args map[string]any, key string, fallback bool) bool {
	raw, ok := args[key].(bool)
	if !ok {
		return fallback
	}
	return raw
}

func intQuery(r *http.Request, key string, fallback int) int {
	value := r.URL.Query().Get(key)
	if value == "" {
		return fallback
	}
	var parsed int
	if _, err := fmt.Sscanf(value, "%d", &parsed); err != nil || parsed == 0 {
		return fallback
	}
	return parsed
}

func auditEventFilterFromRequest(r *http.Request) (store.AuditEventFilter, string, error) {
	query := r.URL.Query()
	eventType := strings.TrimSpace(query.Get("event_type"))
	if eventType == "" {
		eventType = strings.TrimSpace(query.Get("type"))
	}
	filter := store.AuditEventFilter{
		Scope:      strings.TrimSpace(query.Get("scope")),
		EventType:  eventType,
		TargetType: strings.TrimSpace(query.Get("target_type")),
		Limit:      intQuery(r, "limit", 100),
	}
	var err error
	if filter.Since, err = timeQuery(query.Get("since")); err != nil {
		return store.AuditEventFilter{}, "", fmt.Errorf("invalid since: %w", err)
	}
	if filter.Until, err = timeQuery(query.Get("until")); err != nil {
		return store.AuditEventFilter{}, "", fmt.Errorf("invalid until: %w", err)
	}
	format := strings.ToLower(strings.TrimSpace(query.Get("format")))
	if format == "" {
		format = "json"
	}
	if format != "json" && format != "ndjson" {
		return store.AuditEventFilter{}, "", fmt.Errorf("format must be json or ndjson")
	}
	return filter, format, nil
}

func timeQuery(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("x-request-id")
		if id == "" {
			id = time.Now().UTC().Format("20060102150405.000000000")
		}
		w.Header().Set("x-request-id", id)
		next.ServeHTTP(w, r)
	})
}

func methodNotAllowed(method string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("allow", method)
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method_not_allowed", "method": method})
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
