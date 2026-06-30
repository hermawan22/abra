package server

import (
	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/brain"
)

func sourceConfigSchema() map[string]any {
	return objectSchema([]string{"scope", "source_type", "name"}, map[string]any{
		"id":                    stringSchema(),
		"scope":                 stringSchema(),
		"source_type":           stringSchema(),
		"name":                  stringSchema(),
		"base_url":              stringSchema(),
		"connector_kind":        stringSchema(),
		"status":                sourceConfigStatusProperty(),
		"authority":             stringSchema(),
		"authority_score":       map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"freshness_policy":      map[string]any{"type": "object"},
		"schedule_cron":         stringSchema(),
		"allow_private_network": map[string]any{"type": "boolean", "description": "For source_type=mcp, allow localhost, private IPs, or link-local connector URLs. Default false; use only for trusted local/dev connectors."},
		"allow_scope_expansion": map[string]any{"type": "boolean", "description": "For source_type=mcp, allow connector-returned documents to use scopes outside the configured source scope. Default false; require review before enabling multi-scope connectors."},
		"config":                map[string]any{"type": "object"},
		"metadata":              map[string]any{"type": "object"},
		"created_by":            stringSchema(),
		"approval_id":           stringSchema(),
	})
}

func validateMCPSourceSchema() map[string]any {
	return objectSchema([]string{"scope", "tool"}, map[string]any{
		"id":                    stringSchema(),
		"scope":                 stringSchema(),
		"name":                  stringSchema(),
		"base_url":              stringSchema(),
		"mcp_url":               stringSchema(),
		"server_url":            stringSchema(),
		"tool":                  stringSchema(),
		"arguments":             map[string]any{"type": "object"},
		"connector_kind":        stringSchema(),
		"authority":             stringSchema(),
		"authority_score":       map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"document_source_type":  stringSchema(),
		"bearer_token_env":      stringSchema(),
		"header_env":            map[string]any{"type": "object"},
		"allow_private_network": map[string]any{"type": "boolean", "description": "Allow localhost, private IPs, or link-local MCP connector URLs. Default false; use only for trusted local/dev connectors."},
		"allow_scope_expansion": map[string]any{"type": "boolean", "description": "Allow connector-returned documents to use scopes outside the configured source scope. Default false; require review before enabling multi-scope connectors."},
		"config":                map[string]any{"type": "object"},
		"metadata":              map[string]any{"type": "object"},
		"approval_id":           stringSchema(),
	})
}

func inspectConnectorSourceSchema() map[string]any {
	return objectSchema([]string{"scope"}, map[string]any{
		"id":                    stringSchema(),
		"scope":                 stringSchema(),
		"name":                  stringSchema(),
		"base_url":              stringSchema(),
		"mcp_url":               stringSchema(),
		"server_url":            stringSchema(),
		"connector_kind":        stringSchema(),
		"bearer_token_env":      stringSchema(),
		"header_env":            map[string]any{"type": "object"},
		"allow_private_network": map[string]any{"type": "boolean", "description": "Allow localhost, private IPs, or link-local MCP connector URLs. Default false; use only for trusted local/dev connectors."},
		"allow_scope_expansion": map[string]any{"type": "boolean", "description": "Allow connector-returned documents to use scopes outside the configured source scope. Default false; require review before enabling multi-scope connectors."},
		"config":                map[string]any{"type": "object"},
		"metadata":              map[string]any{"type": "object"},
	})
}

func listSourceConfigsSchema() map[string]any {
	return objectSchema([]string{"scope"}, map[string]any{
		"scope": stringSchema(),
		"limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 100},
	})
}

func sourceConfigIDSchema() map[string]any {
	return objectSchema([]string{"source_config_id"}, map[string]any{
		"source_config_id": stringSchema(),
	})
}

func sourceConfigStatusSchema() map[string]any {
	return objectSchema([]string{"source_config_id", "status"}, map[string]any{
		"source_config_id": stringSchema(),
		"status":           sourceConfigStatusProperty(),
		"created_by":       stringSchema(),
		"approval_id":      stringSchema(),
		"metadata":         map[string]any{"type": "object"},
	})
}

func enqueueIngestionJobSchema() map[string]any {
	return objectSchema([]string{"source_config_id"}, map[string]any{
		"source_config_id": stringSchema(),
		"trigger_type":     map[string]any{"type": "string", "enum": []string{"manual", "schedule", "webhook", "backfill", "revalidate"}},
		"created_by":       stringSchema(),
		"approval_id":      stringSchema(),
		"max_attempts":     map[string]any{"type": "integer", "minimum": 1, "maximum": 20},
		"metadata":         map[string]any{"type": "object"},
	})
}

func sourceConfigStatusProperty() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"active", "paused", "disabled", "deleted", "error"}}
}

func mcpIngestDocumentSuccess(index int, doc brain.IngestDocumentInput, ingested brain.IngestDocumentResult, includeStatus bool) map[string]any {
	result := map[string]any{
		"index":       index,
		"document_id": ingested.DocumentID,
		"chunks":      ingested.Chunks,
		"claims":      ingested.Claims,
		"entities":    ingested.Entities,
		"relations":   ingested.Relations,
		"source_url":  doc.SourceURL,
		"scope":       doc.Scope,
	}
	if includeStatus {
		result["status"] = "ingested"
	}
	return result
}

func mcpIngestDocumentError(index int, doc brain.IngestDocumentInput, err error) map[string]any {
	result := map[string]any{
		"index":      index,
		"status":     "error",
		"error":      err.Error(),
		"source_url": doc.SourceURL,
		"scope":      doc.Scope,
	}
	if providerErr, ok := ai.ProviderErrorInfo(err); ok {
		result["error_kind"] = "provider_error"
		result["provider_error"] = providerErrorPayload(err, providerErr)["provider_error"]
	}
	return result
}

func documentSchemaProperties() map[string]any {
	return map[string]any{
		"source_type":       stringSchema(),
		"source_url":        stringSchema(),
		"source_id":         stringSchema(),
		"title":             stringSchema(),
		"scope":             stringSchema(),
		"content":           stringSchema(),
		"source_updated_at": stringSchema(),
		"authority":         stringSchema(),
		"authority_score":   map[string]any{"type": "number", "minimum": 0, "maximum": 1},
		"approval_id":       stringSchema(),
		"metadata":          map[string]any{"type": "object"},
	}
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "required": required, "properties": properties}
}

func stringSchema() map[string]any {
	return map[string]any{"type": "string"}
}

func temporalAsOfSchema() map[string]any {
	return map[string]any{
		"type":        "string",
		"description": "Point-in-time recall timestamp. Accepts RFC3339 or date-only YYYY-MM-DD, normalized to UTC by the server.",
	}
}
