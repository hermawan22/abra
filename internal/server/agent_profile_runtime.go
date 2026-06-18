package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/hermawan22/abra/internal/memory"
	"github.com/hermawan22/abra/internal/policy"
	"github.com/hermawan22/abra/internal/store"
)

func (h *handler) applyAgentProfileToCompose(ctx context.Context, input memory.ComposeInput) (memory.ComposeInput, bool, error) {
	scope := strings.TrimSpace(input.Scope)
	agent := strings.TrimSpace(input.Agent)
	if scope == "" || agent == "" {
		return input, false, nil
	}
	profile, found, err := h.db.FindAgentProfile(ctx, scope, agent)
	if err != nil || !found {
		return input, found, err
	}
	if !store.AgentProfileAllowsScope(profile, scope) {
		return input, true, fmt.Errorf("agent profile %q is not allowed to access scope %q", profile.ProfileKey, scope)
	}
	if !agentProfileAllowsMemoryRead(profile) {
		return input, true, fmt.Errorf("agent profile %q does not allow memory_read", profile.ProfileKey)
	}
	input.AgentProfile = &profile
	applyMemoryPreferencesToCompose(&input, profile.MemoryPreferences)
	return input, true, nil
}

func (h *handler) applyAgentProfileToPolicy(ctx context.Context, event policy.Event, config policy.Config) (policy.Event, policy.Config, bool, error) {
	scope := strings.TrimSpace(event.Scope)
	agent := strings.TrimSpace(event.Agent)
	if scope == "" || agent == "" {
		return event, config, false, nil
	}
	profile, found, err := h.db.FindAgentProfile(ctx, scope, agent)
	if err != nil || !found {
		return event, config, found, err
	}
	if !store.AgentProfileAllowsScope(profile, scope) {
		return event, config, true, fmt.Errorf("agent profile %q is not allowed to access scope %q", profile.ProfileKey, scope)
	}
	if !agentProfileAllowsMemoryRead(profile) {
		return event, config, true, fmt.Errorf("agent profile %q does not allow memory_read", profile.ProfileKey)
	}
	if config.DefaultScope == "" {
		config.DefaultScope = profile.DefaultScope
	}
	if limit := intPreference(profile.MemoryPreferences, "limit"); limit > 0 && config.DefaultLimit <= 0 {
		config.DefaultLimit = limit
	}
	if maxQueries := intPreference(profile.MemoryPreferences, "max_queries"); maxQueries > 0 && config.MaxQueries <= 0 {
		config.MaxQueries = maxQueries
	}
	if boolPreference(profile.MemoryPreferences, "include_unverified") {
		config.IncludeUnverified = true
	}
	return event, config, true, nil
}

func applyMemoryPreferencesToCompose(input *memory.ComposeInput, preferences map[string]any) {
	if input == nil {
		return
	}
	if input.Limit <= 0 {
		input.Limit = intPreference(preferences, "limit")
	}
	if input.MaxQueries <= 0 {
		input.MaxQueries = intPreference(preferences, "max_queries")
	}
	if input.TokenBudget <= 0 {
		input.TokenBudget = intPreference(preferences, "token_budget")
	}
	if boolPreference(preferences, "include_unverified") {
		input.IncludeUnverified = true
	}
}

func agentProfileAllowsMemoryRead(profile store.AgentProfileRecord) bool {
	raw, ok := profile.Permissions["memory_read"]
	if !ok || raw == nil {
		return true
	}
	if allowed, ok := raw.(bool); ok {
		return allowed
	}
	if text, ok := raw.(string); ok {
		return strings.TrimSpace(strings.ToLower(text)) != "false" && strings.TrimSpace(strings.ToLower(text)) != "deny"
	}
	return true
}

func intPreference(preferences map[string]any, key string) int {
	if preferences == nil {
		return 0
	}
	switch value := preferences[key].(type) {
	case float64:
		return int(value)
	case float32:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	case string:
		var out int
		if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &out); err == nil {
			return out
		}
	}
	return 0
}

func boolPreference(preferences map[string]any, key string) bool {
	if preferences == nil {
		return false
	}
	switch value := preferences[key].(type) {
	case bool:
		return value
	case string:
		switch strings.TrimSpace(strings.ToLower(value)) {
		case "1", "true", "yes", "allow", "enabled":
			return true
		default:
			return false
		}
	default:
		return false
	}
}
