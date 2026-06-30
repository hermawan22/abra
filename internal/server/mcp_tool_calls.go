package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hermawan22/abra/internal/brain"
	"github.com/hermawan22/abra/internal/ingest"
	"github.com/hermawan22/abra/internal/jobs"
	"github.com/hermawan22/abra/internal/memory"
	"github.com/hermawan22/abra/internal/policy"
	"github.com/hermawan22/abra/internal/store"
)

func (h *handler) mcpRecallToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	query, _ := args["query"].(string)
	scope, _ := args["scope"].(string)
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	limit := intArg(args, "limit", 5)
	includeUnverified, _ := args["include_unverified"].(bool)
	result, err = h.brain.Recall(r.Context(), query, scope, limit, includeUnverified)
	return result, true, err
}

func (h *handler) mcpIngestDocumentToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	doc := mcpDocumentInput(args, nil)
	if !h.requireAccess(w, r, authActionWrite, doc.Scope) {
		return nil, true, nil
	}
	if !h.requireIngestApproval(w, r, doc.Scope, doc.ApprovalID) {
		return nil, true, nil
	}
	result, err = h.brain.IngestDocument(r.Context(), doc)
	return result, true, err
}

func (h *handler) mcpIngestDocumentsToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	var ok bool
	result, ok, err = h.ingestDocumentsPayload(w, r, args)
	if !ok {
		return nil, true, nil
	}
	return result, true, err
}

func (h *handler) mcpRememberClaimToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionWrite, scope) {
		return nil, true, nil
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
		return nil, true, nil
	}
	result, err = h.brain.RememberClaim(r.Context(), brain.RememberClaimInput{
		Claim:             stringArg(args, "claim"),
		Scope:             scope,
		SourceURL:         stringArg(args, "source_url"),
		SourceType:        stringArg(args, "source_type"),
		Authority:         stringArg(args, "authority"),
		ValidFrom:         stringArg(args, "valid_from"),
		ExpiresAt:         stringArg(args, "expires_at"),
		SupersedesClaimID: stringArg(args, "supersedes_claim_id"),
		CreatedBy:         stringArg(args, "created_by"),
		ApprovalID:        stringArg(args, "approval_id"),
		Metadata:          mapArg(args, "metadata"),
	})
	return result, true, err
}

func (h *handler) mcpCaptureObservationToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionWrite, scope) {
		return nil, true, nil
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
		return nil, true, nil
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
	return result, true, err
}

func (h *handler) mcpCaptureTaskOutcomeToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	input := memory.TaskOutcomeInput{
		Task:           stringArg(args, "task"),
		Scope:          stringArg(args, "scope"),
		Hook:           stringArg(args, "hook"),
		Agent:          stringArg(args, "agent"),
		Outcome:        stringArg(args, "outcome"),
		Summary:        stringArg(args, "summary"),
		FilesChanged:   stringListArg(args, "files_changed"),
		CommandsRun:    commandOutcomeListArg(args, "commands_run"),
		Tests:          testOutcomeArg(args, "tests_result"),
		MissingContext: stringListArg(args, "missing_context"),
		MemoryRefsUsed: memoryReferenceListArg(args, "memory_refs_used"),
		CompletedAt:    stringArg(args, "completed_at"),
		SourceURL:      stringArg(args, "source_url"),
		CreatedBy:      stringArg(args, "created_by"),
		ApprovalID:     stringArg(args, "approval_id"),
		Metadata:       mapArg(args, "metadata"),
	}
	input = memory.NormalizeTaskOutcome(input)
	if !h.requireAccess(w, r, authActionWrite, input.Scope) {
		return nil, true, nil
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:        "agent_write",
		Scope:         input.Scope,
		TargetType:    "memory_write",
		TargetID:      input.Scope,
		ApprovalID:    input.ApprovalID,
		PrincipalType: "agent",
		PrincipalID:   firstNonEmpty(input.CreatedBy, input.Agent),
	}) {
		return nil, true, nil
	}
	result, err = h.captureTaskOutcome(r.Context(), input)
	return result, true, err
}

func (h *handler) mcpListObservationsToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
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
	return result, true, err
}

func (h *handler) mcpChallengeToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	claimID := stringArg(args, "claim_id")
	scope, scopeErr := h.db.ClaimScope(r.Context(), strings.TrimSpace(claimID))
	if scopeErr != nil {
		err = scopeErr
		return result, true, err
	}
	if !h.requireAccess(w, r, authActionWrite, scope) {
		return nil, true, nil
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
		return nil, true, nil
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
	return result, true, err
}

func (h *handler) mcpForgetToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	claimID := stringArg(args, "claim_id")
	scope, scopeErr := h.db.ClaimScope(r.Context(), strings.TrimSpace(claimID))
	if scopeErr != nil {
		err = scopeErr
		return result, true, err
	}
	if !h.requireAccess(w, r, authActionWrite, scope) {
		return nil, true, nil
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
		return nil, true, nil
	}
	result, err = h.brain.ForgetClaim(r.Context(), brain.ForgetClaimInput{
		ClaimID:    claimID,
		Reason:     stringArg(args, "reason"),
		CreatedBy:  stringArg(args, "created_by"),
		ApprovalID: stringArg(args, "approval_id"),
	})
	return result, true, err
}

func (h *handler) mcpDiscoverScopesToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	principal := principalFromContext(r.Context())
	if principal == nil || !principal.allowsAction(authActionRead) {
		err = fmt.Errorf("forbidden: read role required")
		return result, true, err
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
		return result, true, err
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
		"hint":                "Use one exact scope value with brain_think, recall, policy_plan, and working_memory_compose. When you already know the project scope from `abra scope`, call discover_scopes with expected_scope set to that exact value. If an AI client says Abra has no context, run `abra agent verify . --scope <scope> --agent <agent>` first; repair MCP/token/client readiness when server_ready is true but agent_ready is false, and ingest only when verify proves the exact scope or source-backed memory is missing.",
	}
	return result, true, err
}

func (h *handler) mcpRebuildSummariesToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionWrite, scope) {
		return nil, true, nil
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     "backfill",
		Scope:      scope,
		TargetType: "memory_summaries",
		TargetID:   scope,
		ApprovalID: stringArg(args, "approval_id"),
	}) {
		return nil, true, nil
	}
	result, err = h.brain.RebuildSummaries(r.Context(), brain.RebuildSummariesInput{
		Scope:      scope,
		Limit:      intArg(args, "limit", 1000),
		ApprovalID: stringArg(args, "approval_id"),
	})
	return result, true, err
}

func (h *handler) mcpPolicyPlanToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	if scope := stringArg(args, "scope"); strings.TrimSpace(scope) != "" && !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
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
		return result, true, err
	}
	engine := policy.NewEngine(config)
	result = engine.Plan(event)
	return result, true, err
}

func (h *handler) mcpWorkingMemoryComposeToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	input := memory.ComposeInput{
		Task:              stringArg(args, "task"),
		Scope:             scope,
		Hook:              stringArg(args, "hook"),
		Agent:             stringArg(args, "agent"),
		Entity:            stringArg(args, "entity"),
		Mode:              memory.NormalizeRetrievalMode(stringArg(args, "mode")),
		AsOf:              stringArg(args, "as_of"),
		IncludeHistorical: boolArg(args, "include_historical", false),
		Files:             stringListArg(args, "files"),
		ChangedFiles:      stringListArg(args, "changed_files"),
		Language:          stringArg(args, "language"),
		Limit:             intArg(args, "limit", 0),
		MaxQueries:        intArg(args, "max_queries", 0),
		TokenBudget:       intArg(args, "token_budget", 0),
		IncludeUnverified: boolArg(args, "include_unverified", false),
		Diagnostic:        boolArg(args, "diagnostic", false),
		PersistLearning:   boolArg(args, "persist_learning", false),
	}
	if input.PersistLearning && !h.requireAccess(w, r, authActionWrite, scope) {
		return nil, true, nil
	}
	var profileErr error
	input, _, profileErr = h.applyAgentProfileToCompose(r.Context(), input)
	if profileErr != nil {
		err = profileErr
		return result, true, err
	}
	started := time.Now()
	packet, composeErr := h.memory.Compose(r.Context(), input)
	if composeErr == nil {
		if shouldAutoPersistComposeLearning(input) {
			h.persistComposeLearningSuggestions(r.Context(), &packet, stringArg(args, "agent"))
		}
		result = packet
	}
	err = composeErr
	status := "ok"
	if err != nil {
		status = "error"
	}
	h.metrics.observeMemory(status, time.Since(started), packet)
	return result, true, err
}

func (h *handler) mcpListConflictsToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	result, err = h.db.ListConflicts(r.Context(), store.ConflictFilter{
		Scope:      scope,
		Status:     stringArg(args, "status"),
		Severity:   stringArg(args, "severity"),
		ClaimID:    stringArg(args, "claim_id"),
		RelationID: stringArg(args, "relation_id"),
		Limit:      intArg(args, "limit", 50),
	})
	return result, true, err
}

func (h *handler) mcpResolveConflictToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	conflictID := stringArg(args, "conflict_id")
	conflict, getErr := h.db.GetConflict(r.Context(), conflictID)
	if getErr != nil {
		err = getErr
		return result, true, err
	}
	if !h.requireAccess(w, r, authActionOps, conflict.Scope) {
		return nil, true, nil
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
	return result, true, err
}

func (h *handler) mcpUpsertAclPolicyToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
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
		return nil, true, nil
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     "acl_change",
		Scope:      policyRecord.Scope,
		TargetType: "acl_policy",
		TargetID:   aclPolicyApprovalTarget(policyRecord),
		ApprovalID: policyRecord.ApprovalID,
	}) {
		return nil, true, nil
	}
	created, createErr := h.db.UpsertACLPolicy(r.Context(), policyRecord)
	if createErr != nil {
		err = createErr
		return result, true, err
	}
	h.auditACLPolicyUpserted(r.Context(), created, "mcp")
	result = created
	return result, true, err
}

func (h *handler) mcpListAclPoliciesToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionOps, scope) {
		return nil, true, nil
	}
	result, err = h.db.ListACLPolicies(r.Context(), scope, stringArg(args, "subject_type"), stringArg(args, "subject_id"), intArg(args, "limit", 50))
	return result, true, err
}

func (h *handler) mcpAclDecisionToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
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
	return result, true, err
}

func (h *handler) mcpUpsertAgentPolicyToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
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
		return nil, true, nil
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     "acl_change",
		Scope:      policyRecord.Scope,
		TargetType: "agent_policy",
		TargetID:   agentActionPolicyApprovalTarget(policyRecord),
		ApprovalID: policyRecord.ApprovalID,
	}) {
		return nil, true, nil
	}
	created, createErr := h.db.UpsertAgentActionPolicy(r.Context(), policyRecord)
	if createErr != nil {
		err = createErr
		return result, true, err
	}
	h.auditAgentPolicyUpserted(r.Context(), created, "mcp")
	result = created
	return result, true, err
}

func (h *handler) mcpListAgentPoliciesToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionOps, scope) {
		return nil, true, nil
	}
	result, err = h.db.ListAgentActionPolicies(r.Context(), scope, intArg(args, "limit", 50))
	return result, true, err
}

func (h *handler) mcpAgentPolicyDecisionToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
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
		return result, true, err
	}
	h.metrics.observeAgentPolicyDecision("mcp_decision", input.Action, decision.Decision)
	result = decision
	return result, true, err
}

func (h *handler) mcpUpsertAgentProfileToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
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
		return nil, true, nil
	}
	if !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     "acl_change",
		Scope:      profile.Scope,
		TargetType: "agent_profile",
		TargetID:   agentProfileApprovalTarget(profile),
		ApprovalID: profile.ApprovalID,
	}) {
		return nil, true, nil
	}
	created, createErr := h.db.UpsertAgentProfile(r.Context(), profile)
	if createErr != nil {
		err = createErr
		return result, true, err
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
	return result, true, err
}

func (h *handler) mcpListAgentProfilesToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionOps, scope) {
		return nil, true, nil
	}
	result, err = h.db.ListAgentProfiles(r.Context(), scope, stringArg(args, "status"), intArg(args, "limit", 50))
	return result, true, err
}

func (h *handler) mcpUpsertSourceConfigToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	sourceConfig := store.SourceConfigRecord{
		ID:              stringArg(args, "id"),
		Scope:           stringArg(args, "scope"),
		SourceType:      stringArg(args, "source_type"),
		Name:            stringArg(args, "name"),
		BaseURL:         stringArg(args, "base_url"),
		ConnectorKind:   stringArg(args, "connector_kind"),
		Status:          stringArg(args, "status"),
		Authority:       stringArg(args, "authority"),
		AuthorityScore:  floatArg(args, "authority_score", 0),
		FreshnessPolicy: mapArg(args, "freshness_policy"),
		ScheduleCron:    stringArg(args, "schedule_cron"),
		Config:          mapArg(args, "config"),
		Metadata:        mapArg(args, "metadata"),
		CreatedBy:       stringArg(args, "created_by"),
		ApprovalID:      stringArg(args, "approval_id"),
	}
	if boolArg(args, "allow_private_network", false) {
		if sourceConfig.Config == nil {
			sourceConfig.Config = map[string]any{}
		}
		sourceConfig.Config["allow_private_network"] = true
	}
	if boolArg(args, "allow_scope_expansion", false) {
		if sourceConfig.Config == nil {
			sourceConfig.Config = map[string]any{}
		}
		sourceConfig.Config["allow_scope_expansion"] = true
	}
	sourceConfig.Scope = strings.TrimSpace(sourceConfig.Scope)
	sourceConfig.SourceType = strings.TrimSpace(sourceConfig.SourceType)
	sourceConfig.Name = strings.TrimSpace(sourceConfig.Name)
	if sourceConfig.Scope == "" || sourceConfig.SourceType == "" || sourceConfig.Name == "" {
		err = fmt.Errorf("scope, source_type, and name are required")
		return result, true, err
	}
	if validateErr := validateSourceConfigInput(sourceConfig); validateErr != nil {
		err = validateErr
		return result, true, err
	}
	if !h.requireAccess(w, r, authActionWrite, sourceConfig.Scope) {
		return nil, true, nil
	}
	if approvalAction := sourceConfigApprovalAction(sourceConfig); approvalAction != "" && !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     approvalAction,
		Scope:      sourceConfig.Scope,
		TargetType: "source_config",
		TargetID:   sourceConfigApprovalTarget(sourceConfig),
		ApprovalID: sourceConfig.ApprovalID,
	}) {
		return nil, true, nil
	}
	id, upsertErr := h.db.UpsertSourceConfig(r.Context(), sourceConfig)
	if upsertErr != nil {
		err = upsertErr
		return result, true, err
	}
	if auditErr := h.db.InsertAuditEvent(r.Context(), "source_config.upserted", "source_config", id, sourceConfig.Scope, sourceConfig.BaseURL, map[string]any{
		"name":            sourceConfig.Name,
		"source_type":     sourceConfig.SourceType,
		"connector_kind":  sourceConfig.ConnectorKind,
		"status":          sourceConfig.Status,
		"authority":       sourceConfig.Authority,
		"authority_score": sourceConfig.AuthorityScore,
		"schedule_cron":   sourceConfig.ScheduleCron,
		"created_by":      sourceConfig.CreatedBy,
		"approval_id":     sourceConfig.ApprovalID,
		"channel":         "mcp",
	}); auditErr != nil {
		err = auditErr
		return result, true, err
	}
	result = map[string]any{"source_config_id": id, "status": "upserted"}
	return result, true, err
}

func (h *handler) mcpValidateMcpSourceToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	sourceConfig := mcpConnectorSourceConfigFromArgs(args, true)
	var ok bool
	result, ok, err = h.validateMCPSourceRecord(w, r, sourceConfig)
	if !ok {
		return nil, true, nil
	}
	if err != nil {
		return result, true, err
	}
	return result, true, err
}

func (h *handler) mcpInspectConnectorSourceToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	sourceConfig := mcpConnectorSourceConfigFromArgs(args, false)
	sourceConfig.Scope = strings.TrimSpace(sourceConfig.Scope)
	if sourceConfig.Scope == "" {
		err = fmt.Errorf("scope is required")
		return result, true, err
	}
	if !h.requireAccess(w, r, authActionWrite, sourceConfig.Scope) {
		return nil, true, nil
	}
	if approvalAction := sourceValidationApprovalAction(sourceConfig); approvalAction != "" && !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     approvalAction,
		Scope:      sourceConfig.Scope,
		TargetType: "source_config",
		TargetID:   sourceConfigApprovalTarget(sourceConfig),
		ApprovalID: strings.TrimSpace(sourceConfig.ApprovalID),
	}) {
		return nil, true, nil
	}
	tools, listErr := jobs.ListMCPTools(r.Context(), jobs.SourceConfig{
		ID:             firstNonEmpty(sourceConfig.ID, "connector-inspect"),
		Scope:          sourceConfig.Scope,
		SourceType:     ingest.SourceTypeMCP,
		Name:           sourceConfig.Name,
		BaseURL:        sourceConfig.BaseURL,
		ConnectorKind:  sourceConfig.ConnectorKind,
		Authority:      sourceConfig.Authority,
		AuthorityScore: sourceConfig.AuthorityScore,
		Config:         sourceConfig.Config,
		Metadata:       sourceConfig.Metadata,
	})
	if listErr != nil {
		err = listErr
		return result, true, err
	}
	result = map[string]any{"status": "ok", "tools": tools, "count": len(tools)}
	return result, true, err
}

func (h *handler) mcpListSourceConfigsToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	result, err = h.db.ListSourceConfigs(r.Context(), scope, intArg(args, "limit", 50))
	return result, true, err
}

func (h *handler) mcpGetSourceConfigToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	sourceConfigID := stringArg(args, "source_config_id")
	source, getErr := h.db.GetSourceConfig(r.Context(), sourceConfigID)
	if getErr != nil {
		err = getErr
		return result, true, err
	}
	if !h.requireAccess(w, r, authActionRead, source.Scope) {
		return nil, true, nil
	}
	result = source
	return result, true, err
}

func (h *handler) mcpSetSourceConfigStatusToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	sourceConfigID := stringArg(args, "source_config_id")
	status := stringArg(args, "status")
	source, getErr := h.db.GetSourceConfig(r.Context(), sourceConfigID)
	if getErr != nil {
		err = getErr
		return result, true, err
	}
	if !h.requireAccess(w, r, authActionWrite, source.Scope) {
		return nil, true, nil
	}
	if status == "active" && !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     sourceConfigApprovalActionForStatus(source, status),
		Scope:      source.Scope,
		TargetType: "source_config",
		TargetID:   sourceConfigApprovalTarget(source),
		ApprovalID: stringArg(args, "approval_id"),
	}) {
		return nil, true, nil
	}
	changedBy := firstNonEmpty(stringArg(args, "created_by"), "mcp")
	metadata := map[string]any{
		"status_change":     status,
		"status_changed_by": changedBy,
		"channel":           "mcp",
	}
	for key, value := range mapArg(args, "metadata") {
		metadata[key] = value
	}
	updated, updateErr := h.db.UpdateSourceConfigStatus(r.Context(), sourceConfigID, status, metadata)
	if updateErr != nil {
		err = updateErr
		return result, true, err
	}
	if auditErr := h.db.InsertAuditEvent(r.Context(), "source_config.status_changed", "source_config", sourceConfigID, updated.Scope, updated.BaseURL, map[string]any{
		"name":           updated.Name,
		"source_type":    updated.SourceType,
		"connector_kind": updated.ConnectorKind,
		"status":         updated.Status,
		"created_by":     changedBy,
		"approval_id":    stringArg(args, "approval_id"),
		"channel":        "mcp",
	}); auditErr != nil {
		err = auditErr
		return result, true, err
	}
	result = updated
	return result, true, err
}

func (h *handler) mcpEnqueueIngestionJobToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	sourceConfigID := stringArg(args, "source_config_id")
	source, getErr := h.db.GetSourceConfig(r.Context(), sourceConfigID)
	if getErr != nil {
		err = getErr
		return result, true, err
	}
	if !h.requireAccess(w, r, authActionWrite, source.Scope) {
		return nil, true, nil
	}
	if strings.TrimSpace(stringArg(args, "trigger_type")) == "backfill" && !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     "backfill",
		Scope:      source.Scope,
		TargetType: "source_config",
		TargetID:   sourceConfigApprovalTarget(source),
		ApprovalID: stringArg(args, "approval_id"),
	}) {
		return nil, true, nil
	}
	result, err = h.db.EnqueueIngestionJob(r.Context(), store.EnqueueIngestionJobInput{
		SourceConfigID: sourceConfigID,
		TriggerType:    stringArg(args, "trigger_type"),
		CreatedBy:      stringArg(args, "created_by"),
		ApprovalID:     stringArg(args, "approval_id"),
		MaxAttempts:    intArg(args, "max_attempts", 0),
		Metadata:       mapArg(args, "metadata"),
	})
	return result, true, err
}

func (h *handler) mcpListIngestionJobsToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	result, err = h.db.ListIngestionJobs(r.Context(), scope, stringArg(args, "source_config_id"), intArg(args, "limit", 50))
	return result, true, err
}

func (h *handler) mcpRetryIngestionJobToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	jobID := stringArg(args, "job_id")
	current, getErr := h.db.GetIngestionJob(r.Context(), jobID)
	if getErr != nil {
		err = getErr
		return result, true, err
	}
	if !h.requireAccess(w, r, authActionWrite, current.Scope) {
		return nil, true, nil
	}
	result, err = h.db.RetryIngestionJob(r.Context(), jobID, store.RetryIngestionJobInput{
		CreatedBy:   stringArg(args, "created_by"),
		MaxAttempts: intArg(args, "max_attempts", 0),
		Metadata:    mapArg(args, "metadata"),
	})
	return result, true, err
}

func (h *handler) mcpCancelIngestionJobToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	jobID := stringArg(args, "job_id")
	current, getErr := h.db.GetIngestionJob(r.Context(), jobID)
	if getErr != nil {
		err = getErr
		return result, true, err
	}
	if !h.requireAccess(w, r, authActionWrite, current.Scope) {
		return nil, true, nil
	}
	result, err = h.db.CancelIngestionJob(r.Context(), jobID, store.CancelIngestionJobInput{
		Reason:    stringArg(args, "reason"),
		CreatedBy: stringArg(args, "created_by"),
		Metadata:  mapArg(args, "metadata"),
	})
	return result, true, err
}

func (h *handler) mcpProposeLearningToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionWrite, scope) {
		return nil, true, nil
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
		return result, true, err
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
		return result, true, err
	}
	if hasObservationTarget {
		h.linkObservationLearningProposal(r.Context(), observation, proposal, input.CreatedBy, "mcp")
	}
	if created {
		h.auditLearningProposed(r.Context(), proposal, "mcp")
	}
	result = learningProposalMCPResult(proposal, created, nil)
	return result, true, err
}

func (h *handler) mcpListLearningProposalsToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	var proposals any
	proposals, err = h.db.ListLearningProposals(r.Context(), scope, stringArg(args, "status"), intArg(args, "limit", 50))
	if err == nil {
		result = learningProposalsMCPResult(proposals)
	}
	return result, true, err
}

func learningProposalsMCPResult(proposals any) map[string]any {
	return map[string]any{"learning_proposals": proposals}
}

func learningProposalMCPResult(proposal store.LearningProposalRecord, created bool, extras map[string]any) map[string]any {
	result := map[string]any{
		"learning_proposal": proposal,
		"proposal":          proposal,
		"created":           created,
	}
	for key, value := range extras {
		result[key] = value
	}
	return result
}

func (h *handler) mcpDecideLearningProposalToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	proposalID := stringArg(args, "proposal_id")
	proposal, getErr := h.db.GetLearningProposal(r.Context(), proposalID)
	if getErr != nil {
		err = getErr
		return result, true, err
	}
	if !h.requireAccess(w, r, authActionWrite, proposal.Scope) {
		return nil, true, nil
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
		return result, true, err
	}
	h.auditLearningDecided(r.Context(), decided, "mcp")
	result = learningProposalMCPResult(decided, false, map[string]any{"apply_plan": buildLearningApplyPlan(decided, h.cfg.ApprovalMode)})
	return result, true, err
}

func (h *handler) mcpApplyLearningProposalToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	proposalID := stringArg(args, "proposal_id")
	proposal, getErr := h.db.GetLearningProposal(r.Context(), proposalID)
	if getErr != nil {
		err = getErr
		return result, true, err
	}
	if !h.requireAccess(w, r, authActionWrite, proposal.Scope) {
		return nil, true, nil
	}
	input := applyLearningProposalInput{
		AppliedBy:  stringArg(args, "applied_by"),
		ApprovalID: stringArg(args, "approval_id"),
		Metadata:   mapArg(args, "metadata"),
	}
	if !h.requireLearningApplyApproval(w, r, proposal, input) {
		return nil, true, nil
	}
	appliedBy := firstNonEmpty(strings.TrimSpace(input.AppliedBy), proposal.ReviewedBy, proposal.CreatedBy, "mcp")
	approvalID := firstNonEmpty(strings.TrimSpace(input.ApprovalID), proposal.ApprovalID)
	claimed, claimErr := h.db.BeginLearningProposalApply(r.Context(), proposal.ID, store.ApplyLearningProposalInput{
		AppliedBy:  appliedBy,
		ApprovalID: approvalID,
		Metadata:   input.Metadata,
	})
	if claimErr != nil {
		err = claimErr
		return result, true, err
	}
	applyResult, applyErr := h.applyLearningProposal(r.Context(), claimed, input)
	if applyErr != nil {
		_, _ = h.db.ResetLearningProposalApply(r.Context(), proposal.ID, store.ApplyLearningProposalInput{
			Metadata: map[string]any{"apply_channel": "mcp"},
		}, applyErr)
		err = applyErr
		return result, true, err
	}
	applied, markErr := h.db.MarkLearningProposalApplied(r.Context(), proposal.ID, store.ApplyLearningProposalInput{
		AppliedBy:  appliedBy,
		ApprovalID: approvalID,
		Metadata: mergeWebhookMetadata(input.Metadata, map[string]any{
			"apply_result": applyResult,
		}),
	})
	if markErr != nil {
		err = markErr
		return result, true, err
	}
	h.auditLearningApplied(r.Context(), applied, "mcp", applyResult)
	result = learningProposalMCPResult(applied, false, map[string]any{"apply_plan": buildLearningApplyPlan(applied, h.cfg.ApprovalMode), "apply_result": applyResult})
	return result, true, err
}

func (h *handler) mcpRequestApprovalToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	var result any
	var err error
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionWrite, scope) {
		return nil, true, nil
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
	return result, true, err
}
