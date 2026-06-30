package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/brain"
	"github.com/hermawan22/abra/internal/observability"
	"github.com/hermawan22/abra/internal/store"
	"go.opentelemetry.io/otel/attribute"
)

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
	if !h.requireIngestApproval(w, r, input.Scope, input.ApprovalID) {
		return
	}
	input.Metadata = sanitizeUserIngestMetadata(input.Metadata)
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

func (h *handler) ingestDocuments(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.MaxRequestBodyBytes)
	var args map[string]any
	if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	result, ok, err := h.ingestDocumentsPayload(w, r, args)
	if !ok {
		return
	}
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

func (h *handler) ingestDocumentsPayload(w http.ResponseWriter, r *http.Request, args map[string]any) (map[string]any, bool, error) {
	docs, err := mcpDocumentInputs(args)
	if err != nil {
		return nil, true, err
	}
	for _, doc := range docs {
		if !h.requireAccess(w, r, authActionWrite, doc.Scope) {
			return nil, false, nil
		}
	}
	if !h.requireIngestDocumentsApproval(w, r, docs, stringArg(args, "approval_id")) {
		return nil, false, nil
	}
	continueOnError := boolArg(args, "continue_on_error", false)
	results := make([]map[string]any, 0, len(docs))
	accepted := 0
	failed := 0
	if !continueOnError {
		ingested, err := h.brain.IngestDocuments(r.Context(), docs)
		if err != nil {
			return nil, true, err
		}
		for index, result := range ingested {
			accepted++
			results = append(results, mcpIngestDocumentSuccess(index, docs[index], result, false))
		}
		return map[string]any{"accepted": accepted, "documents": results}, true, nil
	}
	for index, doc := range docs {
		ingested, err := h.brain.IngestDocument(r.Context(), doc)
		if err != nil {
			failed++
			results = append(results, mcpIngestDocumentError(index, doc, err))
			continue
		}
		accepted++
		results = append(results, mcpIngestDocumentSuccess(index, doc, ingested, true))
	}
	return map[string]any{"accepted": accepted, "failed": failed, "documents": results}, true, nil
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
	if providerErr.Hint != "" {
		details["hint"] = providerErr.Hint
	}
	if providerErr.BatchSize > 0 {
		details["batch_size"] = providerErr.BatchSize
		details["batch_start"] = providerErr.BatchStart
		details["batch_end"] = providerErr.BatchEnd
	}
	if providerErr.BatchTokens > 0 {
		details["batch_tokens"] = providerErr.BatchTokens
	}
	if hint := providerErrorHint(providerErr.Code, providerErr.Provider); hint != "" {
		details["hint"] = hint
	}
	return map[string]any{
		"error":          err.Error(),
		"error_kind":     "provider_error",
		"provider_error": details,
	}
}

func providerErrorHint(code, provider string) string {
	localProvider := isLocalEmbeddingProviderName(provider)
	switch code {
	case "context_overflow":
		return "Abra retries smaller embedding batches automatically; if one input still exceeds the provider context, lower ABRA_EMBEDDING_BATCH_MAX_ITEMS/ABRA_EMBEDDING_BATCH_MAX_TOKENS or split very large files before ingest."
	case "provider_timeout":
		if !localProvider {
			return "Check the compatible embedding endpoint capacity, raise EMBEDDING_TIMEOUT or lower ABRA_EMBEDDING_BATCH_MAX_ITEMS/ABRA_EMBEDDING_BATCH_MAX_TOKENS, then retry ingest."
		}
		return "Run `abra model status`; if the model is healthy, retry with a longer ABRA_CLI_TIMEOUT or lower ABRA_EMBEDDING_BATCH_MAX_ITEMS/ABRA_EMBEDDING_BATCH_MAX_TOKENS."
	case "auth_failed":
		return "Check the embedding provider API key, base URL, and model config, then retry ingest."
	default:
		return ""
	}
}

func isLocalEmbeddingProviderName(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "local", "local-smart", "qwen3":
		return true
	default:
		return false
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
