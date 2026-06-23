package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/brain"
	"github.com/hermawan22/abra/internal/config"
	"github.com/hermawan22/abra/internal/memory"
	"github.com/hermawan22/abra/internal/observability"
	"github.com/hermawan22/abra/internal/store"
)

func New(cfg config.Config, db *store.Store) (http.Handler, error) {
	brainService, err := brain.New(cfg, db)
	if err != nil {
		return nil, err
	}
	composerOptions := memory.ComposerOptions{
		HealthCacheTTL:    cfg.ComposeHealthCacheTTL,
		RecallConcurrency: cfg.ComposeRecallConcurrency,
		GraphConcurrency:  cfg.ComposeGraphConcurrency,
	}
	if cfg.SynthesisEnabled {
		synthProvider, err := newSynthesisProvider(cfg)
		if err != nil {
			return nil, err
		}
		composerOptions.Synthesizer = memory.NewExtractorSynthesizer(synthProvider, cfg.Extractor.Model, cfg.SynthesisMaxTokens)
	}
	memoryComposer := memory.NewComposerWithOptions(&composerStore{Store: db, brain: brainService}, composerOptions)
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
	mux.HandleFunc("POST /ingest/documents/batch", handler.auth(handler.ingestDocuments))
	mux.HandleFunc("GET /ingest/documents/batch", methodNotAllowed("POST"))
	mux.HandleFunc("POST /ingest/webhooks", handler.auth(handler.ingestWebhook))
	mux.HandleFunc("GET /ingest/webhooks", methodNotAllowed("POST"))
	mux.HandleFunc("POST /recall", handler.auth(handler.recall))
	mux.HandleFunc("GET /recall", methodNotAllowed("POST"))
	mux.HandleFunc("POST /claims", handler.auth(handler.rememberClaim))
	mux.HandleFunc("GET /claims", methodNotAllowed("POST"))
	mux.HandleFunc("POST /observations", handler.auth(handler.captureObservation))
	mux.HandleFunc("GET /observations", handler.auth(handler.listObservations))
	mux.HandleFunc("POST /memory/outcomes", handler.auth(handler.captureTaskOutcomeHTTP))
	mux.HandleFunc("GET /memory/outcomes", methodNotAllowed("POST"))
	mux.HandleFunc("POST /claims/{claimId}/challenge", handler.auth(handler.challengeClaim))
	mux.HandleFunc("POST /claims/{claimId}/forget", handler.auth(handler.forgetClaim))
	mux.HandleFunc("GET /conflicts", handler.auth(handler.listConflicts))
	mux.HandleFunc("POST /conflicts/{conflictId}/resolve", handler.auth(handler.resolveConflict))
	mux.HandleFunc("POST /sources", handler.auth(handler.sources))
	mux.HandleFunc("GET /sources", methodNotAllowed("POST"))
	mux.HandleFunc("GET /memory/health", handler.auth(handler.memoryHealth))
	mux.HandleFunc("POST /memory/summaries", handler.auth(handler.memorySummaries))
	mux.HandleFunc("GET /memory/summaries", methodNotAllowed("POST"))
	mux.HandleFunc("POST /memory/summaries/rebuild", handler.auth(handler.rebuildMemorySummaries))
	mux.HandleFunc("GET /memory/summaries/rebuild", methodNotAllowed("POST"))
	mux.HandleFunc("GET /learning/proposals", handler.auth(handler.listLearningProposals))
	mux.HandleFunc("POST /learning/proposals", handler.auth(handler.createLearningProposal))
	mux.HandleFunc("POST /learning/proposals/{proposalId}/decide", handler.auth(handler.decideLearningProposal))
	mux.HandleFunc("POST /learning/proposals/{proposalId}/apply", handler.auth(handler.applyLearningProposalHTTP))
	mux.HandleFunc("GET /sources/configs", handler.auth(handler.listSourceConfigs))
	mux.HandleFunc("POST /sources/configs", handler.auth(handler.upsertSourceConfig))
	mux.HandleFunc("POST /sources/configs/validate", handler.auth(handler.validateSourceConfig))
	mux.HandleFunc("GET /sources/configs/{sourceConfigId}", handler.auth(handler.getSourceConfig))
	mux.HandleFunc("POST /sources/configs/{sourceConfigId}/pause", handler.auth(handler.pauseSourceConfig))
	mux.HandleFunc("POST /sources/configs/{sourceConfigId}/resume", handler.auth(handler.resumeSourceConfig))
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

func newSynthesisProvider(cfg config.Config) (ai.ExtractorProvider, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Extractor.Provider))
	switch provider {
	case "local", "compatible", "openai-compatible", "openai", "qwen3", "local-smart", "tei", "voyage", "zeroentropy":
		return ai.NewOpenAICompatibleProvider(ai.OpenAICompatibleConfig{
			Name:      provider,
			BaseURL:   cfg.Extractor.BaseURL,
			APIKey:    cfg.Extractor.APIKey,
			ChatModel: cfg.Extractor.Model,
			Timeout:   cfg.Extractor.Timeout,
		}, nil)
	default:
		return nil, fmt.Errorf("unsupported synthesis extractor provider %q", cfg.Extractor.Provider)
	}
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
