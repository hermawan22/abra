package store

import (
	"context"
	"fmt"
	"strings"
)

type ACLPolicyRecord struct {
	ID          string         `json:"id"`
	Scope       string         `json:"scope"`
	Name        string         `json:"name"`
	Status      string         `json:"status"`
	Priority    int            `json:"priority"`
	SubjectType string         `json:"subject_type"`
	SubjectID   string         `json:"subject_id"`
	Effect      string         `json:"effect"`
	Rule        map[string]any `json:"rule"`
	CreatedBy   string         `json:"created_by,omitempty"`
	CreatedAt   string         `json:"created_at,omitempty"`
	UpdatedAt   string         `json:"updated_at,omitempty"`
	Metadata    map[string]any `json:"metadata"`
	ApprovalID  string         `json:"approval_id,omitempty"`
}

type ACLDecisionInput struct {
	PrincipalType string         `json:"principal_type"`
	PrincipalID   string         `json:"principal_id"`
	Action        string         `json:"action"`
	Scope         string         `json:"scope"`
	ResourceType  string         `json:"resource_type"`
	ResourceID    string         `json:"resource_id"`
	Context       map[string]any `json:"context"`
}

type ACLDecisionResult struct {
	Allowed       bool             `json:"allowed"`
	Decision      string           `json:"decision"`
	Reason        string           `json:"reason"`
	MatchedPolicy *ACLPolicyRecord `json:"matched_policy,omitempty"`
}

func (s *Store) UpsertACLPolicy(ctx context.Context, policy ACLPolicyRecord) (ACLPolicyRecord, error) {
	policy.Scope = strings.TrimSpace(policy.Scope)
	policy.Name = strings.TrimSpace(policy.Name)
	policy.SubjectType = strings.TrimSpace(policy.SubjectType)
	policy.SubjectID = strings.TrimSpace(policy.SubjectID)
	policy.Effect = strings.TrimSpace(policy.Effect)
	if policy.Scope == "" || policy.Name == "" || policy.SubjectType == "" || policy.SubjectID == "" || policy.Effect == "" {
		return ACLPolicyRecord{}, fmt.Errorf("scope, name, subject_type, subject_id, and effect are required")
	}
	if policy.ID == "" {
		policy.ID = stableID("acl-policy", policy.Scope, policy.Name)
	}
	if policy.Status == "" {
		policy.Status = "active"
	}
	if policy.Priority == 0 {
		policy.Priority = 100
	}
	if policy.Effect != "allow" && policy.Effect != "deny" && policy.Effect != "require_review" {
		return ACLPolicyRecord{}, fmt.Errorf("acl effect must be allow, deny, or require_review")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO policies (
		  id, scope, policy_type, name, status, priority, subject_type, subject_id,
		  effect, rule, created_by, metadata
		)
		VALUES ($1, $2, 'acl', $3, $4, $5, $6, $7, $8, $9::jsonb, NULLIF($10, ''), $11::jsonb)
		ON CONFLICT (scope, policy_type, name)
		DO UPDATE SET
		  status = EXCLUDED.status,
		  priority = EXCLUDED.priority,
		  subject_type = EXCLUDED.subject_type,
		  subject_id = EXCLUDED.subject_id,
		  effect = EXCLUDED.effect,
		  rule = EXCLUDED.rule,
		  metadata = policies.metadata || EXCLUDED.metadata,
		  updated_at = now()
	`, policy.ID, policy.Scope, policy.Name, policy.Status, policy.Priority, policy.SubjectType, policy.SubjectID, policy.Effect, jsonb(policy.Rule), strings.TrimSpace(policy.CreatedBy), jsonb(policy.Metadata))
	if err != nil {
		return ACLPolicyRecord{}, err
	}
	return s.GetACLPolicyByName(ctx, policy.Scope, policy.Name)
}

func (s *Store) GetACLPolicyByName(ctx context.Context, scope, name string) (ACLPolicyRecord, error) {
	rows, err := s.pool.Query(ctx, aclPolicySelectSQL()+`
		WHERE scope = $1 AND policy_type = 'acl' AND name = $2
	`, strings.TrimSpace(scope), strings.TrimSpace(name))
	if err != nil {
		return ACLPolicyRecord{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return ACLPolicyRecord{}, err
		}
		return ACLPolicyRecord{}, fmt.Errorf("acl policy %q not found in scope %q", name, scope)
	}
	return scanACLPolicy(rows)
}

func (s *Store) ListACLPolicies(ctx context.Context, scope, subjectType, subjectID string, limit int) ([]ACLPolicyRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, aclPolicySelectSQL()+`
		WHERE policy_type = 'acl'
		  AND ($1 = '' OR scope = $1)
		  AND ($2 = '' OR subject_type = $2)
		  AND ($3 = '' OR subject_id = $3)
		ORDER BY scope ASC, priority ASC, created_at DESC
		LIMIT $4
	`, strings.TrimSpace(scope), strings.TrimSpace(subjectType), strings.TrimSpace(subjectID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	policies := []ACLPolicyRecord{}
	for rows.Next() {
		policy, err := scanACLPolicy(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	return policies, rows.Err()
}

func (s *Store) EvaluateACLDecision(ctx context.Context, input ACLDecisionInput) (ACLDecisionResult, error) {
	input.Scope = strings.TrimSpace(input.Scope)
	input.Action = strings.TrimSpace(input.Action)
	input.PrincipalType = strings.TrimSpace(input.PrincipalType)
	input.PrincipalID = strings.TrimSpace(input.PrincipalID)
	input.ResourceType = strings.TrimSpace(input.ResourceType)
	input.ResourceID = strings.TrimSpace(input.ResourceID)
	if input.Scope == "" || input.Action == "" || input.PrincipalType == "" || input.PrincipalID == "" {
		return ACLDecisionResult{}, fmt.Errorf("scope, action, principal_type, and principal_id are required")
	}
	policies, err := s.ListACLPolicies(ctx, input.Scope, input.PrincipalType, input.PrincipalID, 100)
	if err != nil {
		return ACLDecisionResult{}, err
	}
	var review *ACLPolicyRecord
	for _, policy := range policies {
		if policy.Status != "active" || !aclRuleMatches(policy.Rule, input) {
			continue
		}
		next := policy
		switch policy.Effect {
		case "deny":
			return ACLDecisionResult{Allowed: false, Decision: "deny", Reason: "matched deny policy", MatchedPolicy: &next}, nil
		case "allow":
			return ACLDecisionResult{Allowed: true, Decision: "allow", Reason: "matched allow policy", MatchedPolicy: &next}, nil
		case "require_review":
			if review == nil {
				review = &next
			}
		}
	}
	if review != nil {
		return ACLDecisionResult{Allowed: false, Decision: "require_review", Reason: "matched review policy", MatchedPolicy: review}, nil
	}
	return ACLDecisionResult{Allowed: false, Decision: "deny", Reason: "no matching acl policy"}, nil
}

func aclPolicySelectSQL() string {
	return `
		SELECT
		  id,
		  scope,
		  name,
		  status,
		  priority,
		  COALESCE(subject_type, ''),
		  COALESCE(subject_id, ''),
		  effect,
		  rule,
		  COALESCE(created_by, ''),
		  created_at::text,
		  updated_at::text,
		  metadata
		FROM policies
	`
}

func scanACLPolicy(rows interface{ Scan(...any) error }) (ACLPolicyRecord, error) {
	var policy ACLPolicyRecord
	var ruleRaw, metadataRaw []byte
	if err := rows.Scan(
		&policy.ID,
		&policy.Scope,
		&policy.Name,
		&policy.Status,
		&policy.Priority,
		&policy.SubjectType,
		&policy.SubjectID,
		&policy.Effect,
		&ruleRaw,
		&policy.CreatedBy,
		&policy.CreatedAt,
		&policy.UpdatedAt,
		&metadataRaw,
	); err != nil {
		return ACLPolicyRecord{}, err
	}
	policy.Rule = decodeJSONMap(ruleRaw)
	policy.Metadata = decodeJSONMap(metadataRaw)
	return policy, nil
}

func aclRuleMatches(rule map[string]any, input ACLDecisionInput) bool {
	if !matchList(rule, "actions", input.Action) {
		return false
	}
	if !matchList(rule, "resource_types", input.ResourceType) {
		return false
	}
	if !matchList(rule, "resource_ids", input.ResourceID) {
		return false
	}
	return true
}

func matchList(rule map[string]any, key, value string) bool {
	raw, ok := rule[key]
	if !ok || raw == nil {
		return true
	}
	value = strings.ToLower(strings.TrimSpace(value))
	switch typed := raw.(type) {
	case string:
		return aclValueMatches(typed, value)
	case []any:
		for _, item := range typed {
			if text, ok := item.(string); ok && aclValueMatches(text, value) {
				return true
			}
		}
	case []string:
		for _, item := range typed {
			if aclValueMatches(item, value) {
				return true
			}
		}
	}
	return false
}

func aclValueMatches(pattern, value string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if pattern == "" || pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(value, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == value
}
