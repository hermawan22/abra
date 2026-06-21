package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hermawan22/abra/internal/ingest"
	"github.com/hermawan22/abra/internal/jobs"
	"github.com/hermawan22/abra/internal/store"
)

func (h *handler) validateSourceConfig(w http.ResponseWriter, r *http.Request) {
	var input store.SourceConfigRecord
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	payload, ok, err := h.validateMCPSourceRecord(w, r, input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, payload)
}

func (h *handler) validateMCPSourceRecord(w http.ResponseWriter, r *http.Request, input store.SourceConfigRecord) (map[string]any, bool, error) {
	input.Scope = strings.TrimSpace(input.Scope)
	input.SourceType = strings.TrimSpace(input.SourceType)
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		input.Name = "mcp-validation"
	}
	if input.Scope == "" || input.SourceType == "" {
		return nil, false, fmt.Errorf("scope and source_type are required")
	}
	if input.SourceType != string(ingest.SourceTypeMCP) {
		return nil, false, fmt.Errorf("only mcp source validation is supported")
	}
	if err := validateSourceConfigInput(input); err != nil {
		return nil, false, err
	}
	if !h.requireAccess(w, r, authActionWrite, input.Scope) {
		return nil, false, nil
	}
	if approvalAction := sourceValidationApprovalAction(input); approvalAction != "" && !h.requireRiskApproval(w, r, approvalRequirement{
		Action:     approvalAction,
		Scope:      input.Scope,
		TargetType: "source_config",
		TargetID:   sourceConfigApprovalTarget(input),
		ApprovalID: strings.TrimSpace(input.ApprovalID),
	}) {
		return nil, false, nil
	}
	sourceID := strings.TrimSpace(input.ID)
	if sourceID == "" {
		sourceID = "validation-" + sourceConfigApprovalTarget(input)
	}
	docs, err := jobs.ValidateMCPSource(r.Context(), jobs.SourceConfig{
		ID:             sourceID,
		Scope:          input.Scope,
		SourceType:     ingest.SourceTypeMCP,
		Name:           input.Name,
		BaseURL:        strings.TrimSpace(input.BaseURL),
		ConnectorKind:  strings.TrimSpace(input.ConnectorKind),
		Authority:      strings.TrimSpace(input.Authority),
		AuthorityScore: input.AuthorityScore,
		Config:         input.Config,
		Metadata:       input.Metadata,
	})
	if err != nil {
		return nil, false, err
	}
	return map[string]any{"status": "ok", "documents": docs, "count": len(docs)}, true, nil
}
