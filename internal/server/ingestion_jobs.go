package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/hermawan22/abra/internal/store"
)

func (h *handler) enqueueIngestionJob(w http.ResponseWriter, r *http.Request) {
	var input store.EnqueueIngestionJobInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	source, err := h.db.GetSourceConfig(r.Context(), input.SourceConfigID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, source.Scope) {
		return
	}
	job, err := h.db.EnqueueIngestionJob(r.Context(), input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ingestion_job": job})
}

func (h *handler) retryIngestionJob(w http.ResponseWriter, r *http.Request) {
	var input store.RetryIngestionJobInput
	_ = json.NewDecoder(r.Body).Decode(&input)
	jobID := strings.TrimSpace(r.PathValue("jobId"))
	current, err := h.db.GetIngestionJob(r.Context(), jobID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, current.Scope) {
		return
	}
	job, err := h.db.RetryIngestionJob(r.Context(), jobID, input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ingestion_job": job})
}

func (h *handler) cancelIngestionJob(w http.ResponseWriter, r *http.Request) {
	var input store.CancelIngestionJobInput
	_ = json.NewDecoder(r.Body).Decode(&input)
	jobID := strings.TrimSpace(r.PathValue("jobId"))
	current, err := h.db.GetIngestionJob(r.Context(), jobID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if !h.requireAccess(w, r, authActionWrite, current.Scope) {
		return
	}
	job, err := h.db.CancelIngestionJob(r.Context(), jobID, input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ingestion_job": job})
}
