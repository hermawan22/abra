package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/hermawan22/abra/internal/memory"
	"github.com/hermawan22/abra/internal/store"
	"github.com/jackc/pgx/v5"
)

var (
	errInvalidMemoryEditLevel     = errors.New("level must be core, agent_core, or shared")
	errInvalidMemoryEditOperation = errors.New("operation must be add, update, or remove")
	errMemoryEditTargetRequired   = errors.New("key or summary_id is required for update and remove operations")
)

func (h *handler) mcpBrainToolCall(w http.ResponseWriter, r *http.Request, name string, args map[string]any) (any, bool, error) {
	switch name {
	case "brain_sources":
		return h.mcpBrainSources(w, r, args)
	case "brain_summaries":
		return h.mcpBrainSummaries(w, r, args)
	case "brain_core_memory":
		return h.mcpBrainCoreMemory(w, r, args)
	case "brain_shared_memory":
		return h.mcpBrainSharedMemory(w, r, args)
	case "brain_memory_edit_proposal":
		return h.mcpBrainMemoryEditProposal(w, r, args)
	case "brain_think":
		return h.mcpBrainThink(w, r, args)
	case "brain_entity_dossier":
		return h.mcpBrainEntityDossier(w, r, args)
	case "brain_review":
		return h.mcpBrainReview(w, r, args)
	case "brain_scorecard":
		return h.mcpBrainScorecard(w, r, args)
	case "brain_anchor_backfill":
		return h.mcpBrainAnchorBackfill(w, r, args)
	case "brain_maintain":
		return h.mcpBrainMaintain(w, r, args)
	case "brain_explain":
		return h.mcpBrainExplain(w, r, args)
	case "brain_eval_record":
		return h.mcpBrainEvalRecord(w, r, args)
	case "brain_eval_history":
		return h.mcpBrainEvalHistory(w, r, args)
	case "memory_health":
		return h.mcpBrainMemoryHealth(w, r, args)
	default:
		return nil, false, nil
	}
}

func (h *handler) mcpBrainSources(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	result, err := h.db.Sources(r.Context(), stringArg(args, "query"), scope, intArg(args, "limit", 5))
	return result, true, err
}

func (h *handler) mcpBrainSummaries(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	result, err := h.db.ListMemorySummaries(r.Context(), stringArg(args, "query"), scope, intArg(args, "limit", 10))
	return result, true, err
}

func (h *handler) mcpBrainCoreMemory(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	levels := []string{"core", "agent_core"}
	if boolArg(args, "include_shared", false) {
		levels = append(levels, "shared")
	}
	return h.mcpBrainSummaryLevels(w, r, args, levels)
}

func (h *handler) mcpBrainSharedMemory(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	return h.mcpBrainSummaryLevels(w, r, args, []string{"shared"})
}

func (h *handler) mcpBrainSummaryLevels(w http.ResponseWriter, r *http.Request, args map[string]any, levels []string) (any, bool, error) {
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	summaries, err := h.db.ListMemorySummariesByLevels(r.Context(), stringArg(args, "query"), scope, levels, intArg(args, "limit", 12))
	return map[string]any{
		"scope":     scope,
		"agent":     stringArg(args, "agent"),
		"levels":    levels,
		"summaries": summaries,
		"count":     len(summaries),
		"read_only": true,
		"edit_hint": "Use brain_memory_edit_proposal to suggest changes; this tool never writes trusted memory.",
	}, true, err
}

func (h *handler) mcpBrainMemoryEditProposal(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionWrite, scope) {
		return nil, true, nil
	}
	input, err := memoryEditProposalInput(args)
	if err != nil {
		return nil, true, err
	}
	input.Scope = scope
	proposal, created, err := h.createBrainMemoryEditProposal(r, args, input)
	return map[string]any{
		"learning_proposal": proposal,
		"proposal":          proposal,
		"created":           created,
		"truth_write":       false,
		"review_required":   true,
	}, true, err
}

func (h *handler) createBrainMemoryEditProposal(r *http.Request, args map[string]any, input store.CreateLearningProposalInput) (store.LearningProposalRecord, bool, error) {
	if boolArg(args, "dedupe", true) {
		proposal, created, err := h.db.CreateLearningProposalOnce(r.Context(), input)
		if err == nil && created {
			h.auditLearningProposed(r.Context(), proposal, "mcp_brain_memory_edit_proposal")
		}
		return proposal, created, err
	}
	proposal, err := h.db.CreateLearningProposal(r.Context(), input)
	if err == nil {
		h.auditLearningProposed(r.Context(), proposal, "mcp_brain_memory_edit_proposal")
	}
	return proposal, true, err
}

func (h *handler) mcpBrainThink(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	input := brainThinkInputFromArgs(args, scope)
	composeInput, _, err := h.applyAgentProfileToCompose(r.Context(), composeInputFromThink(input))
	if err != nil {
		return nil, true, err
	}
	applyComposeInputToThink(&input, composeInput)
	started := time.Now()
	result, err := h.memory.Think(r.Context(), input)
	status := "ok"
	if err != nil {
		status = "error"
	}
	h.metrics.observeBrainThink(status, time.Since(started), result)
	return result, true, err
}

func brainThinkInputFromArgs(args map[string]any, scope string) memory.ThinkInput {
	return memory.ThinkInput{
		Question:          stringArg(args, "question"),
		Scope:             scope,
		Agent:             stringArg(args, "agent"),
		Entity:            stringArg(args, "entity"),
		Mode:              memory.NormalizeRetrievalMode(stringArg(args, "mode")),
		AsOf:              stringArg(args, "as_of"),
		IncludeHistorical: boolArg(args, "include_historical", false),
		Synthesize:        boolArg(args, "synthesize", false),
		Limit:             intArg(args, "limit", 0),
		MaxQueries:        intArg(args, "max_queries", 0),
		TokenBudget:       intArg(args, "token_budget", 0),
		IncludeUnverified: boolArg(args, "include_unverified", false),
	}
}

func composeInputFromThink(input memory.ThinkInput) memory.ComposeInput {
	return memory.ComposeInput{
		Task:              input.Question,
		Scope:             input.Scope,
		Hook:              "before_task",
		Agent:             input.Agent,
		Entity:            input.Entity,
		Mode:              input.Mode,
		AsOf:              input.AsOf,
		IncludeHistorical: input.IncludeHistorical,
		Limit:             input.Limit,
		MaxQueries:        input.MaxQueries,
		TokenBudget:       input.TokenBudget,
		IncludeUnverified: input.IncludeUnverified,
	}
}

func applyComposeInputToThink(input *memory.ThinkInput, composeInput memory.ComposeInput) {
	input.Limit = composeInput.Limit
	input.MaxQueries = composeInput.MaxQueries
	input.TokenBudget = composeInput.TokenBudget
	input.IncludeUnverified = composeInput.IncludeUnverified
	input.Mode = composeInput.Mode
	input.AgentProfile = composeInput.AgentProfile
}

func (h *handler) mcpBrainEntityDossier(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	input := memory.ComposeInput{
		Task:              "Entity dossier: " + stringArg(args, "entity"),
		Scope:             scope,
		Hook:              "before_task",
		Agent:             stringArg(args, "agent"),
		Entity:            stringArg(args, "entity"),
		Mode:              memory.NormalizeRetrievalMode(stringArg(args, "mode")),
		AsOf:              stringArg(args, "as_of"),
		IncludeHistorical: boolArg(args, "include_historical", false),
		Limit:             intArg(args, "limit", 0),
		TokenBudget:       intArg(args, "token_budget", 0),
		IncludeUnverified: boolArg(args, "include_unverified", false),
		Diagnostic:        true,
	}
	var err error
	input, _, err = h.applyAgentProfileToCompose(r.Context(), input)
	if err != nil {
		return nil, true, err
	}
	result, err := h.memory.EntityDossier(r.Context(), memory.EntityDossierInput{
		Entity:            input.Entity,
		Scope:             input.Scope,
		Agent:             input.Agent,
		Mode:              input.Mode,
		AsOf:              input.AsOf,
		IncludeHistorical: input.IncludeHistorical,
		IncludeUnverified: input.IncludeUnverified,
		Limit:             input.Limit,
		TokenBudget:       input.TokenBudget,
		AgentProfile:      input.AgentProfile,
	})
	return result, true, err
}

func (h *handler) mcpBrainReview(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	result, err := h.memory.BrainReview(r.Context(), memory.BrainReviewInput{Scope: scope, Limit: intArg(args, "limit", 50)})
	return result, true, err
}

func (h *handler) mcpBrainScorecard(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	result, err := h.memory.BrainScorecard(r.Context(), memory.BrainScorecardInput{Scope: scope, Limit: intArg(args, "limit", 50)})
	return result, true, err
}

func (h *handler) mcpBrainAnchorBackfill(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	scope, propose, dryRun := proposalModeArgs(args)
	if !h.requireAccess(w, r, proposalModeAction(propose, dryRun), scope) {
		return nil, true, nil
	}
	result, err := h.memory.AnchorBackfill(r.Context(), memory.AnchorBackfillInput{
		Scope:     scope,
		Limit:     intArg(args, "limit", 50),
		DryRun:    dryRun,
		Propose:   propose,
		CreatedBy: firstNonEmpty(stringArg(args, "created_by"), stringArg(args, "agent"), "mcp"),
	})
	if err == nil {
		for _, proposal := range result.Proposals {
			h.auditLearningProposed(r.Context(), proposal, "mcp_brain_anchor_backfill")
		}
	}
	return result, true, err
}

func (h *handler) mcpBrainMaintain(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	scope, propose, dryRun := proposalModeArgs(args)
	if !h.requireAccess(w, r, proposalModeAction(propose, dryRun), scope) {
		return nil, true, nil
	}
	result, err := h.memory.BrainMaintain(r.Context(), memory.BrainMaintenanceInput{
		Scope:     scope,
		Limit:     intArg(args, "limit", 50),
		DryRun:    dryRun,
		Propose:   propose,
		CreatedBy: firstNonEmpty(stringArg(args, "created_by"), stringArg(args, "agent"), "mcp"),
	})
	if err == nil {
		for _, proposal := range result.Proposals {
			h.auditLearningProposed(r.Context(), proposal, "mcp_brain_maintain")
		}
	}
	return result, true, err
}

func proposalModeArgs(args map[string]any) (string, bool, bool) {
	propose := boolArg(args, "propose", false)
	dryRun := boolArg(args, "dry_run", !propose)
	return stringArg(args, "scope"), propose, dryRun
}

func proposalModeAction(propose, dryRun bool) authAction {
	if propose && !dryRun {
		return authActionWrite
	}
	return authActionRead
}

func (h *handler) mcpBrainExplain(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	traceID := stringArg(args, "trace_id")
	record, err := h.db.GetBrainTrace(r.Context(), traceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return map[string]any{
			"trace_id": traceID,
			"status":   "not_found",
			"message":  "no persisted why_trace found for this trace id",
		}, true, nil
	}
	if err != nil {
		return nil, true, err
	}
	if !h.requireAccess(w, r, authActionRead, record.Scope) {
		return nil, true, nil
	}
	return record, true, nil
}

func (h *handler) mcpBrainEvalRecord(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionWrite, scope) {
		return nil, true, nil
	}
	reportsRaw, err := json.Marshal(args["reports"])
	if err != nil {
		return nil, true, err
	}
	result, err := h.db.InsertBrainEvalRun(r.Context(), store.BrainEvalRunRecord{
		Scope:     scope,
		SuiteName: stringArg(args, "suite_name"),
		SuiteFile: stringArg(args, "suite_file"),
		Agent:     stringArg(args, "agent"),
		Total:     intArg(args, "total", 0),
		Passed:    intArg(args, "passed", 0),
		Success:   boolArg(args, "success", false),
		Reports:   reportsRaw,
		Metadata:  mapArg(args, "metadata"),
	})
	return result, true, err
}

func (h *handler) mcpBrainEvalHistory(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	result, err := h.db.ListBrainEvalRuns(r.Context(), scope, intArg(args, "limit", 10))
	return map[string]any{"runs": result}, true, err
}

func (h *handler) mcpBrainMemoryHealth(w http.ResponseWriter, r *http.Request, args map[string]any) (any, bool, error) {
	scope := stringArg(args, "scope")
	if !h.requireAccess(w, r, authActionRead, scope) {
		return nil, true, nil
	}
	result, err := h.db.MemoryHealth(r.Context(), scope)
	return result, true, err
}

func memoryEditProposalInput(args map[string]any) (store.CreateLearningProposalInput, error) {
	level := strings.TrimSpace(stringArg(args, "level"))
	switch level {
	case "core", "agent_core", "shared":
	default:
		return store.CreateLearningProposalInput{}, errInvalidMemoryEditLevel
	}
	operation := strings.TrimSpace(stringArg(args, "operation"))
	switch operation {
	case "add", "update", "remove":
	default:
		return store.CreateLearningProposalInput{}, errInvalidMemoryEditOperation
	}
	key := strings.TrimSpace(stringArg(args, "key"))
	summaryID := strings.TrimSpace(stringArg(args, "summary_id"))
	targetID := firstNonEmpty(summaryID, key)
	if targetID == "" && operation != "add" {
		return store.CreateLearningProposalInput{}, errMemoryEditTargetRequired
	}
	if targetID == "" {
		targetID = level
	}
	payload := map[string]any{
		"operation":      operation,
		"level":          level,
		"key":            key,
		"summary_id":     summaryID,
		"title":          stringArg(args, "title"),
		"summary":        stringArg(args, "summary"),
		"replacement":    stringArg(args, "replacement_text"),
		"evidence_refs":  stringListArg(args, "evidence_refs"),
		"agent":          stringArg(args, "agent"),
		"metadata":       mapArg(args, "metadata"),
		"truth_write":    false,
		"review_channel": "learning_proposals",
	}
	return store.CreateLearningProposalInput{
		ProposalType: "other",
		Title:        stringArg(args, "title"),
		Rationale:    stringArg(args, "rationale"),
		TargetType:   "memory_summary",
		TargetID:     targetID,
		SourceURL:    stringArg(args, "source_url"),
		Confidence:   floatArg(args, "confidence", 0.7),
		Payload:      payload,
		CreatedBy:    firstNonEmpty(stringArg(args, "created_by"), stringArg(args, "agent"), "mcp"),
	}, nil
}
