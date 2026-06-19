package server

import (
	"strings"
	"testing"
	"time"

	"github.com/hermawan22/abra/internal/memory"
	"github.com/hermawan22/abra/internal/observability"
	"github.com/hermawan22/abra/internal/store"
)

func TestSmartPathMetricsPrometheus(t *testing.T) {
	observability.ResetAIProviderMetricsForTest()
	collector := newMetricsCollector()
	observability.AIProviderWaitingStart("embedding", "local")
	observability.AIProviderWaitingDone("embedding", "local", "ok", 3*time.Millisecond)
	observability.AIProviderInFlightStart("embedding", "local")
	observability.ObserveAIProviderCall("embedding", "local", "ok", 9*time.Millisecond)
	observability.AIProviderInFlightDone("embedding", "local")
	collector.observeRecall("ok", 12*time.Millisecond, store.RecallResult{
		Claims:              []store.ClaimResult{{ID: "claim-1"}},
		SupportingDocuments: []store.DocumentResult{{ID: "doc-1"}},
		GraphContext:        []store.RelationResult{{FromEntity: "A", ToEntity: "B", Type: "depends_on"}},
		RetrievalMode:       "hybrid",
	})
	collector.observeRecall("error", 2*time.Millisecond, store.RecallResult{})
	collector.observeMemory("ok", 34*time.Millisecond, memory.ComposeResult{
		Facts:               []store.ClaimResult{{ID: "claim-1"}, {ID: "claim-2"}},
		SupportingDocuments: []store.DocumentResult{{ID: "doc-1"}},
		GraphContext:        []store.RelationResult{{FromEntity: "A", ToEntity: "B", Type: "depends_on"}},
		Verification: memory.VerificationReport{
			Verdict:         "strong",
			RequiredActions: []string{"cite_evidence", "custom-action"},
			RetrievalQuality: memory.RetrievalQuality{
				ResultCount:    3,
				TopRankScore:   1.25,
				TopTextScore:   0.4,
				TopVectorScore: 0.2,
			},
		},
		AgentPolicyDecisions: []memory.AgentPolicyDecision{
			{Action: "agent_write", Decision: "require_review"},
			{Action: "custom-action", Decision: "surprise"},
		},
		AgentDecision:       memory.AgentDecision{Decision: "proceed", AutonomousAllowed: true},
		LearningSuggestions: []memory.LearningSuggestion{{ProposalType: "graph"}},
		RetrievalTrace: []memory.RetrievalTraceItem{
			{Stage: "health", Operation: "memory_health_lookup", CacheStatus: "fresh", Status: "ok"},
		},
		MemoryHealth: store.MemoryHealthResult{
			Status: "healthy",
			Score:  100,
			Signals: []store.MemoryHealthSignal{
				{Code: "memory_ready", Category: "readiness", Severity: "info"},
			},
		},
	})
	collector.observeMemory("ok", 55*time.Millisecond, memory.ComposeResult{
		Verification: memory.VerificationReport{
			Verdict:         "unsafe",
			RequiredActions: []string{"resolve_active_conflicts", "review_conflict_evidence"},
		},
		AgentDecision: memory.AgentDecision{
			Decision:          "blocked",
			ReviewRequired:    true,
			AutonomousAllowed: false,
		},
		RetrievalTrace: []memory.RetrievalTraceItem{
			{Stage: "health", Operation: "memory_health_lookup", CacheStatus: "cache_hit", Status: "ok"},
		},
		MemoryHealth: store.MemoryHealthResult{
			Status: "critical",
			Score:  45,
			Signals: []store.MemoryHealthSignal{
				{Code: "trusted_claims_from_code_documents", Category: "trust_guard", Severity: "critical"},
				{Code: "ingestion_jobs_retrying", Category: "ingestion", Severity: "warning"},
			},
		},
	})
	collector.observeMemory("ok", 13*time.Millisecond, memory.ComposeResult{
		Verification: memory.VerificationReport{
			Verdict:         "partial",
			RequiredActions: []string{"corroborate_with_additional_source"},
			RetrievalQuality: memory.RetrievalQuality{
				ResultCount:        4,
				LowSourceDiversity: true,
			},
		},
		AgentDecision: memory.AgentDecision{Decision: "caution"},
	})
	collector.observeMemory("error", 3*time.Millisecond, memory.ComposeResult{})
	collector.observeAgentPolicyDecision("decision_api", "forget_claim", "deny")

	out := collector.prometheus()
	for _, want := range []string{
		`abra_smart_path_requests_total{operation="recall",status="ok",verdict="",decision=""} 1`,
		`abra_ai_provider_calls_total{operation="embedding",provider="local",status="ok"} 1`,
		`abra_ai_provider_call_duration_milliseconds_sum{operation="embedding",provider="local",status="ok"} 9`,
		`abra_ai_provider_waits_total{operation="embedding",provider="local",status="ok"} 1`,
		`abra_ai_provider_wait_duration_milliseconds_sum{operation="embedding",provider="local",status="ok"} 3`,
		`abra_ai_provider_in_flight{operation="embedding",provider="local"} 0`,
		`abra_ai_provider_max_in_flight{operation="embedding",provider="local"} 1`,
		`abra_ai_provider_max_waiting{operation="embedding",provider="local"} 1`,
		`abra_smart_path_requests_total{operation="recall",status="error",verdict="",decision=""} 1`,
		`abra_recall_retrieval_mode_total{mode="hybrid",status="ok"} 1`,
		`abra_recall_retrieval_mode_total{mode="unknown",status="error"} 1`,
		`abra_smart_path_requests_total{operation="working_memory",status="ok",verdict="strong",decision="proceed"} 1`,
		`abra_smart_path_requests_total{operation="working_memory",status="error",verdict="",decision=""} 1`,
		`abra_working_memory_retrieval_quality_total{status="ok",verdict="strong",quality="ok"} 1`,
		`abra_working_memory_retrieval_quality_total{status="ok",verdict="partial",quality="low_source_diversity"} 1`,
		`abra_working_memory_retrieval_quality_total{status="error",verdict="unknown",quality="unknown"} 1`,
		`abra_working_memory_retrieval_top_rank_score_sum{status="ok",verdict="strong",quality="ok"} 1.250000`,
		`abra_working_memory_retrieval_last_result_count{status="ok",verdict="strong",quality="ok"} 3`,
		`abra_verification_required_actions_total{operation="working_memory",status="ok",verdict="strong",decision="proceed",action="cite_evidence"} 1`,
		`abra_verification_required_actions_total{operation="working_memory",status="ok",verdict="strong",decision="proceed",action="other"} 1`,
		`abra_verification_required_actions_total{operation="working_memory",status="ok",verdict="unsafe",decision="blocked",action="resolve_active_conflicts"} 1`,
		`abra_verification_required_actions_total{operation="working_memory",status="ok",verdict="partial",decision="caution",action="corroborate_with_additional_source"} 1`,
		`abra_working_memory_health_status_total{api_status="ok",health_status="healthy"} 1`,
		`abra_working_memory_health_status_total{api_status="ok",health_status="critical"} 1`,
		`abra_working_memory_health_status_total{api_status="error",health_status="unknown"} 1`,
		`abra_working_memory_health_signals_returned_sum{api_status="ok",health_status="critical"} 2`,
		`abra_working_memory_health_critical_signals_sum{api_status="ok",health_status="critical"} 1`,
		`abra_working_memory_health_warning_signals_sum{api_status="ok",health_status="critical"} 1`,
		`abra_working_memory_health_last_score{api_status="ok",health_status="healthy"} 100`,
		`abra_working_memory_health_lookup_total{api_status="ok",cache_status="fresh"} 1`,
		`abra_working_memory_health_lookup_total{api_status="ok",cache_status="cache_hit"} 1`,
		`abra_working_memory_health_lookup_total{api_status="error",cache_status="unknown"} 1`,
		`abra_working_memory_health_signal_total{api_status="ok",health_status="healthy",category="readiness",severity="info",code="memory_ready"} 1`,
		`abra_working_memory_health_signal_total{api_status="ok",health_status="critical",category="trust_guard",severity="critical",code="trusted_claims_from_code_documents"} 1`,
		`abra_working_memory_health_signal_total{api_status="ok",health_status="critical",category="ingestion",severity="warning",code="ingestion_jobs_retrying"} 1`,
		`abra_smart_path_facts_returned_sum{operation="working_memory",status="ok",verdict="strong",decision="proceed"} 2`,
		`abra_smart_path_graph_relations_returned_sum{operation="recall",status="ok",verdict="",decision=""} 1`,
		`abra_smart_path_autonomous_allowed_total{operation="working_memory",status="ok",verdict="strong",decision="proceed"} 1`,
		`abra_agent_policy_decisions_total{operation="working_memory",action="agent_write",decision="require_review"} 1`,
		`abra_agent_policy_decisions_total{operation="working_memory",action="other",decision="other"} 1`,
		`abra_agent_policy_decisions_total{operation="decision_api",action="forget_claim",decision="deny"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("metrics missing %q:\n%s", want, out)
		}
	}
}
