package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

type AgentProfileRecord struct {
	ID                string         `json:"id"`
	Scope             string         `json:"scope"`
	ProfileKey        string         `json:"profile_key"`
	DisplayName       string         `json:"display_name"`
	AgentType         string         `json:"agent_type"`
	Status            string         `json:"status"`
	PrincipalRef      string         `json:"principal_ref,omitempty"`
	DefaultScope      string         `json:"default_scope,omitempty"`
	AllowedScopes     []string       `json:"allowed_scopes"`
	DeniedScopes      []string       `json:"denied_scopes"`
	Permissions       map[string]any `json:"permissions"`
	MemoryPreferences map[string]any `json:"memory_preferences"`
	CreatedBy         string         `json:"created_by,omitempty"`
	CreatedAt         string         `json:"created_at,omitempty"`
	UpdatedAt         string         `json:"updated_at,omitempty"`
	Metadata          map[string]any `json:"metadata"`
	ApprovalID        string         `json:"approval_id,omitempty"`
}

func (s *Store) UpsertAgentProfile(ctx context.Context, profile AgentProfileRecord) (AgentProfileRecord, error) {
	profile = normalizeAgentProfile(profile)
	if profile.Scope == "" || profile.ProfileKey == "" || profile.DisplayName == "" {
		return AgentProfileRecord{}, fmt.Errorf("scope, profile_key, and display_name are required")
	}
	if profile.ID == "" {
		profile.ID = stableID("agent-profile", profile.Scope, profile.ProfileKey)
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO agent_profiles (
		  id, scope, profile_key, display_name, agent_type, status, principal_ref,
		  default_scope, allowed_scopes, denied_scopes, permissions, memory_preferences,
		  created_by, metadata
		)
		VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''), NULLIF($8, ''), $9, $10, $11::jsonb, $12::jsonb, NULLIF($13, ''), $14::jsonb)
		ON CONFLICT (scope, profile_key)
		DO UPDATE SET
		  display_name = EXCLUDED.display_name,
		  agent_type = EXCLUDED.agent_type,
		  status = EXCLUDED.status,
		  principal_ref = EXCLUDED.principal_ref,
		  default_scope = EXCLUDED.default_scope,
		  allowed_scopes = EXCLUDED.allowed_scopes,
		  denied_scopes = EXCLUDED.denied_scopes,
		  permissions = EXCLUDED.permissions,
		  memory_preferences = EXCLUDED.memory_preferences,
		  metadata = agent_profiles.metadata || EXCLUDED.metadata,
		  updated_at = now()
	`, profile.ID, profile.Scope, profile.ProfileKey, profile.DisplayName, profile.AgentType, profile.Status, profile.PrincipalRef, profile.DefaultScope, profile.AllowedScopes, profile.DeniedScopes, jsonb(profile.Permissions), jsonb(profile.MemoryPreferences), profile.CreatedBy, jsonb(profile.Metadata))
	if err != nil {
		return AgentProfileRecord{}, err
	}
	return s.GetAgentProfile(ctx, profile.Scope, profile.ProfileKey)
}

func (s *Store) GetAgentProfile(ctx context.Context, scope, profileKey string) (AgentProfileRecord, error) {
	profile, found, err := s.FindAgentProfile(ctx, scope, profileKey)
	if err != nil {
		return AgentProfileRecord{}, err
	}
	if !found {
		return AgentProfileRecord{}, fmt.Errorf("agent profile %q not found in scope %q", profileKey, scope)
	}
	return profile, nil
}

func (s *Store) FindAgentProfile(ctx context.Context, scope, profileKey string) (AgentProfileRecord, bool, error) {
	rows, err := s.pool.Query(ctx, agentProfileSelectSQL()+`
		WHERE scope = $1 AND profile_key = $2
	`, strings.TrimSpace(scope), strings.TrimSpace(profileKey))
	if err != nil {
		return AgentProfileRecord{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return AgentProfileRecord{}, false, err
		}
		return AgentProfileRecord{}, false, nil
	}
	profile, err := scanAgentProfile(rows)
	if err != nil {
		return AgentProfileRecord{}, false, err
	}
	return profile, true, nil
}

func (s *Store) ListAgentProfiles(ctx context.Context, scope, status string, limit int) ([]AgentProfileRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, agentProfileSelectSQL()+`
		WHERE ($1 = '' OR scope = $1)
		  AND ($2 = '' OR status = $2)
		ORDER BY scope ASC, profile_key ASC
		LIMIT $3
	`, strings.TrimSpace(scope), strings.TrimSpace(status), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	profiles := []AgentProfileRecord{}
	for rows.Next() {
		profile, err := scanAgentProfile(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, profile)
	}
	return profiles, rows.Err()
}

func AgentProfileAllowsScope(profile AgentProfileRecord, scope string) bool {
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = strings.TrimSpace(profile.DefaultScope)
	}
	if scope == "" {
		scope = strings.TrimSpace(profile.Scope)
	}
	if profile.Status != "" && profile.Status != "active" {
		return false
	}
	for _, denied := range profile.DeniedScopes {
		if scopePatternMatches(denied, scope) {
			return false
		}
	}
	allowed := cleanStringList(profile.AllowedScopes)
	if len(allowed) == 0 {
		return scopePatternMatches(profile.Scope, scope) || scopePatternMatches(profile.DefaultScope, scope)
	}
	for _, item := range allowed {
		if scopePatternMatches(item, scope) {
			return true
		}
	}
	return false
}

func normalizeAgentProfile(profile AgentProfileRecord) AgentProfileRecord {
	profile.Scope = strings.TrimSpace(profile.Scope)
	profile.ProfileKey = strings.TrimSpace(profile.ProfileKey)
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	profile.AgentType = strings.TrimSpace(profile.AgentType)
	if profile.AgentType == "" {
		profile.AgentType = "agent"
	}
	profile.Status = strings.TrimSpace(profile.Status)
	if profile.Status == "" {
		profile.Status = "active"
	}
	profile.PrincipalRef = strings.TrimSpace(profile.PrincipalRef)
	profile.DefaultScope = strings.TrimSpace(profile.DefaultScope)
	if profile.DefaultScope == "" {
		profile.DefaultScope = profile.Scope
	}
	profile.AllowedScopes = cleanStringList(profile.AllowedScopes)
	profile.DeniedScopes = cleanStringList(profile.DeniedScopes)
	if profile.Permissions == nil {
		profile.Permissions = map[string]any{}
	}
	if profile.MemoryPreferences == nil {
		profile.MemoryPreferences = map[string]any{}
	}
	if profile.Metadata == nil {
		profile.Metadata = map[string]any{}
	}
	profile.CreatedBy = strings.TrimSpace(profile.CreatedBy)
	return profile
}

func scopePatternMatches(pattern, scope string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	scope = strings.ToLower(strings.TrimSpace(scope))
	if pattern == "" {
		return false
	}
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(scope, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == scope
}

func agentProfileSelectSQL() string {
	return `
		SELECT
		  id,
		  scope,
		  profile_key,
		  display_name,
		  agent_type,
		  status,
		  COALESCE(principal_ref, ''),
		  COALESCE(default_scope, ''),
		  allowed_scopes,
		  denied_scopes,
		  permissions,
		  memory_preferences,
		  COALESCE(created_by, ''),
		  created_at::text,
		  updated_at::text,
		  metadata
		FROM agent_profiles
	`
}

func scanAgentProfile(rows interface{ Scan(...any) error }) (AgentProfileRecord, error) {
	var profile AgentProfileRecord
	var permissionsRaw, memoryPreferencesRaw, metadataRaw []byte
	if err := rows.Scan(
		&profile.ID,
		&profile.Scope,
		&profile.ProfileKey,
		&profile.DisplayName,
		&profile.AgentType,
		&profile.Status,
		&profile.PrincipalRef,
		&profile.DefaultScope,
		&profile.AllowedScopes,
		&profile.DeniedScopes,
		&permissionsRaw,
		&memoryPreferencesRaw,
		&profile.CreatedBy,
		&profile.CreatedAt,
		&profile.UpdatedAt,
		&metadataRaw,
	); err != nil {
		if err == pgx.ErrNoRows {
			return AgentProfileRecord{}, err
		}
		return AgentProfileRecord{}, err
	}
	profile.Permissions = decodeJSONMap(permissionsRaw)
	profile.MemoryPreferences = decodeJSONMap(memoryPreferencesRaw)
	profile.Metadata = decodeJSONMap(metadataRaw)
	return profile, nil
}
