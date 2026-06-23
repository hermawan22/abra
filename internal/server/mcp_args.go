package server

import (
	"fmt"
	"strings"

	"github.com/hermawan22/abra/internal/brain"
	"github.com/hermawan22/abra/internal/memory"
)

func stringArg(args map[string]any, key string) string {
	value, _ := args[key].(string)
	return value
}

func stringListArg(args map[string]any, key string) []string {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	values := make([]string, 0, len(raw))
	for _, value := range raw {
		if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
			values = append(values, text)
		}
	}
	return values
}

func commandOutcomeListArg(args map[string]any, key string) []memory.CommandOutcome {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	values := make([]memory.CommandOutcome, 0, len(raw))
	for _, value := range raw {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		values = append(values, memory.CommandOutcome{
			Command:    stringArg(item, "command"),
			Status:     stringArg(item, "status"),
			ExitCode:   intArg(item, "exit_code", 0),
			DurationMS: intArg(item, "duration_ms", 0),
		})
	}
	return values
}

func testOutcomeArg(args map[string]any, key string) memory.TestOutcome {
	raw := mapArg(args, key)
	if raw == nil {
		return memory.TestOutcome{}
	}
	return memory.TestOutcome{
		Status:   stringArg(raw, "status"),
		Summary:  stringArg(raw, "summary"),
		Commands: stringListArg(raw, "commands"),
	}
}

func memoryReferenceListArg(args map[string]any, key string) []memory.MemoryReferenceUsed {
	raw, ok := args[key].([]any)
	if !ok {
		return nil
	}
	values := make([]memory.MemoryReferenceUsed, 0, len(raw))
	for _, value := range raw {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		values = append(values, memory.MemoryReferenceUsed{
			Ref:       stringArg(item, "ref"),
			Kind:      stringArg(item, "kind"),
			ID:        stringArg(item, "id"),
			SourceURL: stringArg(item, "source_url"),
		})
	}
	return values
}

func mapArg(args map[string]any, key string) map[string]any {
	raw, ok := args[key].(map[string]any)
	if !ok {
		return nil
	}
	return raw
}

func stringMapArg(args map[string]any, key string) map[string]string {
	raw, ok := args[key].(map[string]any)
	if !ok {
		return nil
	}
	values := map[string]string{}
	for rawKey, rawValue := range raw {
		key := strings.TrimSpace(rawKey)
		value, _ := rawValue.(string)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			values[key] = value
		}
	}
	return values
}

func mcpDocumentInput(args map[string]any, defaults map[string]any) brain.IngestDocumentInput {
	metadata := mergeWebhookMetadata(mapArg(defaults, "metadata"), mapArg(args, "metadata"))
	authority := firstNonEmpty(stringArg(args, "authority"), stringArg(defaults, "authority"))
	if authority != "" {
		metadata["authority"] = authority
	}
	authorityScore := floatArg(args, "authority_score", floatArg(defaults, "authority_score", 0))
	if authorityScore > 0 {
		metadata["authority_score"] = authorityScore
	}
	metadata = sanitizeUserIngestMetadata(metadata)
	return brain.IngestDocumentInput{
		SourceType:      firstNonEmpty(stringArg(args, "source_type"), stringArg(defaults, "source_type")),
		SourceURL:       stringArg(args, "source_url"),
		SourceID:        stringArg(args, "source_id"),
		Title:           stringArg(args, "title"),
		Scope:           firstNonEmpty(stringArg(args, "scope"), stringArg(defaults, "scope")),
		Content:         stringArg(args, "content"),
		SourceUpdatedAt: firstNonEmpty(stringArg(args, "source_updated_at"), stringArg(defaults, "source_updated_at")),
		ApprovalID:      firstNonEmpty(stringArg(args, "approval_id"), stringArg(defaults, "approval_id")),
		Metadata:        metadata,
	}
}

func sanitizeUserIngestMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return metadata
	}
	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		switch strings.TrimSpace(strings.ToLower(key)) {
		case "source_config_id", "source_config_name", "ingestion_job_id":
			continue
		default:
			out[key] = value
		}
	}
	return out
}

func mcpDocumentInputs(args map[string]any) ([]brain.IngestDocumentInput, error) {
	rawDocs, ok := args["documents"].([]any)
	if !ok || len(rawDocs) == 0 {
		return nil, fmt.Errorf("documents must contain at least one document")
	}
	if len(rawDocs) > 50 {
		return nil, fmt.Errorf("documents batch limit is 50")
	}
	docs := make([]brain.IngestDocumentInput, 0, len(rawDocs))
	for index, raw := range rawDocs {
		docArgs, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("document %d must be an object", index)
		}
		docs = append(docs, mcpDocumentInput(docArgs, args))
	}
	return docs, nil
}

func intArg(args map[string]any, key string, fallback int) int {
	raw, ok := args[key].(float64)
	if !ok || raw == 0 {
		return fallback
	}
	return int(raw)
}

func floatArg(args map[string]any, key string, fallback float64) float64 {
	raw, ok := args[key].(float64)
	if !ok || raw == 0 {
		return fallback
	}
	return raw
}

func boolArg(args map[string]any, key string, fallback bool) bool {
	raw, ok := args[key].(bool)
	if !ok {
		return fallback
	}
	return raw
}
