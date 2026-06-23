package memory

import (
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

type AgentPolicyDecision struct {
	Action        string                         `json:"action"`
	TargetType    string                         `json:"target_type"`
	TargetID      string                         `json:"target_id"`
	Allowed       bool                           `json:"allowed"`
	Decision      string                         `json:"decision"`
	Reason        string                         `json:"reason"`
	MatchedPolicy *store.AgentActionPolicyRecord `json:"matched_policy,omitempty"`
}

type AgentDecision struct {
	Decision           string   `json:"decision"`
	AutonomousAllowed  bool     `json:"autonomous_allowed"`
	ReviewRequired     bool     `json:"review_required"`
	Confidence         float64  `json:"confidence"`
	Reasons            []string `json:"reasons"`
	RequiredActions    []string `json:"required_actions,omitempty"`
	AllowedNextActions []string `json:"allowed_next_actions"`
}

func decideAgentAction(input ComposeInput, result ComposeResult) AgentDecision {
	decision := AgentDecision{
		Decision:           "proceed",
		AutonomousAllowed:  true,
		ReviewRequired:     false,
		Confidence:         result.Verification.Score,
		Reasons:            []string{"Working-memory packet is source-backed enough for the requested task."},
		AllowedNextActions: []string{"cite_evidence", "inspect_relevant_files", "run_validation"},
	}

	if !hasUsableEvidence(result) || (result.Verification.RetrievalCoverage.Targets.Facts > 0 && len(result.Facts) == 0) {
		return applyMemoryHealthGate(AgentDecision{
			Decision:           "blocked",
			AutonomousAllowed:  false,
			ReviewRequired:     true,
			Confidence:         0,
			Reasons:            []string{"No usable source-backed evidence was retrieved for this task and scope."},
			RequiredActions:    []string{"ingest_relevant_sources", "narrow_scope_or_task", "rerun_working_memory_compose"},
			AllowedNextActions: []string{"ask_for_sources", "propose_ingestion"},
		}, result.MemoryHealth)
	}

	switch result.Verification.Verdict {
	case "unsafe":
		decision.Decision = "blocked"
		decision.AutonomousAllowed = false
		decision.ReviewRequired = true
		decision.Reasons = append([]string{"Verification marked this packet unsafe."}, result.Verification.Recommendations...)
		decision.RequiredActions = appendUnique(decision.RequiredActions, result.Verification.RequiredActions...)
		decision.RequiredActions = appendUnique(decision.RequiredActions, "resolve_unsafe_claims", "refresh_or_challenge_memory")
		decision.AllowedNextActions = []string{"propose_learning", "request_approval", "cite_uncertainty"}
	case "weak":
		decision.Decision = "needs_review"
		decision.AutonomousAllowed = false
		decision.ReviewRequired = true
		decision.Reasons = append([]string{"Verification confidence is too weak for autonomous work."}, result.Verification.Recommendations...)
		decision.RequiredActions = appendUnique(decision.RequiredActions, result.Verification.RequiredActions...)
		decision.RequiredActions = appendUnique(decision.RequiredActions, "add_evidence", "rerun_retrieval")
		if result.Verification.RetrievalQuality.LowConfidence {
			decision.RequiredActions = appendUnique(decision.RequiredActions, "rerun_with_more_specific_query")
		}
		decision.AllowedNextActions = []string{"propose_learning", "ask_for_sources", "cite_uncertainty"}
	case "partial":
		decision.Decision = "caution"
		decision.AutonomousAllowed = !result.Verification.ActionRequired
		decision.ReviewRequired = result.Verification.ActionRequired
		decision.Reasons = append([]string{"Verification is partial; use the packet as guidance, not final truth."}, result.Verification.Recommendations...)
		decision.RequiredActions = appendUnique(decision.RequiredActions, result.Verification.RequiredActions...)
		decision.AllowedNextActions = []string{"inspect_relevant_files", "cite_evidence", "propose_learning", "run_validation"}
	case "strong":
		decision.Reasons = append([]string{"Verification is strong and the packet has source evidence."}, result.Verification.Recommendations...)
	}

	if graphMatters(result.Intent) && len(result.GraphContext) == 0 {
		if decision.Decision == "proceed" {
			decision.Decision = "caution"
			decision.AutonomousAllowed = true
		}
		decision.Reasons = append(decision.Reasons, "No graph relations were found for a task where cross-file or dependency impact may matter.")
		decision.RequiredActions = appendUnique(decision.RequiredActions, "inspect_related_files_manually")
	}

	if len(result.GraphWarnings) > 0 {
		decision.Reasons = appendUnique(decision.Reasons, "Graph warnings surfaced competing or opposing relations.")
		decision.RequiredActions = appendUnique(decision.RequiredActions, "review_graph_warnings")
		decision.AllowedNextActions = appendUnique(decision.AllowedNextActions, "list_conflicts")
	}

	if strings.EqualFold(input.Hook, "after_task") && len(result.LearningSuggestions) > 0 {
		decision.AllowedNextActions = appendUnique(decision.AllowedNextActions, "propose_learning")
	}
	decision = applyAgentPolicyDecisions(decision, result.AgentPolicyDecisions)
	decision = applyActiveConflictGate(decision, result.Verification.ActiveConflicts)
	decision = applyMemoryHealthGate(decision, result.MemoryHealth)
	return decision
}

func hasUsableEvidence(result ComposeResult) bool {
	return len(result.Facts)+len(result.SupportingDocuments)+len(result.Summaries)+len(result.GraphContext)+len(result.Evidence) > 0
}

func applyMemoryHealthGate(decision AgentDecision, health store.MemoryHealthResult) AgentDecision {
	switch health.Status {
	case "", "healthy":
		return decision
	case "critical":
		decision.Decision = "blocked"
		decision.AutonomousAllowed = false
		decision.ReviewRequired = true
		decision.Reasons = appendUnique(decision.Reasons, "Memory health is critical for this scope.")
		decision.RequiredActions = appendUnique(decision.RequiredActions, healthRequiredActions(health)...)
		decision.AllowedNextActions = removeAction(decision.AllowedNextActions, "request_approval")
		decision.AllowedNextActions = appendUnique(decision.AllowedNextActions, "inspect_memory_health", "propose_learning", "ask_for_operator_review", "cite_uncertainty")
	case "needs_review":
		if decision.Decision == "proceed" || decision.Decision == "caution" {
			decision.Decision = "needs_review"
		}
		decision.AutonomousAllowed = false
		decision.ReviewRequired = true
		decision.Reasons = appendUnique(decision.Reasons, "Memory health needs review for this scope.")
		decision.RequiredActions = appendUnique(decision.RequiredActions, healthRequiredActions(health)...)
		decision.AllowedNextActions = appendUnique(decision.AllowedNextActions, "inspect_memory_health", "ask_for_operator_review")
	default:
		if decision.Decision == "proceed" {
			decision.Decision = "caution"
		}
		decision.Reasons = appendUnique(decision.Reasons, "Memory health status is "+health.Status+".")
		decision.RequiredActions = appendUnique(decision.RequiredActions, "inspect_memory_health")
		decision.AllowedNextActions = appendUnique(decision.AllowedNextActions, "inspect_memory_health")
	}
	return decision
}

func healthRequiredActions(health store.MemoryHealthResult) []string {
	actions := []string{}
	for _, signal := range health.Signals {
		switch signal.Code {
		case "memory_ready":
			continue
		case "trusted_claims_from_code_documents", "learning_duplicate_pending_groups":
			actions = appendUnique(actions, "clean_up_trust_guard")
		case "ingestion_jobs_failed", "ingestion_jobs_stale_running", "ingestion_jobs_retrying":
			actions = appendUnique(actions, "inspect_ingestion_liveness")
		case "blocking_conflicts", "active_conflicts":
			actions = appendUnique(actions, "resolve_active_conflicts")
		case "source_refresh_due", "source_refresh_overdue":
			actions = appendUnique(actions, "refresh_stale_sources")
		case "source_configs_error":
			actions = appendUnique(actions, "fix_source_configs")
		case "memory_health_unavailable":
			actions = appendUnique(actions, "check_memory_health_endpoint_and_storage")
		default:
			if strings.TrimSpace(signal.Action) != "" {
				actions = appendUnique(actions, signal.Action)
			}
		}
	}
	if len(actions) == 0 && health.Status != "" && health.Status != "healthy" {
		actions = append(actions, "inspect_memory_health")
	}
	return actions
}

func applyActiveConflictGate(decision AgentDecision, conflicts []store.ConflictResult) AgentDecision {
	if len(conflicts) == 0 {
		return decision
	}
	decision.RequiredActions = appendUnique(decision.RequiredActions, "resolve_active_conflicts", "review_conflict_evidence")
	if hasRelationConflict(conflicts) {
		decision.RequiredActions = appendUnique(decision.RequiredActions, "review_relation_conflicts")
	}
	decision.AllowedNextActions = []string{"list_conflicts", "propose_learning", "ask_for_operator_review", "cite_uncertainty"}
	return decision
}

func applyAgentPolicyDecisions(decision AgentDecision, policies []AgentPolicyDecision) AgentDecision {
	for _, policy := range policies {
		switch policy.Decision {
		case "deny":
			decision.Decision = "blocked"
			decision.AutonomousAllowed = false
			decision.ReviewRequired = true
			decision.Reasons = appendUnique(decision.Reasons, "Stored agent policy denies "+policy.Action+" on "+policy.TargetType+".")
			decision.RequiredActions = appendUnique(decision.RequiredActions, "change_policy_or_scope_for_"+policy.Action)
			decision.AllowedNextActions = removeAction(decision.AllowedNextActions, "run_validation")
			decision.AllowedNextActions = appendUnique(decision.AllowedNextActions, "ask_for_operator_review", "cite_uncertainty")
		case "require_review":
			if decision.Decision == "proceed" || decision.Decision == "caution" {
				decision.Decision = "needs_review"
			}
			decision.AutonomousAllowed = false
			decision.ReviewRequired = true
			decision.Reasons = appendUnique(decision.Reasons, "Stored agent policy requires review before "+policy.Action+" on "+policy.TargetType+".")
			decision.RequiredActions = appendUnique(decision.RequiredActions, "request_approval_for_"+policy.Action)
			decision.AllowedNextActions = appendUnique(decision.AllowedNextActions, "request_approval")
		case "allow":
			decision.AllowedNextActions = appendUnique(decision.AllowedNextActions, policy.Action)
		}
	}
	return decision
}

func hasRelationConflict(conflicts []store.ConflictResult) bool {
	for _, conflict := range conflicts {
		if strings.TrimSpace(conflict.PrimaryRelationID) != "" || strings.TrimSpace(conflict.ConflictingRelationID) != "" {
			return true
		}
	}
	return false
}

func graphMatters(intent string) bool {
	switch intent {
	case "migration", "debugging", "implementation", "architecture":
		return true
	default:
		return false
	}
}

func appendUnique(values []string, extra ...string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values)+len(extra))
	for _, value := range append(values, extra...) {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func removeAction(values []string, remove string) []string {
	out := values[:0]
	for _, value := range values {
		if value != remove {
			out = append(out, value)
		}
	}
	return out
}
