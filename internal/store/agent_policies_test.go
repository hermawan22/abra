package store

import "testing"

func TestAgentActionPolicyMatches(t *testing.T) {
	policy := AgentActionPolicyRecord{
		Scope:       "team:example",
		SubjectType: "agent",
		SubjectID:   "frontend-*",
		Effect:      "require_review",
		Rule: map[string]any{
			"actions":      []any{"agent_write", "challenge_claim"},
			"target_types": []any{"memory_write", "claim"},
			"target_ids":   []any{"team:example*"},
		},
	}
	input := AgentActionDecisionInput{
		Action:        "agent_write",
		Scope:         "team:example",
		TargetType:    "memory_write",
		TargetID:      "team:example/service",
		PrincipalType: "agent",
		PrincipalID:   "frontend-bot",
	}
	if !agentActionPolicyMatches(policy, input) {
		t.Fatal("expected policy to match agent write")
	}
	input.PrincipalID = "backend-bot"
	if agentActionPolicyMatches(policy, input) {
		t.Fatal("expected subject mismatch to reject policy")
	}
}

func TestEvaluateAgentActionPolicyRecords(t *testing.T) {
	deny := AgentActionPolicyRecord{
		Scope:       "team:example",
		Status:      "active",
		Priority:    1,
		SubjectType: "agent",
		SubjectID:   "frontend-bot",
		Effect:      "deny",
		Rule: map[string]any{
			"actions":      []any{"forget_claim"},
			"target_types": []any{"claim"},
			"target_ids":   []any{"*"},
		},
	}
	review := AgentActionPolicyRecord{
		Scope:       "team:example",
		Status:      "active",
		Priority:    2,
		SubjectType: "agent",
		SubjectID:   "frontend-bot",
		Effect:      "require_review",
		Rule: map[string]any{
			"actions":      []any{"agent_write"},
			"target_types": []any{"memory_write"},
			"target_ids":   []any{"team:example"},
		},
	}
	policies := []AgentActionPolicyRecord{deny, review}
	writeDecision := evaluateAgentActionPolicyRecords(policies, AgentActionDecisionInput{
		Action:        "agent_write",
		Scope:         "team:example",
		TargetType:    "memory_write",
		TargetID:      "team:example",
		PrincipalType: "agent",
		PrincipalID:   "frontend-bot",
	})
	if writeDecision.Decision != "require_review" || writeDecision.Allowed {
		t.Fatalf("write decision = %#v, want require_review", writeDecision)
	}
	forgetDecision := evaluateAgentActionPolicyRecords(policies, AgentActionDecisionInput{
		Action:        "forget_claim",
		Scope:         "team:example",
		TargetType:    "claim",
		TargetID:      "claim-1",
		PrincipalType: "agent",
		PrincipalID:   "frontend-bot",
	})
	if forgetDecision.Decision != "deny" || forgetDecision.Allowed {
		t.Fatalf("forget decision = %#v, want deny", forgetDecision)
	}
	noPolicy := evaluateAgentActionPolicyRecords(policies, AgentActionDecisionInput{
		Action:        "backfill",
		Scope:         "team:example",
		TargetType:    "memory_summaries",
		TargetID:      "team:example",
		PrincipalType: "agent",
		PrincipalID:   "frontend-bot",
	})
	if noPolicy.Decision != "no_policy" || noPolicy.Allowed {
		t.Fatalf("no policy decision = %#v, want no_policy", noPolicy)
	}
}
