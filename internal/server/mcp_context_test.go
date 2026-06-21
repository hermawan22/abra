package server

import (
	"strings"
	"testing"
)

func TestDecodeWorkingMemoryURIQueryPreservesSlashScope(t *testing.T) {
	scope, task, err := decodeWorkingMemoryURI("abra://working-memory?scope=repo%3Aorg%2Fapp&task=change%20auth")
	if err != nil {
		t.Fatal(err)
	}
	if scope != "repo:org/app" || task != "change auth" {
		t.Fatalf("scope=%q task=%q", scope, task)
	}
}

func TestWorkingMemoryTemplateUsesQueryParameters(t *testing.T) {
	templates := mcpResourceTemplates()
	for _, template := range templates {
		if template["name"] == "working-memory" {
			if template["uriTemplate"] != "abra://working-memory?scope={scope}&task={task}" {
				t.Fatalf("uriTemplate = %#v", template["uriTemplate"])
			}
			return
		}
	}
	t.Fatal("working-memory resource template not found")
}

func TestBeforeCodePromptCanDiscoverScope(t *testing.T) {
	result, err := mcpPrompt("abra-before-code", map[string]any{"task": "change auth"})
	if err != nil {
		t.Fatal(err)
	}
	messages, _ := result["messages"].([]map[string]any)
	if len(messages) == 0 {
		t.Fatalf("messages = %#v", result["messages"])
	}
	content, _ := messages[0]["content"].(map[string]any)
	text, _ := content["text"].(string)
	if !strings.Contains(text, "discover_scopes") || !strings.Contains(text, "working_memory_compose") {
		t.Fatalf("prompt text = %q", text)
	}
}

func TestAgentWorkflowGuideExplainsScopeRecovery(t *testing.T) {
	result, err := (&handler{}).mcpReadResource(nil, "abra://guide/agent-workflow")
	if err != nil {
		t.Fatal(err)
	}
	contents, _ := result["contents"].([]map[string]any)
	if len(contents) == 0 {
		t.Fatalf("contents = %#v", result["contents"])
	}
	text, _ := contents[0]["text"].(string)
	for _, want := range []string{"discover_scopes", "expected_scope", "abra agents verify", "agent_ready=false", "ingest only when verify proves"} {
		if !strings.Contains(text, want) {
			t.Fatalf("guide text missing %q: %q", want, text)
		}
	}
	if strings.Contains(text, "ingest with the printed scope") {
		t.Fatalf("guide text = %q", text)
	}
}
