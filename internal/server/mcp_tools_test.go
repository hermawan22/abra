package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hermawan22/abra/internal/ai"
	"github.com/hermawan22/abra/internal/brain"
	"github.com/hermawan22/abra/internal/config"
	"github.com/hermawan22/abra/internal/memory"
	"github.com/hermawan22/abra/internal/store"
)

type brainToolTestStore struct {
	health         store.MemoryHealthResult
	candidates     []store.EvidenceAnchorCandidate
	proposalInputs []store.CreateLearningProposalInput
	auditEvents    []string
}

func (s *brainToolTestStore) Recall(context.Context, string, string, int, bool) (store.RecallResult, error) {
	return store.RecallResult{}, nil
}

func (s *brainToolTestStore) ListMemorySummaries(context.Context, string, string, int) ([]store.MemorySummaryResult, error) {
	return nil, nil
}

func (s *brainToolTestStore) RelatedGraph(context.Context, string, string, int) ([]store.RelationResult, error) {
	return nil, nil
}

func (s *brainToolTestStore) ListOpenConflictsForClaims(context.Context, string, []string) ([]store.ConflictResult, error) {
	return nil, nil
}

func (s *brainToolTestStore) ListOpenConflictsForRelations(context.Context, string, []string) ([]store.ConflictResult, error) {
	return nil, nil
}

func (s *brainToolTestStore) EvaluateAgentActionPolicies(context.Context, []store.AgentActionDecisionInput) ([]store.AgentActionDecisionResult, error) {
	return nil, nil
}

func (s *brainToolTestStore) MemoryHealth(context.Context, string) (store.MemoryHealthResult, error) {
	return s.health, nil
}

func (s *brainToolTestStore) ListEvidenceAnchorCandidates(context.Context, string, int) ([]store.EvidenceAnchorCandidate, error) {
	return append([]store.EvidenceAnchorCandidate(nil), s.candidates...), nil
}

func (s *brainToolTestStore) CountEvidenceAnchorCandidates(context.Context, string) (int, error) {
	return len(s.candidates), nil
}

func (s *brainToolTestStore) CreateLearningProposalOnce(_ context.Context, input store.CreateLearningProposalInput) (store.LearningProposalRecord, bool, error) {
	s.proposalInputs = append(s.proposalInputs, input)
	id := "proposal-" + input.TargetID
	if id == "proposal-" {
		id = "proposal-scope"
	}
	return store.LearningProposalRecord{
		ID:           id,
		Scope:        input.Scope,
		ProposalType: input.ProposalType,
		Title:        input.Title,
		Rationale:    input.Rationale,
		Status:       "pending",
		TargetType:   input.TargetType,
		TargetID:     input.TargetID,
		SourceURL:    input.SourceURL,
		Confidence:   input.Confidence,
		Payload:      input.Payload,
		CreatedBy:    input.CreatedBy,
	}, true, nil
}

func (s *brainToolTestStore) InsertAuditEvent(_ context.Context, eventType, targetType, targetID, scope, sourceURL string, metadata map[string]any) error {
	channel, _ := metadata["channel"].(string)
	s.auditEvents = append(s.auditEvents, eventType+":"+targetType+":"+targetID+":"+scope+":"+sourceURL+":"+channel)
	return nil
}

func withAdmin(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), authContextKey{}, anonymousAdmin()))
}

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

func TestUserIngestMetadataCannotForgeInternalLineage(t *testing.T) {
	metadata := sanitizeUserIngestMetadata(map[string]any{
		"source_config_id":   "source-1",
		"source_config_name": "trusted source",
		"ingestion_job_id":   "job-1",
		"owner":              "platform",
		"authority":          "official-doc",
	})
	for _, key := range []string{"source_config_id", "source_config_name", "ingestion_job_id"} {
		if _, ok := metadata[key]; ok {
			t.Fatalf("sanitized metadata still contains %q: %#v", key, metadata)
		}
	}
	if metadata["owner"] != "platform" || metadata["authority"] != "official-doc" {
		t.Fatalf("sanitized metadata dropped non-lineage fields: %#v", metadata)
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
	if allowPrivate, ok := validateProperties["allow_private_network"].(map[string]any); !ok || allowPrivate["type"] != "boolean" {
		t.Fatalf("validate_mcp_source allow_private_network schema = %#v", validateProperties["allow_private_network"])
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

func TestConnectorSourceMCPAliasesAreDiscoverable(t *testing.T) {
	for _, tc := range []struct {
		alias     string
		canonical string
	}{
		{"validate_connector_source", "validate_mcp_source"},
		{"upsert_connector_source", "upsert_source_config"},
		{"list_connector_sources", "list_source_configs"},
		{"get_connector_source", "get_source_config"},
		{"set_connector_source_status", "set_source_config_status"},
		{"sync_connector_source", "enqueue_ingestion_job"},
	} {
		aliasTool := mcpTool(t, tc.alias)
		description, _ := aliasTool["description"].(string)
		if !strings.Contains(strings.ToLower(description), "connector") {
			t.Fatalf("%s description = %q, want connector onboarding language", tc.alias, description)
		}
		if mcpToolTraceName(tc.alias) != tc.alias {
			t.Fatalf("trace name for %s = %q", tc.alias, mcpToolTraceName(tc.alias))
		}

		aliasSchema := mcpToolSchema(t, tc.alias)
		canonicalSchema := mcpToolSchema(t, tc.canonical)
		aliasRequired := requiredSet(t, aliasSchema)
		canonicalRequired := requiredSet(t, canonicalSchema)
		for key := range canonicalRequired {
			if !aliasRequired[key] {
				t.Fatalf("%s required = %#v, missing canonical required field %s", tc.alias, aliasSchema["required"], key)
			}
		}
	}

	inspectTool := mcpTool(t, "inspect_connector_source")
	description, _ := inspectTool["description"].(string)
	if !strings.Contains(strings.ToLower(description), "tools/list") {
		t.Fatalf("inspect_connector_source description = %q, want tools/list language", description)
	}
	inspectRequired := requiredSet(t, mcpToolSchema(t, "inspect_connector_source"))
	if !inspectRequired["scope"] || inspectRequired["tool"] {
		t.Fatalf("inspect_connector_source required = %#v, want scope without tool", mcpToolSchema(t, "inspect_connector_source")["required"])
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

func TestMCPToolCallReturnsStructuredContent(t *testing.T) {
	db := &brainToolTestStore{
		health: store.MemoryHealthResult{
			Status: "healthy",
			Score:  100,
			Claims: store.MemoryHealthClaim{Total: 1, Verified: 1, WithEvidence: 1},
		},
	}
	h := handler{
		cfg:    config.Config{MaxRequestBodyBytes: 1 << 20},
		memory: memory.NewComposerWithOptions(db, memory.ComposerOptions{}),
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"brain_review","arguments":{"scope":"repo:test","limit":5}}}`))
	response := httptest.NewRecorder()

	h.mcp(response, withAdmin(request))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, response.Body.String())
	}
	result, _ := payload["result"].(map[string]any)
	structured, _ := result["structuredContent"].(map[string]any)
	if structured["status"] != "healthy" || structured["score"] == nil {
		t.Fatalf("structuredContent = %#v", structured)
	}
	content, _ := result["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %#v", result["content"])
	}
}

func TestMCPBrainAnchorBackfillCreatesReviewableProposals(t *testing.T) {
	db := &brainToolTestStore{
		health: store.MemoryHealthResult{
			Scope:  "repo:test",
			Status: "needs_review",
			Score:  85,
			Claims: store.MemoryHealthClaim{Total: 1, Verified: 1, WithEvidence: 1},
		},
		candidates: []store.EvidenceAnchorCandidate{
			{
				ClaimID:       "claim-1",
				Claim:         "Retry callbacks must remain idempotent.",
				Scope:         "repo:test",
				SourceURL:     "file://runbook.md",
				DocumentID:    "doc-1",
				DocumentChunk: "Operators must verify delivery. Retry callbacks must remain idempotent before replay.",
			},
		},
	}
	h := handler{
		cfg:    config.Config{MaxRequestBodyBytes: 1 << 20},
		memory: memory.NewComposerWithOptions(db, memory.ComposerOptions{}),
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"brain_anchor_backfill","arguments":{"scope":"repo:test","limit":5,"propose":true,"dry_run":false,"agent":"codex"}}}`))
	response := httptest.NewRecorder()

	h.mcp(response, withAdmin(request))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if len(db.proposalInputs) != 1 {
		t.Fatalf("proposal inputs = %#v", db.proposalInputs)
	}
}

func TestMCPBrainMaintainCreatesReviewableProposals(t *testing.T) {
	db := &brainToolTestStore{
		health: store.MemoryHealthResult{
			Scope:     "repo:test",
			Status:    "needs_review",
			Score:     80,
			Claims:    store.MemoryHealthClaim{Total: 2, Verified: 1, WithEvidence: 1, Stale: 1},
			Summaries: store.MemoryHealthSummary{Total: 1},
		},
	}
	h := handler{
		cfg:    config.Config{MaxRequestBodyBytes: 1 << 20},
		memory: memory.NewComposerWithOptions(db, memory.ComposerOptions{}),
	}
	request := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"brain_maintain","arguments":{"scope":"repo:test","limit":5,"propose":true,"dry_run":false,"agent":"codex"}}}`))
	response := httptest.NewRecorder()

	h.mcp(response, withAdmin(request))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if len(db.proposalInputs) == 0 {
		t.Fatalf("expected maintenance proposal input")
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
	for _, property := range []string{"question", "scope", "agent", "entity", "mode", "as_of", "include_historical", "include_unverified", "synthesize"} {
		if _, ok := properties[property]; !ok {
			t.Fatalf("brain_think missing property %q in %#v", property, properties)
		}
	}
	mode, ok := properties["mode"].(map[string]any)
	if !ok {
		t.Fatalf("brain_think mode schema = %#v", properties["mode"])
	}
	enum, ok := mode["enum"].([]string)
	if !ok || !containsString(enum, "fast") || !containsString(enum, "balanced") || !containsString(enum, "deep") {
		t.Fatalf("brain_think mode enum = %#v", mode["enum"])
	}
}

func TestBrainEntityDossierMCPToolIsDiscoverable(t *testing.T) {
	schema := mcpToolSchema(t, "brain_entity_dossier")
	requiredSet := requiredSet(t, schema)
	if !requiredSet["entity"] || !requiredSet["scope"] {
		t.Fatalf("brain_entity_dossier required = %#v, want entity and scope", schema["required"])
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %#v", schema["properties"])
	}
	for _, property := range []string{"entity", "scope", "agent", "mode", "as_of", "include_historical", "include_unverified", "limit", "token_budget"} {
		if _, ok := properties[property]; !ok {
			t.Fatalf("brain_entity_dossier missing property %q in %#v", property, properties)
		}
	}
	mode, ok := properties["mode"].(map[string]any)
	if !ok {
		t.Fatalf("brain_entity_dossier mode schema = %#v", properties["mode"])
	}
	enum, ok := mode["enum"].([]string)
	if !ok || !containsString(enum, "fast") || !containsString(enum, "balanced") || !containsString(enum, "deep") {
		t.Fatalf("brain_entity_dossier mode enum = %#v", mode["enum"])
	}
}

func TestBrainOperationsMCPToolsAreDiscoverable(t *testing.T) {
	for _, tc := range []struct {
		name         string
		requiredKeys []string
		properties   []string
	}{
		{name: "brain_core_memory", requiredKeys: []string{"scope"}, properties: []string{"scope", "query", "agent", "include_shared", "limit"}},
		{name: "brain_shared_memory", requiredKeys: []string{"scope"}, properties: []string{"scope", "query", "agent", "limit"}},
		{name: "brain_memory_edit_proposal", requiredKeys: []string{"scope", "level", "operation", "title", "rationale"}, properties: []string{"scope", "level", "operation", "key", "summary_id", "title", "summary", "rationale", "source_url", "confidence", "agent", "created_by", "dedupe", "evidence_refs", "replacement_text", "metadata"}},
		{name: "brain_review", requiredKeys: []string{"scope"}, properties: []string{"scope", "limit"}},
		{name: "brain_scorecard", requiredKeys: []string{"scope"}, properties: []string{"scope", "limit"}},
		{name: "brain_anchor_backfill", requiredKeys: []string{"scope"}, properties: []string{"scope", "limit", "dry_run", "propose", "agent", "created_by"}},
		{name: "brain_maintain", requiredKeys: []string{"scope"}, properties: []string{"scope", "limit", "dry_run", "propose", "agent", "created_by"}},
		{name: "brain_explain", requiredKeys: []string{"trace_id"}, properties: []string{"trace_id"}},
		{name: "brain_eval_record", requiredKeys: []string{"scope", "total", "passed", "success", "reports"}, properties: []string{"scope", "suite_name", "suite_file", "agent", "total", "passed", "success", "reports", "metadata"}},
		{name: "brain_eval_history", requiredKeys: []string{"scope"}, properties: []string{"scope", "limit"}},
	} {
		schema := mcpToolSchema(t, tc.name)
		required := requiredSet(t, schema)
		for _, property := range tc.requiredKeys {
			if !required[property] {
				t.Fatalf("%s required = %#v, missing %s", tc.name, schema["required"], property)
			}
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("%s properties = %#v", tc.name, schema["properties"])
		}
		for _, property := range tc.properties {
			if _, ok := properties[property]; !ok {
				t.Fatalf("%s missing property %q in %#v", tc.name, property, properties)
			}
		}
		if mcpToolTraceName(tc.name) != tc.name {
			t.Fatalf("trace name for %s = %q", tc.name, mcpToolTraceName(tc.name))
		}
	}
}

func TestMemoryEditProposalInputBuildsReviewableProposal(t *testing.T) {
	input, err := memoryEditProposalInput(map[string]any{
		"level":         "shared",
		"operation":     "update",
		"key":           "workflow",
		"title":         "Workflow memory",
		"summary":       "Agents coordinate through learning proposals.",
		"rationale":     "Shared memory should reflect the reviewed workflow.",
		"agent":         "codex",
		"evidence_refs": []any{"summary-1", "doc-2"},
	})
	if err != nil {
		t.Fatalf("memoryEditProposalInput returned error: %v", err)
	}
	if input.ProposalType != "other" || input.TargetType != "memory_summary" || input.TargetID != "workflow" {
		t.Fatalf("proposal target = %#v", input)
	}
	if input.CreatedBy != "codex" {
		t.Fatalf("created_by = %q, want codex", input.CreatedBy)
	}
	if truthWrite, _ := input.Payload["truth_write"].(bool); truthWrite {
		t.Fatalf("memory edit proposal must not be marked as truth write: %#v", input.Payload)
	}
	if input.Payload["level"] != "shared" || input.Payload["operation"] != "update" {
		t.Fatalf("payload missing memory edit fields: %#v", input.Payload)
	}
}

func TestMemoryEditProposalInputRequiresTargetForUpdate(t *testing.T) {
	_, err := memoryEditProposalInput(map[string]any{
		"level":     "core",
		"operation": "update",
		"title":     "Core memory",
		"rationale": "Update core memory.",
	})
	if !errors.Is(err, errMemoryEditTargetRequired) {
		t.Fatalf("error = %v, want target required", err)
	}
}

func TestEveryMCPToolHasTraceName(t *testing.T) {
	for _, tool := range mcpTools() {
		name, _ := tool["name"].(string)
		if name == "" {
			t.Fatalf("tool missing name: %#v", tool)
		}
		if got := mcpToolTraceName(name); got != name {
			t.Fatalf("trace name for %s = %q", name, got)
		}
	}
}

func TestEveryMCPToolHasHandler(t *testing.T) {
	for _, tool := range mcpTools() {
		name, _ := tool["name"].(string)
		if name == "" {
			t.Fatalf("tool missing name: %#v", tool)
		}
		if mcpToolCallHandlers[name] == nil {
			t.Fatalf("tool %s is advertised without a call handler", name)
		}
	}
}

func TestEveryMCPHandlerIsAdvertised(t *testing.T) {
	advertised := map[string]struct{}{}
	for _, tool := range mcpTools() {
		name, _ := tool["name"].(string)
		if name != "" {
			advertised[name] = struct{}{}
		}
	}
	for name := range mcpToolCallHandlers {
		if _, ok := advertised[name]; !ok {
			t.Fatalf("handler %s is callable but missing from tools/list", name)
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
	for _, property := range []string{"entity", "as_of", "include_historical"} {
		if _, ok := properties[property]; !ok {
			t.Fatalf("working_memory_compose missing property %q in %#v", property, properties)
		}
	}
	mode, ok := properties["mode"].(map[string]any)
	if !ok {
		t.Fatalf("working_memory_compose missing mode property in %#v", properties)
	}
	enum, ok := mode["enum"].([]string)
	if !ok || !containsString(enum, "fast") || !containsString(enum, "balanced") || !containsString(enum, "deep") {
		t.Fatalf("working_memory_compose mode enum = %#v", mode["enum"])
	}
}

func mcpToolSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	tool := mcpTool(t, name)
	schema, ok := tool["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("inputSchema = %#v", tool["inputSchema"])
	}
	return schema
}

func mcpTool(t *testing.T, name string) map[string]any {
	t.Helper()
	for _, candidate := range mcpTools() {
		if candidate["name"] == name {
			return candidate
		}
	}
	t.Fatalf("%s tool not found", name)
	return nil
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
