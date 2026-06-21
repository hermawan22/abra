package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/hermawan22/abra/internal/ingest"
)

const (
	defaultMCPSourceMaxResponseBytes        = 25 << 20
	defaultMCPSourceMaxDocuments            = 50
	defaultMCPSourceMaxDocumentContentBytes = 5 << 20
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

type MCPValidationWarning struct {
	Index     int    `json:"index,omitempty"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	SourceURL string `json:"source_url,omitempty"`
}

type MCPValidationReport struct {
	Status    string                  `json:"status"`
	Count     int                     `json:"count"`
	Documents []MCPValidationDocument `json:"documents"`
	Warnings  []MCPValidationWarning  `json:"warnings,omitempty"`
}

type MCPToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

func ValidateMCPSource(ctx context.Context, source SourceConfig) ([]MCPValidationDocument, error) {
	report, err := ValidateMCPSourceReport(ctx, source)
	if err != nil {
		return nil, err
	}
	return report.Documents, nil
}

func ValidateMCPSourceReport(ctx context.Context, source SourceConfig) (MCPValidationReport, error) {
	docs, err := fetchMCPDocuments(ctx, source)
	if err != nil {
		return MCPValidationReport{}, err
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
	return MCPValidationReport{
		Status:    "ok",
		Count:     len(out),
		Documents: out,
		Warnings:  mcpValidationWarnings(docs, source),
	}, nil
}

func ListMCPTools(ctx context.Context, source SourceConfig) ([]MCPToolInfo, error) {
	var decoded struct {
		Tools []MCPToolInfo `json:"tools"`
	}
	if err := callMCPJSONRPC(ctx, source, "tools/list", nil, &decoded, false); err != nil {
		return nil, err
	}
	return decoded.Tools, nil
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
	ingestDocs := make([]ingest.Document, 0, len(docs))
	for _, doc := range docs {
		ingestDocs = append(ingestDocs, doc.ingestDocument(source))
	}
	states, err := r.documentStates(sourceCtx, ingestDocs)
	if err != nil {
		return stats, err
	}
	changedInputs := make([]IngestDocumentInput, 0, minInt(len(docs), r.options.MaxChangedDocumentsPerSource))
	for index, doc := range docs {
		if err := sourceCtx.Err(); err != nil {
			if heartbeatErr := heartbeatLoopErr(heartbeatErrs); heartbeatErr != nil {
				return stats, heartbeatErr
			}
			return stats, err
		}
		if err := r.heartbeatJob(sourceCtx, jobID); err != nil {
			return stats, err
		}
		ingestDoc := ingestDocs[index]
		state := states[documentStateKey(ingestDoc)]
		if unchanged(ingestDoc, state) {
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
	var result mcpToolResult
	spec, _ := source.MCPSourceSpec()
	if err := callMCPJSONRPC(ctx, source, "tools/call", map[string]any{
		"name":      spec.Tool,
		"arguments": spec.Arguments,
	}, &result, true); err != nil {
		return nil, err
	}
	docs, err := mcpDocumentsFromResult(result, source)
	if err != nil {
		return nil, err
	}
	if len(docs) > defaultMCPSourceMaxDocuments {
		return nil, fmt.Errorf("mcp source %q returned %d documents; limit is %d", source.ID, len(docs), defaultMCPSourceMaxDocuments)
	}
	for index := range docs {
		docs[index] = normalizeMCPDocument(docs[index], source)
		if err := validateMCPDocument(docs[index], source.ID); err != nil {
			return nil, err
		}
	}
	return docs, nil
}

func callMCPJSONRPC(ctx context.Context, source SourceConfig, method string, params any, out any, requireTool bool) error {
	spec, err := source.MCPSourceSpec()
	if err != nil {
		return err
	}
	if requireTool {
		if err := spec.Validate(); err != nil {
			return err
		}
	} else if err := spec.ValidateServer(); err != nil {
		return err
	}
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      fmt.Sprintf("abra-%d", time.Now().UnixNano()),
		"method":  method,
	}
	if params != nil {
		body["params"] = params
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.ServerURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")
	if spec.BearerTokenEnv != "" {
		token := strings.TrimSpace(os.Getenv(spec.BearerTokenEnv))
		if token == "" {
			return fmt.Errorf("mcp source %q bearer_token_env %q is empty", source.ID, spec.BearerTokenEnv)
		}
		req.Header.Set("authorization", "Bearer "+token)
	}
	for header, envName := range spec.HeaderEnv {
		value := strings.TrimSpace(os.Getenv(envName))
		if value == "" {
			return fmt.Errorf("mcp source %q header env %q for %q is empty", source.ID, envName, header)
		}
		req.Header.Set(header, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("call mcp source %q: %w", source.ID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("call mcp source %q: status %d", source.ID, resp.StatusCode)
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, defaultMCPSourceMaxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read mcp source %q response: %w", source.ID, err)
	}
	if len(responseBody) > defaultMCPSourceMaxResponseBytes {
		return fmt.Errorf("mcp source %q response exceeds %d bytes", source.ID, defaultMCPSourceMaxResponseBytes)
	}
	var decoded struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return fmt.Errorf("decode mcp source %q response: %w", source.ID, err)
	}
	if decoded.Error != nil {
		return fmt.Errorf("mcp source %q tool error %d: %s", source.ID, decoded.Error.Code, decoded.Error.Message)
	}
	if len(decoded.Result) == 0 {
		return fmt.Errorf("mcp source %q response missing result", source.ID)
	}
	if err := json.Unmarshal(decoded.Result, out); err != nil {
		return fmt.Errorf("decode mcp source %q result: %w", source.ID, err)
	}
	return nil
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
	if len([]byte(doc.Content)) > defaultMCPSourceMaxDocumentContentBytes {
		return fmt.Errorf("mcp source %q returned document %q content with %d bytes; limit is %d", sourceID, doc.SourceURL, len([]byte(doc.Content)), defaultMCPSourceMaxDocumentContentBytes)
	}
	return nil
}

func mcpValidationWarnings(docs []mcpDocument, source SourceConfig) []MCPValidationWarning {
	warnings := []MCPValidationWarning{}
	seenURLs := map[string]int{}
	for index, doc := range docs {
		if firstIndex, ok := seenURLs[doc.SourceURL]; ok {
			warnings = append(warnings, MCPValidationWarning{
				Index:     index,
				Code:      "duplicate_source_url",
				Message:   fmt.Sprintf("document source_url duplicates document %d", firstIndex),
				SourceURL: doc.SourceURL,
			})
		} else {
			seenURLs[doc.SourceURL] = index
		}
		if source.Scope != "" && doc.Scope != source.Scope {
			warnings = append(warnings, MCPValidationWarning{
				Index:     index,
				Code:      "scope_mismatch",
				Message:   fmt.Sprintf("document scope %q differs from source scope %q", doc.Scope, source.Scope),
				SourceURL: doc.SourceURL,
			})
		}
		if doc.SourceUpdatedAt == "" {
			warnings = append(warnings, MCPValidationWarning{
				Index:     index,
				Code:      "missing_source_updated_at",
				Message:   "document has no source_updated_at; incremental freshness may be weaker",
				SourceURL: doc.SourceURL,
			})
		} else if _, err := time.Parse(time.RFC3339, doc.SourceUpdatedAt); err != nil {
			warnings = append(warnings, MCPValidationWarning{
				Index:     index,
				Code:      "invalid_source_updated_at",
				Message:   "source_updated_at should be RFC3339",
				SourceURL: doc.SourceURL,
			})
		}
	}
	return warnings
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
