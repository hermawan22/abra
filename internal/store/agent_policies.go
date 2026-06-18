package store

import (
	"context"
	"fmt"
	"strings"
)

type AgentActionPolicyRecord struct {
	ID          string         `json:"id"`
	Scope       string         `json:"scope"`
	Name        string         `json:"name"`
	Status      string         `json:"status"`
	Priority    int            `json:"priority"`
	SubjectType string         `json:"subject_type,omitempty"`
	SubjectID   string         `json:"subject_id,omitempty"`
	Effect      string         `json:"effect"`
	Rule        map[string]any `json:"rule"`
	CreatedBy   string         `json:"created_by,omitempty"`
	CreatedAt   string         `json:"created_at,omitempty"`
	UpdatedAt   string         `json:"updated_at,omitempty"`
	Metadata    map[string]any `json:"metadata"`
	ApprovalID  string         `json:"approval_id,omitempty"`
}

type AgentActionDecisionInput struct {
	Action        string         `json:"action"`
	Scope         string         `json:"scope"`
	TargetType    string         `json:"target_type"`
	TargetID      string         `json:"target_id"`
	PrincipalType string         `json:"principal_type,omitempty"`
	PrincipalID   string         `json:"principal_id,omitempty"`
	Context       map[string]any `json:"context,omitempty"`
}

type AgentActionDecisionResult struct {
	Allowed       bool                     `json:"allowed"`
	Decision      string                   `json:"decision"`
	Reason        string                   `json:"reason"`
	MatchedPolicy *AgentActionPolicyRecord `json:"matched_policy,omitempty"`
}

func (s *Store) UpsertAgentActionPolicy(ctx context.Context, policy AgentActionPolicyRecord) (AgentActionPolicyRecord, error) {
	policy.Scope = strings.TrimSpace(policy.Scope)
	policy.Name = strings.TrimSpace(policy.Name)
	policy.SubjectType = strings.TrimSpace(policy.SubjectType)
	policy.SubjectID = strings.TrimSpace(policy.SubjectID)
	policy.Effect = strings.TrimSpace(policy.Effect)
	if policy.Scope == "" || policy.Name == "" || policy.Effect == "" {
		return AgentActionPolicyRecord{}, fmt.Errorf("scope, name, and effect are required")
	}
	if policy.ID == "" {
		policy.ID = stableID("agent-action-policy", policy.Scope, policy.Name)
	}
	if policy.Status == "" {
		policy.Status = "active"
	}
	if policy.Priority == 0 {
		policy.Priority = 100
	}
	if policy.Effect != "allow" && policy.Effect != "deny" && policy.Effect != "require_review" {
		return AgentActionPolicyRecord{}, fmt.Errorf("agent action policy effect must be allow, deny, or require_review")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO policies (
		  id, scope, policy_type, name, status, priority, subject_type, subject_id,
		  effect, rule, created_by, metadata
		)
		VALUES ($1, $2, 'agent_action', $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), $8, $9::jsonb, NULLIF($10, ''), $11::jsonb)
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
		return AgentActionPolicyRecord{}, err
	}
	return s.GetAgentActionPolicyByName(ctx, policy.Scope, policy.Name)
}

func (s *Store) GetAgentActionPolicyByName(ctx context.Context, scope, name string) (AgentActionPolicyRecord, error) {
	rows, err := s.pool.Query(ctx, agentActionPolicySelectSQL()+`
		WHERE scope = $1 AND policy_type = 'agent_action' AND name = $2
	`, strings.TrimSpace(scope), strings.TrimSpace(name))
	if err != nil {
		return AgentActionPolicyRecord{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return AgentActionPolicyRecord{}, err
		}
		return AgentActionPolicyRecord{}, fmt.Errorf("agent action policy %q not found in scope %q", name, scope)
	}
	return scanAgentActionPolicy(rows)
}

func (s *Store) ListAgentActionPolicies(ctx context.Context, scope string, limit int) ([]AgentActionPolicyRecord, error) {
	if limit < 1 || limit > 100 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, agentActionPolicySelectSQL()+`
		WHERE policy_type = 'agent_action'
		  AND ($1 = '' OR scope = $1)
		ORDER BY scope ASC, priority ASC, created_at DESC
		LIMIT $2
	`, strings.TrimSpace(scope), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	policies := []AgentActionPolicyRecord{}
	for rows.Next() {
		policy, err := scanAgentActionPolicy(rows)
		if err != nil {
			return nil, err
		}
		policies = append(policies, policy)
	}
	return policies, rows.Err()
}

func (s *Store) EvaluateAgentActionPolicy(ctx context.Context, input AgentActionDecisionInput) (AgentActionDecisionResult, error) {
	input.Scope = strings.TrimSpace(input.Scope)
	input.Action = strings.TrimSpace(input.Action)
	input.TargetType = strings.TrimSpace(input.TargetType)
	input.TargetID = strings.TrimSpace(input.TargetID)
	input.PrincipalType = strings.TrimSpace(input.PrincipalType)
	input.PrincipalID = strings.TrimSpace(input.PrincipalID)
	if input.Scope == "" || input.Action == "" {
		return AgentActionDecisionResult{}, fmt.Errorf("scope and action are required")
	}
	policies, err := s.ListAgentActionPolicies(ctx, input.Scope, 100)
	if err != nil {
		return AgentActionDecisionResult{}, err
	}
	return evaluateAgentActionPolicyRecords(policies, input), nil
}

func (s *Store) EvaluateAgentActionPolicies(ctx context.Context, inputs []AgentActionDecisionInput) ([]AgentActionDecisionResult, error) {
	if len(inputs) == 0 {
		return []AgentActionDecisionResult{}, nil
	}
	scope := strings.TrimSpace(inputs[0].Scope)
	if scope == "" {
		return nil, fmt.Errorf("scope is required")
	}
	for i := range inputs {
		inputs[i].Scope = strings.TrimSpace(inputs[i].Scope)
		inputs[i].Action = strings.TrimSpace(inputs[i].Action)
		inputs[i].TargetType = strings.TrimSpace(inputs[i].TargetType)
		inputs[i].TargetID = strings.TrimSpace(inputs[i].TargetID)
		inputs[i].PrincipalType = strings.TrimSpace(inputs[i].PrincipalType)
		inputs[i].PrincipalID = strings.TrimSpace(inputs[i].PrincipalID)
		if inputs[i].Scope == "" || inputs[i].Action == "" {
			return nil, fmt.Errorf("scope and action are required")
		}
		if inputs[i].Scope != scope {
			return nil, fmt.Errorf("agent action policy batch must use one scope")
		}
	}
	policies, err := s.ListAgentActionPolicies(ctx, scope, 100)
	if err != nil {
		return nil, err
	}
	results := make([]AgentActionDecisionResult, 0, len(inputs))
	for _, input := range inputs {
		results = append(results, evaluateAgentActionPolicyRecords(policies, input))
	}
	return results, nil
}

func evaluateAgentActionPolicyRecords(policies []AgentActionPolicyRecord, input AgentActionDecisionInput) AgentActionDecisionResult {
	var review *AgentActionPolicyRecord
	for _, policy := range policies {
		if policy.Status != "active" || !agentActionPolicyMatches(policy, input) {
			continue
		}
		next := policy
		switch policy.Effect {
		case "deny":
			return AgentActionDecisionResult{Allowed: false, Decision: "deny", Reason: "matched deny policy", MatchedPolicy: &next}
		case "allow":
			return AgentActionDecisionResult{Allowed: true, Decision: "allow", Reason: "matched allow policy", MatchedPolicy: &next}
		case "require_review":
			if review == nil {
				review = &next
			}
		}
	}
	if review != nil {
		return AgentActionDecisionResult{Allowed: false, Decision: "require_review", Reason: "matched review policy", MatchedPolicy: review}
	}
	return AgentActionDecisionResult{Allowed: false, Decision: "no_policy", Reason: "no matching agent action policy"}
}

func agentActionPolicySelectSQL() string {
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

func scanAgentActionPolicy(rows interface{ Scan(...any) error }) (AgentActionPolicyRecord, error) {
	var policy AgentActionPolicyRecord
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
		return AgentActionPolicyRecord{}, err
	}
	policy.Rule = decodeJSONMap(ruleRaw)
	policy.Metadata = decodeJSONMap(metadataRaw)
	return policy, nil
}

func agentActionPolicyMatches(policy AgentActionPolicyRecord, input AgentActionDecisionInput) bool {
	if policy.SubjectType != "" && !aclValueMatches(policy.SubjectType, input.PrincipalType) {
		return false
	}
	if policy.SubjectID != "" && !aclValueMatches(policy.SubjectID, input.PrincipalID) {
		return false
	}
	if !matchList(policy.Rule, "actions", input.Action) {
		return false
	}
	if !matchList(policy.Rule, "target_types", input.TargetType) {
		return false
	}
	if !matchList(policy.Rule, "target_ids", input.TargetID) {
		return false
	}
	return true
}
