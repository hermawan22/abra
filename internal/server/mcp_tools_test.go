package server

import (
	"errors"
	"testing"

	"github.com/hermawan22/abra/internal/brain"
	"github.com/hermawan22/abra/internal/store"
)

func TestUpsertSourceConfigMCPAllowsOverlaySourceTypes(t *testing.T) {
	var tool map[string]any
	for _, candidate := range mcpTools() {
		if candidate["name"] == "upsert_source_config" {
			tool = candidate
			break
		}
	}
	if tool == nil {
		t.Fatal("upsert_source_config tool not found")
	}
	schema, ok := tool["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("inputSchema = %#v", tool["inputSchema"])
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema["properties"])
	}
	sourceType, ok := properties["source_type"].(map[string]any)
	if !ok {
		t.Fatalf("source_type schema = %#v", properties["source_type"])
	}
	if _, hasEnum := sourceType["enum"]; hasEnum {
		t.Fatalf("source_type schema must allow overlay source types, got enum %#v", sourceType["enum"])
	}
	if sourceType["type"] != "string" {
		t.Fatalf("source_type type = %#v, want string", sourceType["type"])
	}
}

func TestBrainThinkMCPToolIsDiscoverable(t *testing.T) {
	schema := mcpToolSchema(t, "brain_think")
	requiredSet := requiredSet(t, schema)
	if !requiredSet["question"] || !requiredSet["scope"] {
		t.Fatalf("brain_think required = %#v, want question and scope", schema["required"])
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema["properties"])
	}
	for _, property := range []string{"question", "scope", "agent", "include_unverified"} {
		if _, ok := properties[property]; !ok {
			t.Fatalf("brain_think missing property %q in %#v", property, properties)
		}
	}
}

func TestDiscoverScopesMCPToolIsDiscoverable(t *testing.T) {
	schema := mcpToolSchema(t, "discover_scopes")
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema["properties"])
	}
	if _, ok := properties["limit"]; !ok {
		t.Fatalf("discover_scopes missing limit property in %#v", properties)
	}
	for _, property := range []string{"query", "expected_scope"} {
		if _, ok := properties[property]; !ok {
			t.Fatalf("discover_scopes missing %s property in %#v", property, properties)
		}
	}
}

func TestRankScopeSummariesPrioritizesExpectedScope(t *testing.T) {
	scopes := []store.ScopeSummary{
		{Scope: "repo:large-release", Documents: 1000},
		{Scope: "repo:abra", Documents: 14},
		{Scope: "repo:other", Documents: 50},
	}
	ordered, matches, recommended := rankScopeSummaries(scopes, "repo:abra", "")
	if recommended != "repo:abra" {
		t.Fatalf("recommended = %q, want repo:abra", recommended)
	}
	if len(matches) != 1 || matches[0].Scope != "repo:abra" {
		t.Fatalf("matches = %#v", matches)
	}
	if ordered[0].Scope != "repo:abra" {
		t.Fatalf("first ordered scope = %#v", ordered[0])
	}
}

func TestIngestDocumentsMCPContinueOnErrorIsOptional(t *testing.T) {
	schema := mcpToolSchema(t, "ingest_documents")
	required := requiredSet(t, schema)
	if required["continue_on_error"] {
		t.Fatalf("ingest_documents required = %#v, continue_on_error must be optional", schema["required"])
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema["properties"])
	}
	continueOnError, ok := properties["continue_on_error"].(map[string]any)
	if !ok {
		t.Fatalf("continue_on_error schema = %#v", properties["continue_on_error"])
	}
	if continueOnError["type"] != "boolean" {
		t.Fatalf("continue_on_error type = %#v, want boolean", continueOnError["type"])
	}
}

func TestIngestDocumentsMCPStatusEntries(t *testing.T) {
	doc := brain.IngestDocumentInput{SourceURL: "file:///doc.md", Scope: "repo:abra"}
	success := mcpIngestDocumentSuccess(1, doc, brain.IngestDocumentResult{
		DocumentID: "doc-1",
		Chunks:     2,
		Claims:     1,
		Entities:   3,
		Relations:  4,
	}, true)
	if success["status"] != "ingested" {
		t.Fatalf("success status = %#v, want ingested", success["status"])
	}
	if success["index"] != 1 || success["document_id"] != "doc-1" || success["source_url"] != doc.SourceURL || success["scope"] != doc.Scope {
		t.Fatalf("success entry = %#v", success)
	}

	failed := mcpIngestDocumentError(2, doc, errors.New("embedding unavailable"))
	if failed["status"] != "error" {
		t.Fatalf("failed status = %#v, want error", failed["status"])
	}
	if failed["index"] != 2 || failed["error"] != "embedding unavailable" || failed["source_url"] != doc.SourceURL || failed["scope"] != doc.Scope {
		t.Fatalf("failed entry = %#v", failed)
	}
}

func TestScopeDiscoveryLimitsScanBeyondVisibleLimitForScopedTokens(t *testing.T) {
	limit, candidateLimit := scopeDiscoveryLimits(2, &apiPrincipal{
		roles:  map[string]struct{}{"reader": {}},
		scopes: []string{"repo:target"},
	})
	if limit != 2 {
		t.Fatalf("limit = %d, want 2", limit)
	}
	if candidateLimit <= limit {
		t.Fatalf("candidateLimit = %d, want beyond visible limit %d", candidateLimit, limit)
	}
}

func TestScopeDiscoveryLimitsClampClientLimit(t *testing.T) {
	limit, candidateLimit := scopeDiscoveryLimits(1000, anonymousAdmin())
	if limit != maxScopeDiscoveryLimit {
		t.Fatalf("limit = %d, want %d", limit, maxScopeDiscoveryLimit)
	}
	if candidateLimit != maxScopeDiscoveryLimit {
		t.Fatalf("candidateLimit = %d, want %d", candidateLimit, maxScopeDiscoveryLimit)
	}
}

func TestPolicyPlanMCPRequiresScope(t *testing.T) {
	schema := mcpToolSchema(t, "policy_plan")
	required := requiredSet(t, schema)
	for _, key := range []string{"hook", "task", "scope"} {
		if !required[key] {
			t.Fatalf("policy_plan required = %#v, missing %s", schema["required"], key)
		}
	}
}

func TestWorkingMemoryComposeMCPHasDiagnosticMode(t *testing.T) {
	schema := mcpToolSchema(t, "working_memory_compose")
	required := requiredSet(t, schema)
	if required["diagnostic"] {
		t.Fatalf("working_memory_compose required = %#v, diagnostic must be optional", schema["required"])
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema["properties"])
	}
	diagnostic, ok := properties["diagnostic"].(map[string]any)
	if !ok {
		t.Fatalf("working_memory_compose missing diagnostic property in %#v", properties)
	}
	if diagnostic["type"] != "boolean" {
		t.Fatalf("diagnostic type = %#v, want boolean", diagnostic["type"])
	}
}

func mcpToolSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	var tool map[string]any
	for _, candidate := range mcpTools() {
		if candidate["name"] == name {
			tool = candidate
			break
		}
	}
	if tool == nil {
		t.Fatalf("%s tool not found", name)
	}
	schema, ok := tool["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("inputSchema = %#v", tool["inputSchema"])
	}
	return schema
}

func requiredSet(t *testing.T, schema map[string]any) map[string]bool {
	t.Helper()
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("required = %#v", schema["required"])
	}
	requiredSet := map[string]bool{}
	for _, item := range required {
		requiredSet[item] = true
	}
	return requiredSet
}
