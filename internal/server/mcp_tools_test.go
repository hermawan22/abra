package server

import "testing"

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
	var tool map[string]any
	for _, candidate := range mcpTools() {
		if candidate["name"] == "brain_think" {
			tool = candidate
			break
		}
	}
	if tool == nil {
		t.Fatal("brain_think tool not found")
	}
	schema, ok := tool["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("inputSchema = %#v", tool["inputSchema"])
	}
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("required = %#v", schema["required"])
	}
	requiredSet := map[string]bool{}
	for _, item := range required {
		requiredSet[item] = true
	}
	if !requiredSet["question"] || !requiredSet["scope"] {
		t.Fatalf("brain_think required = %#v, want question and scope", required)
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
