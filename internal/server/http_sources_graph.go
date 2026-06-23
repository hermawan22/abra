package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/hermawan22/abra/internal/policy"
	"github.com/hermawan22/abra/internal/store"
)

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

func (h *handler) getSourceConfig(w http.ResponseWriter, r *http.Request) {
	sourceID := strings.TrimSpace(r.PathValue("sourceConfigId"))
	source, err := h.db.GetSourceConfig(r.Context(), sourceID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionRead, source.Scope) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source_config": source})
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
	if approvalAction := sourceConfigApprovalAction(input); approvalAction != "" && !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     approvalAction,
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
		"schedule_cron":   input.ScheduleCron,
		"created_by":      input.CreatedBy,
		"approval_id":     input.ApprovalID,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source_config_id": id, "status": "upserted"})
}

func (h *handler) pauseSourceConfig(w http.ResponseWriter, r *http.Request) {
	h.setSourceConfigStatus(w, r, "paused")
}

func (h *handler) resumeSourceConfig(w http.ResponseWriter, r *http.Request) {
	h.setSourceConfigStatus(w, r, "active")
}

func (h *handler) setSourceConfigStatus(w http.ResponseWriter, r *http.Request, status string) {
	sourceID := strings.TrimSpace(r.PathValue("sourceConfigId"))
	var input struct {
		ApprovalID string         `json:"approval_id"`
		CreatedBy  string         `json:"created_by"`
		Metadata   map[string]any `json:"metadata"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	source, err := h.db.GetSourceConfig(r.Context(), sourceID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, source.Scope) {
		return
	}
	if status == "active" && !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     sourceConfigApprovalActionForStatus(source, status),
		Scope:      source.Scope,
		TargetType: "source_config",
		TargetID:   sourceConfigApprovalTarget(source),
		ApprovalID: strings.TrimSpace(input.ApprovalID),
	}) {
		return
	}
	changedBy := strings.TrimSpace(input.CreatedBy)
	if changedBy == "" {
		changedBy = "api"
	}
	metadata := sourceStatusMetadata(input.Metadata, status, changedBy)
	updated, err := h.db.UpdateSourceConfigStatus(r.Context(), sourceID, status, metadata)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := h.db.InsertAuditEvent(r.Context(), "source_config.status_changed", "source_config", sourceID, updated.Scope, updated.BaseURL, map[string]any{
		"name":           updated.Name,
		"source_type":    updated.SourceType,
		"connector_kind": updated.ConnectorKind,
		"status":         updated.Status,
		"created_by":     changedBy,
		"approval_id":    input.ApprovalID,
	}); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source_config": updated})
}

func sourceStatusMetadata(input map[string]any, status string, changedBy string) map[string]any {
	metadata := map[string]any{}
	for key, value := range input {
		metadata[key] = value
	}
	metadata["status_change"] = status
	metadata["status_changed_by"] = changedBy
	return metadata
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
