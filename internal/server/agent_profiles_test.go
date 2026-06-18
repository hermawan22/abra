package server

import (
	"testing"

	"github.com/hermawan22/abra/internal/store"
)

func TestAgentProfileApprovalTarget(t *testing.T) {
	if got := agentProfileApprovalTarget(store.AgentProfileRecord{ID: "profile-1"}); got != "profile-1" {
		t.Fatalf("target = %q, want profile-1", got)
	}
	got := agentProfileApprovalTarget(store.AgentProfileRecord{Scope: "team:example", ProfileKey: "agent-alpha"})
	if got != "team:example/agent-alpha" {
		t.Fatalf("target = %q, want team:example/agent-alpha", got)
	}
}
