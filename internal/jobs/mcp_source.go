package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hermawan22/abra/internal/ingest"
)

type mcpDocument struct {
	SourceType      string         `json:"source_type"`
	SourceURL       string         `json:"source_url"`
	SourceID        string         `json:"source_id"`
	Title           string         `json:"title"`
	Scope           string         `json:"scope"`
	Content         string         `json:"content"`
	SourceUpdatedAt string         `json:"source_updated_at"`
	Metadata        map[string]any `json:"metadata"`
}

type MCPValidationDocument struct {
	SourceType   string `json:"source_type"`
	SourceURL    string `json:"source_url"`
	SourceID     string `json:"source_id,omitempty"`
	Title        string `json:"title"`
	Scope        string `json:"scope"`
	ContentBytes int    `json:"content_bytes"`
}

func ValidateMCPSource(ctx context.Context, source SourceConfig) ([]MCPValidationDocument, error) {
	docs, err := fetchMCPDocuments(ctx, source)
	if err != nil {
		return nil, err
	}
	out := make([]MCPValidationDocument, 0, len(docs))
	for _, doc := range docs {
		out = append(out, MCPValidationDocument{
			SourceType:   doc.SourceType,
			SourceURL:    doc.SourceURL,
			SourceID:     doc.SourceID,
			Title:        doc.Title,
			Scope:        doc.Scope,
			ContentBytes: len([]byte(doc.Content)),
		})
	}
	return out, nil
}

type mcpJSONRPCResponse struct {
	Result mcpToolResult `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type mcpToolResult struct {
	Content           []mcpContent `json:"content"`
	StructuredContent any          `json:"structuredContent"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (r *Runner) runMCPSource(ctx context.Context, source SourceConfig, jobID string) (SourceStats, error) {
	sourceCtx, cancel := context.WithTimeout(ctx, r.options.SourceTimeout)
	defer cancel()
	heartbeatErrs := r.startHeartbeatLoop(sourceCtx, jobID, cancel)

	docs, err := fetchMCPDocuments(sourceCtx, source)
	if err != nil {
		if heartbeatErr := heartbeatLoopErr(heartbeatErrs); heartbeatErr != nil {
			return SourceStats{}, heartbeatErr
		}
		return SourceStats{}, err
	}
	stats := SourceStats{DocumentsSeen: len(docs)}
	changedInputs := make([]IngestDocumentInput, 0, minInt(len(docs), r.options.MaxChangedDocumentsPerSource))
	for _, doc := range docs {
		if err := sourceCtx.Err(); err != nil {
			if heartbeatErr := heartbeatLoopErr(heartbeatErrs); heartbeatErr != nil {
				return stats, heartbeatErr
			}
			return stats, err
		}
		if err := r.heartbeatJob(sourceCtx, jobID); err != nil {
			return stats, err
		}
		state, err := r.store.DocumentState(sourceCtx, doc.ingestDocument(source))
		if err != nil {
			return stats, fmt.Errorf("read document state for %s: %w", doc.SourceURL, err)
		}
		if unchanged(doc.ingestDocument(source), state) {
			stats.DocumentsSkipped++
			continue
		}
		if len(changedInputs) >= r.options.MaxChangedDocumentsPerSource {
			stats.DocumentsDeferred++
			continue
		}
		changedInputs = append(changedInputs, doc.ingestInput(source, jobID))
	}
	results, err := r.ingestDocumentBatch(sourceCtx, changedInputs)
	if err != nil {
		if heartbeatErr := heartbeatLoopErr(heartbeatErrs); heartbeatErr != nil {
			return stats, heartbeatErr
		}
		return stats, err
	}
	accumulateResults(&stats, results)
	if len(results) > 0 {
		if err := r.heartbeatJob(sourceCtx, jobID); err != nil {
			return stats, err
		}
	}
	if heartbeatErr := heartbeatLoopErr(heartbeatErrs); heartbeatErr != nil {
		return SourceStats{}, heartbeatErr
	}
	return stats, nil
}

func fetchMCPDocuments(ctx context.Context, source SourceConfig) ([]mcpDocument, error) {
	spec, err := source.MCPSourceSpec()
	if err != nil {
		return nil, err
	}
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      fmt.Sprintf("abra-%d", time.Now().UnixNano()),
		"method":  "tools/call",
		"params": map[string]any{
			"name":      spec.Tool,
			"arguments": spec.Arguments,
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.ServerURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")
	if spec.BearerTokenEnv != "" {
		token := strings.TrimSpace(os.Getenv(spec.BearerTokenEnv))
		if token == "" {
			return nil, fmt.Errorf("mcp source %q bearer_token_env %q is empty", source.ID, spec.BearerTokenEnv)
		}
		req.Header.Set("authorization", "Bearer "+token)
	}
	for header, envName := range spec.HeaderEnv {
		value := strings.TrimSpace(os.Getenv(envName))
		if value == "" {
			return nil, fmt.Errorf("mcp source %q header env %q for %q is empty", source.ID, envName, header)
		}
		req.Header.Set(header, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call mcp source %q: %w", source.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("call mcp source %q: status %d", source.ID, resp.StatusCode)
	}
	var decoded mcpJSONRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode mcp source %q response: %w", source.ID, err)
	}
	if decoded.Error != nil {
		return nil, fmt.Errorf("mcp source %q tool error %d: %s", source.ID, decoded.Error.Code, decoded.Error.Message)
	}
	docs, err := mcpDocumentsFromResult(decoded.Result, source)
	if err != nil {
		return nil, err
	}
	for index := range docs {
		docs[index] = normalizeMCPDocument(docs[index], source)
		if err := validateMCPDocument(docs[index], source.ID); err != nil {
			return nil, err
		}
	}
	return docs, nil
}

func mcpDocumentsFromResult(result mcpToolResult, source SourceConfig) ([]mcpDocument, error) {
	if result.StructuredContent != nil {
		if docs, err := mcpDocumentsFromAny(result.StructuredContent); err == nil && len(docs) > 0 {
			return docs, nil
		}
	}
	for _, content := range result.Content {
		if content.Type != "" && content.Type != "text" {
			continue
		}
		text := strings.TrimSpace(content.Text)
		if text == "" {
			continue
		}
		var value any
		if err := json.Unmarshal([]byte(text), &value); err != nil {
			continue
		}
		if docs, err := mcpDocumentsFromAny(value); err == nil && len(docs) > 0 {
			return docs, nil
		}
	}
	return nil, fmt.Errorf("mcp source %q returned no normalized documents", source.ID)
}

func mcpDocumentsFromAny(value any) ([]mcpDocument, error) {
	raw := value
	if object, ok := raw.(map[string]any); ok {
		if docs, exists := object["documents"]; exists {
			raw = docs
		}
	}
	bytes, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var docs []mcpDocument
	if err := json.Unmarshal(bytes, &docs); err == nil {
		return docs, nil
	}
	var doc mcpDocument
	if err := json.Unmarshal(bytes, &doc); err != nil {
		return nil, err
	}
	return []mcpDocument{doc}, nil
}

func normalizeMCPDocument(doc mcpDocument, source SourceConfig) mcpDocument {
	doc.SourceType = strings.TrimSpace(doc.SourceType)
	doc.SourceURL = strings.TrimSpace(doc.SourceURL)
	doc.SourceID = strings.TrimSpace(doc.SourceID)
	doc.Title = strings.TrimSpace(doc.Title)
	doc.Scope = strings.TrimSpace(doc.Scope)
	doc.SourceUpdatedAt = strings.TrimSpace(doc.SourceUpdatedAt)
	if doc.Scope == "" {
		doc.Scope = source.Scope
	}
	if doc.SourceType == "" {
		if spec, err := source.MCPSourceSpec(); err == nil {
			doc.SourceType = spec.SourceType
		}
	}
	if doc.Metadata == nil {
		doc.Metadata = map[string]any{}
	}
	doc.Metadata["connector_kind"] = firstNonEmptyString(source.ConnectorKind, string(source.SourceType))
	doc.Metadata["mcp_source_config_id"] = source.ID
	return doc
}

func validateMCPDocument(doc mcpDocument, sourceID string) error {
	if doc.Scope == "" {
		return fmt.Errorf("mcp source %q returned document without scope", sourceID)
	}
	if doc.SourceType == "" {
		return fmt.Errorf("mcp source %q returned document without source_type", sourceID)
	}
	if doc.SourceURL == "" {
		return fmt.Errorf("mcp source %q returned document without source_url", sourceID)
	}
	if doc.Title == "" {
		return fmt.Errorf("mcp source %q returned document without title", sourceID)
	}
	if strings.TrimSpace(doc.Content) == "" {
		return fmt.Errorf("mcp source %q returned document %q without content", sourceID, doc.SourceURL)
	}
	return nil
}

func (d mcpDocument) ingestDocument(source SourceConfig) ingest.Document {
	checksum := ingest.Checksum([]byte(d.Content))
	path := firstNonEmptyString(d.SourceID, d.SourceURL)
	return ingest.Document{
		SourceID:    d.SourceID,
		SourceType:  ingest.SourceType(d.SourceType),
		SourceURL:   d.SourceURL,
		Path:        path,
		Title:       d.Title,
		Scope:       d.Scope,
		Content:     d.Content,
		Checksum:    checksum,
		Fingerprint: ingest.Fingerprint(source.ID, path, checksum),
		Metadata:    stringMetadata(d.Metadata),
	}
}

func (d mcpDocument) ingestInput(source SourceConfig, jobID string) IngestDocumentInput {
	metadata := map[string]any{}
	for key, value := range source.Metadata {
		metadata[key] = value
	}
	for key, value := range d.Metadata {
		metadata[key] = value
	}
	metadata["source_config_id"] = source.ID
	metadata["source_config_name"] = source.Name
	if jobID != "" {
		metadata["ingestion_job_id"] = jobID
	}
	if source.Authority != "" {
		metadata["authority"] = source.Authority
	}
	if source.AuthorityScore > 0 {
		metadata["authority_score"] = source.AuthorityScore
	}
	return IngestDocumentInput{
		SourceType:      d.SourceType,
		SourceURL:       d.SourceURL,
		SourceID:        d.SourceID,
		Title:           d.Title,
		Scope:           d.Scope,
		Content:         d.Content,
		SourceUpdatedAt: d.SourceUpdatedAt,
		Metadata:        metadata,
	}
}

func stringMetadata(metadata map[string]any) map[string]string {
	out := map[string]string{}
	for key, value := range metadata {
		text := stringValue(value)
		if strings.TrimSpace(key) != "" && text != "" {
			out[strings.TrimSpace(key)] = text
		}
	}
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
