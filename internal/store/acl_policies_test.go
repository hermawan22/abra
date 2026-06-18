package store

import "testing"

func TestACLRuleMatches(t *testing.T) {
	input := ACLDecisionInput{
		Action:       "recall",
		ResourceType: "document",
		ResourceID:   "jira:PLAT-1",
	}
	if !aclRuleMatches(map[string]any{
		"actions":        []any{"recall", "read"},
		"resource_types": []any{"document"},
		"resource_ids":   []any{"jira:*"},
	}, input) {
		t.Fatal("expected wildcard ACL rule to match")
	}
	if aclRuleMatches(map[string]any{
		"actions": []any{"write"},
	}, input) {
		t.Fatal("unexpected action match")
	}
}

func TestACLValueMatches(t *testing.T) {
	if !aclValueMatches("team:*", "team:example") {
		t.Fatal("expected prefix wildcard match")
	}
	if aclValueMatches("team:platform", "team:example") {
		t.Fatal("unexpected exact match")
	}
}
