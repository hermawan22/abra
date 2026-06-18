package store

import "testing"

func TestNormalizeAgentProfileDefaults(t *testing.T) {
	profile := normalizeAgentProfile(AgentProfileRecord{
		Scope:         " team:example ",
		ProfileKey:    " agent-alpha ",
		DisplayName:   " Agent Alpha ",
		AllowedScopes: []string{" team:example ", "", "team:example", "team:design-system"},
	})
	if profile.Scope != "team:example" || profile.ProfileKey != "agent-alpha" || profile.DisplayName != "Agent Alpha" {
		t.Fatalf("profile was not trimmed: %#v", profile)
	}
	if profile.AgentType != "agent" || profile.Status != "active" || profile.DefaultScope != "team:example" {
		t.Fatalf("profile defaults missing: %#v", profile)
	}
	if len(profile.AllowedScopes) != 2 {
		t.Fatalf("allowed scopes were not cleaned: %#v", profile.AllowedScopes)
	}
	if profile.Permissions == nil || profile.MemoryPreferences == nil || profile.Metadata == nil {
		t.Fatalf("json maps should default to empty maps: %#v", profile)
	}
}

func TestAgentProfileAllowsScope(t *testing.T) {
	profile := AgentProfileRecord{
		Scope:         "team:example",
		Status:        "active",
		AllowedScopes: []string{"team:example", "team:design-system:*"},
		DeniedScopes:  []string{"team:design-system:secret"},
	}
	if !AgentProfileAllowsScope(profile, "team:example") {
		t.Fatal("expected exact allowed scope")
	}
	if !AgentProfileAllowsScope(profile, "team:design-system:tokens") {
		t.Fatal("expected wildcard allowed scope")
	}
	if AgentProfileAllowsScope(profile, "team:design-system:secret") {
		t.Fatal("denied scope should win over allowed wildcard")
	}
	if AgentProfileAllowsScope(profile, "team:platform") {
		t.Fatal("unexpected scope access")
	}
}

func TestAgentProfileDefaultScopeFallback(t *testing.T) {
	profile := AgentProfileRecord{Scope: "team:example", Status: "active"}
	if !AgentProfileAllowsScope(profile, "") {
		t.Fatal("empty requested scope should fall back to profile scope")
	}
	profile.Status = "disabled"
	if AgentProfileAllowsScope(profile, "team:example") {
		t.Fatal("disabled profile should not allow scope")
	}
}
