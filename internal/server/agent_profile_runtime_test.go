package server

import (
	"testing"

	"github.com/hermawan22/abra/internal/memory"
	"github.com/hermawan22/abra/internal/store"
)

func TestApplyMemoryPreferencesToComposeUsesDefaultsOnly(t *testing.T) {
	input := memory.ComposeInput{Limit: 3}
	applyMemoryPreferencesToCompose(&input, map[string]any{
		"limit":              float64(9),
		"max_queries":        float64(7),
		"token_budget":       float64(1200),
		"include_unverified": true,
	})
	if input.Limit != 3 {
		t.Fatalf("explicit limit should win, got %d", input.Limit)
	}
	if input.MaxQueries != 7 || input.TokenBudget != 1200 || !input.IncludeUnverified {
		t.Fatalf("preferences were not applied: %#v", input)
	}
}

func TestAgentProfileAllowsMemoryRead(t *testing.T) {
	if !agentProfileAllowsMemoryRead(store.AgentProfileRecord{}) {
		t.Fatal("missing memory_read permission should default to allowed")
	}
	if agentProfileAllowsMemoryRead(store.AgentProfileRecord{Permissions: map[string]any{"memory_read": false}}) {
		t.Fatal("explicit false memory_read should deny")
	}
	if agentProfileAllowsMemoryRead(store.AgentProfileRecord{Permissions: map[string]any{"memory_read": "deny"}}) {
		t.Fatal("string deny memory_read should deny")
	}
}

func TestPreferenceParsers(t *testing.T) {
	prefs := map[string]any{"limit": "6", "include_unverified": "yes"}
	if got := intPreference(prefs, "limit"); got != 6 {
		t.Fatalf("limit preference = %d, want 6", got)
	}
	if !boolPreference(prefs, "include_unverified") {
		t.Fatal("include_unverified preference should parse as true")
	}
}
