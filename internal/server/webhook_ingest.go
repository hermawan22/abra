package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hermawan22/abra/internal/brain"
)

type webhookIngestRequest struct {
	ConnectorKind   string                 `json:"connector_kind"`
	EventType       string                 `json:"event_type"`
	DeliveryID      string                 `json:"delivery_id"`
	Scope           string                 `json:"scope"`
	SourceType      string                 `json:"source_type"`
	SourceURL       string                 `json:"source_url"`
	SourceID        string                 `json:"source_id"`
	Title           string                 `json:"title"`
	Content         string                 `json:"content"`
	SourceUpdatedAt string                 `json:"source_updated_at"`
	Authority       string                 `json:"authority"`
	AuthorityScore  float64                `json:"authority_score"`
	Metadata        map[string]any         `json:"metadata"`
	Documents       []webhookDocumentInput `json:"documents"`
}

type webhookDocumentInput struct {
	Scope           string         `json:"scope"`
	SourceType      string         `json:"source_type"`
	SourceURL       string         `json:"source_url"`
	SourceID        string         `json:"source_id"`
	Title           string         `json:"title"`
	Content         string         `json:"content"`
	SourceUpdatedAt string         `json:"source_updated_at"`
	Authority       string         `json:"authority"`
	AuthorityScore  float64        `json:"authority_score"`
	Metadata        map[string]any `json:"metadata"`
}

func (h *handler) ingestWebhook(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.MaxRequestBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "read_body_failed"})
		return
	}
	if !h.validWebhookSignature(r, body) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_webhook_signature"})
		return
	}

	var input webhookIngestRequest
	if err := json.Unmarshal(body, &input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_json"})
		return
	}
	docs, err := webhookDocuments(input)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(docs) > 50 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "webhook batch limit is 50 documents"})
		return
	}

	results := make([]map[string]any, 0, len(docs))
	for index, doc := range docs {
		if !h.requireAccess(w, r, authActionWrite, doc.Scope) {
			return
		}
		result, err := h.brain.IngestDocument(r.Context(), doc)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "index": index})
			return
		}
		results = append(results, map[string]any{
			"index":       index,
			"document_id": result.DocumentID,
			"chunks":      result.Chunks,
			"claims":      result.Claims,
			"entities":    result.Entities,
			"relations":   result.Relations,
			"source_url":  doc.SourceURL,
			"scope":       doc.Scope,
		})
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"accepted":       len(results),
		"delivery_id":    strings.TrimSpace(input.DeliveryID),
		"connector_kind": strings.TrimSpace(input.ConnectorKind),
		"documents":      results,
	})
}

func (h *handler) validWebhookSignature(r *http.Request, body []byte) bool {
	if len(h.cfg.WebhookSecrets) == 0 {
		return true
	}
	signature := strings.TrimSpace(r.Header.Get("x-abra-signature"))
	if signature == "" {
		signature = strings.TrimSpace(r.Header.Get("x-hub-signature-256"))
	}
	if signature == "" {
		return false
	}
	signature = strings.TrimPrefix(signature, "sha256=")
	provided, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	for _, secret := range h.cfg.WebhookSecrets {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write(body)
		if hmac.Equal(provided, mac.Sum(nil)) {
			return true
		}
	}
	return false
}

func webhookDocuments(input webhookIngestRequest) ([]brain.IngestDocumentInput, error) {
	rawDocs := input.Documents
	if len(rawDocs) == 0 {
		rawDocs = []webhookDocumentInput{{
			Scope:           input.Scope,
			SourceType:      input.SourceType,
			SourceURL:       input.SourceURL,
			SourceID:        input.SourceID,
			Title:           input.Title,
			Content:         input.Content,
			SourceUpdatedAt: input.SourceUpdatedAt,
			Authority:       input.Authority,
			AuthorityScore:  input.AuthorityScore,
			Metadata:        input.Metadata,
		}}
	}
	docs := make([]brain.IngestDocumentInput, 0, len(rawDocs))
	for index, raw := range rawDocs {
		scope := firstNonEmpty(raw.Scope, input.Scope)
		sourceType := firstNonEmpty(raw.SourceType, input.SourceType)
		sourceURL := firstNonEmpty(raw.SourceURL, input.SourceURL)
		title := firstNonEmpty(raw.Title, input.Title)
		content := strings.TrimSpace(raw.Content)
		if content == "" && len(rawDocs) == 1 {
			content = strings.TrimSpace(input.Content)
		}
		if scope == "" || sourceType == "" || sourceURL == "" || title == "" || content == "" {
			return nil, fmt.Errorf("document %d requires scope, source_type, source_url, title, and content", index)
		}
		authority := firstNonEmpty(raw.Authority, input.Authority)
		authorityScore := raw.AuthorityScore
		if authorityScore == 0 {
			authorityScore = input.AuthorityScore
		}
		metadata := mergeWebhookMetadata(input.Metadata, raw.Metadata, map[string]any{
			"connector_kind":      strings.TrimSpace(input.ConnectorKind),
			"webhook_event_type":  strings.TrimSpace(input.EventType),
			"webhook_delivery_id": strings.TrimSpace(input.DeliveryID),
			"webhook_received_at": time.Now().UTC().Format(time.RFC3339Nano),
		})
		if authority != "" {
			metadata["authority"] = authority
		}
		if authorityScore > 0 {
			metadata["authority_score"] = authorityScore
		}
		docs = append(docs, brain.IngestDocumentInput{
			SourceType:      sourceType,
			SourceURL:       sourceURL,
			SourceID:        firstNonEmpty(raw.SourceID, input.SourceID),
			Title:           title,
			Scope:           scope,
			Content:         content,
			SourceUpdatedAt: firstNonEmpty(raw.SourceUpdatedAt, input.SourceUpdatedAt),
			Metadata:        metadata,
		})
	}
	return docs, nil
}

func mergeWebhookMetadata(maps ...map[string]any) map[string]any {
	merged := map[string]any{}
	for _, item := range maps {
		for key, value := range item {
			if strings.TrimSpace(key) != "" && value != nil {
				merged[key] = value
			}
		}
	}
	return merged
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
