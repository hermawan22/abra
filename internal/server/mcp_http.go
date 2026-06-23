package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/hermawan22/abra/internal/ingest"
	"github.com/hermawan22/abra/internal/observability"
	"github.com/hermawan22/abra/internal/store"
	"github.com/hermawan22/abra/internal/version"
	"go.opentelemetry.io/otel/attribute"
)

func (h *handler) mcp(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, h.cfg.MaxRequestBodyBytes)
	var rpc struct {
		JSONRPC string         `json:"jsonrpc"`
		ID      any            `json:"id"`
		Method  string         `json:"method"`
		Params  map[string]any `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rpc); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "parse error"}})
		return
	}
	switch rpc.Method {
	case "initialize":
		writeJSON(w, http.StatusOK, map[string]any{
			"jsonrpc": "2.0",
			"id":      rpc.ID,
			"result": map[string]any{
				"protocolVersion": "2025-06-18",
				"serverInfo":      map[string]any{"name": "abra", "version": version.Version},
				"capabilities":    mcpCapabilities(),
			},
		})
	case "tools/list":
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{"tools": mcpTools()}})
	case "tools/call":
		h.mcpToolCall(w, r, rpc.ID, rpc.Params)
	case "resources/list":
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{"resources": mcpResources()}})
	case "resources/templates/list":
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{"resourceTemplates": mcpResourceTemplates()}})
	case "resources/read":
		h.mcpResourceRead(w, r, rpc.ID, rpc.Params)
	case "prompts/list":
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": map[string]any{"prompts": mcpPrompts()}})
	case "prompts/get":
		h.mcpPromptGet(w, rpc.ID, rpc.Params)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
	}
}

func (h *handler) mcpToolCall(w http.ResponseWriter, r *http.Request, id any, params map[string]any) {
	name, _ := params["name"].(string)
	args, _ := params["arguments"].(map[string]any)
	var err error
	ctx, span := observability.Start(r.Context(), "abra.mcp.tool",
		attribute.String("mcp.tool.name", mcpToolTraceName(name)),
	)
	r = r.WithContext(ctx)
	defer func() {
		observability.End(span, err)
	}()

	toolHandler := mcpToolCallHandlers[name]
	if toolHandler == nil {
		err = fmt.Errorf("unsupported mcp tool %q", name)
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32602, "message": "unsupported tool"}})
		return
	}
	result, handled, callErr := toolHandler(h, w, r, name, args)
	err = callErr
	if !handled || result == nil && err == nil {
		return
	}
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": -32000, "message": err.Error()}})
		return
	}
	span.SetAttributes(attribute.Bool("mcp.tool.success", true))
	raw, _ := json.MarshalIndent(result, "", "  ")
	writeJSON(w, http.StatusOK, map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"result": map[string]any{
			"content":           []map[string]any{{"type": "text", "text": string(raw)}},
			"structuredContent": result,
		},
	})
}

func mcpConnectorSourceConfigFromArgs(args map[string]any, requireTool bool) store.SourceConfigRecord {
	sourceConfig := store.SourceConfigRecord{
		ID:              stringArg(args, "id"),
		Scope:           stringArg(args, "scope"),
		SourceType:      string(ingest.SourceTypeMCP),
		Name:            firstNonEmpty(stringArg(args, "name"), "mcp-connector"),
		BaseURL:         firstNonEmpty(stringArg(args, "base_url"), stringArg(args, "mcp_url"), stringArg(args, "server_url")),
		ConnectorKind:   stringArg(args, "connector_kind"),
		Authority:       stringArg(args, "authority"),
		AuthorityScore:  floatArg(args, "authority_score", 0),
		FreshnessPolicy: mapArg(args, "freshness_policy"),
		ScheduleCron:    stringArg(args, "schedule_cron"),
		Config:          mapArg(args, "config"),
		Metadata:        mapArg(args, "metadata"),
		CreatedBy:       stringArg(args, "created_by"),
		ApprovalID:      stringArg(args, "approval_id"),
	}
	if sourceConfig.Config == nil {
		sourceConfig.Config = map[string]any{}
	}
	serverURL, _ := sourceConfig.Config["server_url"].(string)
	if sourceConfig.BaseURL != "" && strings.TrimSpace(serverURL) == "" {
		sourceConfig.Config["server_url"] = sourceConfig.BaseURL
	}
	if tool := stringArg(args, "tool"); tool != "" || requireTool {
		sourceConfig.Config["tool"] = tool
	}
	if arguments := mapArg(args, "arguments"); len(arguments) > 0 {
		sourceConfig.Config["arguments"] = arguments
	}
	if bearerTokenEnv := stringArg(args, "bearer_token_env"); bearerTokenEnv != "" {
		sourceConfig.Config["bearer_token_env"] = bearerTokenEnv
	}
	if headerEnv := stringMapArg(args, "header_env"); len(headerEnv) > 0 {
		sourceConfig.Config["header_env"] = headerEnv
	}
	if documentSourceType := stringArg(args, "document_source_type"); documentSourceType != "" {
		sourceConfig.Config["document_source_type"] = documentSourceType
	}
	return sourceConfig
}
