package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/brain"
	"github.com/hermawan22/abra/internal/config"
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

func TestSourceConfigLifecycleMCPToolsAreDiscoverable(t *testing.T) {
	validateSchema := mcpToolSchema(t, "validate_mcp_source")
	validateRequired := requiredSet(t, validateSchema)
	if !validateRequired["scope"] || !validateRequired["tool"] || validateRequired["base_url"] {
		t.Fatalf("validate_mcp_source required = %#v, want scope and tool without base_url", validateSchema["required"])
	}
	validateProperties, ok := validateSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("validate properties = %#v", validateSchema["properties"])
	}
	if _, ok := validateProperties["header_env"]; !ok {
		t.Fatalf("validate_mcp_source missing header_env property: %#v", validateProperties)
	}
	if _, ok := validateProperties["approval_id"]; !ok {
		t.Fatalf("validate_mcp_source missing approval_id property: %#v", validateProperties)
	}

	getSchema := mcpToolSchema(t, "get_source_config")
	getRequired := requiredSet(t, getSchema)
	if !getRequired["source_config_id"] {
		t.Fatalf("get_source_config required = %#v, want source_config_id", getSchema["required"])
	}

	statusSchema := mcpToolSchema(t, "set_source_config_status")
	statusRequired := requiredSet(t, statusSchema)
	if !statusRequired["source_config_id"] || !statusRequired["status"] {
		t.Fatalf("set_source_config_status required = %#v, want source_config_id and status", statusSchema["required"])
	}
	properties, ok := statusSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", statusSchema["properties"])
	}
	status, ok := properties["status"].(map[string]any)
	if !ok {
		t.Fatalf("status schema = %#v", properties["status"])
	}
	enum, ok := status["enum"].([]string)
	if !ok {
		t.Fatalf("status enum = %#v", status["enum"])
	}
	if !containsString(enum, "active") || !containsString(enum, "paused") {
		t.Fatalf("status enum = %#v, want active and paused", enum)
	}

	enqueueSchema := mcpToolSchema(t, "enqueue_ingestion_job")
	enqueueProperties, ok := enqueueSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("enqueue properties = %#v", enqueueSchema["properties"])
	}
	if _, ok := enqueueProperties["approval_id"]; !ok {
		t.Fatalf("enqueue_ingestion_job missing approval_id property: %#v", enqueueProperties)
	}
}

func TestMCPAppliesRequestBodyLimit(t *testing.T) {
	h := handler{cfg: config.Config{MaxRequestBodyBytes: 32}}
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"padding":"`+strings.Repeat("x", 80)+`"}}`))
	response := httptest.NewRecorder()

	h.mcp(response, request)

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, response.Body.String())
	}
	errPayload, _ := payload["error"].(map[string]any)
	if errPayload["code"] != float64(-32700) {
		t.Fatalf("payload = %#v, want JSON-RPC parse error", payload)
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

func TestObservationMCPToolsAreDiscoverable(t *testing.T) {
	capture := mcpToolSchema(t, "capture_observation")
	captureRequired := requiredSet(t, capture)
	if !captureRequired["scope"] || !captureRequired["observation_text"] {
		t.Fatalf("capture_observation required = %#v, want scope and observation_text", capture["required"])
	}
	captureProperties, ok := capture["properties"].(map[string]any)
	if !ok {
		t.Fatalf("capture properties = %#v", capture["properties"])
	}
	for _, property := range []string{"observation_type", "status", "source_url", "created_by", "approval_id", "metadata"} {
		if _, ok := captureProperties[property]; !ok {
			t.Fatalf("capture_observation missing property %q in %#v", property, captureProperties)
		}
	}

	list := mcpToolSchema(t, "list_observations")
	listRequired := requiredSet(t, list)
	if !listRequired["scope"] {
		t.Fatalf("list_observations required = %#v, want scope", list["required"])
	}
	listProperties, ok := list["properties"].(map[string]any)
	if !ok {
		t.Fatalf("list properties = %#v", list["properties"])
	}
	for _, property := range []string{"query", "observation_type", "status", "since", "until", "limit"} {
		if _, ok := listProperties[property]; !ok {
			t.Fatalf("list_observations missing property %q in %#v", property, listProperties)
		}
	}
}

func TestProposeLearningMCPSupportsObservationTargets(t *testing.T) {
	schema := mcpToolSchema(t, "propose_learning")
	required := requiredSet(t, schema)
	for _, property := range []string{"scope", "proposal_type", "title", "rationale"} {
		if !required[property] {
			t.Fatalf("propose_learning required = %#v, missing %s", schema["required"], property)
		}
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema["properties"])
	}
	for _, property := range []string{"target_type", "target_id", "source_url", "confidence", "payload", "created_by"} {
		if _, ok := properties[property]; !ok {
			t.Fatalf("propose_learning missing property %q in %#v", property, properties)
		}
	}
	proposalType, _ := properties["proposal_type"].(map[string]any)
	enums, _ := proposalType["enum"].([]string)
	if len(enums) == 0 {
		rawEnums, _ := proposalType["enum"].([]any)
		for _, raw := range rawEnums {
			enums = append(enums, raw.(string))
		}
	}
	hasClaim := false
	for _, value := range enums {
		if value == "claim" {
			hasClaim = true
		}
	}
	if !hasClaim {
		t.Fatalf("proposal_type enum = %#v, want claim", proposalType["enum"])
	}
}

func TestLearningProposalReviewMCPToolsAreDiscoverable(t *testing.T) {
	listSchema := mcpToolSchema(t, "list_learning_proposals")
	listRequired := requiredSet(t, listSchema)
	if !listRequired["scope"] {
		t.Fatalf("list_learning_proposals required = %#v, want scope", listSchema["required"])
	}
	listProperties, ok := listSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("list properties = %#v", listSchema["properties"])
	}
	for _, property := range []string{"scope", "status", "limit"} {
		if _, ok := listProperties[property]; !ok {
			t.Fatalf("list_learning_proposals missing property %q in %#v", property, listProperties)
		}
	}

	decideSchema := mcpToolSchema(t, "decide_learning_proposal")
	decideRequired := requiredSet(t, decideSchema)
	for _, property := range []string{"proposal_id", "status"} {
		if !decideRequired[property] {
			t.Fatalf("decide_learning_proposal required = %#v, missing %s", decideSchema["required"], property)
		}
	}
	decideProperties, ok := decideSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("decide properties = %#v", decideSchema["properties"])
	}
	for _, property := range []string{"proposal_id", "status", "reviewed_by", "review_reason", "approval_id", "metadata"} {
		if _, ok := decideProperties[property]; !ok {
			t.Fatalf("decide_learning_proposal missing property %q in %#v", property, decideProperties)
		}
	}

	applySchema := mcpToolSchema(t, "apply_learning_proposal")
	applyRequired := requiredSet(t, applySchema)
	if !applyRequired["proposal_id"] {
		t.Fatalf("apply_learning_proposal required = %#v, want proposal_id", applySchema["required"])
	}
	applyProperties, ok := applySchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("apply properties = %#v", applySchema["properties"])
	}
	for _, property := range []string{"proposal_id", "applied_by", "approval_id", "metadata"} {
		if _, ok := applyProperties[property]; !ok {
			t.Fatalf("apply_learning_proposal missing property %q in %#v", property, applyProperties)
		}
	}
	if _, ok := applyProperties["payload"]; ok {
		t.Fatalf("apply_learning_proposal should not accept mutable payload overrides: %#v", applyProperties)
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

func TestRankScopeSummariesCountsGraphOnlyScopes(t *testing.T) {
	scopes := []store.ScopeSummary{
		{Scope: "repo:empty"},
		{Scope: "repo:graph-heavy", Entities: 3, Relations: 10},
		{Scope: "repo:small-doc", Documents: 1},
	}
	ordered, _, _ := rankScopeSummaries(scopes, "", "")
	if ordered[0].Scope != "repo:graph-heavy" {
		t.Fatalf("first ordered scope = %#v, want graph-heavy", ordered[0])
	}
}

func TestRankScopeSummariesCountsObservationOnlyScopes(t *testing.T) {
	scopes := []store.ScopeSummary{
		{Scope: "repo:empty"},
		{Scope: "repo:observation-only", Observations: 2},
		{Scope: "repo:small-doc", Documents: 1},
	}
	ordered, _, _ := rankScopeSummaries(scopes, "", "")
	if ordered[0].Scope != "repo:observation-only" {
		t.Fatalf("first ordered scope = %#v, want observation-only", ordered[0])
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
	failFastSuccess := mcpIngestDocumentSuccess(1, doc, brain.IngestDocumentResult{DocumentID: "doc-1"}, false)
	if _, ok := failFastSuccess["status"]; ok {
		t.Fatalf("fail-fast success must omit status for backward compatibility: %#v", failFastSuccess)
	}

	failed := mcpIngestDocumentError(2, doc, errors.New("embedding unavailable"))
	if failed["status"] != "error" {
		t.Fatalf("failed status = %#v, want error", failed["status"])
	}
	if failed["index"] != 2 || failed["error"] != "embedding unavailable" || failed["source_url"] != doc.SourceURL || failed["scope"] != doc.Scope {
		t.Fatalf("failed entry = %#v", failed)
	}

	providerFailed := mcpIngestDocumentError(3, doc, &ai.ProviderError{
		Operation: "embedding",
		Code:      "provider_unreachable",
		Status:    503,
	})
	if providerFailed["error_kind"] != "provider_error" {
		t.Fatalf("provider failed entry = %#v", providerFailed)
	}
	detail, ok := providerFailed["provider_error"].(map[string]any)
	if !ok || detail["code"] != "provider_unreachable" || detail["status_code"] != 503 {
		t.Fatalf("provider failed detail = %#v", providerFailed)
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

func TestWorkingMemoryComposeMCPHasReadOnlyDefaultAndPersistenceOptIn(t *testing.T) {
	schema := mcpToolSchema(t, "working_memory_compose")
	required := requiredSet(t, schema)
	if required["diagnostic"] {
		t.Fatalf("working_memory_compose required = %#v, diagnostic must be optional", schema["required"])
	}
	if required["persist_learning"] {
		t.Fatalf("working_memory_compose required = %#v, persist_learning must be optional", schema["required"])
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
	persistLearning, ok := properties["persist_learning"].(map[string]any)
	if !ok {
		t.Fatalf("working_memory_compose missing persist_learning property in %#v", properties)
	}
	if persistLearning["type"] != "boolean" {
		t.Fatalf("persist_learning type = %#v, want boolean", persistLearning["type"])
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

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
